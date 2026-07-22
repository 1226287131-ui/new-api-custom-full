package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	defaultVideoCacheDir             = "/data/video-cache"
	defaultVideoCacheTTL             = 48 * time.Hour
	defaultVideoCacheCleanupInterval = time.Hour
)

func videoCacheDir() string {
	dir := strings.TrimSpace(os.Getenv("VIDEO_CACHE_DIR"))
	if dir == "" {
		return defaultVideoCacheDir
	}
	return dir
}

func videoCacheFilePath(taskID string) string {
	safeID := strings.TrimSpace(taskID)
	safeID = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(safeID)
	return filepath.Join(videoCacheDir(), safeID+".mp4")
}

// CachedVideoPath returns a complete, atomically-written cache file.
func CachedVideoPath(taskID string) (string, bool) {
	if strings.TrimSpace(taskID) == "" {
		return "", false
	}
	path := videoCacheFilePath(taskID)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", false
	}
	return path, true
}

// VideoCacheSource describes one authenticated or inline video source.
// Exactly one of URL and DataURL should normally be set.
type VideoCacheSource struct {
	URL     string
	DataURL string
	Headers http.Header
	Proxy   string
}

// CacheVideoSource stores a remote or data URL video in the local cache.
func CacheVideoSource(ctx context.Context, taskID string, source VideoCacheSource) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cachedPath, ok := CachedVideoPath(taskID); ok {
		return cachedPath, nil
	}

	if strings.TrimSpace(source.DataURL) != "" {
		return cacheVideoDataURL(taskID, source.DataURL)
	}

	remoteURL := strings.TrimSpace(source.URL)
	if remoteURL == "" {
		return "", fmt.Errorf("video cache source URL is empty")
	}
	if strings.HasPrefix(strings.ToLower(remoteURL), "data:") {
		return cacheVideoDataURL(taskID, remoteURL)
	}
	if err := ValidateSSRFProtectedFetchURL(remoteURL); err != nil {
		return "", fmt.Errorf("video cache URL blocked: %w", err)
	}

	client := GetSSRFProtectedHTTPClient()
	if strings.TrimSpace(source.Proxy) != "" {
		var err error
		client, err = GetHttpClientWithProxy(source.Proxy)
		if err != nil {
			return "", fmt.Errorf("create video cache proxy client: %w", err)
		}
	}

	timeoutSeconds := common.GetEnvOrDefault("VIDEO_CACHE_DOWNLOAD_TIMEOUT_SECONDS", 600)
	downloadCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return "", fmt.Errorf("create video cache request: %w", err)
	}
	req.Header.Set("Accept", "video/*,application/octet-stream;q=0.9,*/*;q=0.1")
	for key, values := range source.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download video for cache: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("video cache upstream returned status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if contentType == "application/json" || contentType == "text/html" || contentType == "text/plain" {
		return "", fmt.Errorf("video cache upstream returned non-video content type %s", contentType)
	}

	return cacheVideoReader(taskID, resp.Body)
}

// CacheRemoteVideo downloads a completed video to the local cache using a
// Bearer token. It is kept for existing callers and simple OpenAI-compatible
// providers.
func CacheRemoteVideo(ctx context.Context, taskID, remoteURL, proxy, apiKey string) (string, error) {
	headers := make(http.Header)
	if strings.TrimSpace(apiKey) != "" {
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	return CacheVideoSource(ctx, taskID, VideoCacheSource{
		URL:     remoteURL,
		Headers: headers,
		Proxy:   proxy,
	})
}

// CacheRemoteVideoWithHeaders downloads a completed video with provider-
// specific headers such as x-goog-api-key or Token authentication.
func CacheRemoteVideoWithHeaders(ctx context.Context, taskID, remoteURL, proxy string, headers http.Header) (string, error) {
	return CacheVideoSource(ctx, taskID, VideoCacheSource{
		URL:     remoteURL,
		Headers: headers,
		Proxy:   proxy,
	})
}

// CacheVideoDataURL decodes an inline base64 video into the local cache.
func CacheVideoDataURL(_ context.Context, taskID, dataURL string) (string, error) {
	return cacheVideoDataURL(taskID, dataURL)
}

func cacheVideoDataURL(taskID, dataURL string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(dataURL), ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "data:") || !strings.Contains(strings.ToLower(parts[0]), ";base64") {
		return "", fmt.Errorf("invalid base64 video data URL")
	}

	payload := strings.TrimSpace(parts[1])
	decoder := base64.StdEncoding
	if !strings.Contains(payload, "=") && len(payload)%4 != 0 {
		decoder = base64.RawStdEncoding
	}
	return cacheVideoReader(taskID, base64.NewDecoder(decoder, strings.NewReader(payload)))
}

func cacheVideoReader(taskID string, reader io.Reader) (string, error) {
	maxMB := common.GetEnvOrDefault("VIDEO_CACHE_MAX_MB", 1024)
	if maxMB <= 0 {
		maxMB = 1024
	}
	maxBytes := int64(maxMB) * 1024 * 1024

	dir := videoCacheDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("create video cache directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".video-cache-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create video cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTemp := true
	defer func() {
		_ = tmp.Close()
		if cleanupTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	written, err := io.Copy(tmp, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("write video cache: %w", err)
	}
	if written > maxBytes {
		return "", fmt.Errorf("video cache exceeds %d MB limit", maxMB)
	}
	if written <= 0 {
		return "", fmt.Errorf("video cache response is empty")
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync video cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close video cache temp file: %w", err)
	}

	finalPath := videoCacheFilePath(taskID)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("commit video cache: %w", err)
	}
	cleanupTemp = false
	_ = os.Chmod(finalPath, 0640)
	return finalPath, nil
}

// CleanupVideoCache removes files older than the retention period.
func CleanupVideoCache() (int, error) {
	entries, err := os.ReadDir(videoCacheDir())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-defaultVideoCacheTTL)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".mp4") && !strings.HasPrefix(name, ".video-cache-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(videoCacheDir(), name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// StartVideoCacheCleanup runs an immediate cleanup and then checks hourly.
func StartVideoCacheCleanup() {
	cleanup := func() {
		removed, err := CleanupVideoCache()
		if err != nil {
			common.SysError(fmt.Sprintf("video cache cleanup failed: %v", err))
			return
		}
		if removed > 0 {
			common.SysLog(fmt.Sprintf("video cache cleanup removed %d expired files", removed))
		}
	}

	cleanup()
	go func() {
		ticker := time.NewTicker(defaultVideoCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			cleanup()
		}
	}()
}

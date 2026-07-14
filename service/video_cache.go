package service

import (
	"context"
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

// CacheRemoteVideo downloads a completed video to the local cache.
func CacheRemoteVideo(ctx context.Context, taskID, remoteURL, proxy, apiKey string) (string, error) {
	if cachedPath, ok := CachedVideoPath(taskID); ok {
		return cachedPath, nil
	}

	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return "", fmt.Errorf("video cache source URL is empty")
	}
	if err := ValidateSSRFProtectedFetchURL(remoteURL); err != nil {
		return "", fmt.Errorf("video cache URL blocked: %w", err)
	}

	client := GetSSRFProtectedHTTPClient()
	if strings.TrimSpace(proxy) != "" {
		var err error
		client, err = GetHttpClientWithProxy(proxy)
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
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download video for cache: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("video cache upstream returned status %d", resp.StatusCode)
	}

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

	written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1))
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
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(videoCacheDir(), entry.Name())); err != nil {
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

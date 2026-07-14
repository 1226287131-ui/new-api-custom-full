package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	defaultVideoInputCacheDir             = "/data/video-input-cache"
	defaultVideoInputCacheTTL             = 12 * time.Hour
	defaultVideoInputCacheCleanupInterval = time.Hour
	defaultVideoInputCacheMaxMB           = 20
)

var videoInputMimeTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type videoInputValidationError struct {
	message string
}

func (e *videoInputValidationError) Error() string {
	return e.message
}

func invalidVideoInput(message string, args ...any) error {
	return &videoInputValidationError{message: fmt.Sprintf(message, args...)}
}

func IsVideoInputValidationError(err error) bool {
	var validationErr *videoInputValidationError
	return errors.As(err, &validationErr)
}

func videoInputCacheDir() string {
	dir := strings.TrimSpace(os.Getenv("VIDEO_INPUT_CACHE_DIR"))
	if dir == "" {
		return defaultVideoInputCacheDir
	}
	return dir
}

func videoInputCacheMaxBytes() int64 {
	maxMB := common.GetEnvOrDefault("VIDEO_INPUT_CACHE_MAX_MB", defaultVideoInputCacheMaxMB)
	if maxMB <= 0 {
		maxMB = defaultVideoInputCacheMaxMB
	}
	return int64(maxMB) * 1024 * 1024
}

func videoInputPublicURL(request *http.Request, fileName string) (string, error) {
	baseURL := strings.TrimSpace(os.Getenv("VIDEO_INPUT_CACHE_PUBLIC_BASE_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(system_setting.ServerAddress)
	}
	if baseURL == "" && request != nil {
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		if forwardedProto := firstForwardedValue(request.Header.Get("X-Forwarded-Proto")); forwardedProto != "" {
			scheme = forwardedProto
		}
		host := firstForwardedValue(request.Header.Get("X-Forwarded-Host"))
		if host == "" {
			host = request.Host
		}
		if host != "" {
			baseURL = scheme + "://" + host
		}
	}

	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("public server address is not a valid HTTP URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/") + "/video-input-cache/" + url.PathEscape(fileName), nil
}

func firstForwardedValue(value string) string {
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	return strings.TrimSpace(value)
}

// CacheVideoInput stores one authenticated Sora input_reference upload and
// returns a short-lived public URL that JSON-only upstreams can fetch.
func CacheVideoInput(request *http.Request, fileHeader *multipart.FileHeader) (string, error) {
	if fileHeader == nil {
		return "", invalidVideoInput("input_reference file is missing")
	}
	maxBytes := videoInputCacheMaxBytes()
	if fileHeader.Size <= 0 {
		return "", invalidVideoInput("input_reference file is empty")
	}
	if fileHeader.Size > maxBytes {
		return "", invalidVideoInput("input_reference exceeds %d MB limit", maxBytes/(1024*1024))
	}

	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("open input_reference: %w", err)
	}
	defer file.Close()

	header := make([]byte, 512)
	n, readErr := io.ReadFull(file, header)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("read input_reference: %w", readErr)
	}
	mimeType := http.DetectContentType(header[:n])
	extension, ok := videoInputMimeTypes[mimeType]
	if !ok {
		return "", invalidVideoInput("input_reference must be a JPEG, PNG, or WebP image")
	}

	token, err := common.GenerateRandomCharsKey(40)
	if err != nil {
		return "", fmt.Errorf("generate input_reference id: %w", err)
	}
	fileName := token + extension
	publicURL, err := videoInputPublicURL(request, fileName)
	if err != nil {
		return "", err
	}

	dir := videoInputCacheDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("create video input cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".video-input-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create video input cache file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTemp := true
	defer func() {
		_ = tmp.Close()
		if cleanupTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	source := io.MultiReader(bytes.NewReader(header[:n]), file)
	written, err := io.Copy(tmp, io.LimitReader(source, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("write video input cache: %w", err)
	}
	if written > maxBytes {
		return "", invalidVideoInput("input_reference exceeds %d MB limit", maxBytes/(1024*1024))
	}
	if written <= 0 {
		return "", invalidVideoInput("input_reference file is empty")
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync video input cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close video input cache: %w", err)
	}

	finalPath := filepath.Join(dir, fileName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("commit video input cache: %w", err)
	}
	cleanupTemp = false
	_ = os.Chmod(finalPath, 0640)
	return publicURL, nil
}

// CachedVideoInputPath resolves only generated image names inside the cache.
func CachedVideoInputPath(fileName string) (path string, mimeType string, ok bool) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" || filepath.Base(fileName) != fileName {
		return "", "", false
	}
	extension := strings.ToLower(filepath.Ext(fileName))
	switch extension {
	case ".jpg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	case ".webp":
		mimeType = "image/webp"
	default:
		return "", "", false
	}
	stem := strings.TrimSuffix(fileName, extension)
	if len(stem) != 40 {
		return "", "", false
	}
	for _, char := range stem {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')) {
			return "", "", false
		}
	}

	path = filepath.Join(videoInputCacheDir(), fileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", "", false
	}
	return path, mimeType, true
}

// CleanupVideoInputCache removes temporary reference images after 12 hours.
func CleanupVideoInputCache() (int, error) {
	entries, err := os.ReadDir(videoInputCacheDir())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-defaultVideoInputCacheTTL)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(videoInputCacheDir(), entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func StartVideoInputCacheCleanup() {
	cleanup := func() {
		removed, err := CleanupVideoInputCache()
		if err != nil {
			common.SysError(fmt.Sprintf("video input cache cleanup failed: %v", err))
			return
		}
		if removed > 0 {
			common.SysLog(fmt.Sprintf("video input cache cleanup removed %d expired files", removed))
		}
	}

	cleanup()
	go func() {
		ticker := time.NewTicker(defaultVideoInputCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			cleanup()
		}
	}()
}

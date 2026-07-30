package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

const (
	defaultImageCacheDir             = "/data/image-cache"
	defaultImageCacheTTL             = 2 * time.Hour
	defaultImageCacheCleanupInterval = time.Hour
	defaultImageCacheMaxMB           = 50
	defaultImageCacheDownloadTimeout = 120
	defaultImageCacheRetryDelay      = 2 * time.Second
)

var imageCacheMimeExtensions = map[string]string{
	"image/avif": ".avif",
	"image/gif":  ".gif",
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// ImageCacheSource describes one image returned by an upstream image API.
// Exactly one of URL, DataURL, or Raw should normally be set.
type ImageCacheSource struct {
	URL      string
	DataURL  string
	Raw      []byte
	MIMEType string
	Headers  http.Header
	Proxy    string
}

// ImageCacheInfo is stored in the consume log's Other JSON field. URLs are
// local capability URLs and never contain the upstream image address.
type ImageCacheInfo struct {
	URLs        []string
	TotalCount  int
	CachedCount int
	FailedCount int
	CachedAt    int64
	ExpiresAt   int64
	Status      string
	FailedReasons []string
}

// ImageCacheJob is scheduled only after the upstream image response and the
// initial consume log have been returned. It keeps the cache download out of
// the customer's API latency path.
type ImageCacheJob struct {
	RequestID string
	UserID    int
	Request   *http.Request
	Sources   []ImageCacheSource
}

// StartImageCacheJob downloads image sources in the background and enriches
// the matching consume log after the files are available.
func StartImageCacheJob(job *ImageCacheJob) {
	if job == nil || strings.TrimSpace(job.RequestID) == "" || len(job.Sources) == 0 || !common.LogConsumeEnabled {
		return
	}
	go func() {
		info := CacheImageSources(context.Background(), job.Request, job.Sources)
		for index, reason := range info.FailedReasons {
			common.SysError(fmt.Sprintf(
				"image cache failed: request=%s source=%d/%d reason=%s",
				job.RequestID,
				index+1,
				info.TotalCount,
				reason,
			))
		}
		if err := model.UpdateConsumeLogImageCache(job.RequestID, job.UserID, imageCacheInfoToOther(info)); err != nil {
			common.SysError(fmt.Sprintf("image cache log update failed for request %s: %v", job.RequestID, err))
		}
	}()
}

func imageCacheDir() string {
	dir := strings.TrimSpace(os.Getenv("IMAGE_CACHE_DIR"))
	if dir == "" {
		return defaultImageCacheDir
	}
	return dir
}

func imageCacheMaxBytes() int64 {
	maxMB := common.GetEnvOrDefault("IMAGE_CACHE_MAX_MB", defaultImageCacheMaxMB)
	if maxMB <= 0 {
		maxMB = defaultImageCacheMaxMB
	}
	return int64(maxMB) * 1024 * 1024
}

func imageCacheDownloadTimeout() time.Duration {
	seconds := common.GetEnvOrDefault("IMAGE_CACHE_DOWNLOAD_TIMEOUT_SECONDS", defaultImageCacheDownloadTimeout)
	if seconds <= 0 {
		seconds = defaultImageCacheDownloadTimeout
	}
	return time.Duration(seconds) * time.Second
}

// BuildImageCacheURL returns a local image URL suitable for a usage log.
func BuildImageCacheURL(request *http.Request, fileName string) string {
	baseURL := strings.TrimSpace(os.Getenv("IMAGE_CACHE_PUBLIC_BASE_URL"))
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
		return "/image-cache/" + url.PathEscape(fileName)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/") + "/image-cache/" + url.PathEscape(fileName)
}

// CacheImageSources downloads or decodes all extracted image sources. A
// failed cache write is recorded as partial/failed but never blocks the
// original successful upstream response.
func CacheImageSources(ctx context.Context, request *http.Request, sources []ImageCacheSource) ImageCacheInfo {
	info := ImageCacheInfo{
		TotalCount: len(sources),
		CachedAt:   time.Now().Unix(),
	}
	for _, source := range sources {
		cachedURL, err := CacheImageSource(ctx, request, source)
		if err != nil {
			info.FailedCount++
			info.FailedReasons = append(info.FailedReasons, err.Error())
			continue
		}
		info.URLs = append(info.URLs, cachedURL)
		info.CachedCount++
	}
	if info.CachedCount == info.TotalCount && info.TotalCount > 0 {
		info.Status = "cached"
	} else if info.CachedCount > 0 {
		info.Status = "partial"
	} else if info.TotalCount > 0 {
		info.Status = "failed"
	}
	if info.CachedCount > 0 {
		info.ExpiresAt = time.Now().Add(defaultImageCacheTTL).Unix()
	}
	return info
}

// SetImageCacheInfo makes image cache metadata available to the consume-log
// builder later in the same request.
func SetImageCacheInfo(c *gin.Context, info ImageCacheInfo) {
	if c == nil || info.TotalCount <= 0 {
		return
	}
	common.SetContextKey(c, constant.ContextKeyImageCache, info)
}

// AddImageCacheInfoToOther merges request-local cache metadata into a usage
// log without changing the persisted schema.
func AddImageCacheInfoToOther(c *gin.Context, other map[string]interface{}) {
	if c == nil || other == nil {
		return
	}
	info, ok := common.GetContextKeyType[ImageCacheInfo](c, constant.ContextKeyImageCache)
	if !ok || info.TotalCount <= 0 {
		return
	}
	for key, value := range imageCacheInfoToOther(info) {
		other[key] = value
	}
}

func imageCacheInfoToOther(info ImageCacheInfo) map[string]interface{} {
	other := map[string]interface{}{
		"image_cache_count":  info.CachedCount,
		"image_cache_total":  info.TotalCount,
		"image_cache_status": info.Status,
	}
	if len(info.URLs) > 0 {
		other["image_cache_urls"] = info.URLs
	}
	if info.ExpiresAt > 0 {
		other["image_cache_expires_at"] = info.ExpiresAt
	}
	return other
}

// CacheImageSource stores one remote or inline image and returns its local
// URL. Remote downloads are SSRF-protected and written atomically.
func CacheImageSource(ctx context.Context, request *http.Request, source ImageCacheSource) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(source.DataURL) != "" {
		return cacheImageDataURL(request, source.DataURL)
	}
	if len(source.Raw) > 0 {
		return cacheImageReader(request, bytes.NewReader(source.Raw), source.MIMEType)
	}

	remoteURL := strings.TrimSpace(source.URL)
	if remoteURL == "" {
		return "", fmt.Errorf("image cache source URL is empty")
	}
	if strings.HasPrefix(strings.ToLower(remoteURL), "data:") {
		return cacheImageDataURL(request, remoteURL)
	}
	if err := ValidateImageCacheFetchURL(remoteURL); err != nil {
		return "", fmt.Errorf("image cache URL blocked: %w", err)
	}

	client := GetImageCacheHTTPClient()
	if strings.TrimSpace(source.Proxy) != "" {
		proxyClient, proxyErr := GetImageCacheHTTPClientWithProxy(source.Proxy)
		if proxyErr != nil {
			return "", fmt.Errorf("create image cache proxy client: %w", proxyErr)
		}
		client = proxyClient
	}
	if client == nil {
		client = http.DefaultClient
	}

	// A URL returned by an image API is often already signed. Sending the
	// provider API key to that URL can make some CDNs reject the request, so
	// try the plain URL first and only fall back to the provider headers when
	// the response cannot be cached.
	headerAttempts := []http.Header{nil}
	if len(source.Headers) > 0 {
		headerAttempts = append(headerAttempts, source.Headers.Clone())
	}

	var lastErr error
	for headerIndex, headers := range headerAttempts {
		for retry := 0; retry < 2; retry++ {
			if retry > 0 {
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("download image for cache: %w", ctx.Err())
				case <-time.After(defaultImageCacheRetryDelay):
				}
			}

			cachedURL, statusCode, err := downloadAndCacheImage(
				ctx,
				request,
				remoteURL,
				headers,
				client,
			)
			if err == nil {
				return cachedURL, nil
			}
			lastErr = err

			// Retry transient upstream failures with the same headers. For an
			// auth-related or non-image response, try the alternate header set.
			if retry == 0 && isRetryableImageCacheStatus(statusCode) {
				continue
			}
			break
		}

		if headerIndex == 0 && len(headerAttempts) > 1 && shouldTryImageCacheAuthFallback(lastErr) {
			continue
		}
		break
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("image cache download failed")
	}
	return "", lastErr
}

func downloadAndCacheImage(
	ctx context.Context,
	request *http.Request,
	remoteURL string,
	headers http.Header,
	client *http.Client,
) (string, int, error) {
	downloadCtx, cancel := context.WithTimeout(ctx, imageCacheDownloadTimeout())
	defer cancel()
	requestToFetch, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("create image cache request: %w", err)
	}
	requestToFetch.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/*;q=0.9")
	for key, values := range headers {
		for _, value := range values {
			requestToFetch.Header.Add(key, value)
		}
	}

	response, err := client.Do(requestToFetch)
	if err != nil {
		return "", 0, fmt.Errorf("download image for cache: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", response.StatusCode, fmt.Errorf("image cache upstream returned status %d", response.StatusCode)
	}

	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	cachedURL, err := cacheImageReader(request, response.Body, contentType)
	if err != nil {
		return "", response.StatusCode, err
	}
	return cachedURL, response.StatusCode, nil
}

func isRetryableImageCacheStatus(statusCode int) bool {
	return statusCode == 0 || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func shouldTryImageCacheAuthFallback(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status 401") ||
		strings.Contains(message, "status 403") ||
		strings.Contains(message, "unsupported image mime") ||
		strings.Contains(message, "source is empty")
}

// CacheImageDataURL decodes an inline base64 image into the local cache.
func CacheImageDataURL(request *http.Request, dataURL string) (string, error) {
	return cacheImageDataURL(request, dataURL)
}

func cacheImageDataURL(request *http.Request, dataURL string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(dataURL), ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "data:") || !strings.Contains(strings.ToLower(parts[0]), ";base64") {
		return "", fmt.Errorf("invalid base64 image data URL")
	}
	metadata := strings.TrimPrefix(parts[0], "data:")
	mimeType := strings.TrimSpace(strings.SplitN(metadata, ";", 2)[0])
	if mimeType == "" {
		mimeType = "image/png"
	}
	payload := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(parts[1]))

	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(payload)
		if err != nil {
			lastErr = err
			continue
		}
		return cacheImageReader(request, bytes.NewReader(decoded), mimeType)
	}
	return "", fmt.Errorf("decode image base64: %w", lastErr)
}

func cacheImageReader(request *http.Request, reader io.Reader, hintedMIME string) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("image cache reader is nil")
	}
	header := make([]byte, 512)
	n, readErr := io.ReadFull(reader, header)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("read image cache source: %w", readErr)
	}
	if n <= 0 {
		return "", fmt.Errorf("image cache source is empty")
	}

	mimeType := normalizeImageMIME(hintedMIME)
	extension, ok := imageCacheMimeExtensions[mimeType]
	if !ok {
		mimeType = normalizeImageMIME(http.DetectContentType(header[:n]))
		extension, ok = imageCacheMimeExtensions[mimeType]
	}
	if !ok {
		return "", fmt.Errorf("unsupported image MIME type %q", mimeType)
	}

	dir := imageCacheDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("create image cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".image-cache-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create image cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTemp := true
	defer func() {
		_ = tmp.Close()
		if cleanupTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	source := io.MultiReader(bytes.NewReader(header[:n]), reader)
	maxBytes := imageCacheMaxBytes()
	written, err := io.Copy(tmp, io.LimitReader(source, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("write image cache: %w", err)
	}
	if written > maxBytes {
		return "", fmt.Errorf("image cache exceeds %d MB limit", maxBytes/(1024*1024))
	}
	if written <= 0 {
		return "", fmt.Errorf("image cache source is empty")
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync image cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close image cache temp file: %w", err)
	}

	token, err := common.GenerateRandomCharsKey(40)
	if err != nil {
		return "", fmt.Errorf("generate image cache name: %w", err)
	}
	fileName := token + extension
	finalPath := filepath.Join(dir, fileName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", fmt.Errorf("commit image cache: %w", err)
	}
	cleanupTemp = false
	_ = os.Chmod(finalPath, 0640)
	return BuildImageCacheURL(request, fileName), nil
}

func normalizeImageMIME(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if parsed, _, err := mime.ParseMediaType(value); err == nil {
		value = parsed
	}
	return value
}

// CachedImagePath resolves only generated image names inside the image cache.
func CachedImagePath(fileName string) (path string, mimeType string, ok bool) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, `/\\`) || strings.Contains(fileName, "..") {
		return "", "", false
	}
	mimeType = ""
	for candidate, extension := range imageCacheMimeExtensions {
		if strings.EqualFold(filepath.Ext(fileName), extension) {
			mimeType = candidate
			break
		}
	}
	if mimeType == "" {
		return "", "", false
	}
	path = filepath.Join(imageCacheDir(), fileName)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", "", false
	}
	return path, mimeType, true
}

// ImageCacheFileExpired checks the retention window before the hourly cleanup
// goroutine has had a chance to remove a stale file.
func ImageCacheFileExpired(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return true
	}
	return time.Now().After(info.ModTime().Add(defaultImageCacheTTL))
}

// CleanupImageCache removes generated image files and abandoned temporary
// files older than the 2-hour retention period.
func CleanupImageCache() (int, error) {
	entries, err := os.ReadDir(imageCacheDir())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-defaultImageCacheTTL)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		isTemp := strings.HasPrefix(name, ".image-cache-")
		_, _, isImage := CachedImagePath(name)
		if !isTemp && !isImage {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(imageCacheDir(), name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// StartImageCacheCleanup runs an immediate cleanup and then checks hourly.
func StartImageCacheCleanup() {
	cleanup := func() {
		removed, err := CleanupImageCache()
		if err != nil {
			common.SysError(fmt.Sprintf("image cache cleanup failed: %v", err))
			return
		}
		if removed > 0 {
			common.SysLog(fmt.Sprintf("image cache cleanup removed %d expired files", removed))
		}
	}

	cleanup()
	go func() {
		ticker := time.NewTicker(defaultImageCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			cleanup()
		}
	}()
}

// ExtractImageSources recognizes common OpenAI/Gemini image response shapes,
// including JSON and SSE data events. It intentionally only follows image
// fields so unrelated metadata URLs are not cached.
func ExtractImageSources(body []byte, contentType string) []ImageCacheSource {
	if len(body) == 0 {
		return nil
	}
	contentType = normalizeImageMIME(contentType)
	if strings.HasPrefix(contentType, "image/") {
		return []ImageCacheSource{{Raw: append([]byte(nil), body...), MIMEType: contentType}}
	}

	var sources []ImageCacheSource
	seen := make(map[string]struct{})
	addJSON := func(raw []byte) {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return
		}
		walkImageResponse(value, "", "", &sources, seen)
	}
	if json.Valid(body) {
		addJSON(body)
		return sources
	}

	// Image streaming adaptors forward one JSON object per SSE data line.
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		addJSON([]byte(payload))
	}
	return sources
}

func walkImageResponse(value any, fieldHint, mimeHint string, sources *[]ImageCacheSource, seen map[string]struct{}) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			walkImageResponse(item, fieldHint, mimeHint, sources, seen)
		}
	case map[string]any:
		localMIME := mimeHint
		for key, item := range typed {
			normalized := normalizeImageField(key)
			if normalized == "mimetype" || normalized == "mime" {
				if value, ok := item.(string); ok {
					localMIME = value
				}
			}
		}
		for key, item := range typed {
			walkImageResponse(item, key, localMIME, sources, seen)
		}
	case string:
		value := strings.TrimSpace(typed)
		if strings.HasPrefix(strings.ToLower(value), "data:image/") {
			addImageSource(sources, seen, ImageCacheSource{DataURL: value})
			return
		}
		normalized := normalizeImageField(fieldHint)
		if isImageURLField(normalized) && isHTTPURL(value) {
			addImageSource(sources, seen, ImageCacheSource{URL: value, MIMEType: mimeHint})
			return
		}
		if isBase64ImageField(normalized) && value != "" {
			mimeType := normalizeImageMIME(mimeHint)
			if mimeType == "" {
				mimeType = "image/png"
			}
			addImageSource(sources, seen, ImageCacheSource{
				DataURL:  "data:" + mimeType + ";base64," + value,
				MIMEType: mimeType,
			})
		}
	}
}

func addImageSource(sources *[]ImageCacheSource, seen map[string]struct{}, source ImageCacheSource) {
	key := source.URL
	if key == "" {
		key = source.DataURL
	}
	if key == "" {
		key = string(source.Raw)
	}
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*sources = append(*sources, source)
}

func normalizeImageField(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func isImageURLField(field string) bool {
	switch field {
	case "url", "image", "imageurl", "outputurl", "imageuri", "fileurl", "downloadurl", "uri", "images", "outputs":
		return true
	default:
		return false
	}
}

func isBase64ImageField(field string) bool {
	switch field {
	case "b64json", "b64image", "bytesbase64encoded", "base64", "base64encoded", "imagebase64":
		return true
	default:
		return false
	}
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

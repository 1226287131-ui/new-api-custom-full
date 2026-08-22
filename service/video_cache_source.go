package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

var ErrVideoCacheExpired = errors.New("video cache retention expired")

// VideoCacheSourceForTask resolves the private provider source for a completed
// video task. Public result URLs are deliberately ignored so a cache miss can
// never make the server fetch its own public proxy endpoint.
func VideoCacheSourceForTask(task *model.Task, channel *model.Channel) (VideoCacheSource, error) {
	if task == nil || channel == nil {
		return VideoCacheSource{}, fmt.Errorf("task and channel are required")
	}

	baseURL := channel.GetBaseURL()
	if baseURL == "" && channel.Type >= 0 && channel.Type < len(constant.ChannelBaseURLs) {
		baseURL = constant.ChannelBaseURLs[channel.Type]
	}
	key := strings.TrimSpace(task.PrivateData.Key)
	if key == "" {
		key = strings.TrimSpace(channel.Key)
	}

	candidate := firstUsableVideoSource(
		task.PrivateData.UpstreamResultURL,
		task.PrivateData.ResultURL,
		task.FailReason,
		ExtractVideoResultURL(task.Data),
	)
	dataURL := ""
	if strings.HasPrefix(strings.ToLower(candidate), "data:") {
		dataURL = candidate
		candidate = ""
	}
	if dataURL == "" {
		dataURL = ExtractVideoDataURL(task.Data)
	}

	if candidate != "" {
		candidate = ResolveVideoResultURL(baseURL, candidate)
	}

	// Sora-compatible providers do not return a separate download URL. Their
	// authenticated content endpoint is a valid cache source.
	if candidate == "" && dataURL == "" {
		switch channel.Type {
		case constant.ChannelTypeOpenAI,
			constant.ChannelTypeSora,
			constant.ChannelTypeOpenAIVideo,
			constant.ChannelTypeGrokVideo,
			constant.ChannelTypeMiniMaxVideo:
			upstreamTaskID := strings.TrimSpace(task.GetUpstreamTaskID())
			if baseURL != "" && upstreamTaskID != "" {
				candidate = strings.TrimRight(baseURL, "/") + "/v1/videos/" + url.PathEscape(upstreamTaskID) + "/content"
			}
		}
	}

	if dataURL == "" && candidate == "" {
		return VideoCacheSource{}, fmt.Errorf("video cache source is unavailable for task %s", task.TaskID)
	}
	headers := videoCacheSourceHeaders(channel.Type, key)
	if !videoCacheSourceUsesChannelCredentials(baseURL, candidate) {
		// A cross-origin result URL is normally a signed CDN/object-storage URL.
		// Forwarding the provider key breaks signature validation and leaks it to
		// an unrelated host.
		headers = make(http.Header)
	}
	return VideoCacheSource{
		URL:           candidate,
		DataURL:       dataURL,
		Headers:       headers,
		Proxy:         channel.GetSetting().Proxy,
		TrustedOrigin: baseURL,
	}, nil
}

func videoCacheSourceHeaders(channelType int, key string) http.Header {
	headers := make(http.Header)
	if key == "" {
		return headers
	}

	switch channelType {
	case constant.ChannelTypeAli,
		constant.ChannelTypeMiniMax,
		constant.ChannelTypeVolcEngine,
		constant.ChannelTypeDoubaoVideo:
		headers.Set("Authorization", "Bearer "+key)
	case constant.ChannelTypeGemini:
		headers.Set("x-goog-api-key", key)
	case constant.ChannelTypeVidu:
		headers.Set("Authorization", "Token "+key)
	case constant.ChannelTypeOpenAI,
		constant.ChannelTypeSora,
		constant.ChannelTypeNewAPIVideo,
		constant.ChannelTypeOpenAIVideo,
		constant.ChannelTypeGrokVideo,
		constant.ChannelTypeMiniMaxVideo:
		headers.Set("Authorization", "Bearer "+key)
	}
	return headers
}

func videoCacheSourceUsesChannelCredentials(baseURL string, sourceURL string) bool {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" || strings.HasPrefix(strings.ToLower(sourceURL), "data:") {
		return false
	}

	source, err := url.Parse(sourceURL)
	if err != nil || !source.IsAbs() || source.Host == "" {
		return true
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !base.IsAbs() || base.Host == "" {
		return false
	}
	return strings.EqualFold(source.Host, base.Host)
}

// CacheVideoTask caches a completed task using the source resolver above.
func CacheVideoTask(ctx context.Context, task *model.Task, channel *model.Channel) (string, error) {
	if task == nil {
		return "", fmt.Errorf("task is required")
	}
	if VideoCacheExpired(task) {
		return "", ErrVideoCacheExpired
	}
	if cachedPath, ok := CachedVideoPath(task.TaskID); ok {
		return cachedPath, nil
	}
	source, err := VideoCacheSourceForTask(task, channel)
	if err != nil {
		return "", err
	}
	return CacheVideoSource(ctx, task.TaskID, source)
}

// CacheVideoTaskResult saves a completed provider result while retaining its
// private source only on the server. Callers must mark the task successful
// only after this function returns without an error.
func CacheVideoTaskResult(ctx context.Context, task *model.Task, channel *model.Channel, resultURL string) (string, error) {
	if task == nil || channel == nil {
		return "", fmt.Errorf("task and channel are required")
	}
	if VideoCacheExpired(task) {
		return "", ErrVideoCacheExpired
	}

	resultURL = strings.TrimSpace(resultURL)
	if strings.HasPrefix(strings.ToLower(resultURL), "data:") {
		return CacheVideoDataURL(ctx, task.TaskID, resultURL)
	}
	if resultURL != "" && !isLocalVideoProxySource(resultURL) {
		baseURL := channel.GetBaseURL()
		if baseURL == "" && channel.Type >= 0 && channel.Type < len(constant.ChannelBaseURLs) {
			baseURL = constant.ChannelBaseURLs[channel.Type]
		}
		task.PrivateData.UpstreamResultURL = ResolveVideoResultURL(baseURL, resultURL)
	}
	return CacheVideoTask(ctx, task, channel)
}

// MarkVideoCacheFailure records a recoverable local-cache failure. The task
// remains SUCCESS; these fields make retries survive process restarts.
func MarkVideoCacheFailure(task *model.Task, cacheErr error) {
	if task == nil || task.PrivateData.VideoCachedAt > 0 {
		return
	}
	attempts := task.PrivateData.VideoCacheAttempts + 1
	task.PrivateData.VideoCacheAttempts = attempts
	task.PrivateData.VideoCacheLastError = truncateVideoCacheError(cacheErr)
	if attempts <= videoCacheRetryMaxAttempts {
		task.PrivateData.VideoCacheNextRetryAt = time.Now().Add(videoCacheRetryDelay(attempts)).Unix()
	} else {
		task.PrivateData.VideoCacheNextRetryAt = 0
	}
}

// MarkVideoTaskCached records the first completed local cache write. It is
// intentionally not refreshed by later reads, so public links cannot extend
// the retention window.
func MarkVideoTaskCached(task *model.Task) {
	if task == nil || task.PrivateData.VideoCachedAt > 0 {
		return
	}
	task.PrivateData.VideoCachedAt = time.Now().Unix()
	task.PrivateData.VideoCacheAttempts = 0
	task.PrivateData.VideoCacheNextRetryAt = 0
	task.PrivateData.VideoCacheLastError = ""
}

// VideoCacheExpired reports whether a completed task is outside its local
// retention period. Legacy rows use their finish time because they predate the
// explicit cache timestamp.
func VideoCacheExpired(task *model.Task) bool {
	if task == nil {
		return false
	}
	cachedAt := task.PrivateData.VideoCachedAt
	if cachedAt <= 0 {
		cachedAt = task.FinishTime
	}
	if cachedAt <= 0 {
		return false
	}
	return !time.Now().Before(time.Unix(cachedAt, 0).Add(defaultVideoCacheTTL))
}

func firstUsableVideoSource(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if !isVideoSourceCandidate(candidate) || strings.Contains(candidate, "[redacted]") {
			continue
		}
		if isLocalVideoProxySource(candidate) {
			continue
		}
		return candidate
	}
	return ""
}

func isVideoSourceCandidate(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || candidate == "[redacted]" {
		return false
	}
	lower := strings.ToLower(candidate)
	if strings.HasPrefix(lower, "data:video/") {
		return true
	}
	if strings.HasPrefix(candidate, "/") {
		return true
	}
	parsed, err := url.Parse(candidate)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func isLocalVideoProxySource(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	if !strings.Contains(parsed.Path, "/video-cache/") &&
		!(strings.Contains(parsed.Path, "/v1/videos/") && strings.HasSuffix(parsed.Path, "/content")) {
		return false
	}

	// Relative URLs are generated by this application. For absolute URLs, the
	// path alone is insufficient: an upstream may expose the same OpenAI
	// content path, and that URL must remain available as a private cache source.
	if !parsed.IsAbs() && parsed.Host == "" {
		return true
	}
	server, err := url.Parse(strings.TrimSpace(system_setting.ServerAddress))
	if err != nil || !server.IsAbs() || server.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, server.Scheme) && strings.EqualFold(parsed.Host, server.Host)
}

package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractVideoResultURLPrefersResultPayload(t *testing.T) {
	body := []byte(`{
		"data": {
			"input": {"url": "https://images.example/input.png"},
			"result": {"data": [{"url": "https://upstream.example/video.mp4"}]}
		}
	}`)

	assert.Equal(t, "https://upstream.example/video.mp4", ExtractVideoResultURL(body))
}

func TestResolveVideoResultURL(t *testing.T) {
	assert.Equal(t,
		"https://upstream.example/api/video.mp4",
		ResolveVideoResultURL("https://upstream.example", "/api/video.mp4"),
	)
	assert.Equal(t,
		"https://cdn.example/video.mp4",
		ResolveVideoResultURL("https://upstream.example", "https://cdn.example/video.mp4"),
	)
	assert.Equal(t, "/api/video.mp4", ResolveVideoResultURL("not-a-url", "/api/video.mp4"))
}

func TestVideoCacheProxyPrefersDedicatedDownloadProxy(t *testing.T) {
	t.Setenv(videoCacheProxyEnv, "socks5h://video-egress:1080")

	assert.Equal(t, "socks5h://video-egress:1080", videoCacheProxy(VideoCacheSource{
		Proxy:             "http://channel-proxy.example:8080",
		UseDedicatedProxy: true,
	}))
}

func TestVideoCacheProxyKeepsUnselectedChannelOnItsOwnProxy(t *testing.T) {
	t.Setenv(videoCacheProxyEnv, "socks5h://video-egress:1080")

	assert.Equal(t, "http://channel-proxy.example:8080", videoCacheProxy(VideoCacheSource{
		Proxy: " http://channel-proxy.example:8080 ",
	}))
}

func TestVideoCacheProxyFallsBackToChannelProxyWhenDedicatedEgressIsUnavailable(t *testing.T) {
	t.Setenv(videoCacheProxyEnv, "")

	assert.Equal(t, "http://channel-proxy.example:8080", videoCacheProxy(VideoCacheSource{
		Proxy:             " http://channel-proxy.example:8080 ",
		UseDedicatedProxy: true,
	}))
}

func TestVideoResultURLFailureReason(t *testing.T) {
	assert.Equal(t,
		"upstream video generation returned no final video URL",
		VideoResultURLFailureReason("https://upstream.example/Video%20generation%20returned%20no%20final%20video%20URL"),
	)
	assert.Empty(t, VideoResultURLFailureReason("https://upstream.example/video/result"))
}

func TestNormalizeNewAPIVideoTaskResultPromotesUsableURL(t *testing.T) {
	result := &relaycommon.TaskInfo{
		Status:        model.TaskStatusInProgress,
		Url:           "/api/video.mp4",
		TerminalError: true,
	}

	normalizeNewAPIVideoTaskResult("https://upstream.example", result)

	assert.Equal(t, model.TaskStatusSuccess, result.Status)
	assert.Equal(t, "100%", result.Progress)
	assert.Equal(t, "https://upstream.example/api/video.mp4", result.Url)
	assert.False(t, result.TerminalError)
}

func TestRedactVideoResponseBodyReplacesUpstreamURLs(t *testing.T) {
	const publicURL = "https://api.example/video-cache/task_public.mp4"
	body := []byte(`{"data":{"result":{"data":[{"url":"https://upstream.example/video.mp4"}]}}}`)

	redacted := redactVideoResponseBody(body, publicURL, true)
	assert.NotContains(t, string(redacted), "upstream.example")
	assert.Equal(t, publicURL, ExtractVideoResultURL(redacted))
}

func TestRedactVideoResponseBodyHidesPendingURLs(t *testing.T) {
	body := []byte(`{"data":{"result":{"data":[{"url":"https://upstream.example/video.mp4"}]}}}`)

	redacted := redactVideoResponseBody(body, "", true)
	assert.NotContains(t, string(redacted), "upstream.example")
	assert.Empty(t, ExtractVideoResultURL(redacted))
}

func TestSanitizeNewAPIVideoTaskDataHidesProviderIdentity(t *testing.T) {
	const (
		publicTaskID   = "task_public"
		upstreamTaskID = "provider_task_123"
		publicURL      = "https://api.example/video-cache/task_public.mp4"
	)
	body := []byte(`{
		"id":"provider_task_123",
		"task_id":"provider_task_123",
		"upstream_task_id":"provider_task_123",
		"status":"SUCCESS",
		"message":"download from https://upstream.example/private/provider_task_123",
		"data":{
			"id":"provider_task_123",
			"taskId":"provider_task_123",
			"downloadUrl":"https://upstream.example/video.mp4",
			"asset":{"id":"asset_456"}
		}
	}`)

	sanitized := SanitizeNewAPIVideoTaskData(body, publicTaskID, upstreamTaskID, publicURL)
	text := string(sanitized)
	assert.NotContains(t, text, "upstream.example")
	assert.NotContains(t, text, upstreamTaskID)
	assert.NotContains(t, text, "upstream_task_id")
	assert.Contains(t, text, publicTaskID)
	assert.Contains(t, text, publicURL)
	assert.Contains(t, text, "asset_456")
}

func TestSanitizeNewAPIVideoTaskDataHidesPendingProviderData(t *testing.T) {
	body := []byte(`{"task_id":"provider_task_123","url":"https://upstream.example/video.mp4"}`)

	sanitized := SanitizeNewAPIVideoTaskData(body, "task_public", "provider_task_123", "")
	assert.JSONEq(t, `{"task_id":"task_public"}`, string(sanitized))
}

func TestSanitizeNewAPIVideoTaskDataRejectsInvalidJSON(t *testing.T) {
	assert.JSONEq(t, `{}`, string(SanitizeNewAPIVideoTaskData([]byte(`not-json`), "task_public", "provider", "")))
}

func TestCleanupVideoCacheRemovesOnlyExpiredFiles(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)

	oldPath := filepath.Join(cacheDir, "old.mp4")
	freshPath := filepath.Join(cacheDir, "fresh.mp4")
	otherPath := filepath.Join(cacheDir, "keep.txt")
	require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0600))
	require.NoError(t, os.WriteFile(freshPath, []byte("fresh"), 0600))
	require.NoError(t, os.WriteFile(otherPath, []byte("keep"), 0600))
	oldTime := time.Now().Add(-defaultVideoCacheTTL - time.Hour)
	require.NoError(t, os.Chtimes(oldPath, oldTime, oldTime))
	require.NoError(t, os.Chtimes(otherPath, oldTime, oldTime))

	removed, err := CleanupVideoCache()
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.NoFileExists(t, oldPath)
	assert.FileExists(t, freshPath)
	assert.FileExists(t, otherPath)
}

func TestVideoCacheFilePathStaysInsideCacheDirectory(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)

	path := videoCacheFilePath(`../nested\task`)
	assert.Equal(t, cacheDir, filepath.Dir(path))
	assert.False(t, strings.Contains(filepath.Base(path), ".."))
}

func TestCacheVideoDataURLWritesLocalFile(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)

	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("video-bytes"))
	path, err := CacheVideoDataURL(context.Background(), "task_data", dataURL)
	require.NoError(t, err)
	assert.FileExists(t, path)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("video-bytes"), contents)
}

func TestVideoCacheExpiredUsesFixedFirstCacheTime(t *testing.T) {
	now := time.Now().Unix()
	expired := &model.Task{
		FinishTime: now,
		PrivateData: model.TaskPrivateData{
			VideoCachedAt: now - int64(defaultVideoCacheTTL.Seconds()) - 1,
		},
	}
	fresh := &model.Task{
		FinishTime: now - int64(defaultVideoCacheTTL.Seconds()) - 1,
		PrivateData: model.TaskPrivateData{
			VideoCachedAt: now,
		},
	}

	assert.True(t, VideoCacheExpired(expired))
	assert.False(t, VideoCacheExpired(fresh))

	MarkVideoTaskCached(fresh)
	assert.Equal(t, now, fresh.PrivateData.VideoCachedAt)
}

func TestVideoCacheRetryMetadataBacksOffAndClearsOnSuccess(t *testing.T) {
	task := &model.Task{PrivateData: model.TaskPrivateData{}}
	before := time.Now().Add(29 * time.Second).Unix()
	MarkVideoCacheFailure(task, fmt.Errorf("temporary disk error"))
	assert.Equal(t, 1, task.PrivateData.VideoCacheAttempts)
	assert.GreaterOrEqual(t, task.PrivateData.VideoCacheNextRetryAt, before)
	assert.Equal(t, "temporary disk error", task.PrivateData.VideoCacheLastError)

	MarkVideoTaskCached(task)
	assert.NotZero(t, task.PrivateData.VideoCachedAt)
	assert.Zero(t, task.PrivateData.VideoCacheAttempts)
	assert.Zero(t, task.PrivateData.VideoCacheNextRetryAt)
	assert.Empty(t, task.PrivateData.VideoCacheLastError)
}

func TestRetryVideoTaskCachesSuccessfulTaskAfterTransientFailure(t *testing.T) {
	truncate(t)
	t.Setenv("VIDEO_CACHE_DIR", t.TempDir())
	channel := &model.Channel{Id: 981, Type: constant.ChannelTypeKling, Key: "channel-key", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:     "task_retry_success",
		Platform:   constant.TaskPlatform("kling"),
		ChannelId:  channel.Id,
		Status:     model.TaskStatusSuccess,
		Progress:   "100%",
		FinishTime: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamResultURL:     "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("retry-video")),
			VideoCacheAttempts:    1,
			VideoCacheNextRetryAt: time.Now().Add(-time.Minute).Unix(),
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	require.NoError(t, RetryVideoTaskCaches(context.Background()))
	var saved model.Task
	require.NoError(t, model.DB.Where("task_id = ?", task.TaskID).First(&saved).Error)
	assert.NotZero(t, saved.PrivateData.VideoCachedAt)
	assert.Zero(t, saved.PrivateData.VideoCacheAttempts)
	assert.FileExists(t, filepath.Join(videoCacheDir(), task.TaskID+".mp4"))
}

func TestCacheRemoteVideoWithHeaders(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() { *fetchSetting = originalFetchSetting })
	fetchSetting.EnableSSRFProtection = false
	InitHttpClient()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "provider-key", r.Header.Get("x-goog-api-key"))
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("remote-video"))
	}))
	defer server.Close()

	headers := make(http.Header)
	headers.Set("x-goog-api-key", "provider-key")
	path, err := CacheRemoteVideoWithHeaders(context.Background(), "task_remote", server.URL, "", headers)
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("remote-video"), contents)
}

func TestCacheVideoSourceAllowsTrustedProviderPort(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)
	configureSSRFTestFetchSetting(t)
	fetchSetting := system_setting.GetFetchSetting()
	fetchSetting.AllowPrivateIp = true

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("trusted-video"))
	}))
	defer server.Close()

	path, err := CacheVideoSource(context.Background(), "task_trusted_port", VideoCacheSource{
		URL:           server.URL + "/video.mp4",
		TrustedOrigin: server.URL,
	})
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("trusted-video"), contents)
}

func TestVideoCacheSourceForTaskUsesContentEndpointAndBearer(t *testing.T) {
	baseURL := "https://upstream.example"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeSora, Key: "channel-key", BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Equal(t, "Bearer provider-key", source.Headers.Get("Authorization"))
	assert.Equal(t, baseURL, source.TrustedOrigin)
}

func TestVideoCacheSourceForTaskPropagatesDedicatedProxySelection(t *testing.T) {
	baseURL := "https://upstream.example"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeSora, Key: "channel-key", BaseURL: &baseURL}
	channel.SetSetting(dto.ChannelSettings{VideoCacheProxyEnabled: true})

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.True(t, source.UseDedicatedProxy)
}

func TestVideoCacheSourceForTaskDoesNotSendCredentialsToExternalResultURL(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() { system_setting.ServerAddress = previousServerAddress })
	baseURL := "https://gateway.example"

	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
			ResultURL:      "https://upstream.example/v1/videos/provider-task/content",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeOpenAIVideo, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Empty(t, source.Headers.Get("Authorization"))
}

func TestVideoCacheSourceForTaskUsesGrokContentEndpoint(t *testing.T) {
	baseURL := "https://upstream.example/"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeGrokVideo, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Equal(t, "Bearer provider-key", source.Headers.Get("Authorization"))
}

func TestVideoCacheSourceForTaskUsesMiniMaxVideoContentEndpoint(t *testing.T) {
	baseURL := "https://upstream.example/"
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			Key:            "provider-key",
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeMiniMaxVideo, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
	assert.Equal(t, "Bearer provider-key", source.Headers.Get("Authorization"))
}

func TestVideoCacheSourceIgnoresNonURLFailureReason(t *testing.T) {
	baseURL := "https://upstream.example"
	task := &model.Task{
		TaskID:     "task_public",
		FailReason: "upstream reported a temporary failure",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "provider-task",
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeSora, BaseURL: &baseURL}

	source, err := VideoCacheSourceForTask(task, channel)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos/provider-task/content", source.URL)
}

func TestSanitizeVideoTaskReasonRemovesProviderURL(t *testing.T) {
	reason := "download failed: https://upstream.example/private/video.mp4 (task provider-task)"
	redacted := SanitizeVideoTaskReason(reason, "provider-task")
	assert.Equal(t, "download failed: [redacted] (task [redacted])", redacted)
}

func TestExtractVideoDataURL(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("inline-video"))
	body := []byte(`{"response":{"videos":[{"mimeType":"video/mp4","bytesBase64Encoded":"` + encoded + `"}]}}`)

	assert.Equal(t, "data:video/mp4;base64,"+encoded, ExtractVideoDataURL(body))
}

func TestSanitizeOpenAIVideoResponseAddsLocalResultURL(t *testing.T) {
	body := []byte(`{"id":"provider-task","metadata":{"url":"https://upstream.example/video.mp4"}}`)
	localURL := "https://api.example/video-cache/task_public.mp4"
	sanitized := SanitizeOpenAIVideoResponse(body, "task_public", "provider-task", localURL)

	assert.NotContains(t, string(sanitized), "upstream.example")
	assert.NotContains(t, string(sanitized), "provider-task")
	assert.Contains(t, string(sanitized), localURL)
	assert.NotContains(t, string(sanitized), "https://api.example/v1/videos")
}

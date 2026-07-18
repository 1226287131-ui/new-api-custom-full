package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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
	require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0600))
	require.NoError(t, os.WriteFile(freshPath, []byte("fresh"), 0600))
	oldTime := time.Now().Add(-defaultVideoCacheTTL - time.Hour)
	require.NoError(t, os.Chtimes(oldPath, oldTime, oldTime))

	removed, err := CleanupVideoCache()
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.NoFileExists(t, oldPath)
	assert.FileExists(t, freshPath)
}

func TestVideoCacheFilePathStaysInsideCacheDirectory(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_CACHE_DIR", cacheDir)

	path := videoCacheFilePath(`../nested\task`)
	assert.Equal(t, cacheDir, filepath.Dir(path))
	assert.False(t, strings.Contains(filepath.Base(path), ".."))
}

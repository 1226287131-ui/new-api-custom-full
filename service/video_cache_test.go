package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

package service

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestExtractImageSourcesFromOpenAIJSON(t *testing.T) {
	body := []byte(`{"data":[{"url":"https://upstream.example/one.png"},{"b64_json":"` + testPNGBase64 + `"}]}`)
	sources := ExtractImageSources(body, "application/json")

	require.Len(t, sources, 2)
	assert.Equal(t, "https://upstream.example/one.png", sources[0].URL)
	assert.Contains(t, sources[1].DataURL, "data:image/png;base64,")
}

func TestExtractImageSourcesFromSSE(t *testing.T) {
	body := []byte("event: image_generation.completed\ndata: {\"url\":\"https://upstream.example/one.png\"}\n\ndata: [DONE]\n\n")
	sources := ExtractImageSources(body, "text/event-stream")

	require.Len(t, sources, 1)
	assert.Equal(t, "https://upstream.example/one.png", sources[0].URL)
}

func TestCacheImageDataURLWritesLocalFile(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("IMAGE_CACHE_DIR", cacheDir)
	t.Setenv("IMAGE_CACHE_PUBLIC_BASE_URL", "https://api.example")

	dataURL := "data:image/png;base64," + testPNGBase64
	publicURL, err := CacheImageDataURL(nil, dataURL)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(publicURL, "https://api.example/image-cache/"))

	parsed, err := url.Parse(publicURL)
	require.NoError(t, err)
	path, mimeType, ok := CachedImagePath(filepath.Base(parsed.Path))
	require.True(t, ok)
	assert.Equal(t, "image/png", mimeType)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, testPNGBytes(t), contents)
}

func TestCacheImageDataURLAcceptsRawURLSafeBase64(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("IMAGE_CACHE_DIR", cacheDir)
	t.Setenv("IMAGE_CACHE_PUBLIC_BASE_URL", "https://api.example")

	dataURL := "data:image/png;base64," + base64.RawURLEncoding.EncodeToString(testPNGBytes(t))
	publicURL, err := CacheImageDataURL(nil, dataURL)
	require.NoError(t, err)

	parsed, err := url.Parse(publicURL)
	require.NoError(t, err)
	path, _, ok := CachedImagePath(filepath.Base(parsed.Path))
	require.True(t, ok)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, testPNGBytes(t), contents)
}

func TestCacheImageSourceDetectsImageBytesWhenContentTypeIsGeneric(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("IMAGE_CACHE_DIR", cacheDir)
	t.Setenv("IMAGE_CACHE_PUBLIC_BASE_URL", "https://api.example")

	publicURL, err := CacheImageSource(nil, nil, ImageCacheSource{
		Raw:      testPNGBytes(t),
		MIMEType: "application/octet-stream",
	})
	require.NoError(t, err)

	parsed, err := url.Parse(publicURL)
	require.NoError(t, err)
	_, mimeType, ok := CachedImagePath(filepath.Base(parsed.Path))
	require.True(t, ok)
	assert.Equal(t, "image/png", mimeType)
}

func TestCleanupImageCacheRemovesOnlyExpiredGeneratedFiles(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("IMAGE_CACHE_DIR", cacheDir)

	oldPath := filepath.Join(cacheDir, "old.png")
	freshPath := filepath.Join(cacheDir, "fresh.png")
	otherPath := filepath.Join(cacheDir, "keep.txt")
	tmpPath := filepath.Join(cacheDir, ".image-cache-old.tmp")
	for path, contents := range map[string][]byte{
		oldPath:   testPNGBytes(t),
		freshPath: testPNGBytes(t),
		otherPath: []byte("keep"),
		tmpPath:   []byte("tmp"),
	} {
		require.NoError(t, os.WriteFile(path, contents, 0600))
	}
	oldTime := time.Now().Add(-defaultImageCacheTTL - time.Hour)
	require.NoError(t, os.Chtimes(oldPath, oldTime, oldTime))
	require.NoError(t, os.Chtimes(tmpPath, oldTime, oldTime))
	require.NoError(t, os.Chtimes(otherPath, oldTime, oldTime))

	removed, err := CleanupImageCache()
	require.NoError(t, err)
	assert.Equal(t, 2, removed)
	assert.NoFileExists(t, oldPath)
	assert.NoFileExists(t, tmpPath)
	assert.FileExists(t, freshPath)
	assert.FileExists(t, otherPath)
}

func TestCachedImagePathRejectsTraversal(t *testing.T) {
	t.Setenv("IMAGE_CACHE_DIR", t.TempDir())

	_, _, ok := CachedImagePath("../secret.png")
	assert.False(t, ok)
	_, _, ok = CachedImagePath(`nested\\secret.png`)
	assert.False(t, ok)
}

func testPNGBytes(t *testing.T) []byte {
	decoded, err := base64.StdEncoding.DecodeString(testPNGBase64)
	require.NoError(t, err)
	return decoded
}

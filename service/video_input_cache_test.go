package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupVideoInputCacheRemovesOnlyExpiredFiles(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_INPUT_CACHE_DIR", cacheDir)

	oldPath := filepath.Join(cacheDir, "old.png")
	freshPath := filepath.Join(cacheDir, "fresh.png")
	require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0600))
	require.NoError(t, os.WriteFile(freshPath, []byte("fresh"), 0600))
	oldTime := time.Now().Add(-defaultVideoInputCacheTTL - time.Hour)
	require.NoError(t, os.Chtimes(oldPath, oldTime, oldTime))

	removed, err := CleanupVideoInputCache()
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.NoFileExists(t, oldPath)
	assert.FileExists(t, freshPath)
}

func TestCachedVideoInputPathRejectsUnexpectedNames(t *testing.T) {
	t.Setenv("VIDEO_INPUT_CACHE_DIR", t.TempDir())

	_, _, ok := CachedVideoInputPath("../reference.png")
	assert.False(t, ok)
	_, _, ok = CachedVideoInputPath("short.png")
	assert.False(t, ok)
	_, _, ok = CachedVideoInputPath("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.svg")
	assert.False(t, ok)
}

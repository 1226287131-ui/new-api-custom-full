package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCacheDisablesCachingForSpaRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Cache("build-hash"))
	router.GET("/keys", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/keys", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, "no-store, no-cache, must-revalidate, private, max-age=0", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
	require.Equal(t, "build-hash", recorder.Header().Get("Cache-Version"))
}

func TestCacheMarksHashedFrontendAssetsImmutable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Cache("build-hash"))
	router.GET("/static/js/index.abc123.js", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/static/js/index.abc123.js", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, "public, max-age=31536000, immutable", recorder.Header().Get("Cache-Control"))
}

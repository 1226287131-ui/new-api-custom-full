package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func Cache(version string) func(c *gin.Context) {
	return func(c *gin.Context) {
		if isImmutableFrontendAsset(c.Request.URL.Path) {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// The HTML shell and SPA routes must always be revalidated. Keeping an
			// old shell after a deployment can make it request chunks that no
			// longer exist in the embedded frontend.
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private, max-age=0")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		}
		c.Header("Cache-Version", version)
		c.Next()
	}
}

func isImmutableFrontendAsset(path string) bool {
	return path == "/static" || strings.HasPrefix(path, "/static/")
}

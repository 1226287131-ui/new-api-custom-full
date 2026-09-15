package router

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		gzip.Gzip(gzip.DefaultCompression),
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(),
		middleware.Cache(frontendCacheVersion(assets.IndexPage)),
		static.Serve("/", frontendFS),
		func(c *gin.Context) {
			path := c.Request.URL.Path
			if strings.HasPrefix(path, "/v1") || strings.HasPrefix(path, "/api") || isFrontendAssetPath(path) {
				c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private, max-age=0")
				c.Header("Pragma", "no-cache")
				c.Header("Expires", "0")
				controller.RelayNotFound(c)
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)
}

func frontendCacheVersion(indexPage []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(indexPage))
}

func isFrontendAssetPath(path string) bool {
	return path == "/static" ||
		strings.HasPrefix(path, "/static/") ||
		path == "/assets" ||
		strings.HasPrefix(path, "/assets/") ||
		path == "/logo.png" ||
		path == "/favicon.ico"
}

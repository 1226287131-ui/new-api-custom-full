package controller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// PublicImageProxy serves a locally cached generated image. The random file
// name is a short-lived capability; the handler never redirects upstream.
func PublicImageProxy(c *gin.Context) {
	fileName := c.Param("file_name")
	path, mimeType, ok := service.CachedImagePath(fileName)
	if !ok || service.ImageCacheFileExpired(path) {
		c.Status(http.StatusNotFound)
		return
	}

	file, err := os.Open(path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		c.Status(http.StatusNotFound)
		return
	}

	c.Header("Content-Type", mimeType)
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, filepath.Base(path)))
	c.Header("Cache-Control", "public, max-age=3600")
	http.ServeContent(c.Writer, c.Request, filepath.Base(path), info.ModTime(), file)
}

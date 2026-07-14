package controller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// PublicVideoInput serves short-lived reference images to video upstreams.
// Filenames contain 40 random characters and uploads still require API auth.
func PublicVideoInput(c *gin.Context) {
	path, mimeType, ok := service.CachedVideoInputPath(c.Param("file_name"))
	if !ok {
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
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to stat video input cache file: %s", err.Error()))
		c.Status(http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", mimeType)
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(path)))
	c.Header("Cache-Control", "public, max-age=43200")
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, filepath.Base(path), info.ModTime(), file)
}

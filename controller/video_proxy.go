package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// videoProxyError returns a standardized OpenAI-style error response.
func videoProxyError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

func VideoProxy(c *gin.Context) {
	videoProxy(c, false)
}

// PublicVideoProxy serves cached videos through a shareable .mp4 URL. The
// task ID is the capability; no API token is required for this route.
func PublicVideoProxy(c *gin.Context) {
	videoProxy(c, true)
}

func videoProxy(c *gin.Context, public bool) {
	taskID := c.Param("task_id")
	if public {
		fileName := strings.TrimSpace(c.Param("file_name"))
		if fileName != "" {
			if !strings.HasSuffix(fileName, ".mp4") {
				videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Video not found")
				return
			}
			taskID = strings.TrimSuffix(fileName, ".mp4")
		}
	}
	if taskID == "" {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}

	userID := c.GetInt("id")
	var task *model.Task
	var exists bool
	var err error
	if public {
		task, exists, err = model.GetByOnlyTaskId(taskID)
	} else {
		task, exists, err = model.GetByTaskId(userID, taskID)
	}
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to query task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Task not found")
		return
	}
	if task.Status != model.TaskStatusSuccess {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Task is not completed yet, current status: %s", task.Status))
		return
	}
	if service.VideoCacheExpired(task) {
		status := http.StatusGone
		if public {
			status = http.StatusNotFound
		}
		videoProxyError(c, status, "invalid_request_error", "Video cache has expired")
		return
	}

	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get channel for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to retrieve channel information")
		return
	}
	if !constant.IsVideoTaskChannelType(channel.Type) {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Video is not available for this task")
		return
	}

	if cachedPath, ok := service.CachedVideoPath(task.TaskID); ok {
		if err := serveCachedVideo(c, cachedPath); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to serve cached video for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to read cached video")
		}
		return
	}

	// Cache misses may trigger a server-side download, but the browser is
	// never redirected to a provider URL.
	if _, cacheErr := service.CacheVideoTask(c.Request.Context(), task, channel); cacheErr == nil {
		service.MarkVideoTaskCached(task)
		task.PrivateData.ResultURL = taskcommon.BuildPublicVideoURL(task.TaskID)
		if updateErr := task.Update(); updateErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to persist video cache metadata for task %s: %s", taskID, updateErr.Error()))
		}
		if cachedPath, ok := service.CachedVideoPath(task.TaskID); ok {
			if err := serveCachedVideo(c, cachedPath); err != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to serve cached video for task %s: %s", taskID, err.Error()))
				videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to read cached video")
			}
			return
		}
	}

	// Legacy Gemini/Vertex rows may not have retained a private source URL.
	// Resolve those sources once, cache them, and keep the result local.
	var fallbackURL string
	fallbackHeaders := make(http.Header)
	if channel.Type == constant.ChannelTypeGemini {
		apiKey := strings.TrimSpace(task.PrivateData.Key)
		if apiKey == "" {
			apiKey = strings.TrimSpace(channel.Key)
		}
		if apiKey != "" {
			fallbackURL, err = getGeminiVideoURL(channel, task, apiKey)
			fallbackHeaders.Set("x-goog-api-key", apiKey)
		}
	} else if channel.Type == constant.ChannelTypeVertexAi {
		fallbackURL, err = getVertexVideoURL(channel, task)
	}
	if err == nil && strings.TrimSpace(fallbackURL) != "" {
		var cacheErr error
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(fallbackURL)), "data:") {
			_, cacheErr = service.CacheVideoDataURL(c.Request.Context(), task.TaskID, fallbackURL)
		} else {
			_, cacheErr = service.CacheRemoteVideoWithHeaders(c.Request.Context(), task.TaskID, fallbackURL, channel.GetSetting().Proxy, fallbackHeaders)
			task.PrivateData.UpstreamResultURL = fallbackURL
		}
		if cacheErr == nil {
			service.MarkVideoTaskCached(task)
			task.PrivateData.ResultURL = taskcommon.BuildPublicVideoURL(task.TaskID)
			if updateErr := task.Update(); updateErr != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to persist legacy video cache source for task %s: %s", taskID, updateErr.Error()))
			}
			if cachedPath, ok := service.CachedVideoPath(task.TaskID); ok {
				if err := serveCachedVideo(c, cachedPath); err != nil {
					logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to serve cached video for task %s: %s", taskID, err.Error()))
					videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to read cached video")
				}
				return
			}
		}
		if cacheErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to cache legacy video task %s on demand: %s", taskID, cacheErr.Error()))
		}
	}

	status := http.StatusServiceUnavailable
	if public {
		status = http.StatusNotFound
	}
	videoProxyError(c, status, "server_error", "Video cache is not available")
}

func serveCachedVideo(c *gin.Context, cachedPath string) error {
	file, err := os.Open(cachedPath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	c.Writer.Header().Set("Content-Type", "video/mp4")
	c.Writer.Header().Set("Cache-Control", "public, max-age=86400")
	if c.Query("download") == "1" {
		c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(cachedPath)))
	} else {
		c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(cachedPath)))
	}
	http.ServeContent(c.Writer, c.Request, filepath.Base(cachedPath), info.ModTime(), file)
	return nil
}

// videoURLUsesUpstreamOrigin is retained for compatibility with the existing
// controller tests and callers that classify provider-origin URLs. Video
// responses themselves are cache-only and never use this helper to redirect.
func videoURLUsesUpstreamOrigin(baseURL, videoURL string) bool {
	base, baseErr := url.Parse(strings.TrimSpace(baseURL))
	video, videoErr := url.Parse(strings.TrimSpace(videoURL))
	if baseErr != nil || videoErr != nil || !base.IsAbs() || !video.IsAbs() {
		return false
	}
	return strings.EqualFold(base.Scheme, video.Scheme) && strings.EqualFold(base.Host, video.Host)
}

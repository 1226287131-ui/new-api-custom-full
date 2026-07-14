package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

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

// PublicVideoProxy serves cached NewAPI videos through a shareable .mp4 URL.
// It deliberately refuses to fall back to the upstream URL, keeping the
// upstream address private.
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

	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get channel for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to retrieve channel information")
		return
	}
	if public && channel.Type != constant.ChannelTypeNewAPIVideo {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Public video is not available")
		return
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	proxy := channel.GetSetting().Proxy
	if channel.Type == constant.ChannelTypeNewAPIVideo {
		if cachedPath, ok := service.CachedVideoPath(task.TaskID); ok {
			if err := serveCachedVideo(c, cachedPath); err != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to serve cached video for task %s: %s", taskID, err.Error()))
				videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to read cached video")
			}
			return
		}

		// Fallback for tasks completed before eager caching or a temporary cache failure.
		// Older records may only retain the provider response in task.Data.
		upstreamURL := strings.TrimSpace(task.PrivateData.UpstreamResultURL)
		if upstreamURL == "" {
			upstreamURL = service.ExtractVideoResultURL(task.Data)
		}
		upstreamURL = service.ResolveVideoResultURL(baseURL, upstreamURL)
		if upstreamURL != "" {
			if _, cacheErr := service.CacheRemoteVideo(c.Request.Context(), task.TaskID, upstreamURL, proxy, channel.Key); cacheErr == nil {
				if cachedPath, ok := service.CachedVideoPath(task.TaskID); ok {
					if err := serveCachedVideo(c, cachedPath); err != nil {
						logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to serve cached video for task %s: %s", taskID, err.Error()))
						videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to read cached video")
					}
					return
				}
			} else {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to cache video task %s on demand: %s", taskID, cacheErr.Error()))
				if public {
					videoProxyError(c, http.StatusServiceUnavailable, "server_error", "Video cache is not available yet")
					return
				}
				// If the upstream blocks this server's egress IP, let the browser
				// fetch the video directly so embedded players can still play it.
				if validateErr := service.ValidateSSRFProtectedFetchURL(upstreamURL); validateErr == nil {
					c.Redirect(http.StatusTemporaryRedirect, upstreamURL)
					return
				}
			}
		}
		if public {
			videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Video cache is not available")
			return
		}
	}

	var videoURL string
	client := service.GetSSRFProtectedHTTPClient()
	if proxy != "" {
		// 渠道代理路径的连接由代理侧建立，无法做拨号时逐 IP 校验，
		// 因此后面对 videoURL 保留请求前的一次性 SSRF 校验。
		client, err = service.GetHttpClientWithProxy(proxy)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create proxy client for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy client")
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "", nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create request: %s", err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}

	switch channel.Type {
	case constant.ChannelTypeGemini:
		apiKey := task.PrivateData.Key
		if apiKey == "" {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Missing stored API key for Gemini task %s", taskID))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "API key not stored for task")
			return
		}
		videoURL, err = getGeminiVideoURL(channel, task, apiKey)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Gemini video URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to resolve Gemini video URL")
			return
		}
		req.Header.Set("x-goog-api-key", apiKey)
	case constant.ChannelTypeVertexAi:
		videoURL, err = getVertexVideoURL(channel, task)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Vertex video URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to resolve Vertex video URL")
			return
		}
	case constant.ChannelTypeOpenAI, constant.ChannelTypeSora:
		videoURL = fmt.Sprintf("%s/v1/videos/%s/content", baseURL, task.GetUpstreamTaskID())
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	case constant.ChannelTypeNewAPIVideo:
		videoURL = strings.TrimSpace(task.PrivateData.UpstreamResultURL)
	default:
		// Video URL is stored in PrivateData.ResultURL (fallback to FailReason for old data)
		videoURL = task.GetResultURL()
	}

	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL is empty for task %s", taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}

	if strings.HasPrefix(videoURL, "data:") {
		if err := writeVideoDataURL(c, videoURL); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to decode video data URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		}
		return
	}

	var validateErr error
	if proxy == "" {
		validateErr = service.ValidateSSRFProtectedFetchURL(videoURL)
	} else {
		fetchSetting := system_setting.GetFetchSetting()
		validateErr = common.ValidateURLWithFetchSetting(videoURL, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain)
	}
	if validateErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL blocked for task %s: %v", taskID, validateErr))
		videoProxyError(c, http.StatusForbidden, "server_error", fmt.Sprintf("request blocked: %v", validateErr))
		return
	}

	req.URL, err = url.Parse(videoURL)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to parse URL %s: %s", videoURL, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to fetch video from %s: %s", videoURL, err.Error()))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Upstream returned status %d for %s", resp.StatusCode, videoURL))
		videoProxyError(c, http.StatusBadGateway, "server_error",
			fmt.Sprintf("Upstream service returned status %d", resp.StatusCode))
		return
	}

	for key, values := range resp.Header {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	c.Writer.Header().Set("Cache-Control", "public, max-age=86400")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err = io.Copy(c.Writer, resp.Body); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to stream video content: %s", err.Error()))
	}
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

func writeVideoDataURL(c *gin.Context, dataURL string) error {
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid data url")
	}

	header := parts[0]
	payload := parts[1]
	if !strings.HasPrefix(header, "data:") || !strings.Contains(header, ";base64") {
		return fmt.Errorf("unsupported data url")
	}

	mimeType := strings.TrimPrefix(header, "data:")
	mimeType = strings.TrimSuffix(mimeType, ";base64")
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	videoBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		videoBytes, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return err
		}
	}

	c.Writer.Header().Set("Content-Type", mimeType)
	c.Writer.Header().Set("Cache-Control", "public, max-age=86400")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(videoBytes)
	return err
}

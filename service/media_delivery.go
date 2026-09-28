package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
)

// MediaDeliveryEnabled is deliberately opt-in. Public builds keep the
// optional Hong Kong media delivery code available, but do not activate it
// unless the deployment explicitly enables it.
func MediaDeliveryEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("MEDIA_DELIVERY_ENABLED")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

// CachedVideoPublicURL selects the URL for a completed task without ever
// exposing the provider's original URL. Historical tasks default to US.
func CachedVideoPublicURL(task *model.Task) string {
	if task == nil {
		return ""
	}
	if MediaDeliveryEnabled() && task.PrivateData.EffectiveMediaDeliveryNode() == "hk" && strings.TrimSpace(task.PrivateData.HongKongMediaURL) != "" {
		return strings.TrimSpace(task.PrivateData.HongKongMediaURL)
	}
	return taskcommon.BuildPublicVideoURL(task.TaskID)
}

// PublishVideoTaskDelivery uploads a locally cached video to HK when the task
// snapshot requested it. Any HK outage falls back to the US URL and leaves the
// task successful; the next retry pass can publish it again.
func PublishVideoTaskDelivery(ctx context.Context, task *model.Task, localPath string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if task == nil {
		return "", fmt.Errorf("task is required")
	}
	usURL := taskcommon.BuildPublicVideoURL(task.TaskID)
	if !MediaDeliveryEnabled() {
		return usURL, nil
	}
	if task.PrivateData.EffectiveMediaDeliveryNode() != "hk" {
		return usURL, nil
	}
	path := strings.TrimSpace(localPath)
	if path == "" {
		var ok bool
		path, ok = CachedVideoPath(task.TaskID)
		if !ok {
			return usURL, fmt.Errorf("local video cache is missing")
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return usURL, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.Size() <= 0 {
		if err == nil { err = fmt.Errorf("local video cache is empty") }
		return usURL, err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return usURL, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return usURL, err
	}
	baseUpload := strings.TrimRight(strings.TrimSpace(os.Getenv("MEDIA_HK_INGEST_BASE_URL")), "/")
	publicBase := strings.TrimRight(strings.TrimSpace(os.Getenv("MEDIA_HK_PUBLIC_BASE_URL")), "/")
	token := strings.TrimSpace(os.Getenv("MEDIA_HK_INGEST_TOKEN"))
	if baseUpload == "" || publicBase == "" || token == "" {
		return usURL, fmt.Errorf("Hong Kong media delivery is not configured")
	}
	if !validMediaDeliveryBase(baseUpload) || !validMediaDeliveryBase(publicBase) {
		return usURL, fmt.Errorf("Hong Kong media delivery URL is invalid")
	}
	name := filepath.Base(task.TaskID) + ".mp4"
	uploadURL := baseUpload + "/__media_ingest/video-cache/" + url.PathEscape(name)
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPut, uploadURL, file)
	if err != nil { return usURL, err }
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Content-SHA256", hex.EncodeToString(hash.Sum(nil)))
	req.Header.Set("Content-Type", "video/mp4")
	req.ContentLength = stat.Size()
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil { return usURL, err }
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated { return usURL, fmt.Errorf("HK video upload returned status %d", resp.StatusCode) }
	publicURL := publicBase + "/video-cache/" + url.PathEscape(name)
	headReq, err := http.NewRequestWithContext(requestCtx, http.MethodHead, publicURL, nil)
	if err != nil { return usURL, err }
	headResp, err := client.Do(headReq)
	if err != nil { return usURL, err }
	headResp.Body.Close()
	if headResp.StatusCode < http.StatusOK || headResp.StatusCode >= http.StatusMultipleChoices {
		return usURL, fmt.Errorf("HK video verification returned status %d", headResp.StatusCode)
	}
	task.PrivateData.HongKongMediaURL = publicURL
	task.PrivateData.HongKongMediaUploadedAt = time.Now().Unix()
	task.PrivateData.HongKongMediaLastError = ""
	return publicURL, nil
}

func validMediaDeliveryBase(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && (parsed.Path == "" || parsed.Path == "/")
}

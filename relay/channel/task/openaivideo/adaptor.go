package openaivideo

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const normalizedRequestContextKey = "openai_video_normalized_request"

var ModelList = []string{"seedance-2.0"}

type normalizedVideoRequest struct {
	payload map[string]any
	request relaycommon.TaskSubmitReq
	action  string
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if cached, ok := c.Get(normalizedRequestContextKey); ok {
		if normalized, valid := cached.(*normalizedVideoRequest); valid {
			info.Action = normalized.action
			c.Set("task_request", normalized.request)
			return nil
		}
	}

	payload, form, err := readClientPayload(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if form != nil {
		defer form.RemoveAll()
	}

	modelName := payloadString(payload, "model")
	if modelName == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest)
	}
	prompt := strings.TrimSpace(payloadString(payload, "prompt"))
	if prompt == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("prompt field is required"), "invalid_prompt", http.StatusBadRequest)
	}

	duration, err := normalizeDuration(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_duration", http.StatusBadRequest)
	}
	ratio, resolution, size, err := normalizeVideoFormat(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_video_format", http.StatusBadRequest)
	}

	images, err := payloadStringSlice(payload, "images", "images[]")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_images", http.StatusBadRequest)
	}
	images = appendUnique(images, payloadString(payload, "image"))
	images = appendUnique(images, payloadString(payload, "input_reference"))
	videos, err := payloadStringSlice(payload, "videos", "videos[]")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_videos", http.StatusBadRequest)
	}
	audios, err := payloadStringSlice(payload, "audios", "audios[]")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_audios", http.StatusBadRequest)
	}

	if form != nil {
		for _, fileHeader := range form.File["input_reference"] {
			inputURL, cacheErr := service.CacheVideoInput(c.Request, fileHeader)
			if cacheErr != nil {
				status := http.StatusInternalServerError
				if service.IsVideoInputValidationError(cacheErr) {
					status = http.StatusBadRequest
				}
				return service.TaskErrorWrapperLocal(cacheErr, "invalid_input_reference", status)
			}
			images = appendUnique(images, inputURL)
		}
	}

	if err := validateMediaURLs("images", images); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_images", http.StatusBadRequest)
	}
	if err := validateMediaURLs("videos", videos); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_videos", http.StatusBadRequest)
	}
	if err := validateMediaURLs("audios", audios); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_audios", http.StatusBadRequest)
	}

	upstreamPayload := make(map[string]any, len(payload)+2)
	for key, value := range payload {
		upstreamPayload[key] = value
	}
	for _, key := range []string{
		"seconds", "size", "image", "input_reference", "quality", "async",
		"images[]", "videos[]", "audios[]",
	} {
		delete(upstreamPayload, key)
	}
	upstreamPayload["model"] = modelName
	upstreamPayload["prompt"] = prompt
	upstreamPayload["duration"] = duration
	upstreamPayload["ratio"] = ratio
	upstreamPayload["resolution"] = resolution
	if len(images) > 0 {
		upstreamPayload["images"] = images
	}
	if len(videos) > 0 {
		upstreamPayload["videos"] = videos
	}
	if len(audios) > 0 {
		upstreamPayload["audios"] = audios
	}

	request := relaycommon.TaskSubmitReq{
		Model:    modelName,
		Prompt:   prompt,
		Images:   images,
		Duration: duration,
		Seconds:  strconv.Itoa(duration),
		Size:     size,
		Metadata: map[string]any{
			"videos":     videos,
			"audios":     audios,
			"ratio":      ratio,
			"resolution": resolution,
		},
	}
	action := constant.TaskActionTextGenerate
	if len(images)+len(videos)+len(audios) > 0 {
		action = constant.TaskActionGenerate
	}

	info.Action = action
	c.Set("task_request", request)
	c.Set(normalizedRequestContextKey, &normalizedVideoRequest{
		payload: upstreamPayload,
		request: request,
		action:  action,
	})
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	value, ok := c.Get(normalizedRequestContextKey)
	if !ok {
		return nil
	}
	normalized, ok := value.(*normalizedVideoRequest)
	if !ok || normalized == nil || normalized.request.Duration <= 0 {
		return nil
	}
	return map[string]float64{"seconds": float64(normalized.request.Duration)}
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/videos", nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	value, ok := c.Get(normalizedRequestContextKey)
	if !ok {
		return nil, fmt.Errorf("normalized Openai Video request is missing")
	}
	normalized, ok := value.(*normalizedVideoRequest)
	if !ok || normalized == nil {
		return nil, fmt.Errorf("normalized Openai Video request is invalid")
	}

	payload := make(map[string]any, len(normalized.payload))
	for key, value := range normalized.payload {
		payload[key] = value
	}
	payload["model"] = info.UpstreamModelName

	body, err := common.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "marshal_upstream_body_failed")
	}
	return bytes.NewReader(body), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return "", body, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", body), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}
	data, _ := raw["data"].(map[string]any)
	upstreamID := firstString(raw, "id", "task_id")
	if upstreamID == "" {
		upstreamID = firstString(data, "id", "task_id")
	}
	if upstreamID == "" {
		return "", body, service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
	}

	request, _ := relaycommon.GetTaskRequest(c)
	video := dto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.Model = info.OriginModelName
	video.CreatedAt = time.Now().Unix()
	video.Seconds = request.Seconds
	video.Size = request.Size
	if status := firstString(raw, "status"); status != "" {
		video.Status = normalizeOpenAIVideoStatus(status)
	} else if status := firstString(data, "status"); status != "" {
		video.Status = normalizeOpenAIVideoStatus(status)
	}
	video.Progress = progressInt(raw, data)

	c.JSON(http.StatusOK, video)
	return upstreamID, body, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	requestURL := strings.TrimRight(baseURL, "/") + "/v1/videos/" + url.PathEscape(taskID)
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return nil, errors.Wrap(err, "unmarshal_task_result_failed")
	}
	data, _ := raw["data"].(map[string]any)
	status := firstString(raw, "status")
	if status == "" {
		status = firstString(data, "status")
	}

	result := &relaycommon.TaskInfo{
		Code:     0,
		Progress: progressString(raw, data),
		Url:      extractOpenAIVideoResultURL(raw, data),
	}
	if reason := service.VideoResultURLFailureReason(result.Url); reason != "" {
		result.Status = model.TaskStatusFailure
		result.Progress = taskcommon.ProgressComplete
		result.Reason = reason
		return result, nil
	}

	errorCode, errorMessage := responseError(raw, data)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "submitted", "created", "not_start":
		result.Status = model.TaskStatusQueued
		if result.Progress == "" {
			result.Progress = taskcommon.ProgressQueued
		}
	case "running", "processing", "in_progress":
		result.Status = model.TaskStatusInProgress
		if result.Progress == "" {
			result.Progress = taskcommon.ProgressInProgress
		}
	case "success", "succeeded", "completed":
		if (errorCode != "" || errorMessage != "") && result.Url == "" {
			result.Status = model.TaskStatusFailure
			result.Reason = errorMessage
		} else {
			result.Status = model.TaskStatusSuccess
		}
		if result.Progress == "" {
			result.Progress = taskcommon.ProgressComplete
		}
	case "failure", "failed", "error", "cancelled", "canceled":
		result.Status = model.TaskStatusFailure
		result.Progress = taskcommon.ProgressComplete
		result.Reason = errorMessage
		if result.Reason == "" {
			result.Reason = firstString(raw, "message", "detail", "reason")
			if result.Reason == "" {
				result.Reason = firstString(data, "message", "detail", "reason")
			}
		}
	default:
		if errorCode != "" || errorMessage != "" {
			result.Status = model.TaskStatusFailure
			result.Progress = taskcommon.ProgressComplete
			result.Reason = errorMessage
		} else if result.Url != "" {
			result.Status = model.TaskStatusSuccess
			result.Progress = taskcommon.ProgressComplete
		} else {
			result.Status = model.TaskStatusInProgress
			if result.Progress == "" {
				result.Progress = taskcommon.ProgressInProgress
			}
		}
	}
	if result.Status == model.TaskStatusFailure && strings.TrimSpace(result.Reason) == "" {
		result.Reason = "video generation failed"
	}
	if result.Url != "" && result.Status != model.TaskStatusFailure {
		result.Status = model.TaskStatusSuccess
		result.Progress = taskcommon.ProgressComplete
	}
	return result, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	video := dto.NewOpenAIVideo()
	video.ID = task.TaskID
	video.TaskID = task.TaskID
	video.Model = task.Properties.OriginModelName
	video.Status = task.Status.ToVideoStatus()
	video.SetProgressStr(task.Progress)
	video.CreatedAt = task.CreatedAt
	video.Seconds = task.Properties.VideoSeconds
	video.Size = task.Properties.VideoSize
	if task.Status == model.TaskStatusSuccess {
		video.CompletedAt = task.UpdatedAt
		resultURL := taskcommon.BuildPublicVideoURL(task.TaskID)
		video.ResultURL = resultURL
		video.SetMetadata("url", resultURL)
	}
	if task.Status == model.TaskStatusFailure {
		video.Error = &dto.OpenAIVideoError{
			Message: task.FailReason,
			Code:    "video_generation_failed",
		}
	}
	return common.Marshal(video)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return "openai-video"
}

func readClientPayload(c *gin.Context) (map[string]any, *multipart.Form, error) {
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, nil, err
		}
		body, err := storage.Bytes()
		if err != nil {
			return nil, nil, err
		}
		var payload map[string]any
		if err := common.Unmarshal(body, &payload); err != nil {
			return nil, nil, err
		}
		return payload, nil, nil
	}
	if strings.Contains(contentType, "multipart/form-data") {
		form, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return nil, nil, err
		}
		payload := make(map[string]any, len(form.Value))
		for key, values := range form.Value {
			if len(values) == 1 {
				payload[key] = values[0]
			} else if len(values) > 1 {
				payload[key] = values
			}
		}
		return payload, form, nil
	}
	return nil, nil, fmt.Errorf("Content-Type must be application/json or multipart/form-data")
}

func normalizeDuration(payload map[string]any) (int, error) {
	value, hasValue := payload["duration"]
	fromSeconds := false
	if !hasValue || strings.TrimSpace(fmt.Sprint(value)) == "" {
		value, hasValue = payload["seconds"]
		fromSeconds = hasValue
	}
	if !hasValue || strings.TrimSpace(fmt.Sprint(value)) == "" {
		return 5, nil
	}

	duration, err := parseInteger(value)
	if err != nil {
		return 0, fmt.Errorf("duration must be an integer")
	}
	if fromSeconds {
		switch duration {
		case 4:
			duration = 5
		case 8:
			duration = 10
		case 12:
			duration = 15
		}
	}
	if duration <= 0 || duration > relaycommon.MaxTaskDurationSeconds {
		return 0, fmt.Errorf("duration must be between 1 and %d", relaycommon.MaxTaskDurationSeconds)
	}
	return duration, nil
}

func normalizeVideoFormat(payload map[string]any) (string, string, string, error) {
	ratio := strings.ToLower(payloadString(payload, "ratio"))
	resolution := strings.ToLower(payloadString(payload, "resolution"))
	size := strings.ToLower(payloadString(payload, "size"))
	if size != "" {
		sizeRatio, sizeResolution, err := videoFormatFromSize(size)
		if err != nil {
			return "", "", "", err
		}
		if ratio == "" {
			ratio = sizeRatio
		}
		if resolution == "" {
			resolution = sizeResolution
		}
	}
	if resolution == "" {
		switch strings.ToLower(payloadString(payload, "quality")) {
		case "4k":
			resolution = "4k"
		case "1080p", "high":
			resolution = "1080p"
		default:
			resolution = "720p"
		}
	}
	if ratio == "" {
		ratio = "16:9"
	}
	if ratio != "16:9" && ratio != "9:16" && ratio != "1:1" {
		return "", "", "", fmt.Errorf("ratio must be one of 16:9, 9:16, or 1:1")
	}
	if resolution != "480p" && resolution != "720p" && resolution != "1080p" && resolution != "4k" {
		return "", "", "", fmt.Errorf("resolution must be one of 480p, 720p, 1080p, or 4k")
	}
	return ratio, resolution, canonicalSize(ratio, resolution), nil
}

func videoFormatFromSize(size string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1280x720":
		return "16:9", "720p", nil
	case "720x1280":
		return "9:16", "720p", nil
	case "1024x1024":
		return "1:1", "720p", nil
	case "1920x1080", "1792x1024":
		return "16:9", "1080p", nil
	case "1080x1920", "1024x1792":
		return "9:16", "1080p", nil
	case "3840x2160":
		return "16:9", "4k", nil
	case "2160x3840":
		return "9:16", "4k", nil
	default:
		return "", "", fmt.Errorf("size is not supported")
	}
}

func canonicalSize(ratio, resolution string) string {
	if ratio == "1:1" {
		switch resolution {
		case "4k":
			return "2160x2160"
		case "1080p":
			return "1080x1080"
		default:
			return "1024x1024"
		}
	}
	landscape := ratio == "16:9"
	width, height := 1280, 720
	switch resolution {
	case "480p":
		width, height = 854, 480
	case "1080p":
		width, height = 1920, 1080
	case "4k":
		width, height = 3840, 2160
	}
	if !landscape {
		width, height = height, width
	}
	return fmt.Sprintf("%dx%d", width, height)
}

func parseInteger(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		parsed, err := strconv.ParseInt(strconv.FormatInt(typed, 10), 10, strconv.IntSize)
		if err != nil {
			return 0, fmt.Errorf("integer is out of range")
		}
		return int(parsed), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return 0, fmt.Errorf("integer is required")
		}
		formatted := strconv.FormatFloat(typed, 'f', 0, 64)
		parsed, err := strconv.ParseInt(formatted, 10, strconv.IntSize)
		if err != nil {
			return 0, fmt.Errorf("integer is out of range")
		}
		return int(parsed), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(typed))
	default:
		return strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	}
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func payloadStringSlice(payload map[string]any, keys ...string) ([]string, error) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		var result []string
		switch typed := value.(type) {
		case []string:
			result = append(result, typed...)
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok {
					result = append(result, text)
				} else {
					return nil, fmt.Errorf("%s must contain only strings", key)
				}
			}
		case string:
			result = append(result, typed)
		default:
			return nil, fmt.Errorf("%s must be a string or an array of strings", key)
		}
		trimmed := make([]string, 0, len(result))
		for _, item := range result {
			if item = strings.TrimSpace(item); item != "" {
				trimmed = append(trimmed, item)
			}
		}
		return trimmed, nil
	}
	return nil, nil
}

func appendUnique(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return values
	}
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func validateMediaURLs(field string, values []string) error {
	for index, value := range values {
		parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("%s[%d] must be an HTTP or HTTPS URL", field, index)
		}
	}
	return nil
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func progressInt(maps ...map[string]any) int {
	for _, data := range maps {
		if data == nil {
			continue
		}
		value, ok := data["progress"]
		if !ok {
			continue
		}
		progress, err := parseInteger(value)
		if err == nil {
			if progress < 0 {
				return 0
			}
			if progress > 100 {
				return 100
			}
			return progress
		}
	}
	return 0
}

func progressString(maps ...map[string]any) string {
	progress := progressInt(maps...)
	if progress <= 0 {
		return ""
	}
	return strconv.Itoa(progress) + "%"
}

func normalizeOpenAIVideoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "submitted", "created", "not_start":
		return dto.VideoStatusQueued
	case "running", "processing", "in_progress":
		return dto.VideoStatusInProgress
	case "success", "succeeded", "completed":
		return dto.VideoStatusCompleted
	case "failure", "failed", "error", "cancelled", "canceled":
		return dto.VideoStatusFailed
	default:
		return dto.VideoStatusQueued
	}
}

func responseError(maps ...map[string]any) (string, string) {
	for _, data := range maps {
		if data == nil {
			continue
		}
		if errorValue, ok := data["error"]; ok {
			switch errorData := errorValue.(type) {
			case map[string]any:
				code := firstString(errorData, "code", "type")
				message := firstString(errorData, "message", "detail")
				if code != "" || message != "" {
					return code, message
				}
			case string:
				if message := strings.TrimSpace(errorData); message != "" {
					return "", message
				}
			}
		}
		if message := firstString(data, "error_message", "fail_reason", "reason"); message != "" {
			return firstString(data, "error_code", "code"), message
		}
	}
	return "", ""
}

func extractOpenAIVideoResultURL(raw, data map[string]any) string {
	for _, value := range []map[string]any{raw, data} {
		if candidate := firstString(value, "result_url", "video_url", "download_url", "file_url", "output_url", "url"); candidate != "" {
			return candidate
		}
	}

	for _, value := range []map[string]any{raw, data} {
		for _, key := range []string{"result", "output", "video", "videos"} {
			if candidate := findOpenAIVideoResultURL(value[key]); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func findOpenAIVideoResultURL(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if candidate := firstString(typed, "result_url", "video_url", "download_url", "file_url", "output_url", "url"); candidate != "" {
			return candidate
		}
		for _, key := range []string{"result", "output", "data", "video", "videos", "files"} {
			if candidate := findOpenAIVideoResultURL(typed[key]); candidate != "" {
				return candidate
			}
		}
	case []any:
		for _, item := range typed {
			if candidate := findOpenAIVideoResultURL(item); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

package grokvideo

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const (
	normalizedRequestContextKey = "grok_video_normalized_request"
	multipartContentTypeContext = "grok_video_multipart_content_type"
)

var ModelList = []string{
	"grok-imagine-video",
	"grok-imagine-video-1.5",
}

type normalizedVideoRequest struct {
	request             relaycommon.TaskSubmitReq
	action              string
	aspectRatio         string
	resolution          string
	inputReferenceCount int
}

// TaskAdaptor implements the native Grok video multipart contract. It is kept
// separate from the JSON-based Openai Video adaptor because Grok requires the
// optional image references to be uploaded as multipart files.
type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if cached, ok := c.Get(normalizedRequestContextKey); ok {
		if normalized, valid := cached.(*normalizedVideoRequest); valid && normalized != nil {
			info.Action = normalized.action
			c.Set("task_request", normalized.request)
			return nil
		}
	}

	if !strings.Contains(strings.ToLower(c.GetHeader("Content-Type")), "multipart/form-data") {
		return service.TaskErrorWrapperLocal(fmt.Errorf("Content-Type must be multipart/form-data"), "invalid_request", http.StatusBadRequest)
	}

	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	defer form.RemoveAll()

	modelName, _, err := formValue(form, "model")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_model", http.StatusBadRequest)
	}
	if modelName == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest)
	}
	prompt, _, err := formValue(form, "prompt")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_prompt", http.StatusBadRequest)
	}
	if prompt == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("prompt field is required"), "invalid_prompt", http.StatusBadRequest)
	}

	aspectRatio, err := normalizeAspectRatio(form)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_aspect_ratio", http.StatusBadRequest)
	}
	seconds, err := normalizeSeconds(form)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_seconds", http.StatusBadRequest)
	}
	resolution, err := normalizeResolution(form)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_resolution", http.StatusBadRequest)
	}

	if _, exists := form.Value["input_reference"]; exists {
		return service.TaskErrorWrapperLocal(fmt.Errorf("input_reference must be uploaded as a file"), "invalid_input_reference", http.StatusBadRequest)
	}
	files := form.File["input_reference"]
	for index, fileHeader := range files {
		if fileHeader == nil || fileHeader.Size == 0 {
			return service.TaskErrorWrapperLocal(fmt.Errorf("input_reference file %d is empty", index+1), "invalid_input_reference", http.StatusBadRequest)
		}
	}

	request := relaycommon.TaskSubmitReq{
		Model:    modelName,
		Prompt:   prompt,
		Duration: seconds,
		Seconds:  strconv.Itoa(seconds),
		Size:     canonicalVideoSize(aspectRatio, resolution),
		Metadata: map[string]interface{}{
			"aspect_ratio": aspectRatio,
			"resolution":   resolution,
		},
	}
	action := constant.TaskActionTextGenerate
	if len(files) > 0 {
		action = constant.TaskActionGenerate
	}

	info.Action = action
	c.Set("task_request", request)
	c.Set(normalizedRequestContextKey, &normalizedVideoRequest{
		request:             request,
		action:              action,
		aspectRatio:         aspectRatio,
		resolution:          resolution,
		inputReferenceCount: len(files),
	})
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	request, err := relaycommon.GetTaskRequest(c)
	if err != nil || request.Duration <= 0 {
		return nil
	}
	return map[string]float64{"seconds": float64(request.Duration)}
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/videos", nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	contentType, ok := c.Get(multipartContentTypeContext)
	if !ok {
		return fmt.Errorf("Grok Video multipart content type is missing")
	}
	contentTypeText, ok := contentType.(string)
	if !ok || strings.TrimSpace(contentTypeText) == "" {
		return fmt.Errorf("Grok Video multipart content type is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", contentTypeText)
	req.Header.Set("Accept", "application/json")
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	value, ok := c.Get(normalizedRequestContextKey)
	if !ok {
		return nil, fmt.Errorf("normalized Grok Video request is missing")
	}
	normalized, ok := value.(*normalizedVideoRequest)
	if !ok || normalized == nil {
		return nil, fmt.Errorf("normalized Grok Video request is invalid")
	}

	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, fmt.Errorf("parse multipart request: %w", err)
	}
	defer form.RemoveAll()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := []struct {
		name  string
		value string
	}{
		{name: "model", value: taskcommon.DefaultString(info.UpstreamModelName, normalized.request.Model)},
		{name: "prompt", value: normalized.request.Prompt},
		{name: "aspect_ratio", value: normalized.aspectRatio},
		{name: "seconds", value: normalized.request.Seconds},
		{name: "resolution", value: normalized.resolution},
	}
	for _, field := range fields {
		if err := writer.WriteField(field.name, field.value); err != nil {
			return nil, fmt.Errorf("write multipart field %s: %w", field.name, err)
		}
	}

	files := form.File["input_reference"]
	if len(files) != normalized.inputReferenceCount {
		return nil, fmt.Errorf("input_reference file count changed")
	}
	for index, fileHeader := range files {
		if fileHeader == nil || fileHeader.Size == 0 {
			return nil, fmt.Errorf("input_reference file %d is empty", index+1)
		}
		file, err := fileHeader.Open()
		if err != nil {
			return nil, fmt.Errorf("open input_reference file %d: %w", index+1, err)
		}

		fileContentType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
		if fileContentType == "" {
			fileContentType = "image/png"
		}
		partHeader := make(textproto.MIMEHeader)
		partHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name":     "input_reference",
			"filename": fileHeader.Filename,
		}))
		partHeader.Set("Content-Type", fileContentType)
		part, partErr := writer.CreatePart(partHeader)
		if partErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("create input_reference multipart part %d: %w", index+1, partErr)
		}
		_, copyErr := io.Copy(part, file)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("copy input_reference file %d: %w", index+1, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close input_reference file %d: %w", index+1, closeErr)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart request: %w", err)
	}
	c.Set(multipartContentTypeContext, writer.FormDataContentType())
	return bytes.NewReader(body.Bytes()), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return "", body, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", body), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}
	data := nestedData(raw)
	upstreamID := firstString(raw, "id", "task_id")
	if upstreamID == "" {
		upstreamID = firstString(data, "id", "task_id")
	}
	if upstreamID == "" {
		return "", body, service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
	}

	request, _ := relaycommon.GetTaskRequest(c)
	if seconds := firstValueString(raw, data, "seconds"); seconds != "" {
		if parsedSeconds, parseErr := strconv.Atoi(seconds); parseErr == nil && parsedSeconds >= 1 && parsedSeconds <= 15 {
			request.Duration = parsedSeconds
			request.Seconds = strconv.Itoa(parsedSeconds)
		}
	}
	if size := firstValueString(raw, data, "size"); size != "" {
		request.Size = size
	}
	c.Set("task_request", request)

	video := dto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.Model = info.OriginModelName
	video.CreatedAt = time.Now().Unix()
	video.Seconds = request.Seconds
	video.Size = request.Size
	video.Status = normalizeVideoStatus(firstValueString(raw, data, "status"))
	video.Progress = progressValue(raw, data)
	c.JSON(http.StatusOK, video)
	return upstreamID, body, nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return "grok-video"
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
	data := nestedData(raw)
	status := strings.ToLower(strings.TrimSpace(firstValueString(raw, data, "status")))
	_, failureReason := responseError(raw, data)

	result := &relaycommon.TaskInfo{
		Code:     0,
		Progress: progressString(raw, data),
	}
	switch status {
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
	case "completed", "complete", "success", "succeeded":
		result.Status = model.TaskStatusSuccess
		if result.Progress == "" {
			result.Progress = taskcommon.ProgressComplete
		}
	case "failed", "failure", "error", "cancelled", "canceled":
		result.Status = model.TaskStatusFailure
		result.Progress = taskcommon.ProgressComplete
		result.Reason = failureReason
	default:
		if failureReason != "" {
			result.Status = model.TaskStatusFailure
			result.Progress = taskcommon.ProgressComplete
			result.Reason = failureReason
		} else {
			result.Status = model.TaskStatusInProgress
			if result.Progress == "" {
				result.Progress = taskcommon.ProgressInProgress
			}
		}
	}
	if result.Status == model.TaskStatusFailure && result.Reason == "" {
		result.Reason = "video generation failed"
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

func formValue(form *multipart.Form, field string) (string, bool, error) {
	values, ok := form.Value[field]
	if !ok || len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", true, fmt.Errorf("%s must be supplied once", field)
	}
	return strings.TrimSpace(values[0]), true, nil
}

func normalizeAspectRatio(form *multipart.Form) (string, error) {
	aspectRatio, present, err := formValue(form, "aspect_ratio")
	if err != nil {
		return "", err
	}
	if !present || aspectRatio == "" {
		return "16:9", nil
	}
	switch aspectRatio {
	case "16:9", "9:16", "1:1", "4:3", "3:4", "2:3", "3:2":
		return aspectRatio, nil
	default:
		return "", fmt.Errorf("aspect_ratio must be one of 16:9, 9:16, 1:1, 4:3, 3:4, 2:3, or 3:2")
	}
}

func normalizeSeconds(form *multipart.Form) (int, error) {
	value, present, err := formValue(form, "seconds")
	if err != nil {
		return 0, err
	}
	if !present || value == "" {
		return 5, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 || seconds > 15 {
		return 0, fmt.Errorf("seconds must be an integer between 1 and 15")
	}
	return seconds, nil
}

func normalizeResolution(form *multipart.Form) (string, error) {
	resolution, present, err := formValue(form, "resolution")
	if err != nil {
		return "", err
	}
	if !present || resolution == "" {
		return "720p", nil
	}
	resolution = strings.ToLower(resolution)
	switch resolution {
	case "480p", "720p", "1080p":
		return resolution, nil
	default:
		return "", fmt.Errorf("resolution must be one of 480p, 720p, or 1080p")
	}
}

func canonicalVideoSize(aspectRatio, resolution string) string {
	height := 720
	switch resolution {
	case "480p":
		height = 480
	case "1080p":
		height = 1080
	}
	width := height
	switch aspectRatio {
	case "16:9":
		width = int(math.Round(float64(height) * 16 / 9))
	case "9:16":
		width = int(math.Round(float64(height) * 9 / 16))
	case "4:3":
		width = int(math.Round(float64(height) * 4 / 3))
	case "3:4":
		width = int(math.Round(float64(height) * 3 / 4))
	case "2:3":
		width = int(math.Round(float64(height) * 2 / 3))
	case "3:2":
		width = int(math.Round(float64(height) * 3 / 2))
	}
	return fmt.Sprintf("%dx%d", width, height)
}

func nestedData(raw map[string]any) map[string]any {
	if data, ok := raw["data"].(map[string]any); ok {
		return data
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

func firstValueString(primary, secondary map[string]any, keys ...string) string {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			value, ok := data[key]
			if !ok || value == nil {
				continue
			}
			switch typed := value.(type) {
			case string:
				if text := strings.TrimSpace(typed); text != "" {
					return text
				}
			case float64:
				return strconv.FormatFloat(typed, 'f', -1, 64)
			case float32:
				return strconv.FormatFloat(float64(typed), 'f', -1, 32)
			case int:
				return strconv.Itoa(typed)
			case int64:
				return strconv.FormatInt(typed, 10)
			}
		}
	}
	return ""
}

func progressValue(primary, secondary map[string]any) int {
	value := firstValueString(primary, secondary, "progress")
	if value == "" {
		return 0
	}
	progress, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func progressString(primary, secondary map[string]any) string {
	progress := progressValue(primary, secondary)
	if progress <= 0 {
		return ""
	}
	return strconv.Itoa(progress) + "%"
}

func normalizeVideoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "submitted", "created", "not_start":
		return dto.VideoStatusQueued
	case "running", "processing", "in_progress":
		return dto.VideoStatusInProgress
	case "completed", "complete", "success", "succeeded":
		return dto.VideoStatusCompleted
	case "failed", "failure", "error", "cancelled", "canceled":
		return dto.VideoStatusFailed
	default:
		return dto.VideoStatusQueued
	}
}

func responseError(primary, secondary map[string]any) (string, string) {
	for _, data := range []map[string]any{primary, secondary} {
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
		if message := firstString(data, "error_message", "fail_reason", "reason", "message", "detail"); message != "" {
			return firstString(data, "error_code", "code"), message
		}
	}
	return "", ""
}

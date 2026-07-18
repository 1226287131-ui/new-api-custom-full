package newapivideo

import (
	"bytes"
	"fmt"
	"io"
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

const normalizedRequestContextKey = "newapi_video_normalized_request"

type normalizedVideoRequest struct {
	payload map[string]interface{}
	request relaycommon.TaskSubmitReq
	action  string
}

// TaskAdaptor converts the OpenAI/Sora video contract to the JSON-only
// video-v1 contract while preserving the legacy /v1/video/generations API.
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

	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Model) == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("prompt field is required"), "invalid_prompt", http.StatusBadRequest)
	}

	payload, form, err := readClientVideoPayload(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if form != nil {
		defer form.RemoveAll()
	}

	isSoraRequest := strings.TrimSuffix(c.Request.URL.Path, "/") == "/v1/videos"
	duration, err := normalizeVideoDuration(payload, isSoraRequest)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_seconds", http.StatusBadRequest)
	}
	ratio, size, err := normalizeVideoRatio(payload, isSoraRequest)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_size", http.StatusBadRequest)
	}
	quality, err := normalizeVideoQuality(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_quality", http.StatusBadRequest)
	}

	inputReference := strings.TrimSpace(req.InputReference)
	if inputReference != "" {
		if err := validateVideoInputURL(inputReference); err != nil {
			return service.TaskErrorWrapperLocal(err, "invalid_input_reference", http.StatusBadRequest)
		}
	}
	if form != nil {
		files := form.File["input_reference"]
		if len(files) > 1 {
			return service.TaskErrorWrapperLocal(fmt.Errorf("only one input_reference file is supported"), "invalid_input_reference", http.StatusBadRequest)
		}
		if len(files) == 1 {
			inputReference, err = service.CacheVideoInput(c.Request, files[0])
			if err != nil {
				status := http.StatusInternalServerError
				if service.IsVideoInputValidationError(err) {
					status = http.StatusBadRequest
				}
				return service.TaskErrorWrapperLocal(err, "invalid_input_reference", status)
			}
		}
	}

	payload["prompt"] = strings.TrimSpace(req.Prompt)
	payload["duration"] = duration
	payload["ratio"] = ratio
	payload["quality"] = quality
	payload["async"] = true
	delete(payload, "seconds")
	delete(payload, "size")
	delete(payload, "input_reference")
	if inputReference != "" {
		payload["image"] = inputReference
		req.Image = inputReference
		req.Images = []string{inputReference}
	} else if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		req.Images = []string{strings.TrimSpace(req.Image)}
	}

	req.Duration = duration
	req.Seconds = strconv.Itoa(duration)
	req.Size = size
	action := constant.TaskActionTextGenerate
	if len(req.Images) > 0 {
		action = constant.TaskActionGenerate
	}
	info.Action = action
	c.Set("task_request", req)
	c.Set(normalizedRequestContextKey, &normalizedVideoRequest{
		payload: payload,
		request: req,
		action:  action,
	})
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil || req.Duration <= 0 {
		return nil
	}
	return map[string]float64{"seconds": float64(req.Duration)}
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/video/generations", nil
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
		return nil, fmt.Errorf("normalized video request is missing")
	}
	normalized, ok := value.(*normalizedVideoRequest)
	if !ok || normalized == nil {
		return nil, fmt.Errorf("normalized video request is invalid")
	}

	payload := make(map[string]interface{}, len(normalized.payload)+1)
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

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var raw map[string]interface{}
	if err := common.Unmarshal(body, &raw); err != nil {
		return "", body, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", body), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}

	taskID = firstString(raw, "task_id", "id")
	if taskID == "" {
		if data, ok := raw["data"].(map[string]interface{}); ok {
			taskID = firstString(data, "task_id", "id")
		}
	}
	if taskID == "" {
		return "", body, service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
	}

	response := map[string]interface{}{
		"id":         info.PublicTaskID,
		"task_id":    info.PublicTaskID,
		"object":     "video",
		"model":      info.OriginModelName,
		"status":     "queued",
		"progress":   0,
		"created_at": time.Now().Unix(),
	}
	if req, requestErr := relaycommon.GetTaskRequest(c); requestErr == nil {
		response["seconds"] = req.Seconds
		response["size"] = req.Size
	}
	c.JSON(http.StatusOK, response)
	return taskID, body, nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return []string{"video-v1"}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "newapi-video"
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]interface{}, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/video/generations/" + taskID
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
	var raw map[string]interface{}
	if err := common.Unmarshal(body, &raw); err != nil {
		return nil, errors.Wrap(err, "unmarshal_task_result_failed")
	}

	data := raw
	if nested, ok := raw["data"].(map[string]interface{}); ok {
		data = nested
	}

	status := strings.ToUpper(strings.TrimSpace(firstString(raw, "status")))
	if status == "" {
		status = strings.ToUpper(strings.TrimSpace(firstString(data, "status")))
	}

	result := &relaycommon.TaskInfo{
		Code:     0,
		Progress: progressString(raw, data),
		Url:      firstString(raw, "result_url", "video_url", "url"),
	}
	if result.Url == "" {
		result.Url = firstString(data, "result_url", "video_url", "url")
	}
	errorCode := videoErrorCode(raw, data)
	terminalError := isTerminalVideoErrorCode(errorCode)
	if invalidResultReason := service.VideoResultURLFailureReason(result.Url); invalidResultReason != "" {
		result.Status = model.TaskStatusFailure
		result.Reason = invalidResultReason
		result.Progress = "100%"
		return result, nil
	}

	switch status {
	case "SUCCESS", "SUCCEEDED", "COMPLETED":
		if terminalError && result.Url == "" {
			result.Status = model.TaskStatusInProgress
			result.TerminalError = true
			result.Reason = terminalVideoErrorReason(errorCode)
		} else {
			result.Status = model.TaskStatusSuccess
		}
		if result.Progress == "" {
			result.Progress = "100%"
		}
	case "FAILURE", "FAILED", "ERROR", "CANCELLED":
		result.Status = model.TaskStatusFailure
		result.Reason = errorMessage(raw, data)
		if result.Reason == "" {
			result.Reason = "video generation failed"
		}
		if result.Progress == "" {
			result.Progress = "100%"
		}
	case "QUEUED", "PENDING", "NOT_START", "SUBMITTED", "CREATED":
		result.Status = model.TaskStatusQueued
		if result.Progress == "" {
			result.Progress = "10%"
		}
	case "RUNNING", "PROCESSING", "IN_PROGRESS":
		result.Status = model.TaskStatusInProgress
		if terminalError {
			result.TerminalError = true
			result.Reason = terminalVideoErrorReason(errorCode)
		}
		if result.Progress == "" {
			result.Progress = "50%"
		}
	default:
		result.Status = model.TaskStatusInProgress
		if terminalError {
			result.TerminalError = true
			result.Reason = terminalVideoErrorReason(errorCode)
		}
		if result.Progress == "" {
			result.Progress = "50%"
		}
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
		if resultURL := strings.TrimSpace(taskcommon.BuildPublicVideoURL(task.TaskID)); resultURL != "" {
			video.ResultURL = resultURL
			video.SetMetadata("url", resultURL)
		}
	}
	if task.Status == model.TaskStatusFailure {
		video.Error = &dto.OpenAIVideoError{
			Message: task.FailReason,
			Code:    "video_generation_failed",
		}
	}
	return common.Marshal(video)
}

func readClientVideoPayload(c *gin.Context) (map[string]interface{}, *multipart.Form, error) {
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
		var payload map[string]interface{}
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
		payload := make(map[string]interface{}, len(form.Value))
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

func normalizeVideoDuration(payload map[string]interface{}, soraRequest bool) (int, error) {
	value, hasDuration := payload["duration"]
	field := "duration"
	if !hasDuration {
		value, hasDuration = payload["seconds"]
		field = "seconds"
	}
	if !hasDuration || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
		return 5, nil
	}

	duration, err := parseVideoInteger(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be one of 4, 8, 12, 5, 10, or 15", field)
	}
	if field == "seconds" {
		switch duration {
		case 4:
			return 5, nil
		case 8:
			return 10, nil
		case 12:
			return 15, nil
		}
	}
	if duration == 5 || duration == 10 || duration == 15 {
		return duration, nil
	}
	if soraRequest {
		return 0, fmt.Errorf("seconds must be one of 4, 8, or 12")
	}
	return 0, fmt.Errorf("duration must be one of 5, 10, or 15")
}

func parseVideoInteger(value interface{}) (int, error) {
	var raw string
	switch typed := value.(type) {
	case string:
		raw = strings.TrimSpace(typed)
	case float64:
		raw = strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		raw = strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return typed, nil
	case int64:
		raw = strconv.FormatInt(typed, 10)
	default:
		raw = strings.TrimSpace(fmt.Sprint(value))
	}
	return strconv.Atoi(raw)
}

func normalizeVideoRatio(payload map[string]interface{}, soraRequest bool) (string, string, error) {
	if rawRatio := payloadString(payload, "ratio"); rawRatio != "" {
		switch strings.ToLower(rawRatio) {
		case "16:9":
			return "16:9", "1280x720", nil
		case "9:16":
			return "9:16", "720x1280", nil
		case "1:1":
			return "1:1", "1024x1024", nil
		default:
			return "", "", fmt.Errorf("ratio must be one of 16:9, 9:16, or 1:1")
		}
	}

	size := strings.ToLower(payloadString(payload, "size"))
	switch size {
	case "1280x720", "1792x1024", "1920x1080":
		return "16:9", size, nil
	case "720x1280", "1024x1792", "1080x1920":
		return "9:16", size, nil
	case "720x720", "1024x1024":
		return "1:1", size, nil
	case "16:9":
		return "16:9", "1280x720", nil
	case "9:16":
		return "9:16", "720x1280", nil
	case "1:1":
		return "1:1", "1024x1024", nil
	case "":
		if soraRequest {
			return "9:16", "720x1280", nil
		}
		return "16:9", "1280x720", nil
	default:
		return "", "", fmt.Errorf("size is not supported")
	}
}

func normalizeVideoQuality(payload map[string]interface{}) (string, error) {
	quality := strings.ToLower(payloadString(payload, "quality"))
	if quality == "" {
		return "hd", nil
	}
	if quality != "hd" && quality != "sd" {
		return "", fmt.Errorf("quality must be hd or sd")
	}
	return quality, nil
}

func payloadString(payload map[string]interface{}, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func validateVideoInputURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("input_reference must be an HTTP or HTTPS URL when sent as JSON")
	}
	return nil
}

func firstString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return typed
				}
			case float64:
				return strconv.FormatInt(int64(typed), 10)
			}
		}
	}
	return ""
}

func progressString(raw, data map[string]interface{}) string {
	value := raw["progress"]
	if value == nil {
		value = data["progress"]
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return ""
		}
		if strings.HasSuffix(typed, "%") {
			return typed
		}
		return typed + "%"
	case float64:
		return strconv.Itoa(int(typed)) + "%"
	default:
		return ""
	}
}

func errorMessage(raw, data map[string]interface{}) string {
	for _, values := range []map[string]interface{}{raw, data} {
		if message := firstString(values, "message"); message != "" {
			return message
		}
		if message, ok := values["error"].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
		if errValue, ok := values["error"].(map[string]interface{}); ok {
			if message := firstString(errValue, "message", "detail"); message != "" {
				return message
			}
		}
	}
	return ""
}

func videoErrorCode(raw, data map[string]interface{}) string {
	for _, values := range []map[string]interface{}{raw, data} {
		if errValue, ok := values["error"].(map[string]interface{}); ok {
			if code := firstString(errValue, "code", "type"); code != "" {
				return strings.ToLower(strings.TrimSpace(code))
			}
		}
		if code := firstString(values, "error_code"); code != "" {
			return strings.ToLower(strings.TrimSpace(code))
		}
	}
	return ""
}

func isTerminalVideoErrorCode(code string) bool {
	switch code {
	case "generation_failed", "video_generation_failed", "moderation_failed", "content_policy_violation", "cancelled":
		return true
	default:
		return false
	}
}

func terminalVideoErrorReason(code string) string {
	if code == "" {
		return "upstream reported a terminal error without a usable video result"
	}
	return "upstream reported " + code + " without a usable video result"
}

package minimaxvideo

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/openaivideo"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const (
	normalizedRequestContextKey = "minimax_video_normalized_request"
	maxReferenceImages          = 5
)

var ModelList = []string{
	"MiniMax-H3-933-1440P-GF",
}

var supportedSizes = map[string]struct{}{
	"3360x1440": {},
	"2560x1440": {},
	"1920x1440": {},
	"1440x1440": {},
	"1440x1920": {},
	"1440x2560": {},
}

type normalizedVideoRequest struct {
	payload map[string]any
	request relaycommon.TaskSubmitReq
	action  string
}

// TaskAdaptor implements the MiniMax Video JSON contract. It deliberately
// stays separate from Openai Video because MiniMax uses exact 1440p size
// values and supports image references only.
type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey          string
	baseURL         string
	responseAdaptor openaivideo.TaskAdaptor
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.responseAdaptor.Init(info)
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if cached, ok := c.Get(normalizedRequestContextKey); ok {
		if normalized, valid := cached.(*normalizedVideoRequest); valid && normalized != nil {
			info.Action = normalized.action
			c.Set("task_request", normalized.request)
			return nil
		}
	}

	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		return service.TaskErrorWrapperLocal(fmt.Errorf("Content-Type must be application/json"), "invalid_request", http.StatusBadRequest)
	}

	payload := make(map[string]any)
	if err := common.UnmarshalBodyReusable(c, &payload); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if err := validateSupportedFields(payload); err != nil {
		return service.TaskErrorWrapperLocal(err, "unsupported_parameter", http.StatusBadRequest)
	}

	modelName, err := requiredString(payload, "model")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "missing_model", http.StatusBadRequest)
	}
	prompt, err := requiredString(payload, "prompt")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_prompt", http.StatusBadRequest)
	}
	seconds, err := parseSeconds(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_seconds", http.StatusBadRequest)
	}
	size, err := parseSize(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_size", http.StatusBadRequest)
	}
	audio, err := parseAudio(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_audio", http.StatusBadRequest)
	}
	images, err := parseImages(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_images", http.StatusBadRequest)
	}

	upstreamPayload := map[string]any{
		"model":  modelName,
		"prompt": prompt,
		// The published schema labels seconds as a number, but the upstream
		// decoder currently requires a string. Accept both downstream forms and
		// always emit the upstream-compatible representation.
		"seconds": strconv.Itoa(seconds),
		"size":    size,
		"audio":   audio,
	}
	if len(images) > 0 {
		upstreamPayload["images"] = images
	}

	request := relaycommon.TaskSubmitReq{
		Model:    modelName,
		Prompt:   prompt,
		Images:   images,
		Duration: seconds,
		Seconds:  strconv.Itoa(seconds),
		Size:     size,
		Metadata: map[string]any{
			"resolution":            "1440p",
			"audio":                 audio,
			"reference_image_count": len(images),
		},
	}
	action := constant.TaskActionTextGenerate
	if len(images) > 0 {
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
	request, err := relaycommon.GetTaskRequest(c)
	if err != nil || request.Duration <= 0 {
		return nil
	}
	return map[string]float64{"seconds": float64(request.Duration)}
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
		return nil, fmt.Errorf("normalized MiniMax Video request is missing")
	}
	normalized, ok := value.(*normalizedVideoRequest)
	if !ok || normalized == nil {
		return nil, fmt.Errorf("normalized MiniMax Video request is invalid")
	}

	payload := make(map[string]any, len(normalized.payload))
	for key, value := range normalized.payload {
		payload[key] = value
	}
	payload["model"] = taskcommon.DefaultString(info.UpstreamModelName, normalized.request.Model)

	body, err := common.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "marshal MiniMax Video request")
	}
	return bytes.NewReader(body), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError) {
	return a.responseAdaptor.DoResponse(c, resp, info)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return "minimax-video"
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	return a.responseAdaptor.FetchTask(baseURL, key, body, proxy)
}

func (a *TaskAdaptor) ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	return a.responseAdaptor.ParseTaskResult(body)
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	return a.responseAdaptor.ConvertToOpenAIVideo(task)
}

func validateSupportedFields(payload map[string]any) error {
	for _, field := range []string{"video_urls", "audio_urls", "file_urls", "videos", "audios", "files"} {
		if _, exists := payload[field]; exists {
			return fmt.Errorf("%s is not supported; MiniMax Video accepts image references through images only", field)
		}
	}
	for field := range payload {
		switch field {
		case "model", "prompt", "seconds", "size", "audio", "images":
		default:
			return fmt.Errorf("%s is not supported by MiniMax Video", field)
		}
	}
	return nil
}

func requiredString(payload map[string]any, field string) (string, error) {
	value, exists := payload[field]
	if !exists || value == nil {
		return "", fmt.Errorf("%s field is required", field)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%s field is required", field)
	}
	return text, nil
}

func parseSeconds(payload map[string]any) (int, error) {
	value, exists := payload["seconds"]
	if !exists || value == nil {
		return 0, fmt.Errorf("seconds field is required")
	}

	var seconds int
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, fmt.Errorf("seconds must be an integer between 5 and 15")
		}
		if math.Trunc(typed) != typed {
			return 0, fmt.Errorf("seconds must be an integer between 5 and 15")
		}
		seconds = int(typed)
	case float32:
		value := float64(typed)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("seconds must be an integer between 5 and 15")
		}
		if math.Trunc(value) != value {
			return 0, fmt.Errorf("seconds must be an integer between 5 and 15")
		}
		seconds = int(value)
	case int:
		seconds = typed
	case int64:
		if typed > math.MaxInt || typed < math.MinInt {
			return 0, fmt.Errorf("seconds is out of range")
		}
		seconds = int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, fmt.Errorf("seconds must be an integer between 5 and 15")
		}
		seconds = parsed
	default:
		return 0, fmt.Errorf("seconds must be a number between 5 and 15")
	}

	if seconds < 5 || seconds > 15 {
		return 0, fmt.Errorf("seconds must be between 5 and 15")
	}
	return seconds, nil
}

func parseSize(payload map[string]any) (string, error) {
	size, err := requiredString(payload, "size")
	if err != nil {
		return "", err
	}
	size = strings.ToLower(size)
	if _, ok := supportedSizes[size]; !ok {
		return "", fmt.Errorf("size must be one of 3360x1440, 2560x1440, 1920x1440, 1440x1440, 1440x1920, or 1440x2560")
	}
	return size, nil
}

func parseAudio(payload map[string]any) (bool, error) {
	value, exists := payload["audio"]
	if !exists || value == nil {
		return true, nil
	}
	audio, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("audio must be a boolean")
	}
	return audio, nil
}

func parseImages(payload map[string]any) ([]string, error) {
	value, exists := payload["images"]
	if !exists || value == nil {
		return nil, nil
	}

	var images []string
	switch typed := value.(type) {
	case []any:
		images = make([]string, 0, len(typed))
		for index, item := range typed {
			image, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("images[%d] must be a string URL", index)
			}
			images = append(images, strings.TrimSpace(image))
		}
	case []string:
		images = make([]string, 0, len(typed))
		for _, image := range typed {
			images = append(images, strings.TrimSpace(image))
		}
	default:
		return nil, fmt.Errorf("images must be an array of image URLs")
	}

	if len(images) > maxReferenceImages {
		return nil, fmt.Errorf("images supports at most %d reference images", maxReferenceImages)
	}
	for index, image := range images {
		parsed, err := url.ParseRequestURI(image)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("images[%d] must be an HTTP or HTTPS URL", index)
		}
	}
	return images, nil
}

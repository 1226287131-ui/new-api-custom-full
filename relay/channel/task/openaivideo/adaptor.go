package openaivideo

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
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
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const normalizedRequestContextKey = "openai_video_normalized_request"

const (
	defaultMaxReferenceImages = 9
	defaultMaxReferenceVideos = 3
	defaultMaxReferenceAudios = 3

	sd25MaxReferenceImages = 30
	sd25MaxReferenceVideos = 10
	sd25MaxReferenceAudios = 10
)

var ModelList = []string{"seedance-2.0", "video-v3"}

type videoProfile struct {
	name                 string
	defaultDuration      int
	minDuration          int
	maxDuration          int
	allowedDurations     []int
	maxReferenceImages   int
	maxReferenceVideos   int
	maxReferenceAudios   int
	forceResolution      string
	allowAutoRatio       bool
	nativeMultimodalMode bool
}

var defaultVideoProfile = videoProfile{
	name:               "default",
	defaultDuration:    5,
	minDuration:        5,
	maxDuration:        15,
	allowedDurations:   []int{5, 10, 15},
	maxReferenceImages: defaultMaxReferenceImages,
	maxReferenceVideos: defaultMaxReferenceVideos,
	maxReferenceAudios: defaultMaxReferenceAudios,
}

var seedance25VideoProfile = videoProfile{
	name:                 "seedance-2.5",
	defaultDuration:      4,
	minDuration:          4,
	maxDuration:          30,
	maxReferenceImages:   sd25MaxReferenceImages,
	maxReferenceVideos:   sd25MaxReferenceVideos,
	maxReferenceAudios:   sd25MaxReferenceAudios,
	forceResolution:      "720p",
	allowAutoRatio:       true,
	nativeMultimodalMode: true,
}

type multimodalContent struct {
	items  []any
	prompt string
	images []string
	videos []string
	audios []string
}

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

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
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
	mappedModelName, _, err := helper.ResolveModelMapping(
		c.GetString("model_mapping"),
		modelName,
	)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "model_mapping_failed", http.StatusBadRequest)
	}
	profile := videoProfileForRequest(info, modelName, mappedModelName)

	duration, err := normalizeDuration(payload, profile)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_duration", http.StatusBadRequest)
	}
	ratio, resolution, size, err := normalizeVideoFormat(payload, profile)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_video_format", http.StatusBadRequest)
	}

	prompt := strings.TrimSpace(payloadString(payload, "prompt"))
	images, err := collectPayloadStringSlices(payload, "images", "images[]", "reference_image_urls")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_images", http.StatusBadRequest)
	}
	images = appendUnique(images, payloadString(payload, "image"))
	videos, err := collectPayloadStringSlices(payload, "videos", "videos[]", "reference_videos")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_videos", http.StatusBadRequest)
	}
	audios, err := collectPayloadStringSlices(payload, "audios", "audios[]", "reference_audios")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_audios", http.StatusBadRequest)
	}
	inputReferences, err := collectPayloadStringSlices(payload, "input_reference", "input_reference[]")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_input_reference", http.StatusBadRequest)
	}
	if profile.nativeMultimodalMode {
		images, videos, audios = appendSD25InputReferences(images, videos, audios, inputReferences)
	} else {
		for _, inputReference := range inputReferences {
			images = appendUnique(images, inputReference)
		}
	}

	var nativeContent *multimodalContent
	if profile.nativeMultimodalMode {
		nativeContent, err = parseMultimodalContent(payload)
		if err != nil {
			return service.TaskErrorWrapperLocal(err, "invalid_content", http.StatusBadRequest)
		}
		if nativeContent != nil {
			hasUploadedReference := form != nil && len(form.File["input_reference"]) > 0
			if prompt != "" || len(images) > 0 || len(videos) > 0 || len(audios) > 0 || hasUploadedReference {
				return service.TaskErrorWrapperLocal(
					fmt.Errorf("content cannot be combined with prompt or top-level reference media"),
					"invalid_content",
					http.StatusBadRequest,
				)
			}
			prompt = nativeContent.prompt
			images = nativeContent.images
			videos = nativeContent.videos
			audios = nativeContent.audios
		}
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

	if len(images) > profile.maxReferenceImages {
		return service.TaskErrorWrapperLocal(fmt.Errorf("images supports at most %d reference images", profile.maxReferenceImages), "invalid_images", http.StatusBadRequest)
	}
	if len(videos) > profile.maxReferenceVideos {
		return service.TaskErrorWrapperLocal(fmt.Errorf("videos supports at most %d reference videos", profile.maxReferenceVideos), "invalid_videos", http.StatusBadRequest)
	}
	if len(audios) > profile.maxReferenceAudios {
		return service.TaskErrorWrapperLocal(fmt.Errorf("audios supports at most %d reference audios", profile.maxReferenceAudios), "invalid_audios", http.StatusBadRequest)
	}

	fast720 := isFast720Model(mappedModelName)
	if prompt == "" && (!fast720 || len(images) == 0) {
		return service.TaskErrorWrapperLocal(fmt.Errorf("prompt field is required unless the fast-720p model receives at least one image"), "invalid_prompt", http.StatusBadRequest)
	}

	generateAudio, hasGenerateAudio, err := payloadBool(payload, "generate_audio")
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_generate_audio", http.StatusBadRequest)
	}
	if fast720 && (generateAudio || len(videos) > 0 || len(audios) > 0) {
		return service.TaskErrorWrapperLocal(fmt.Errorf("fast-720p does not support generate_audio, videos, or audios"), "invalid_fast_720p_parameters", http.StatusBadRequest)
	}
	if err := validateOptionalVideoParameters(payload); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_video_parameters", http.StatusBadRequest)
	}
	if fast720 && resolution != "720p" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("fast-720p only supports 720p resolution"), "invalid_video_format", http.StatusBadRequest)
	}
	if fast720 {
		resolution = "720p"
		size = canonicalSize(ratio, resolution)
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
	usesMultimodalContent := profile.nativeMultimodalMode && (nativeContent != nil || len(videos) > 0 || len(audios) > 0)
	var upstreamContent []any
	if usesMultimodalContent {
		if nativeContent != nil {
			upstreamContent = nativeContent.items
		} else {
			upstreamContent = buildMultimodalContent(prompt, images, videos, audios)
		}
	}

	upstreamPayload := make(map[string]any, len(payload)+6)
	for key, value := range payload {
		upstreamPayload[key] = value
	}
	for _, key := range []string{
		"seconds", "size", "image", "input_reference", "quality", "async",
		"aspect_ratio", "images[]", "videos[]", "audios[]",
		"input_reference[]", "reference_image_urls", "reference_videos", "reference_audios",
	} {
		delete(upstreamPayload, key)
	}
	if profile.nativeMultimodalMode {
		for _, key := range []string{"content", "images", "videos", "audios"} {
			delete(upstreamPayload, key)
		}
	}
	upstreamPayload["model"] = modelName
	upstreamPayload["duration"] = duration
	upstreamPayload["ratio"] = ratio
	upstreamPayload["resolution"] = resolution
	if hasGenerateAudio {
		if fast720 {
			delete(upstreamPayload, "generate_audio")
		} else {
			upstreamPayload["generate_audio"] = generateAudio
		}
	}
	if usesMultimodalContent {
		upstreamPayload["content"] = upstreamContent
		// The SD2.5 gateway requires a top-level prompt even when content carries text.
		upstreamPayload["prompt"] = prompt
	} else {
		if prompt != "" {
			upstreamPayload["prompt"] = prompt
		} else {
			delete(upstreamPayload, "prompt")
		}
		if len(images) > 0 {
			upstreamPayload["images"] = images
		}
		if len(videos) > 0 {
			upstreamPayload["videos"] = videos
		}
		if len(audios) > 0 {
			upstreamPayload["audios"] = audios
		}
	}

	request := relaycommon.TaskSubmitReq{
		Model:    modelName,
		Prompt:   prompt,
		Images:   images,
		Duration: duration,
		Seconds:  strconv.Itoa(duration),
		Size:     size,
		Metadata: map[string]any{
			"videos":        videos,
			"audios":        audios,
			"ratio":         ratio,
			"resolution":    resolution,
			"fast_720p":     fast720,
			"video_profile": profile.name,
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

func videoProfileForRequest(info *relaycommon.RelayInfo, modelNames ...string) videoProfile {
	if info != nil && info.ChannelMeta != nil {
		switch normalizeOpenAIVideoProfile(info.ChannelSetting.OpenAIVideoProfile) {
		case "seedance-2.5":
			return seedance25VideoProfile
		case "default":
			return defaultVideoProfile
		}
	}
	return videoProfileForModels(modelNames...)
}

func normalizeOpenAIVideoProfile(profile string) string {
	normalized := strings.ToLower(strings.TrimSpace(profile))
	normalized = strings.NewReplacer("_", "-", " ", "").Replace(normalized)
	switch normalized {
	case "seedance-2.5", "seedance2.5", "sd-2.5", "sd2.5", "video-v3":
		return "seedance-2.5"
	case "default", "seedance-2.0", "seedance2.0", "sd-2.0", "sd2.0":
		return "default"
	default:
		return ""
	}
}

func videoProfileForModels(modelNames ...string) videoProfile {
	for _, modelName := range modelNames {
		if isSeedance25ModelName(modelName) {
			return seedance25VideoProfile
		}
	}
	return defaultVideoProfile
}

func isSeedance25ModelName(modelName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	normalized = strings.NewReplacer("_", "-", " ", "").Replace(normalized)
	switch normalized {
	case "video-v3", "seedance-2.5", "seedance2.5", "sd-2.5", "sd2.5":
		return true
	default:
		return false
	}
}

func normalizeDuration(payload map[string]any, profile videoProfile) (int, error) {
	value, hasValue := payload["duration"]
	if !hasValue || strings.TrimSpace(fmt.Sprint(value)) == "" {
		value, hasValue = payload["seconds"]
	}
	if !hasValue || strings.TrimSpace(fmt.Sprint(value)) == "" {
		return profile.defaultDuration, nil
	}

	duration, err := parseInteger(value)
	if err != nil {
		return 0, fmt.Errorf("duration must be an integer")
	}
	if len(profile.allowedDurations) > 0 {
		for _, allowedDuration := range profile.allowedDurations {
			if duration == allowedDuration {
				return duration, nil
			}
		}
		return 0, fmt.Errorf("duration must be one of 5, 10, or 15 seconds")
	}
	if duration < profile.minDuration || duration > profile.maxDuration {
		return 0, fmt.Errorf("duration must be between %d and %d seconds", profile.minDuration, profile.maxDuration)
	}
	return duration, nil
}

func normalizeVideoFormat(payload map[string]any, profile videoProfile) (string, string, string, error) {
	ratio := strings.ToLower(payloadString(payload, "ratio"))
	if ratio == "" {
		ratio = strings.ToLower(payloadString(payload, "aspect_ratio"))
	}
	resolution := strings.ToLower(payloadString(payload, "resolution"))
	size := strings.ToLower(payloadString(payload, "size"))
	if size != "" {
		sizeRatio, sizeResolution, err := videoFormatFromSize(size, profile)
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
	if profile.forceResolution != "" {
		resolution = profile.forceResolution
	} else if resolution == "" {
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
	if !isSupportedVideoRatio(ratio, profile.allowAutoRatio) {
		if profile.allowAutoRatio {
			return "", "", "", fmt.Errorf("ratio must be one of auto, 21:9, 16:9, 4:3, 1:1, 3:4, or 9:16")
		}
		return "", "", "", fmt.Errorf("ratio must be one of 21:9, 16:9, 4:3, 1:1, 3:4, or 9:16")
	}
	if profile.forceResolution == "" && resolution != "480p" && resolution != "720p" && resolution != "1080p" {
		return "", "", "", fmt.Errorf("resolution must be one of 480p, 720p, or 1080p")
	}
	return ratio, resolution, canonicalSize(ratio, resolution), nil
}

func isSupportedVideoRatio(ratio string, allowAuto bool) bool {
	if allowAuto && ratio == "auto" {
		return true
	}
	switch ratio {
	case "21:9", "16:9", "4:3", "1:1", "3:4", "9:16":
		return true
	default:
		return false
	}
}

func videoFormatFromSize(size string, profile videoProfile) (string, string, error) {
	if profile.forceResolution != "" {
		ratio, err := ratioFromSize(size)
		if err != nil {
			return "", "", err
		}
		return ratio, profile.forceResolution, nil
	}
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
	default:
		return "", "", fmt.Errorf("size is not supported")
	}
}

func ratioFromSize(size string) (string, error) {
	normalizedSize := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(size, "×", "x")))
	switch normalizedSize {
	case "854x480":
		return "16:9", nil
	case "480x854":
		return "9:16", nil
	}

	parts := strings.Split(normalizedSize, "x")
	if len(parts) != 2 {
		return "", fmt.Errorf("size must use the widthxheight format")
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || width <= 0 {
		return "", fmt.Errorf("size width must be a positive integer")
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || height <= 0 {
		return "", fmt.Errorf("size height must be a positive integer")
	}

	divisor := greatestCommonDivisor(width, height)
	ratio := fmt.Sprintf("%d:%d", width/divisor, height/divisor)
	if !isSupportedVideoRatio(ratio, false) {
		return "", fmt.Errorf("size must use a supported aspect ratio")
	}
	return ratio, nil
}

func greatestCommonDivisor(left, right int) int {
	for right != 0 {
		left, right = right, left%right
	}
	return left
}

func canonicalSize(ratio, resolution string) string {
	if ratio == "auto" {
		return "auto"
	}
	height := 720
	switch resolution {
	case "480p":
		height = 480
	case "1080p":
		height = 1080
	}
	width := height
	switch ratio {
	case "21:9":
		width = int(math.Round(float64(height) * 21 / 9))
	case "16:9":
		width = int(math.Round(float64(height) * 16 / 9))
	case "4:3":
		width = int(math.Round(float64(height) * 4 / 3))
	case "3:4":
		width = int(math.Round(float64(height) * 3 / 4))
	case "9:16":
		width = int(math.Round(float64(height) * 9 / 16))
	}
	return fmt.Sprintf("%dx%d", width, height)
}

func appendSD25InputReferences(images, videos, audios, inputReferences []string) ([]string, []string, []string) {
	for _, inputReference := range inputReferences {
		switch inputReferenceMediaKind(inputReference) {
		case "video":
			videos = appendUnique(videos, inputReference)
		case "audio":
			audios = appendUnique(audios, inputReference)
		default:
			images = appendUnique(images, inputReference)
		}
	}
	return images, videos, audios
}

func inputReferenceMediaKind(inputReference string) string {
	normalized := strings.ToLower(strings.TrimSpace(inputReference))
	switch {
	case strings.HasPrefix(normalized, "data:video/"):
		return "video"
	case strings.HasPrefix(normalized, "data:audio/"):
		return "audio"
	}

	parsed, err := url.Parse(normalized)
	if err != nil {
		return "image"
	}
	switch strings.ToLower(path.Ext(parsed.Path)) {
	case ".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v":
		return "video"
	case ".mp3", ".wav", ".m4a", ".aac", ".flac", ".ogg", ".opus":
		return "audio"
	default:
		return "image"
	}
}

func parseMultimodalContent(payload map[string]any) (*multimodalContent, error) {
	value, exists := payload["content"]
	if !exists || value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("content must be an array")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("content must not be empty")
	}

	result := &multimodalContent{items: items}
	promptParts := make([]string, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("content[%d] must be an object", index)
		}
		contentType := strings.ToLower(payloadString(object, "type"))
		switch contentType {
		case "text":
			text := strings.TrimSpace(payloadString(object, "text"))
			if text == "" {
				return nil, fmt.Errorf("content[%d].text must not be empty", index)
			}
			promptParts = append(promptParts, text)
		case "image_url":
			mediaURL, err := multimodalContentURL(object, "image_url")
			if err != nil {
				return nil, fmt.Errorf("content[%d] %w", index, err)
			}
			result.images = appendUnique(result.images, mediaURL)
		case "video_url":
			mediaURL, err := multimodalContentURL(object, "video_url")
			if err != nil {
				return nil, fmt.Errorf("content[%d] %w", index, err)
			}
			result.videos = appendUnique(result.videos, mediaURL)
		case "audio_url":
			mediaURL, err := multimodalContentURL(object, "audio_url")
			if err != nil {
				return nil, fmt.Errorf("content[%d] %w", index, err)
			}
			result.audios = appendUnique(result.audios, mediaURL)
		default:
			return nil, fmt.Errorf("content[%d].type must be text, image_url, video_url, or audio_url", index)
		}
	}
	result.prompt = strings.Join(promptParts, "\n")
	return result, nil
}

func multimodalContentURL(item map[string]any, key string) (string, error) {
	value, exists := item[key]
	if !exists || value == nil {
		return "", fmt.Errorf("%s.url is required", key)
	}

	var mediaURL string
	switch typed := value.(type) {
	case string:
		mediaURL = strings.TrimSpace(typed)
	case map[string]any:
		mediaURL = payloadString(typed, "url")
	default:
		return "", fmt.Errorf("%s must be a URL string or object", key)
	}
	if mediaURL == "" {
		return "", fmt.Errorf("%s.url is required", key)
	}
	return mediaURL, nil
}

func buildMultimodalContent(prompt string, images, videos, audios []string) []any {
	content := make([]any, 0, 1+len(images)+len(videos)+len(audios))
	if prompt != "" {
		content = append(content, map[string]any{"type": "text", "text": prompt})
	}
	for _, imageURL := range images {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": imageURL},
		})
	}
	for _, videoURL := range videos {
		content = append(content, map[string]any{
			"type":      "video_url",
			"video_url": map[string]any{"url": videoURL},
		})
	}
	for _, audioURL := range audios {
		content = append(content, map[string]any{
			"type":      "audio_url",
			"audio_url": map[string]any{"url": audioURL},
		})
	}
	return content
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

func collectPayloadStringSlices(payload map[string]any, keys ...string) ([]string, error) {
	var result []string
	for _, key := range keys {
		values, err := payloadStringSlice(payload, key)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			result = appendUnique(result, value)
		}
	}
	return result, nil
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
		value = strings.TrimSpace(value)
		if field == "images" && strings.HasPrefix(strings.ToLower(value), "data:image/") {
			if err := validateImageDataURL(value); err != nil {
				return fmt.Errorf("%s[%d] %w", field, index, err)
			}
			continue
		}
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("%s[%d] must be an HTTP or HTTPS URL", field, index)
		}
	}
	return nil
}

func validateImageDataURL(value string) error {
	separator := strings.IndexByte(value, ',')
	if separator <= len("data:image/") || !strings.Contains(strings.ToLower(value[:separator]), ";base64") {
		return fmt.Errorf("must be a base64 data:image URL")
	}
	encoded := strings.TrimSpace(value[separator+1:])
	if encoded == "" {
		return fmt.Errorf("must contain image data")
	}
	if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
		return fmt.Errorf("must contain valid base64 image data")
	}
	return nil
}

func payloadBool(payload map[string]any, key string) (bool, bool, error) {
	value, exists := payload[key]
	if !exists || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
		return false, false, nil
	}
	switch typed := value.(type) {
	case bool:
		return typed, true, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed, true, nil
		}
	default:
		return false, true, fmt.Errorf("%s must be a boolean", key)
	}
	return false, true, fmt.Errorf("%s must be a boolean", key)
}

func validateOptionalVideoParameters(payload map[string]any) error {
	if value, exists := payload["bypass_face_check"]; exists && value != nil {
		if _, _, err := payloadBool(map[string]any{"bypass_face_check": value}, "bypass_face_check"); err != nil {
			return err
		}
	}
	if value, exists := payload["grid_strength"]; exists && value != nil {
		strength, err := parseFiniteFloat(value)
		if err != nil || strength < 0 || strength > 1 {
			return fmt.Errorf("grid_strength must be a number between 0 and 1")
		}
	}
	return nil
}

func parseFiniteFloat(value any) (float64, error) {
	var parsed float64
	switch typed := value.(type) {
	case float64:
		parsed = typed
	case float32:
		parsed = float64(typed)
	case int:
		parsed = float64(typed)
	case int64:
		parsed = float64(typed)
	case string:
		value, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, err
		}
		parsed = value
	default:
		return 0, fmt.Errorf("number is required")
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("number must be finite")
	}
	return parsed, nil
}

func isFast720Model(modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	return strings.Contains(modelName, "fast") && strings.Contains(modelName, "720")
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

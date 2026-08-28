package minimaxvideo

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
	"path/filepath"
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
	multipartContentTypeContext = "minimax_video_multipart_content_type"

	minDurationSeconds = 4
	maxDurationSeconds = 15
	maxReferenceImages = 9
	maxReferenceVideos = 3
	maxReferenceAudios = 3
	firstLastFrameMode = "first_last_frame"
	defaultWorkflowID  = "multi-reference"
)

var ModelList = []string{
	"MiniMax-H3-933-1440P-GF",
}

var (
	imageReferenceFields = []string{
		"input_reference", "input_reference[]", "image", "images", "images[]", "reference_images",
	}
	videoReferenceFields = []string{
		"reference_video", "reference_videos", "video_urls", "videos", "videos[]",
	}
	videoAudioReferenceFields = []string{
		"reference_video_audio", "reference_video_audios",
	}
	audioReferenceFields = []string{
		"reference_audio", "reference_audios", "audio_urls", "audios", "audios[]",
	}
)

var supportedTopLevelFields = map[string]struct{}{
	"model": {}, "mode": {}, "prompt": {}, "prompt_enhance": {}, "seconds": {}, "duration": {},
	"size": {}, "audio": {}, "metadata": {}, "resolution": {}, "clarity": {},
	"aspect_ratio": {}, "megapixels": {}, "workflow_id": {},
}

var supportedWorkflowIDs = map[string]struct{}{
	"text-to-video":      {},
	"multi-reference":    {},
	"fl2v":               {},
	"cf-multi-reference": {},
	"cf-fl2v":            {},
	"mj":                 {},
	"cf-mj":              {},
}

type normalizedVideoRequest struct {
	payload     map[string]any
	request     relaycommon.TaskSubmitReq
	action      string
	isMultipart bool
	media       mediaReferences
}

type mediaReferences struct {
	images      []string
	videos      []string
	videoAudios []string
	audios      []string
	files       []multipartFileReference
}

type multipartFileReference struct {
	category string
	field    string
	filename string
}

// TaskAdaptor implements MiniMax's OpenAI Video-compatible contract. The
// native contract accepts URL fields in JSON and repeated media fields in
// multipart requests, including mixed image/video/audio references.
type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey          string
	baseURL         string
	promptEnhance   bool
	responseAdaptor openaivideo.TaskAdaptor
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.promptEnhance = info.ChannelSetting.MiniMaxVideoPromptEnhance
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

	payload, form, err := readClientPayload(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if form != nil {
		defer form.RemoveAll()
	}
	if err := validateSupportedFields(payload, form); err != nil {
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
	workflowID, err := normalizeWorkflowID(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_workflow_id", http.StatusBadRequest)
	}

	duration, err := normalizeDuration(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_seconds", http.StatusBadRequest)
	}
	size, err := normalizeSize(payload)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_size", http.StatusBadRequest)
	}
	aspectRatio, aspectRatioProvided, err := normalizeAspectRatio(payload, nil)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_aspect_ratio", http.StatusBadRequest)
	}
	if isMiniMaxSuperResolutionSize(size) && !aspectRatioProvided {
		return service.TaskErrorWrapperLocal(
			fmt.Errorf("aspect_ratio is required when size is 2K or 4K"),
			"missing_aspect_ratio",
			http.StatusBadRequest,
		)
	}
	billingResolution, err := resolveH3BillingResolution("", "", size, 0, false)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_resolution", http.StatusBadRequest)
	}

	media, err := collectMedia(payload, nil, form)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_reference_media", http.StatusBadRequest)
	}
	if err := validateMultipartFiles(form); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_reference_media", http.StatusBadRequest)
	}
	if err := validateMediaLimits(media); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_reference_media", http.StatusBadRequest)
	}
	if err := validateMediaURLs(media); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_reference_media", http.StatusBadRequest)
	}
	upstreamPayload := buildNormalizedJSONPayload(modelName, prompt, duration, size, aspectRatio, workflowID, media)

	requestMetadata := map[string]any{
		"reference_image_count": media.imageCount(),
		"reference_video_count": media.videoCount(),
		"reference_audio_count": media.audioCount(),
		"size":                  size,
	}
	if aspectRatioProvided {
		requestMetadata["aspect_ratio"] = aspectRatio
	}

	request := relaycommon.TaskSubmitReq{
		Model:             modelName,
		Prompt:            prompt,
		Images:            media.images,
		Duration:          duration,
		Seconds:           strconv.Itoa(duration),
		Size:              size,
		BillingResolution: billingResolution,
		Metadata:          requestMetadata,
	}
	if len(media.images) > 0 {
		request.Image = media.images[0]
		request.InputReference = media.images[0]
	}

	action := constant.TaskActionTextGenerate
	if media.hasAny() {
		action = constant.TaskActionGenerate
	}

	info.Action = action
	c.Set("task_request", request)
	c.Set(normalizedRequestContextKey, &normalizedVideoRequest{
		payload:     upstreamPayload,
		request:     request,
		action:      action,
		isMultipart: form != nil,
		media:       media,
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
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	if contentType, ok := c.Get(multipartContentTypeContext); ok {
		contentTypeText, valid := contentType.(string)
		if !valid || strings.TrimSpace(contentTypeText) == "" {
			return fmt.Errorf("MiniMax Video multipart content type is invalid")
		}
		req.Header.Set("Content-Type", contentTypeText)
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
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

	if normalized.isMultipart {
		return a.buildMultipartRequestBody(c, info, normalized)
	}

	payload := clonePayload(normalized.payload)
	payload["model"] = taskcommon.DefaultString(info.UpstreamModelName, normalized.request.Model)
	payload["prompt_enhance"] = strconv.FormatBool(a.promptEnhance)
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "marshal MiniMax Video request")
	}
	return bytes.NewReader(body), nil
}

func (a *TaskAdaptor) buildMultipartRequestBody(c *gin.Context, info *relaycommon.RelayInfo, normalized *normalizedVideoRequest) (io.Reader, error) {
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, fmt.Errorf("parse multipart request: %w", err)
	}
	defer form.RemoveAll()

	payload := formValuesToPayload(form)
	currentMedia, err := collectMedia(payload, nil, form)
	if err != nil {
		return nil, fmt.Errorf("normalize multipart references: %w", err)
	}
	if !sameMediaCounts(currentMedia, normalized.media) {
		return nil, fmt.Errorf("multipart reference fields changed while building request")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writeField := func(name, value string) error {
		if err := writer.WriteField(name, value); err != nil {
			return fmt.Errorf("write multipart field %s: %w", name, err)
		}
		return nil
	}

	aspectRatio := payloadStringValue(normalized.payload, "aspect_ratio")
	fields := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "model", value: taskcommon.DefaultString(info.UpstreamModelName, normalized.request.Model), set: true},
		{name: "prompt", value: normalized.request.Prompt, set: true},
		{name: "prompt_enhance", value: strconv.FormatBool(a.promptEnhance), set: true},
		{name: "seconds", value: normalized.request.Seconds, set: true},
		{name: "size", value: canonicalMiniMaxUpstreamSize(normalized.request.Size), set: true},
		{name: "workflow_id", value: payloadStringValue(normalized.payload, "workflow_id"), set: true},
		{name: "aspect_ratio", value: aspectRatio, set: aspectRatio != ""},
	}
	for _, field := range fields {
		if field.set {
			if err := writeField(field.name, field.value); err != nil {
				return nil, err
			}
		}
	}
	for _, field := range []struct {
		name   string
		values []string
	}{
		{name: "images", values: currentMedia.images},
		{name: "reference_videos", values: currentMedia.videos},
		{name: "reference_audios", values: currentMedia.audios},
	} {
		for _, value := range field.values {
			if err := writeField(field.name, value); err != nil {
				return nil, err
			}
		}
	}

	if err := writeMultipartFiles(writer, form, currentMedia.files); err != nil {
		return nil, err
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
		if payload == nil {
			return nil, nil, fmt.Errorf("request body must be a JSON object")
		}
		return payload, nil, nil
	}
	if strings.Contains(contentType, "multipart/form-data") {
		form, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return nil, nil, err
		}
		return formValuesToPayload(form), form, nil
	}
	return nil, nil, fmt.Errorf("Content-Type must be application/json or multipart/form-data")
}

func formValuesToPayload(form *multipart.Form) map[string]any {
	payload := make(map[string]any, len(form.Value))
	for key, values := range form.Value {
		if len(values) == 1 {
			payload[key] = values[0]
		} else if len(values) > 1 {
			payload[key] = append([]string(nil), values...)
		}
	}
	return payload
}

func validateSupportedFields(payload map[string]any, form *multipart.Form) error {
	for field := range payload {
		if _, supported := supportedTopLevelFields[field]; supported {
			continue
		}
		if isReferenceField(field) {
			continue
		}
		return fmt.Errorf("%s is not supported by MiniMax Video", field)
	}
	if form == nil {
		return nil
	}
	for field := range form.File {
		if !isReferenceField(field) {
			return fmt.Errorf("multipart file field %s is not supported by MiniMax Video", field)
		}
	}
	return nil
}

func isReferenceField(field string) bool {
	for _, candidate := range imageReferenceFields {
		if field == candidate {
			return true
		}
	}
	for _, candidate := range videoReferenceFields {
		if field == candidate {
			return true
		}
	}
	for _, candidate := range videoAudioReferenceFields {
		if field == candidate {
			return true
		}
	}
	for _, candidate := range audioReferenceFields {
		if field == candidate {
			return true
		}
	}
	return false
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

func parseMetadata(value any) (map[string]any, error) {
	if value == nil || strings.TrimSpace(payloadScalarString(value)) == "" {
		return nil, nil
	}
	switch typed := value.(type) {
	case map[string]any:
		return clonePayload(typed), nil
	case string:
		var metadata map[string]any
		if err := common.Unmarshal([]byte(typed), &metadata); err != nil {
			return nil, fmt.Errorf("metadata must be an object or a JSON object string")
		}
		if metadata == nil {
			return nil, fmt.Errorf("metadata must be an object")
		}
		return metadata, nil
	default:
		return nil, fmt.Errorf("metadata must be an object")
	}
}

func validateMetadata(metadata map[string]any) error {
	if metadata == nil {
		return nil
	}
	value, exists := metadata["multiple"]
	if !exists || value == nil || strings.TrimSpace(payloadScalarString(value)) == "" {
		return nil
	}
	multiple, err := parseInteger(value)
	if err != nil || multiple < 8 || multiple > 128 || multiple%4 != 0 {
		return fmt.Errorf("metadata.multiple must be an integer from 8 to 128 and a multiple of 4")
	}
	return nil
}

func normalizeWorkflowID(payload map[string]any) (string, error) {
	value, provided := payload["workflow_id"]
	if !provided || value == nil || strings.TrimSpace(payloadScalarString(value)) == "" {
		return defaultWorkflowID, nil
	}
	workflowID, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("workflow_id must be a string")
	}
	workflowID = strings.TrimSpace(workflowID)
	if _, supported := supportedWorkflowIDs[workflowID]; !supported {
		return "", fmt.Errorf("workflow_id must be one of text-to-video, multi-reference, fl2v, cf-multi-reference, cf-fl2v, mj, or cf-mj")
	}
	return workflowID, nil
}

func normalizeDuration(payload map[string]any) (int, error) {
	durationValue, hasDuration := payload["duration"]
	secondsValue, hasSeconds := payload["seconds"]

	parse := func(value any) (int, error) {
		duration, err := parseInteger(value)
		if err != nil || duration < minDurationSeconds || duration > maxDurationSeconds {
			return 0, fmt.Errorf("seconds must be an integer between %d and %d", minDurationSeconds, maxDurationSeconds)
		}
		return duration, nil
	}
	if hasDuration {
		return parse(durationValue)
	}
	if hasSeconds {
		return parse(secondsValue)
	}
	return 5, nil
}

func normalizeMode(payload, metadata map[string]any) (string, bool, error) {
	value, provided := firstValue(payload, metadata, "mode")
	if !provided {
		return "", false, nil
	}
	mode, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("mode must be a string")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "", false, nil
	}
	if mode != firstLastFrameMode {
		return "", true, fmt.Errorf("mode must be %q when provided", firstLastFrameMode)
	}
	return mode, true, nil
}

func validateMode(mode string, provided bool, media mediaReferences) error {
	if !provided {
		return nil
	}
	if mode != firstLastFrameMode {
		return fmt.Errorf("mode must be %q when provided", firstLastFrameMode)
	}
	if media.imageCount() != 2 {
		return fmt.Errorf("first_last_frame mode requires exactly two reference images")
	}
	if media.videoCount() > 0 || media.videoAudioCount() > 0 || media.audioCount() > 0 {
		return fmt.Errorf("first_last_frame mode does not support reference videos or audio")
	}
	return nil
}

func normalizeSize(payload map[string]any) (string, error) {
	value, provided := payload["size"]
	if !provided || value == nil {
		return "", fmt.Errorf("size field is required")
	}
	size, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("size must be a string")
	}
	size = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(size, "×", "x")))
	size = strings.ReplaceAll(size, " ", "")
	if size == "" {
		return "", fmt.Errorf("size field is required")
	}
	if _, supported := constant.MiniMaxH3ResolutionTierForSize(size); supported {
		return size, nil
	}
	if normalizeStandardH3BillingResolution(size) == "" {
		return "", fmt.Errorf("size must be a documented MiniMax-H3 dimension or a standard video resolution")
	}
	return size, nil
}

func normalizeAspectRatio(payload, metadata map[string]any) (string, bool, error) {
	value, provided := firstValue(payload, metadata, "aspect_ratio")
	if !provided {
		return "", false, nil
	}
	ratio, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("aspect_ratio must be a string")
	}
	ratio = strings.ToLower(strings.TrimSpace(ratio))
	if !isSupportedAspectRatio(ratio) {
		return "", true, fmt.Errorf("aspect_ratio must be one of 1:1, 2:3, 3:2, 3:4, 4:3, 9:16, 16:9, or 21:9")
	}
	return ratio, true, nil
}

func normalizeTextOption(payload, metadata map[string]any, field string) (string, bool, error) {
	value, provided := firstValue(payload, metadata, field)
	if !provided {
		return "", false, nil
	}
	text := strings.TrimSpace(payloadScalarString(value))
	if text == "" {
		return "", false, nil
	}
	if strings.ContainsAny(text, "\r\n") {
		return "", true, fmt.Errorf("%s must be a single line value", field)
	}
	return text, true, nil
}

func normalizeMegapixels(payload, metadata map[string]any) (float64, bool, error) {
	value, provided := firstValue(payload, metadata, "megapixels")
	if !provided {
		return 0, false, nil
	}
	megapixels, err := parseFiniteFloat(value)
	if err != nil || megapixels <= 0 || megapixels > 64 {
		return 0, true, fmt.Errorf("megapixels must be a finite number greater than 0 and no greater than 64")
	}
	return megapixels, true, nil
}

func normalizeBoolOption(payload, metadata map[string]any, field string) (bool, bool, error) {
	value, provided := firstValue(payload, metadata, field)
	if !provided {
		return false, false, nil
	}
	parsed, err := parseBool(value)
	if err != nil {
		return false, true, fmt.Errorf("%s must be a boolean", field)
	}
	return parsed, true, nil
}

func parseAudio(payload, metadata map[string]any) (bool, error) {
	value, provided := firstValue(payload, metadata, "audio")
	if !provided {
		return true, nil
	}
	parsed, err := parseBool(value)
	if err != nil {
		return false, fmt.Errorf("audio must be a boolean")
	}
	return parsed, nil
}

func buildNormalizedJSONPayload(modelName, prompt string, duration int, size, aspectRatio, workflowID string, media mediaReferences) map[string]any {
	payload := map[string]any{
		"model":       modelName,
		"prompt":      prompt,
		"seconds":     duration,
		"size":        canonicalMiniMaxUpstreamSize(size),
		"workflow_id": workflowID,
	}
	if aspectRatio != "" {
		payload["aspect_ratio"] = aspectRatio
	}
	if len(media.images) > 0 {
		payload["images"] = media.images
	}
	if len(media.videos) > 0 {
		payload["reference_videos"] = media.videos
	}
	if len(media.audios) > 0 {
		payload["reference_audios"] = media.audios
	}
	return payload
}

// canonicalMiniMaxUpstreamSize preserves MiniMax's case-sensitive 2K and 4K enums.
// Internal task metadata stays lowercase so it continues to match billing tiers.
func canonicalMiniMaxUpstreamSize(size string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "2k":
		return "2K"
	case "4k":
		return "4K"
	default:
		return size
	}
}

func isMiniMaxSuperResolutionSize(size string) bool {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "2k", "4k":
		return true
	default:
		return false
	}
}

func collectMedia(payload, metadata map[string]any, form *multipart.Form) (mediaReferences, error) {
	var media mediaReferences
	for _, field := range imageReferenceFields {
		if err := media.addURLs(payload[field], "images"); err != nil {
			return media, fmt.Errorf("%s %w", field, err)
		}
		if err := media.addURLs(metadata[field], "images"); err != nil {
			return media, fmt.Errorf("metadata.%s %w", field, err)
		}
	}
	for _, field := range videoReferenceFields {
		if err := media.addURLs(payload[field], "videos"); err != nil {
			return media, fmt.Errorf("%s %w", field, err)
		}
		if err := media.addURLs(metadata[field], "videos"); err != nil {
			return media, fmt.Errorf("metadata.%s %w", field, err)
		}
	}
	for _, field := range videoAudioReferenceFields {
		if err := media.addURLs(payload[field], "audios"); err != nil {
			return media, fmt.Errorf("%s %w", field, err)
		}
		if err := media.addURLs(metadata[field], "audios"); err != nil {
			return media, fmt.Errorf("metadata.%s %w", field, err)
		}
	}
	for _, field := range audioReferenceFields {
		if err := media.addURLs(payload[field], "audios"); err != nil {
			return media, fmt.Errorf("%s %w", field, err)
		}
		if err := media.addURLs(metadata[field], "audios"); err != nil {
			return media, fmt.Errorf("metadata.%s %w", field, err)
		}
	}

	if form != nil {
		for _, field := range imageReferenceFields {
			media.addFiles(form.File[field], "images", field)
		}
		for _, field := range videoReferenceFields {
			media.addFiles(form.File[field], "videos", field)
		}
		for _, field := range videoAudioReferenceFields {
			media.addFiles(form.File[field], "audios", field)
		}
		for _, field := range audioReferenceFields {
			media.addFiles(form.File[field], "audios", field)
		}
	}
	return media, nil
}

func (media *mediaReferences) addURLs(value any, category string) error {
	if value == nil {
		return nil
	}
	values, err := stringValues(value)
	if err != nil {
		return err
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch category {
		case "images":
			media.images = appendUnique(media.images, value)
		case "videos":
			media.videos = appendUnique(media.videos, value)
		case "video_audios":
			media.videoAudios = appendUnique(media.videoAudios, value)
		case "audios":
			media.audios = appendUnique(media.audios, value)
		}
	}
	return nil
}

func (media *mediaReferences) addFiles(files []*multipart.FileHeader, category, field string) {
	seen := make(map[string]struct{})
	for _, existing := range media.files {
		if existing.category == category {
			seen[existing.filename] = struct{}{}
		}
	}
	for _, file := range files {
		if file == nil {
			media.files = append(media.files, multipartFileReference{category: category, field: field})
			continue
		}
		filename := strings.TrimSpace(filepath.Base(file.Filename))
		if filename == "" {
			filename = "unnamed"
		}
		if _, exists := seen[filename]; exists {
			continue
		}
		seen[filename] = struct{}{}
		media.files = append(media.files, multipartFileReference{category: category, field: field, filename: filename})
	}
}

func validateMediaLimits(media mediaReferences) error {
	if media.imageCount() > maxReferenceImages {
		return fmt.Errorf("reference images support at most %d files", maxReferenceImages)
	}
	if media.videoCount() > maxReferenceVideos {
		return fmt.Errorf("reference videos support at most %d files", maxReferenceVideos)
	}
	if media.videoAudioCount() > maxReferenceAudios {
		return fmt.Errorf("reference video audios support at most %d files", maxReferenceAudios)
	}
	if media.audioCount() > maxReferenceAudios {
		return fmt.Errorf("reference audios support at most %d files", maxReferenceAudios)
	}
	return nil
}

func validateMediaURLs(media mediaReferences) error {
	for category, values := range map[string][]string{
		"images": media.images, "videos": media.videos, "reference_video_audios": media.videoAudios, "audios": media.audios,
	} {
		for index, value := range values {
			parsed, err := url.ParseRequestURI(value)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return fmt.Errorf("%s[%d] must be an HTTP or HTTPS URL", category, index)
			}
			if err := service.ValidateSSRFProtectedFetchURL(value); err != nil {
				return fmt.Errorf("%s[%d] URL is not allowed: %w", category, index, err)
			}
		}
	}
	for _, file := range media.files {
		if file.filename == "" {
			return fmt.Errorf("%s file name is required", file.field)
		}
	}
	return nil
}

func validateMultipartFiles(form *multipart.Form) error {
	if form == nil {
		return nil
	}
	maxMB := constant.MaxFileDownloadMB
	if maxMB <= 0 {
		maxMB = 64
	}
	maxBytes := int64(maxMB) * 1024 * 1024
	for field, files := range form.File {
		for index, file := range files {
			if file == nil || file.Size <= 0 {
				return fmt.Errorf("%s file %d is empty", field, index+1)
			}
			if file.Size > maxBytes {
				return fmt.Errorf("%s file %d exceeds %d MiB", field, index+1, maxMB)
			}
		}
	}
	return nil
}

func writeMultipartFiles(writer *multipart.Writer, form *multipart.Form, references []multipartFileReference) error {
	for _, reference := range references {
		var fileHeader *multipart.FileHeader
		for _, candidate := range form.File[reference.field] {
			if candidate != nil && filepath.Base(strings.TrimSpace(candidate.Filename)) == reference.filename {
				fileHeader = candidate
				break
			}
		}
		if fileHeader == nil {
			return fmt.Errorf("multipart file %s was not found", reference.filename)
		}
		if fileHeader.Size <= 0 {
			return fmt.Errorf("multipart file %s is empty", reference.filename)
		}
		file, err := fileHeader.Open()
		if err != nil {
			return fmt.Errorf("open multipart file %s: %w", reference.filename, err)
		}
		contentType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		partHeader := make(textproto.MIMEHeader)
		partHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name":     reference.field,
			"filename": reference.filename,
		}))
		partHeader.Set("Content-Type", contentType)
		part, partErr := writer.CreatePart(partHeader)
		if partErr != nil {
			_ = file.Close()
			return fmt.Errorf("create multipart file %s: %w", reference.filename, partErr)
		}
		_, copyErr := io.Copy(part, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("copy multipart file %s: %w", reference.filename, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close multipart file %s: %w", reference.filename, closeErr)
		}
	}
	return nil
}

func sameMediaCounts(left, right mediaReferences) bool {
	return left.imageCount() == right.imageCount() &&
		left.videoCount() == right.videoCount() &&
		left.videoAudioCount() == right.videoAudioCount() &&
		left.audioCount() == right.audioCount()
}

func (media mediaReferences) imageCount() int {
	return len(media.images) + countFiles(media.files, "images")
}

func (media mediaReferences) videoCount() int {
	return len(media.videos) + countFiles(media.files, "videos")
}

func (media mediaReferences) videoAudioCount() int {
	return len(media.videoAudios) + countFiles(media.files, "video_audios")
}

func (media mediaReferences) audioCount() int {
	return len(media.audios) + countFiles(media.files, "audios")
}

func (media mediaReferences) hasAny() bool {
	return media.imageCount()+media.videoCount()+media.videoAudioCount()+media.audioCount() > 0
}

func countFiles(files []multipartFileReference, category string) int {
	count := 0
	for _, file := range files {
		if file.category == category {
			count++
		}
	}
	return count
}

func stringValues(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		values := make([]string, 0, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("item %d must be a string", index)
			}
			values = append(values, text)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("must be a string or an array of strings")
	}
}

func firstValue(payload, metadata map[string]any, field string) (any, bool) {
	if value, ok := payload[field]; ok && value != nil && strings.TrimSpace(payloadScalarString(value)) != "" {
		return value, true
	}
	if value, ok := metadata[field]; ok && value != nil && strings.TrimSpace(payloadScalarString(value)) != "" {
		return value, true
	}
	return nil, false
}

func parseInteger(value any) (int, error) {
	var raw string
	switch typed := value.(type) {
	case string:
		raw = strings.TrimSpace(typed)
	case int:
		return typed, nil
	case int8:
		return int(typed), nil
	case int16:
		return int(typed), nil
	case int32:
		return int(typed), nil
	case int64:
		raw = strconv.FormatInt(typed, 10)
	case uint:
		raw = strconv.FormatUint(uint64(typed), 10)
	case uint8:
		raw = strconv.FormatUint(uint64(typed), 10)
	case uint16:
		raw = strconv.FormatUint(uint64(typed), 10)
	case uint32:
		raw = strconv.FormatUint(uint64(typed), 10)
	case uint64:
		raw = strconv.FormatUint(typed, 10)
	case float32:
		return parseInteger(float64(typed))
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return 0, fmt.Errorf("integer is required")
		}
		raw = strconv.FormatFloat(typed, 'f', 0, 64)
	default:
		return 0, fmt.Errorf("integer is required")
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("integer is out of range")
	}
	if strconv.IntSize == 32 && (parsed < -2147483648 || parsed > 2147483647) {
		return 0, fmt.Errorf("integer is out of range")
	}
	return int(parsed), nil
}

func parseFiniteFloat(value any) (float64, error) {
	var raw string
	switch typed := value.(type) {
	case string:
		raw = strings.TrimSpace(typed)
	case float32:
		raw = strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case float64:
		raw = strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		raw = strconv.Itoa(typed)
	case int64:
		raw = strconv.FormatInt(typed, 10)
	case uint64:
		raw = strconv.FormatUint(typed, 10)
	default:
		return 0, fmt.Errorf("number is required")
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("number must be finite")
	}
	return parsed, nil
}

func parseBool(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(typed))
	default:
		return false, fmt.Errorf("boolean is required")
	}
}

func isSupportedAspectRatio(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1:1", "2:3", "3:2", "3:4", "4:3", "9:16", "16:9", "21:9":
		return true
	default:
		return false
	}
}

func payloadStringValue(payload map[string]any, field string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[field]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(payloadScalarString(value))
}

func payloadScalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []string:
		if len(typed) == 1 {
			return typed[0]
		}
	case []any:
		if len(typed) == 1 {
			return payloadScalarString(typed[0])
		}
	default:
		return fmt.Sprint(value)
	}
	return ""
}

func payloadBoolValue(payload map[string]any, field string, fallback bool) bool {
	value, ok := payload[field]
	if !ok || value == nil {
		return fallback
	}
	parsed, err := parseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func hasPayloadValue(payload map[string]any, field string) bool {
	value, ok := payload[field]
	return ok && value != nil && strings.TrimSpace(payloadScalarString(value)) != ""
}

func clonePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
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

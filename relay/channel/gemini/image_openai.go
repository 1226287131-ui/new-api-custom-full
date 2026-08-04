package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const maxGeminiInlineImageBytes = 20 * 1024 * 1024

var geminiMarkdownImageURLPattern = regexp.MustCompile(`(?i)!\[[^\]]*\]\(\s*(?:<)?(https?://[^\s)>]+)(?:>)?(?:\s+["'][^)]*["'])?\s*\)`)

var supportedGeminiImageAspectRatios = map[string]struct{}{
	"auto": {},
	"1:1":  {},
	"2:3":  {},
	"3:2":  {},
	"3:4":  {},
	"4:3":  {},
	"4:5":  {},
	"5:4":  {},
	"9:16": {},
	"16:9": {},
	"21:9": {},
	"1:8":  {},
	"1:4":  {},
	"4:1":  {},
	"8:1":  {},
}

func convertOpenAIImageRequestToGemini(c *gin.Context, request dto.ImageRequest) (*dto.GeminiChatRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required for Gemini image generation")
	}

	if request.N != nil {
		if *request.N == 0 || *request.N > dto.MaxImageN {
			return nil, fmt.Errorf("n must be between 1 and %d for Gemini image models", dto.MaxImageN)
		}
	}

	aspectRatio, imageSize, err := normalizeOpenAIImageOptions(request)
	if err != nil {
		return nil, err
	}

	imageParts, err := collectOpenAIImageParts(c, request)
	if err != nil {
		return nil, err
	}
	parts := make([]dto.GeminiPart, 0, len(imageParts)+1)
	parts = append(parts, imageParts...)
	parts = append(parts, dto.GeminiPart{Text: request.Prompt})

	imageConfig := map[string]string{"aspectRatio": aspectRatio}
	if imageSize != "" {
		imageConfig["imageSize"] = imageSize
	}
	imageConfigJSON, err := common.Marshal(imageConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini image config: %w", err)
	}

	geminiRequest := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: parts,
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageConfig:        imageConfigJSON,
		},
	}
	if request.N != nil {
		candidateCount := int(*request.N)
		geminiRequest.GenerationConfig.CandidateCount = &candidateCount
	}
	return geminiRequest, nil
}

func normalizeOpenAIImageOptions(request dto.ImageRequest) (string, string, error) {
	aspectRatio := aspectRatioFromOpenAISize(request.Size)
	if aspectRatio == "" {
		aspectRatio = "1:1"
	}

	if raw, ok := findOpenAIImageOption(request.Extra, "aspect_ratio", "aspectRatio", "ratio", "aspect"); ok {
		normalized, err := normalizeGeminiImageAspectRatio(raw)
		if err != nil {
			return "", "", err
		}
		aspectRatio = normalized
	} else if request.Size != "" && aspectRatio == "1:1" && !isKnownOpenAIImageSize(request.Size) {
		if _, err := normalizeGeminiImageAspectRatio(request.Size); err != nil {
			return "", "", fmt.Errorf("unsupported image size %q: %w", request.Size, err)
		}
	}

	resolution := ""
	if raw, ok := findOpenAIImageOption(request.Extra, "output_resolution", "outputResolution", "image_size", "imageSize", "resolution", "exact_size", "exactSize", "quality"); ok {
		resolution = raw
	}
	if resolution == "" && request.Size != "" && !isAspectRatio(request.Size) && request.Size != "auto" {
		resolution = request.Size
	}
	if resolution == "" && request.Quality != "" {
		resolution = request.Quality
	}
	if resolution == "" {
		return aspectRatio, "", nil
	}

	imageSize, err := normalizeGeminiImageSize(resolution)
	if err != nil {
		return "", "", err
	}
	return aspectRatio, imageSize, nil
}

func aspectRatioFromOpenAISize(size string) string {
	normalized := strings.ToLower(strings.TrimSpace(size))
	if normalized == "" {
		return ""
	}
	if normalized == "auto" {
		return "auto"
	}
	if isAspectRatio(normalized) {
		if _, ok := supportedGeminiImageAspectRatios[normalized]; ok {
			return normalized
		}
		return ""
	}

	switch normalized {
	case "256x256", "512x512", "1024x1024", "2048x2048", "4096x4096":
		return "1:1"
	case "1024x1536", "768x1152":
		return "2:3"
	case "1536x1024", "1152x768":
		return "3:2"
	case "1024x1365", "768x1024":
		return "3:4"
	case "1365x1024", "1024x768":
		return "4:3"
	case "1024x1792", "720x1280", "1080x1920":
		return "9:16"
	case "1792x1024", "1280x720", "1920x1080":
		return "16:9"
	case "1344x576", "2560x1080":
		return "21:9"
	}

	parts := strings.Split(normalized, "x")
	if len(parts) != 2 {
		return ""
	}
	width, widthErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	height, heightErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return ""
	}

	ratio := width / height
	bestRatio := ""
	bestDifference := math.MaxFloat64
	for _, candidate := range []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9", "1:8", "1:4", "4:1", "8:1"} {
		candidateParts := strings.Split(candidate, ":")
		candidateWidth, _ := strconv.ParseFloat(candidateParts[0], 64)
		candidateHeight, _ := strconv.ParseFloat(candidateParts[1], 64)
		candidateRatio := candidateWidth / candidateHeight
		difference := math.Abs(ratio-candidateRatio) / candidateRatio
		if difference < bestDifference {
			bestDifference = difference
			bestRatio = candidate
		}
	}
	return bestRatio
}

func normalizeGeminiImageAspectRatio(raw string) (string, error) {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(raw)))
	if _, ok := supportedGeminiImageAspectRatios[normalized]; !ok {
		if mapped := aspectRatioFromOpenAISize(normalized); mapped != "" {
			return mapped, nil
		}
		return "", fmt.Errorf("unsupported image aspect ratio %q; use the supported Banana ratios including 4:5, 5:4, 1:8, 1:4, 4:1, and 8:1", raw)
	}
	return normalized, nil
}

func normalizeGeminiImageSize(raw string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	switch normalized {
	case "1K", "2K", "4K":
		return normalized, nil
	case "HD", "HIGH":
		return "2K", nil
	case "STANDARD", "MEDIUM", "LOW", "AUTO":
		return "1K", nil
	default:
		if strings.Contains(normalized, "X") {
			return helper.ClassifyImageResolution(normalized)
		}
		return "", fmt.Errorf("unsupported image resolution %q; use 1K, 2K, or 4K", raw)
	}
}

func isAspectRatio(raw string) bool {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func isKnownOpenAIImageSize(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	return normalized == "auto" || normalized == "1k" || normalized == "2k" || normalized == "4k" ||
		normalized == "hd" || normalized == "high" || aspectRatioFromOpenAISize(normalized) != ""
}

func findOpenAIImageOption(extra map[string]json.RawMessage, names ...string) (string, bool) {
	if len(extra) == 0 {
		return "", false
	}
	root := make(map[string]any, len(extra))
	for key, raw := range extra {
		var value any
		if err := common.Unmarshal(raw, &value); err == nil {
			root[key] = value
		}
	}
	for _, name := range names {
		wanted := map[string]struct{}{normalizeImageOptionKey(name): {}}
		if value, ok := findImageOptionValue(root, wanted, 0); ok {
			return value, true
		}
	}
	return "", false
}

func findImageOptionValue(value any, wanted map[string]struct{}, depth int) (string, bool) {
	if depth > 8 {
		return "", false
	}

	if object, ok := value.(map[string]any); ok {
		for key, child := range object {
			if _, ok := wanted[normalizeImageOptionKey(key)]; ok {
				if scalar, ok := imageOptionScalar(child); ok {
					return scalar, true
				}
			}
		}
		// Prefer Google's nested image config over unrelated vendor fields.
		for _, preferred := range []string{"extra_body", "google", "generation_config", "generationConfig", "image_config", "imageConfig", "parameters"} {
			for key, child := range object {
				if normalizeImageOptionKey(key) == normalizeImageOptionKey(preferred) {
					if found, ok := findImageOptionValue(child, wanted, depth+1); ok {
						return found, true
					}
				}
			}
		}
		for _, child := range object {
			if found, ok := findImageOptionValue(child, wanted, depth+1); ok {
				return found, true
			}
		}
	}

	if values, ok := value.([]any); ok {
		for _, child := range values {
			if found, ok := findImageOptionValue(child, wanted, depth+1); ok {
				return found, true
			}
		}
	}
	return "", false
}

func imageOptionScalar(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), strings.TrimSpace(typed) != ""
	case float64:
		return fmt.Sprintf("%v", typed), true
	case json.Number:
		return typed.String(), true
	default:
		return "", false
	}
}

func normalizeImageOptionKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(key)))
}

// normalizeNativeGeminiImageRequest makes native Gemini requests resilient to
// clients that use OpenAI-style snake_case names. The upstream Gemini API is
// strict about the camelCase imageConfig keys, so forwarding the raw object can
// silently fall back to a square image.
func normalizeNativeGeminiImageRequest(request *dto.GeminiChatRequest, info *relaycommon.RelayInfo) error {
	if request == nil {
		return nil
	}
	if info == nil || !isGeminiImagineModel(info) {
		// responseFormat is a compatibility wrapper used by some clients. It is
		// not part of Google's native GenerateContent schema, so never forward it
		// to ordinary Gemini models.
		request.GenerationConfig.ResponseFormat = nil
		return nil
	}

	modalities := request.GenerationConfig.ResponseModalities
	if len(modalities) == 0 {
		modalities = []string{"TEXT", "IMAGE"}
	} else {
		hasImage := false
		for _, modality := range modalities {
			if strings.EqualFold(strings.TrimSpace(modality), "IMAGE") {
				hasImage = true
				break
			}
		}
		if !hasImage {
			modalities = append(modalities, "IMAGE")
		}
	}
	request.GenerationConfig.ResponseModalities = modalities

	if len(request.GenerationConfig.ResponseFormat) > 0 {
		// Some Gemini-compatible upstreams use responseFormat.image rather than
		// Google's native imageConfig. Preserve that wrapper so those upstreams
		// can apply the requested ratio and resolution.
		responseFormat, err := normalizeNativeGeminiResponseFormat(request.GenerationConfig.ResponseFormat)
		if err != nil {
			return err
		}
		request.GenerationConfig.ResponseFormat = responseFormat
	}
	imageConfig, err := normalizeNativeGeminiImageConfig(request.GenerationConfig.ImageConfig)
	if err != nil {
		return err
	}
	if len(imageConfig) > 0 {
		request.GenerationConfig.ImageConfig = imageConfig
	}
	return nil
}

func isGeminiImagineModel(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	names := []string{info.UpstreamModelName, info.OriginModelName}
	if info.ChannelMeta != nil {
		names = append(names, info.ChannelMeta.UpstreamModelName)
	}
	for _, name := range names {
		if model_setting.IsGeminiModelSupportImagine(name) {
			return true
		}
	}
	return false
}

func resolveGeminiUpstreamModelName(info *relaycommon.RelayInfo) string {
	if info == nil {
		return ""
	}
	if name := strings.TrimSpace(info.UpstreamModelName); name != "" {
		return name
	}
	if info.ChannelMeta != nil {
		if name := strings.TrimSpace(info.ChannelMeta.UpstreamModelName); name != "" {
			return name
		}
	}
	return strings.TrimSpace(info.OriginModelName)
}

func normalizeNativeGeminiResponseFormat(raw json.RawMessage) (json.RawMessage, error) {
	var root map[string]any
	if err := common.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid Gemini response format: %w", err)
	}
	if len(root) == 0 {
		return raw, nil
	}

	var image map[string]any
	for key, value := range root {
		if normalizeImageOptionKey(key) != "image" {
			continue
		}
		var ok bool
		image, ok = value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Gemini response format image must be an object")
		}
		delete(root, key)
		break
	}
	if image == nil {
		return raw, nil
	}

	canonicalImage := make(map[string]any, len(image)+2)
	for key, value := range image {
		normalizedKey := normalizeImageOptionKey(key)
		switch normalizedKey {
		case "aspectratio", "ratio", "aspect":
			canonicalImage["aspectRatio"] = value
		case "imagesize", "resolution", "outputresolution", "exactsize", "quality", "size":
			canonicalImage["imageSize"] = value
		default:
			canonicalImage[key] = value
		}
	}
	if value, ok := findImageOptionValue(canonicalImage, map[string]struct{}{"aspectratio": {}}, 0); ok {
		normalized, err := normalizeGeminiImageAspectRatio(value)
		if err != nil {
			return nil, err
		}
		canonicalImage["aspectRatio"] = normalized
	}
	if value, ok := findImageOptionValue(canonicalImage, map[string]struct{}{"imagesize": {}}, 0); ok {
		normalized, err := normalizeGeminiImageSize(value)
		if err != nil {
			return nil, err
		}
		canonicalImage["imageSize"] = normalized
	}
	root["image"] = canonicalImage

	encoded, err := common.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini response format: %w", err)
	}
	return encoded, nil
}

func normalizeNativeGeminiImageConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return raw, nil
	}

	var root map[string]any
	if err := common.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid Gemini image config: %w", err)
	}
	if len(root) == 0 {
		return raw, nil
	}

	wantedAspect := make(map[string]struct{})
	for _, name := range []string{"aspect_ratio", "aspectRatio", "ratio", "aspect"} {
		wantedAspect[normalizeImageOptionKey(name)] = struct{}{}
	}
	wantedSize := make(map[string]struct{})
	for _, name := range []string{"image_size", "imageSize", "resolution", "output_resolution", "outputResolution", "exact_size", "exactSize", "quality", "size"} {
		wantedSize[normalizeImageOptionKey(name)] = struct{}{}
	}

	var aspectRatio string
	if value, ok := findImageOptionValue(root, wantedAspect, 0); ok {
		aspectRatio = value
	}
	var imageSize string
	if value, ok := findImageOptionValue(root, wantedSize, 0); ok {
		imageSize = value
	}

	canonical := make(map[string]any, len(root)+2)
	for key, value := range root {
		normalizedKey := normalizeImageOptionKey(key)
		if _, ok := wantedAspect[normalizedKey]; ok {
			continue
		}
		if _, ok := wantedSize[normalizedKey]; ok {
			continue
		}
		canonical[key] = value
	}

	if aspectRatio != "" {
		normalized, err := normalizeGeminiImageAspectRatio(aspectRatio)
		if err != nil {
			return nil, err
		}
		canonical["aspectRatio"] = normalized
	}
	if imageSize != "" {
		normalized, err := normalizeGeminiImageSize(imageSize)
		if err != nil {
			return nil, err
		}
		canonical["imageSize"] = normalized
	}

	if len(canonical) == 0 {
		return nil, nil
	}
	encoded, err := common.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini image config: %w", err)
	}
	return encoded, nil
}

func collectOpenAIImageParts(c *gin.Context, request dto.ImageRequest) ([]dto.GeminiPart, error) {
	sources := make([]string, 0)
	appendSources := func(raw json.RawMessage) error {
		if len(raw) == 0 {
			return nil
		}
		var value any
		if err := common.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("invalid image input: %w", err)
		}
		collectImageSourceStrings(value, &sources, 0)
		return nil
	}

	if err := appendSources(request.Image); err != nil {
		return nil, err
	}
	if err := appendSources(request.Images); err != nil {
		return nil, err
	}
	for key, raw := range request.Extra {
		if isImageInputKey(key) {
			if err := appendSources(raw); err != nil {
				return nil, err
			}
		}
	}

	fileSources, err := collectMultipartImageSources(c)
	if err != nil {
		return nil, err
	}
	sources = append(sources, fileSources...)

	parts := make([]dto.GeminiPart, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		if _, exists := seen[source]; exists {
			continue
		}
		seen[source] = struct{}{}

		mimeType, data, err := resolveGeminiImageSource(c, source)
		if err != nil {
			return nil, fmt.Errorf("resolve image input %q: %w", previewImageSource(source), err)
		}
		if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
			return nil, fmt.Errorf("image input has unsupported MIME type %q", mimeType)
		}
		parts = append(parts, dto.GeminiPart{InlineData: &dto.GeminiInlineData{
			MimeType: mimeType,
			Data:     data,
		}})
	}
	return parts, nil
}

func isImageInputKey(key string) bool {
	switch normalizeImageOptionKey(key) {
	case "image", "images", "inputimage", "inputimages", "inputreference", "referenceimage", "referenceimages", "referenceimageurl", "referenceimageurls":
		return true
	default:
		return false
	}
}

func collectImageSourceStrings(value any, sources *[]string, depth int) {
	if depth > 8 || value == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			*sources = append(*sources, typed)
		}
	case []any:
		for _, child := range typed {
			collectImageSourceStrings(child, sources, depth+1)
		}
	case map[string]any:
		for key, child := range typed {
			normalized := normalizeImageOptionKey(key)
			if normalized == "url" || normalized == "imageurl" || normalized == "data" || normalized == "b64json" || normalized == "inlinedata" {
				collectImageSourceStrings(child, sources, depth+1)
			}
		}
	}
}

func collectMultipartImageSources(c *gin.Context) ([]string, error) {
	if c == nil || c.Request == nil || !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/") {
		return nil, nil
	}
	form := c.Request.MultipartForm
	if form == nil {
		var err error
		form, err = common.ParseMultipartFormReusable(c)
		if err != nil {
			return nil, fmt.Errorf("parse multipart image input: %w", err)
		}
	}

	var sources []string
	for key, files := range form.File {
		if !isImageInputKey(key) {
			continue
		}
		for _, fileHeader := range files {
			file, err := fileHeader.Open()
			if err != nil {
				return nil, fmt.Errorf("open multipart image: %w", err)
			}
			data, readErr := io.ReadAll(io.LimitReader(file, maxGeminiInlineImageBytes+1))
			_ = file.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read multipart image: %w", readErr)
			}
			if int64(len(data)) > maxGeminiInlineImageBytes {
				return nil, fmt.Errorf("multipart image exceeds %d MB", maxGeminiInlineImageBytes/(1024*1024))
			}
			mimeType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
			if index := strings.Index(mimeType, ";"); index >= 0 {
				mimeType = strings.TrimSpace(mimeType[:index])
			}
			if mimeType == "" || mimeType == "application/octet-stream" {
				mimeType = http.DetectContentType(data)
			}
			if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
				return nil, fmt.Errorf("multipart file %q is not an image", fileHeader.Filename)
			}
			sources = append(sources, "data:"+mimeType+";base64,"+base64.StdEncoding.EncodeToString(data))
		}
	}
	return sources, nil
}

func resolveGeminiImageSource(c *gin.Context, source string) (string, string, error) {
	if strings.HasPrefix(strings.ToLower(source), "data:") {
		return service.DecodeBase64FileData(source)
	}
	if strings.HasPrefix(strings.ToLower(source), "http://") || strings.HasPrefix(strings.ToLower(source), "https://") {
		return service.GetBase64Data(c, types.NewURLFileSource(source), "formatting image for Gemini")
	}
	return service.DecodeBase64FileData(source)
}

func previewImageSource(source string) string {
	if len(source) <= 80 {
		return source
	}
	return source[:80] + "..."
}

func isGeminiNativeImageRequest(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	return info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits
}

// GeminiNativeImageHandler converts Gemini generateContent image parts into
// the OpenAI Images API response shape used by /v1/images/generations.
func GeminiNativeImageHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var geminiResponse dto.GeminiChatResponse
	if err := common.Unmarshal(responseBody, &geminiResponse); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	openAIResponse := dto.ImageResponse{
		Created: common.GetTimestamp(),
		Data:    make([]dto.ImageData, 0),
	}
	for _, candidate := range geminiResponse.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && part.InlineData.Data != "" && strings.HasPrefix(strings.ToLower(part.InlineData.MimeType), "image/") {
				openAIResponse.Data = append(openAIResponse.Data, dto.ImageData{
					B64Json: part.InlineData.Data,
				})
				continue
			}
			for _, imageURL := range extractGeminiMarkdownImageURLs(part.Text) {
				openAIResponse.Data = append(openAIResponse.Data, dto.ImageData{Url: imageURL})
			}
		}
	}

	if len(openAIResponse.Data) == 0 {
		reason := "no images generated"
		if geminiResponse.PromptFeedback != nil && geminiResponse.PromptFeedback.BlockReason != nil {
			reason = "request blocked by Gemini API: " + *geminiResponse.PromptFeedback.BlockReason
		}
		return nil, types.NewOpenAIError(fmt.Errorf("%s", reason), types.ErrorCodeBadResponseBody, http.StatusBadRequest)
	}

	jsonResponse, err := common.Marshal(openAIResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)

	const imageTokens = 258
	generatedImages := len(openAIResponse.Data)
	if info != nil && info.PriceData.UsePrice && generatedImages <= dto.MaxImageN {
		info.PriceData.AddOtherRatio("n", float64(generatedImages))
	}
	usage := &dto.Usage{
		PromptTokens: imageTokens * generatedImages,
		TotalTokens:  imageTokens * generatedImages,
	}
	return usage, nil
}

func extractGeminiMarkdownImageURLs(text string) []string {
	matches := geminiMarkdownImageURLPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	urls := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 || match[1] == "" {
			continue
		}
		if _, exists := seen[match[1]]; exists {
			continue
		}
		seen[match[1]] = struct{}{}
		urls = append(urls, match[1])
	}
	return urls
}

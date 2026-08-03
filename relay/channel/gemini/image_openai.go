package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const maxGeminiInlineImageBytes = 20 * 1024 * 1024

var supportedGeminiImageAspectRatios = map[string]struct{}{
	"1:1":  {},
	"2:3":  {},
	"3:2":  {},
	"3:4":  {},
	"4:3":  {},
	"9:16": {},
	"16:9": {},
	"21:9": {},
}

func convertOpenAIImageRequestToGemini(c *gin.Context, request dto.ImageRequest) (*dto.GeminiChatRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required for Gemini image generation")
	}

	if request.N != nil && *request.N > 1 {
		return nil, fmt.Errorf("Gemini image models currently support n=1 only")
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

	return &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: parts,
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageConfig:        imageConfigJSON,
		},
	}, nil
}

func normalizeOpenAIImageOptions(request dto.ImageRequest) (string, string, error) {
	aspectRatio := aspectRatioFromOpenAISize(request.Size)
	if aspectRatio == "" {
		aspectRatio = "1:1"
	}

	if raw, ok := findOpenAIImageOption(request.Extra, "aspect_ratio", "aspectRatio", "ratio"); ok {
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
	if raw, ok := findOpenAIImageOption(request.Extra, "image_size", "imageSize", "resolution", "output_resolution", "outputResolution"); ok {
		resolution = raw
	}
	if resolution == "" && request.Quality != "" {
		resolution = request.Quality
	}
	if resolution == "" && request.Size != "" && !isAspectRatio(request.Size) && request.Size != "auto" {
		resolution = request.Size
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
	if normalized == "" || normalized == "auto" {
		return ""
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
	default:
		return ""
	}
}

func normalizeGeminiImageAspectRatio(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if _, ok := supportedGeminiImageAspectRatios[normalized]; !ok {
		return "", fmt.Errorf("unsupported image aspect ratio %q; use 1:1, 2:3, 3:2, 3:4, 4:3, 9:16, 16:9, or 21:9", raw)
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
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[normalizeImageOptionKey(name)] = struct{}{}
	}
	return findImageOptionValue(root, wanted, 0)
}

func findImageOptionValue(value any, wanted map[string]struct{}, depth int) (string, bool) {
	if depth > 8 {
		return "", false
	}

	if object, ok := value.(map[string]any); ok {
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
		for key, child := range object {
			if _, ok := wanted[normalizeImageOptionKey(key)]; ok {
				if scalar, ok := imageOptionScalar(child); ok {
					return scalar, true
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
			if part.InlineData == nil || part.InlineData.Data == "" || !strings.HasPrefix(strings.ToLower(part.InlineData.MimeType), "image/") {
				continue
			}
			openAIResponse.Data = append(openAIResponse.Data, dto.ImageData{
				B64Json: part.InlineData.Data,
			})
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

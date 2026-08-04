package geminichat

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

var geminiToOpenAIImageAspectRatios = map[string]struct{}{
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

// applyGeminiImageOptionsToOpenAIRequest keeps the image-specific part of a
// native Gemini request when the selected channel is OpenAI-compatible. The
// nested extra_body shape is understood by the reverse OpenAI->Gemini bridge
// as well as by common OpenAI-compatible Gemini gateways.
func applyGeminiImageOptionsToOpenAIRequest(geminiRequest *dto.GeminiChatRequest, openAIRequest *dto.GeneralOpenAIRequest, info convmeta.Meta) error {
	if geminiRequest == nil || openAIRequest == nil {
		return nil
	}

	config := make(map[string]any)
	if len(geminiRequest.GenerationConfig.ImageConfig) > 0 {
		if err := common.Unmarshal(geminiRequest.GenerationConfig.ImageConfig, &config); err != nil {
			return fmt.Errorf("invalid Gemini image config: %w", err)
		}
	}
	responseFormatConfig, err := geminiResponseFormatImageConfig(geminiRequest.GenerationConfig.ResponseFormat)
	if err != nil {
		return err
	}
	for key, value := range responseFormatConfig {
		if _, exists := config[key]; !exists {
			config[key] = value
		}
	}

	aspectRatio, imageSize, err := normalizeGeminiToOpenAIImageConfig(config)
	if err != nil {
		return err
	}
	// OpenAI's `size` is the resolution tier or an exact width x height. The
	// aspect ratio is a separate field; putting `16:9` in `size` makes some
	// gateways ignore the requested 4K tier and fall back to their default.
	if aspectRatio != "" {
		openAIRequest.AspectRatio = aspectRatio
	}
	if imageSize != "" {
		openAIRequest.Size = strings.ToLower(imageSize)
		openAIRequest.OutputResolution = imageSize
	}

	imageRequested := geminiResponseRequestsImage(geminiRequest.GenerationConfig.ResponseModalities) ||
		aspectRatio != "" || imageSize != "" || len(responseFormatConfig) > 0 ||
		model_setting.IsGeminiModelSupportImagine(geminiToOpenAIModelName(openAIRequest, info))
	if !imageRequested {
		return nil
	}

	// OpenAI-compatible gateways commonly use the chat-completions modalities
	// field for image-capable Gemini models. Keep text enabled because Gemini
	// image responses may include a short text part as well.
	openAIRequest.Modalities = json.RawMessage(`["text","image"]`)

	// Keep only provider-specific options that are not represented by the
	// documented OpenAI fields above. Do not synthesize a `google.image_config`
	// object for the canonical aspect/size values, because it can introduce a
	// second, conflicting source of truth in OpenAI-compatible gateways.
	providerConfig := make(map[string]any)
	for key, value := range config {
		switch normalizeGeminiImageKey(key) {
		case "aspectratio", "ratio", "aspect", "imagesize", "resolution", "outputresolution", "exactsize", "quality", "size":
			continue
		default:
			providerConfig[key] = value
		}
	}
	if len(providerConfig) == 0 {
		return nil
	}
	extraBody := make(map[string]any)
	if len(openAIRequest.ExtraBody) > 0 {
		if err := common.Unmarshal(openAIRequest.ExtraBody, &extraBody); err != nil {
			return fmt.Errorf("invalid OpenAI extra_body: %w", err)
		}
	}
	googleBody := ensureObject(extraBody, "google")
	imageConfig := ensureObject(googleBody, "image_config")
	for key, value := range providerConfig {
		imageConfig[key] = value
	}
	googleBody["image_config"] = imageConfig
	extraBody["google"] = googleBody
	encoded, err := common.Marshal(extraBody)
	if err != nil {
		return fmt.Errorf("marshal OpenAI image extra_body: %w", err)
	}
	openAIRequest.ExtraBody = encoded
	return nil
}

func geminiToOpenAIModelName(request *dto.GeneralOpenAIRequest, info convmeta.Meta) string {
	if info != nil {
		if name := strings.TrimSpace(convmeta.UpstreamModelName(info)); name != "" {
			return name
		}
		if name := strings.TrimSpace(info.GetOriginModelName()); name != "" {
			return name
		}
	}
	if request != nil {
		return request.Model
	}
	return ""
}

func geminiResponseRequestsImage(modalities []string) bool {
	for _, modality := range modalities {
		if strings.EqualFold(strings.TrimSpace(modality), "IMAGE") {
			return true
		}
	}
	return false
}

func geminiResponseFormatImageConfig(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	var root map[string]any
	if err := common.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid Gemini response format: %w", err)
	}
	if len(root) == 0 {
		return nil, nil
	}
	if image, ok := findGeminiImageObject(root, 0); ok {
		return image, nil
	}
	return root, nil
}

func findGeminiImageObject(value map[string]any, depth int) (map[string]any, bool) {
	if depth > 8 {
		return nil, false
	}
	for key, child := range value {
		if normalizeGeminiImageKey(key) != "image" {
			continue
		}
		if image, ok := child.(map[string]any); ok {
			return image, true
		}
	}
	for _, child := range value {
		if object, ok := child.(map[string]any); ok {
			if image, found := findGeminiImageObject(object, depth+1); found {
				return image, true
			}
		}
	}
	return nil, false
}

func normalizeGeminiToOpenAIImageConfig(config map[string]any) (string, string, error) {
	if len(config) == 0 {
		return "", "", nil
	}
	aspectRaw, _ := findGeminiImageConfigScalarByKeys(config, []string{"aspectRatio", "aspect_ratio", "ratio", "aspect"})
	imageSizeRaw, _ := findGeminiImageConfigScalarByKeys(config, []string{"imageSize", "image_size", "outputResolution", "output_resolution", "resolution", "exactSize", "exact_size", "size", "quality"})
	dimensionSize := geminiImageDimensionSize(config)
	if aspectRaw == "" && dimensionSize != "" {
		aspectRaw = dimensionSize
	}
	if imageSizeRaw == "" && dimensionSize != "" {
		imageSizeRaw = dimensionSize
	}
	if aspectRaw == "" && imageSizeRaw != "" {
		if _, aspectErr := normalizeGeminiToOpenAIAspectRatio(imageSizeRaw); aspectErr == nil {
			aspectRaw = imageSizeRaw
			if strings.Contains(imageSizeRaw, ":") {
				imageSizeRaw = ""
			}
		}
	}

	aspectRatio := ""
	if aspectRaw != "" {
		var err error
		aspectRatio, err = normalizeGeminiToOpenAIAspectRatio(aspectRaw)
		if err != nil {
			return "", "", err
		}
	}
	imageSize := ""
	if imageSizeRaw != "" {
		var err error
		imageSize, err = normalizeGeminiToOpenAIImageSize(imageSizeRaw)
		if err != nil {
			return "", "", err
		}
	}
	return aspectRatio, imageSize, nil
}

func geminiImageDimensionSize(config map[string]any) string {
	width, widthOK := findGeminiImageConfigScalar(config, map[string]struct{}{
		"width":       {},
		"imagewidth":  {},
		"outputwidth": {},
	}, 0)
	height, heightOK := findGeminiImageConfigScalar(config, map[string]struct{}{
		"height":       {},
		"imageheight":  {},
		"outputheight": {},
	}, 0)
	if !widthOK || !heightOK {
		return ""
	}
	return strings.TrimSpace(width) + "x" + strings.TrimSpace(height)
}

func findGeminiImageConfigScalar(value any, wanted map[string]struct{}, depth int) (string, bool) {
	if depth > 8 || value == nil {
		return "", false
	}
	if object, ok := value.(map[string]any); ok {
		for _, preferred := range []string{"image", "image_config", "imageConfig", "generationConfig", "generation_config", "parameters"} {
			for key, child := range object {
				if normalizeGeminiImageKey(key) != normalizeGeminiImageKey(preferred) {
					continue
				}
				if found, ok := findGeminiImageConfigScalar(child, wanted, depth+1); ok {
					return found, true
				}
			}
		}
		for key, child := range object {
			if _, ok := wanted[normalizeGeminiImageKey(key)]; ok {
				if scalar, ok := geminiImageConfigScalar(child); ok {
					return scalar, true
				}
			}
		}
		for _, child := range object {
			if found, ok := findGeminiImageConfigScalar(child, wanted, depth+1); ok {
				return found, true
			}
		}
	}
	if values, ok := value.([]any); ok {
		for _, child := range values {
			if found, ok := findGeminiImageConfigScalar(child, wanted, depth+1); ok {
				return found, true
			}
		}
	}
	return "", false
}

func findGeminiImageConfigScalarByKeys(value any, keys []string) (string, bool) {
	for _, key := range keys {
		if found, ok := findGeminiImageConfigScalar(value, map[string]struct{}{normalizeGeminiImageKey(key): {}}, 0); ok {
			return found, true
		}
	}
	return "", false
}

func geminiImageConfigScalar(value any) (string, bool) {
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

func normalizeGeminiImageKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(key)))
}

func normalizeGeminiToOpenAIAspectRatio(raw string) (string, error) {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(raw)))
	if _, ok := geminiToOpenAIImageAspectRatios[normalized]; ok {
		return normalized, nil
	}
	if mapped := geminiToOpenAIDimensionAspectRatio(normalized); mapped != "" {
		return mapped, nil
	}
	return "", fmt.Errorf("unsupported Gemini image aspect ratio %q", raw)
}

func geminiToOpenAIDimensionAspectRatio(raw string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "x")
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
	if bestDifference < math.MaxFloat64 {
		return bestRatio
	}
	return ""
}

func normalizeGeminiToOpenAIImageSize(raw string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	switch normalized {
	case "1K", "2K", "4K":
		return normalized, nil
	case "HD", "HIGH":
		return "2K", nil
	case "STANDARD", "MEDIUM", "LOW", "AUTO":
		return "1K", nil
	default:
		parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "x")
		if len(parts) == 2 {
			width, widthErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			height, heightErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if widthErr == nil && heightErr == nil && width > 0 && height > 0 {
				pixels := width * height
				switch {
				case pixels >= 8_000_000:
					return "4K", nil
				case pixels >= 2_000_000:
					return "2K", nil
				default:
					return "1K", nil
				}
			}
		}
		return "", fmt.Errorf("unsupported Gemini image resolution %q; use 1K, 2K, or 4K", raw)
	}
}

func ensureObject(parent map[string]any, key string) map[string]any {
	if value, ok := parent[key].(map[string]any); ok {
		return value
	}
	value := make(map[string]any)
	parent[key] = value
	return value
}

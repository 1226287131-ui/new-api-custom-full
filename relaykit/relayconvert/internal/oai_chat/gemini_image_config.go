package oaichat

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

var openAIGeminiImageAspectRatios = map[string]struct{}{
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

func applyOpenAIGeminiImageConfig(request dto.GeneralOpenAIRequest, geminiRequest *dto.GeminiChatRequest) error {
	config := make(map[string]interface{})
	if len(geminiRequest.GenerationConfig.ImageConfig) > 0 {
		if err := kitutil.Unmarshal(geminiRequest.GenerationConfig.ImageConfig, &config); err != nil {
			return fmt.Errorf("invalid Gemini image config: %w", err)
		}
	}

	aspectRatio := ""
	if request.Size != "" {
		aspectRatio = openAIImageAspectRatio(request.Size)
		if aspectRatio == "" && !isOpenAIImageResolution(request.Size) && !strings.EqualFold(strings.TrimSpace(request.Size), "auto") {
			return fmt.Errorf("unsupported image size %q for Gemini image model", request.Size)
		}
	}
	for _, raw := range []string{request.AspectRatio, request.Ratio} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		normalized, err := normalizeOpenAIGeminiAspectRatio(raw)
		if err != nil {
			return err
		}
		aspectRatio = normalized
	}
	if raw, ok := findImageConfigOption(request.ExtraBody, "aspect_ratio", "aspectRatio", "ratio"); ok {
		normalized, err := normalizeOpenAIGeminiAspectRatio(raw)
		if err != nil {
			return err
		}
		aspectRatio = normalized
	}
	if aspectRatio != "" {
		config["aspectRatio"] = aspectRatio
	}

	imageSize := request.OutputResolution
	if imageSize == "" {
		imageSize = request.ImageSize
	}
	if imageSize == "" {
		imageSize = request.Resolution
	}
	if raw, ok := findImageConfigOption(request.ExtraBody, "output_resolution", "outputResolution", "image_size", "imageSize", "resolution", "quality"); ok {
		imageSize = raw
	}
	if imageSize == "" && request.Size != "" && !isOpenAIImageAspectRatio(request.Size) && !strings.EqualFold(strings.TrimSpace(request.Size), "auto") {
		imageSize = request.Size
	}
	if imageSize == "" {
		imageSize = request.Quality
	}
	if imageSize != "" {
		normalized, err := normalizeOpenAIGeminiImageSize(imageSize)
		if err != nil {
			return err
		}
		config["imageSize"] = normalized
	}

	if len(config) == 0 {
		return nil
	}
	imageConfig, err := kitutil.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal Gemini image config: %w", err)
	}
	geminiRequest.GenerationConfig.ImageConfig = imageConfig
	if len(geminiRequest.GenerationConfig.ResponseModalities) == 0 {
		geminiRequest.GenerationConfig.ResponseModalities = []string{"TEXT", "IMAGE"}
	}
	return nil
}

func firstImageConfigValue(object map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, wanted := range keys {
		for key, value := range object {
			if normalizeImageConfigKey(key) == normalizeImageConfigKey(wanted) {
				return value, true
			}
		}
	}
	return nil, false
}

func findImageConfigOption(raw []byte, names ...string) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var root map[string]interface{}
	if err := kitutil.Unmarshal(raw, &root); err != nil {
		return "", false
	}
	for _, name := range names {
		wanted := map[string]struct{}{normalizeImageConfigKey(name): {}}
		if value, ok := findImageConfigValue(root, wanted, 0); ok {
			return value, true
		}
	}
	return "", false
}

func findImageConfigValue(value interface{}, wanted map[string]struct{}, depth int) (string, bool) {
	if depth > 8 || value == nil {
		return "", false
	}
	if object, ok := value.(map[string]interface{}); ok {
		for key, child := range object {
			if _, exists := wanted[normalizeImageConfigKey(key)]; exists {
				if scalar, ok := imageConfigScalar(child); ok {
					return scalar, true
				}
			}
		}
		for _, preferred := range []string{"extra_body", "google", "generation_config", "generationConfig", "image_config", "imageConfig", "parameters"} {
			if child, exists := firstImageConfigValue(object, preferred); exists {
				if found, ok := findImageConfigValue(child, wanted, depth+1); ok {
					return found, true
				}
			}
		}
		for _, child := range object {
			if found, ok := findImageConfigValue(child, wanted, depth+1); ok {
				return found, true
			}
		}
	}
	if values, ok := value.([]interface{}); ok {
		for _, child := range values {
			if found, ok := findImageConfigValue(child, wanted, depth+1); ok {
				return found, true
			}
		}
	}
	return "", false
}

func imageConfigScalar(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), strings.TrimSpace(typed) != ""
	case float64:
		return fmt.Sprintf("%v", typed), true
	default:
		return "", false
	}
}

func normalizeImageConfigKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(key)))
}

func openAIImageAspectRatio(size string) string {
	normalized := strings.ToLower(strings.TrimSpace(size))
	if _, ok := openAIGeminiImageAspectRatios[normalized]; ok {
		return normalized
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

func isOpenAIImageAspectRatio(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ":")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func isOpenAIImageResolution(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1k", "2k", "4k", "hd", "high", "standard", "medium", "low":
		return true
	default:
		return false
	}
}

func normalizeOpenAIGeminiAspectRatio(raw string) (string, error) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), " ", ""))
	if _, ok := openAIGeminiImageAspectRatios[normalized]; !ok {
		if mapped := openAIImageAspectRatio(normalized); mapped != "" {
			return mapped, nil
		}
		return "", fmt.Errorf("unsupported Gemini image aspect ratio %q", raw)
	}
	return normalized, nil
}

func normalizeOpenAIGeminiImageSize(raw string) (string, error) {
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
			parts := strings.Split(normalized, "X")
			if len(parts) != 2 {
				return "", fmt.Errorf("unsupported Gemini image resolution %q", raw)
			}
			width, widthErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
			height, heightErr := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
			if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
				return "", fmt.Errorf("unsupported Gemini image resolution %q", raw)
			}
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
		return "", fmt.Errorf("unsupported Gemini image resolution %q; use 1K, 2K, or 4K", raw)
	}
}

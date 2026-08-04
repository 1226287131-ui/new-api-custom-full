package dto

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// mergeGeminiImageConfigAliases folds the image options used by common
// Gemini-compatible clients into the canonical imageConfig object. Native
// Gemini keeps these options under generationConfig.imageConfig, while some
// clients put them at the request or generationConfig level.
//
// Explicit image-size fields take precedence over quality aliases. This keeps
// a real imageSize/outputResolution value authoritative when a client sends a
// UI-only quality field as well.
func mergeGeminiImageConfigAliases(existing json.RawMessage, sources ...map[string]json.RawMessage) (json.RawMessage, error) {
	config := make(map[string]json.RawMessage)
	if len(existing) > 0 && strings.TrimSpace(string(existing)) != "null" {
		if err := common.Unmarshal(existing, &config); err != nil {
			return nil, fmt.Errorf("invalid Gemini image config: %w", err)
		}
	}

	for _, source := range sources {
		if len(source) == 0 {
			continue
		}
		if err := mergeGeminiImageConfigSource(config, source); err != nil {
			return nil, err
		}
	}
	if len(config) == 0 {
		return nil, nil
	}
	return common.Marshal(config)
}

func finalizeGeminiImageConfigQuality(configRaw json.RawMessage) (json.RawMessage, error) {
	if len(configRaw) == 0 || strings.TrimSpace(string(configRaw)) == "null" {
		return configRaw, nil
	}
	var config map[string]json.RawMessage
	if err := common.Unmarshal(configRaw, &config); err != nil {
		return nil, fmt.Errorf("invalid Gemini image config: %w", err)
	}
	if err := applyGeminiImageQualityFallback(config); err != nil {
		return nil, err
	}
	return common.Marshal(config)
}

func mergeGeminiImageConfigSource(target, source map[string]json.RawMessage) error {
	// A few OpenAI-compatible clients wrap image options in image/imageConfig.
	// Copy those fields first, so their provider-specific options are retained.
	for _, wrapper := range []string{"imageConfig", "image_config", "image", "imageOptions", "image_options"} {
		raw, ok := findGeminiRawField(source, wrapper)
		if !ok {
			continue
		}
		if common.GetJsonType(raw) != "object" {
			// `image` is also used by some clients as a single image URL;
			// only an object can contain image-generation options.
			continue
		}
		var nested map[string]json.RawMessage
		if err := common.Unmarshal(raw, &nested); err != nil {
			return fmt.Errorf("invalid Gemini image options at %s: %w", wrapper, err)
		}
		mergeGeminiImageConfigObject(target, nested)
	}

	// Direct aliases are intentionally processed in a stable order. The first
	// explicit value in each semantic group remains the source of truth.
	for _, key := range []string{"aspectRatio", "aspect_ratio", "ratio", "aspect"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "aspectRatio", raw, "aspect")
		}
	}
	for _, key := range []string{"imageSize", "image_size", "outputResolution", "output_resolution", "resolution", "exactSize", "exact_size", "size"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "imageSize", raw, "resolution")
		}
	}
	for _, key := range []string{"width", "imageWidth", "image_width", "outputWidth", "output_width"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "width", raw, "width")
		}
	}
	for _, key := range []string{"height", "imageHeight", "image_height", "outputHeight", "output_height"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "height", raw, "height")
		}
	}

	// Keep quality as a fallback signal until all sources have been merged.
	// A client can send generationConfig.quality=standard together with a
	// top-level resolution=4k; resolving quality here would incorrectly lock
	// the request to 1K before the explicit 4K field is seen.
	if raw, ok := findGeminiRawField(source, "quality"); ok {
		setGeminiImageFieldIfMissing(target, "quality", raw, "quality")
	}

	return nil
}

func mergeGeminiImageConfigObject(target, source map[string]json.RawMessage) {
	if len(source) == 0 {
		return
	}
	for key, value := range source {
		if isGeminiImageConfigWrapper(key) {
			continue
		}
		if !hasGeminiImageField(target, key) {
			target[key] = value
		}
	}
	// Re-run the alias pass so snake_case nested options also get canonical
	// names without replacing an already supplied standard field.
	_ = mergeGeminiImageConfigSourceWithoutWrappers(target, source)
}

func mergeGeminiImageConfigSourceWithoutWrappers(target, source map[string]json.RawMessage) error {
	for _, key := range []string{"aspectRatio", "aspect_ratio", "ratio", "aspect"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "aspectRatio", raw, "aspect")
		}
	}
	for _, key := range []string{"imageSize", "image_size", "outputResolution", "output_resolution", "resolution", "exactSize", "exact_size", "size"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "imageSize", raw, "resolution")
		}
	}
	for _, key := range []string{"width", "imageWidth", "image_width", "outputWidth", "output_width"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "width", raw, "width")
		}
	}
	for _, key := range []string{"height", "imageHeight", "image_height", "outputHeight", "output_height"} {
		if raw, ok := findGeminiRawField(source, key); ok {
			setGeminiImageFieldIfMissing(target, "height", raw, "height")
		}
	}
	if raw, ok := findGeminiRawField(source, "quality"); ok {
		setGeminiImageFieldIfMissing(target, "quality", raw, "quality")
	}
	return nil
}

func findGeminiRawField(source map[string]json.RawMessage, wanted string) (json.RawMessage, bool) {
	if value, ok := source[wanted]; ok {
		return value, true
	}
	wanted = normalizeGeminiImageOptionKey(wanted)
	for key, value := range source {
		if normalizeGeminiImageOptionKey(key) == wanted {
			return value, true
		}
	}
	return nil, false
}

func applyGeminiImageQualityFallback(config map[string]json.RawMessage) error {
	if len(config) == 0 || hasGeminiImageField(config, "resolution") {
		return nil
	}
	raw, ok := findGeminiRawField(config, "quality")
	if !ok {
		return nil
	}
	mapped, err := geminiQualityResolution(raw)
	if err != nil {
		return err
	}
	if len(mapped) > 0 {
		config["imageSize"] = mapped
	}
	return nil
}

func geminiImageConfigResolution(configRaw json.RawMessage) string {
	if len(configRaw) == 0 || strings.TrimSpace(string(configRaw)) == "null" {
		return ""
	}
	var config map[string]json.RawMessage
	if err := common.Unmarshal(configRaw, &config); err != nil {
		return ""
	}
	for _, key := range []string{"imageSize", "image_size", "outputResolution", "output_resolution", "resolution", "exactSize", "exact_size", "size"} {
		raw, ok := findGeminiRawField(config, key)
		if !ok {
			continue
		}
		var value string
		if err := common.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	width := geminiImageConfigDimension(config, "width", "imageWidth", "image_width", "outputWidth", "output_width")
	height := geminiImageConfigDimension(config, "height", "imageHeight", "image_height", "outputHeight", "output_height")
	if width != "" && height != "" {
		return width + "x" + height
	}
	return ""
}

func geminiImageConfigDimension(config map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := findGeminiRawField(config, key)
		if !ok {
			continue
		}
		var number json.Number
		if err := common.Unmarshal(raw, &number); err == nil && strings.TrimSpace(number.String()) != "" {
			return number.String()
		}
		var value string
		if err := common.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func setGeminiImageFieldIfMissing(target map[string]json.RawMessage, canonical string, value json.RawMessage, group string) {
	if len(value) == 0 || strings.TrimSpace(string(value)) == "null" || hasGeminiImageField(target, group) {
		return
	}
	target[canonical] = value
}

func hasGeminiImageField(object map[string]json.RawMessage, wanted string) bool {
	wanted = normalizeGeminiImageOptionKey(wanted)
	for key := range object {
		normalized := normalizeGeminiImageOptionKey(key)
		if normalized == wanted {
			return true
		}
		switch wanted {
		case "resolution":
			if normalized == "imagesize" || normalized == "outputresolution" || normalized == "exactsize" {
				return true
			}
		case "aspect":
			if normalized == "aspectratio" || normalized == "ratio" {
				return true
			}
		case "width":
			if normalized == "imagewidth" || normalized == "outputwidth" {
				return true
			}
		case "height":
			if normalized == "imageheight" || normalized == "outputheight" {
				return true
			}
		}
	}
	return false
}

func isGeminiImageConfigWrapper(key string) bool {
	switch normalizeGeminiImageOptionKey(key) {
	case "imageconfig", "image", "imageoptions":
		return true
	default:
		return false
	}
}

func normalizeGeminiImageOptionKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(key)))
}

func geminiQualityResolution(raw json.RawMessage) (json.RawMessage, error) {
	var quality string
	if err := common.Unmarshal(raw, &quality); err != nil {
		// Non-string quality is left for the normal converter/validator to
		// reject if it is otherwise relevant.
		return nil, nil
	}
	var resolution string
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "1k", "standard", "medium", "low":
		resolution = "1K"
	case "2k", "hd", "high":
		resolution = "2K"
	case "4k", "ultra", "highest", "max":
		resolution = "4K"
	default:
		return nil, nil
	}
	return common.Marshal(resolution)
}

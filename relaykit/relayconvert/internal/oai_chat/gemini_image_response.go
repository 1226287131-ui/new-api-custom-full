package oaichat

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	relaymedia "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// OpenAI-compatible image gateways commonly put generated images in one of
// four places: an image_url content item, a b64_json/data field, a data URL,
// or a Markdown image embedded in message.content. Gemini clients generally
// only consume inlineData, so normalize all of those forms here.
var openAIResponseMarkdownImagePattern = regexp.MustCompile(`(?i)!?\[[^\]]*\]\(\s*(?:<)?((?:https?://|data:image/)[^\s)>]+)(?:>)?(?:\s+["'][^)]*["'])?\s*\)`)

func openAIMessageToGeminiParts(c context.Context, message dto.Message, info convmeta.Meta) ([]dto.GeminiPart, error) {
	parts := make([]dto.GeminiPart, 0)
	switch content := message.Content.(type) {
	case string:
		return appendOpenAIResponseTextParts(c, parts, content, info)
	case map[string]any:
		return appendOpenAIResponseContentItem(c, parts, content, info)
	case []any:
		for _, item := range content {
			var err error
			parts, err = appendOpenAIResponseContentItem(c, parts, item, info)
			if err != nil {
				return nil, err
			}
		}
	case []dto.MediaContent:
		for _, item := range content {
			var err error
			parts, err = appendOpenAIResponseContentItem(c, parts, item, info)
			if err != nil {
				return nil, err
			}
		}
	default:
		// A few gateways decode content into a concrete slice/map type instead
		// of []any. Re-normalize that uncommon shape without changing DTOs.
		if content != nil {
			if raw, err := kitutil.Marshal(content); err == nil {
				var items []any
				if kitutil.Unmarshal(raw, &items) == nil {
					for _, item := range items {
						parts, err = appendOpenAIResponseContentItem(c, parts, item, info)
						if err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}

	if len(parts) == 0 {
		if text := message.StringContent(); text != "" {
			return appendOpenAIResponseTextParts(c, parts, text, info)
		}
	}
	return parts, nil
}

func appendOpenAIResponseContentItem(c context.Context, parts []dto.GeminiPart, item any, info convmeta.Meta) ([]dto.GeminiPart, error) {
	switch typed := item.(type) {
	case dto.MediaContent:
		if typed.Type == dto.ContentTypeText {
			return appendOpenAIResponseTextParts(c, parts, typed.Text, info)
		}
		if image := typed.GetImageMedia(); image != nil {
			return appendOpenAIImagePart(c, parts, image.Url, image.MimeType)
		}
		return parts, nil
	case map[string]any:
		contentType := normalizeOpenAIResponseContentType(stringValue(typed["type"]))
		if contentType == "text" || contentType == "outputtext" {
			return appendOpenAIResponseTextParts(c, parts, stringValue(typed["text"]), info)
		}
		if contentType == "imageurl" || contentType == "image" || contentType == "outputimage" || contentType == "generatedimage" || contentType == "inlineimage" || contentType == "inputimage" {
			source, mimeType, ok := openAIImageSourceFromValue(typed)
			if !ok {
				return parts, nil
			}
			return appendOpenAIImagePart(c, parts, source, mimeType)
		}
		if source, mimeType, ok := openAIImageSourceFromValue(typed); ok {
			return appendOpenAIImagePart(c, parts, source, mimeType)
		}
		if text := stringValue(typed["text"]); text != "" {
			return appendOpenAIResponseTextParts(c, parts, text, info)
		}
	case string:
		return appendOpenAIResponseTextParts(c, parts, typed, info)
	}
	return parts, nil
}

func appendOpenAIResponseTextParts(c context.Context, parts []dto.GeminiPart, text string, info convmeta.Meta) ([]dto.GeminiPart, error) {
	if strings.TrimSpace(text) == "" {
		return parts, nil
	}

	matches := openAIResponseMarkdownImagePattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) > 0 {
		cursor := 0
		for _, match := range matches {
			if len(match) < 4 {
				continue
			}
			start, end := match[0], match[1]
			if start > cursor {
				parts = appendGeminiTextPart(parts, text[cursor:start])
			}
			source := text[match[2]:match[3]]
			var err error
			parts, err = appendOpenAIImagePart(c, parts, source, "")
			if err != nil {
				return nil, err
			}
			cursor = end
		}
		if cursor < len(text) {
			parts = appendGeminiTextPart(parts, text[cursor:])
		}
		return parts, nil
	}

	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(strings.ToLower(trimmed), "data:image/") {
		return appendOpenAIImagePart(c, parts, trimmed, "")
	}

	// Some gateways return {"url": ...} or {"b64_json": ...} as the text
	// content of a chat completion instead of using an image content item.
	if isGeminiImagineResponse(info) && strings.HasPrefix(trimmed, "{") {
		var object map[string]any
		if kitutil.Unmarshal([]byte(trimmed), &object) == nil {
			if source, mimeType, ok := openAIImageSourceFromValue(object); ok {
				return appendOpenAIImagePart(c, parts, source, mimeType)
			}
		}
	}
	if isGeminiImagineResponse(info) && isHTTPImageURL(trimmed) {
		return appendOpenAIImagePart(c, parts, trimmed, "")
	}
	return appendGeminiTextPart(parts, text), nil
}

func appendGeminiTextPart(parts []dto.GeminiPart, text string) []dto.GeminiPart {
	if text == "" {
		return parts
	}
	return append(parts, dto.GeminiPart{Text: text})
}

func appendOpenAIImagePart(c context.Context, parts []dto.GeminiPart, source string, mimeHint string) ([]dto.GeminiPart, error) {
	mimeType, data, err := resolveOpenAIImageSource(c, source, mimeHint)
	if err != nil {
		return nil, fmt.Errorf("convert OpenAI image response to Gemini inlineData: %w", err)
	}
	return append(parts, dto.GeminiPart{
		InlineData: &dto.GeminiInlineData{
			MimeType: mimeType,
			Data:     data,
		},
	}), nil
}

func resolveOpenAIImageSource(c context.Context, source string, mimeHint string) (string, string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", "", fmt.Errorf("image source is empty")
	}
	if strings.HasPrefix(strings.ToLower(source), "data:") {
		return decodeOpenAIDataURL(source, mimeHint)
	}
	if strings.HasPrefix(strings.ToLower(source), "http://") || strings.HasPrefix(strings.ToLower(source), "https://") {
		data, mimeType, err := relaymedia.ResolveBase64Data(c, types.NewURLFileSource(source), "formatting image response for Gemini")
		if err != nil {
			return "", "", err
		}
		return normalizeResolvedImageData(data, mimeType, mimeHint)
	}
	return normalizeResolvedImageData(source, "", mimeHint)
}

func decodeOpenAIDataURL(source string, mimeHint string) (string, string, error) {
	comma := strings.IndexByte(source, ',')
	if comma < 0 {
		return "", "", fmt.Errorf("invalid image data URL")
	}
	header := strings.TrimSpace(source[:comma])
	data := strings.Join(strings.Fields(source[comma+1:]), "")
	mimeType := strings.TrimPrefix(strings.TrimSpace(strings.SplitN(header, ";", 2)[0]), "data:")
	if mimeType == "" || !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		mimeType = mimeHint
	}
	if !strings.Contains(strings.ToLower(header), ";base64") {
		decoded, err := url.PathUnescape(data)
		if err != nil {
			return "", "", fmt.Errorf("decode image data URL: %w", err)
		}
		data = base64.StdEncoding.EncodeToString([]byte(decoded))
	}
	return normalizeResolvedImageData(data, mimeType, mimeHint)
}

func normalizeResolvedImageData(data string, mimeType string, mimeHint string) (string, string, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return "", "", fmt.Errorf("image data is empty")
	}
	if strings.HasPrefix(strings.ToLower(data), "data:") {
		return decodeOpenAIDataURL(data, mimeType)
	}
	data = strings.Join(strings.Fields(data), "")
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		if decoded, rawErr := base64.RawStdEncoding.DecodeString(data); rawErr == nil {
			return normalizeResolvedImageBytes(decoded, data, mimeType, mimeHint)
		}
		return "", "", fmt.Errorf("decode base64 image data: %w", err)
	}
	return normalizeResolvedImageBytes(decoded, data, mimeType, mimeHint)
}

func normalizeResolvedImageBytes(decoded []byte, data string, mimeType string, mimeHint string) (string, string, error) {
	if len(decoded) == 0 {
		return "", "", fmt.Errorf("decoded image data is empty")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		mimeType = mimeHint
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		mimeType = http.DetectContentType(decoded)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		return "", "", fmt.Errorf("response data is not an image")
	}
	return strings.TrimSpace(mimeType), data, nil
}

func openAIImageSourceFromValue(value map[string]any) (string, string, bool) {
	mimeType := firstStringValue(value, "mime_type", "mimeType", "content_type", "contentType", "media_type", "mediaType")
	for _, key := range []string{"b64_json", "b64Json", "base64", "base64Data", "imageBase64", "bytesBase64Encoded"} {
		if source := stringValue(value[key]); source != "" {
			return source, mimeType, true
		}
	}
	for _, key := range []string{"url", "image_url", "imageUrl", "output_url", "outputUrl", "download_url", "downloadUrl", "image", "output_image", "outputImage"} {
		child, exists := value[key]
		if !exists {
			continue
		}
		if source := stringValue(child); source != "" {
			return source, mimeType, true
		}
		if object, ok := child.(map[string]any); ok {
			if source, nestedMime, found := openAIImageSourceFromValue(object); found {
				if mimeType == "" {
					mimeType = nestedMime
				}
				return source, mimeType, true
			}
		}
	}
	if data := stringValue(value["data"]); data != "" && (strings.HasPrefix(strings.ToLower(data), "data:") || looksLikeBase64(data)) {
		return data, mimeType, true
	}
	return "", "", false
}

func firstStringValue(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := stringValue(value[key]); text != "" {
			return text
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func looksLikeBase64(value string) bool {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), "")
	if len(value) < 32 {
		return false
	}
	for _, char := range value {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '+' && char != '/' && char != '=' && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func normalizeOpenAIResponseContentType(value string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(value)))
}

func isHTTPImageURL(value string) bool {
	if !strings.HasPrefix(strings.ToLower(value), "http://") && !strings.HasPrefix(strings.ToLower(value), "https://") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != ""
}

func isGeminiImagineResponse(info convmeta.Meta) bool {
	if info == nil {
		return false
	}
	for _, model := range []string{
		convmeta.UpstreamModelName(info),
		info.GetOriginModelName(),
	} {
		if convmeta.OptionsOf(info).Gemini.SupportsImagineModel(model) {
			return true
		}
	}
	return false
}

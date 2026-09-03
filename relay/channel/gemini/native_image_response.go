package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

const nativeGeminiImageFetchTimeout = 120 * time.Second

var nativeGeminiResponseKeyAliases = map[string]string{
	"inline_data":            "inlineData",
	"mime_type":              "mimeType",
	"file_data":              "fileData",
	"file_uri":               "fileUri",
	"function_call":          "functionCall",
	"function_response":      "functionResponse",
	"thought_signature":      "thoughtSignature",
	"prompt_feedback":        "promptFeedback",
	"block_reason":           "blockReason",
	"usage_metadata":         "usageMetadata",
	"prompt_token_count":     "promptTokenCount",
	"candidates_token_count": "candidatesTokenCount",
	"total_token_count":      "totalTokenCount",
	"thoughts_token_count":   "thoughtsTokenCount",
}

// normalizeNativeGeminiImageResponse repairs provider responses that use
// OpenAI-style image fields or snake_case Gemini fields before they reach a
// native Gemini client. Native clients generally only inspect inlineData.
func normalizeNativeGeminiImageResponse(responseBody []byte, info *relaycommon.RelayInfo) ([]byte, error) {
	var payload any
	if err := common.Unmarshal(responseBody, &payload); err != nil {
		return responseBody, nil
	}

	root, ok := payload.(map[string]any)
	if !ok {
		return responseBody, nil
	}

	normalized, changed := normalizeNativeGeminiResponseValue(root)
	root, ok = normalized.(map[string]any)
	if !ok {
		return responseBody, nil
	}

	imageCount, changed := normalizeNativeGeminiInlineParts(root, changed)
	imageModel := info == nil || isGeminiImagineModel(info)
	if imageCount == 0 && imageModel {
		changed = convertNativeGeminiMarkdownImages(root, info, changed)
		changed = appendNativeGeminiProviderImages(root, info, changed)
	} else if imageCount > 0 {
		changed = stripNativeGeminiMarkdownImageURLs(root, changed)
	}
	if !changed {
		return responseBody, nil
	}

	canonicalBody, err := common.Marshal(root)
	if err != nil {
		return responseBody, fmt.Errorf("marshal normalized Gemini image response: %w", err)
	}
	return canonicalBody, nil
}

func normalizeNativeGeminiResponseValue(value any) (any, bool) {
	switch typed := value.(type) {
	case []any:
		changed := false
		result := make([]any, len(typed))
		for index, item := range typed {
			normalized, itemChanged := normalizeNativeGeminiResponseValue(item)
			result[index] = normalized
			changed = changed || itemChanged
		}
		return result, changed
	case map[string]any:
		result := make(map[string]any, len(typed))
		changed := false

		for key, item := range typed {
			if _, isAlias := nativeGeminiResponseKeyAliases[key]; isAlias {
				continue
			}
			normalized, itemChanged := normalizeNativeGeminiResponseValue(item)
			result[key] = normalized
			changed = changed || itemChanged
		}
		for key, item := range typed {
			canonicalKey := nativeGeminiResponseKeyAliases[key]
			if canonicalKey == "" || canonicalKey == key {
				continue
			}
			if _, exists := result[canonicalKey]; exists {
				changed = true
				continue
			}
			normalized, itemChanged := normalizeNativeGeminiResponseValue(item)
			result[canonicalKey] = normalized
			changed = true
			changed = changed || itemChanged
		}
		return result, changed
	default:
		return value, false
	}
}

func normalizeNativeGeminiInlineParts(root map[string]any, changed bool) (int, bool) {
	candidates, ok := root["candidates"].([]any)
	if !ok {
		return 0, changed
	}
	imageCount := 0
	for _, candidateValue := range candidates {
		candidate, ok := candidateValue.(map[string]any)
		if !ok {
			continue
		}
		content, ok := candidate["content"].(map[string]any)
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}
		for _, partValue := range parts {
			part, ok := partValue.(map[string]any)
			if !ok {
				continue
			}
			inlineData, ok := part["inlineData"].(map[string]any)
			if !ok {
				continue
			}
			data, _ := inlineData["data"].(string)
			if strings.TrimSpace(data) == "" {
				continue
			}
			mimeType, _ := inlineData["mimeType"].(string)
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(data)), "data:image/") {
				parsedMime, parsedData, err := service.DecodeBase64FileData(data)
				if err == nil {
					inlineData["mimeType"] = parsedMime
					inlineData["data"] = parsedData
					mimeType = parsedMime
					changed = true
				}
			}
			if strings.TrimSpace(mimeType) == "" || strings.EqualFold(strings.TrimSpace(mimeType), "image") || strings.EqualFold(strings.TrimSpace(mimeType), "application/octet-stream") {
				inlineData["mimeType"] = "image/png"
				changed = true
				mimeType = "image/png"
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
				imageCount++
			}
		}
	}
	return imageCount, changed
}

func stripNativeGeminiMarkdownImageURLs(root map[string]any, changed bool) bool {
	candidates, ok := root["candidates"].([]any)
	if !ok {
		return changed
	}
	for _, candidateValue := range candidates {
		candidate, ok := candidateValue.(map[string]any)
		if !ok {
			continue
		}
		content, ok := candidate["content"].(map[string]any)
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}
		for _, partValue := range parts {
			part, ok := partValue.(map[string]any)
			if !ok {
				continue
			}
			text, ok := part["text"].(string)
			if !ok || len(extractGeminiMarkdownImageURLs(text)) == 0 {
				continue
			}
			cleaned := geminiMarkdownImageURLPattern.ReplaceAllString(text, "")
			if cleaned == text {
				continue
			}
			if strings.TrimSpace(cleaned) == "" {
				delete(part, "text")
			} else {
				part["text"] = cleaned
			}
			changed = true
		}
	}
	return changed
}

func convertNativeGeminiMarkdownImages(root map[string]any, info *relaycommon.RelayInfo, changed bool) bool {
	candidates, ok := root["candidates"].([]any)
	if !ok {
		return changed
	}
	for _, candidateValue := range candidates {
		candidate, ok := candidateValue.(map[string]any)
		if !ok {
			continue
		}
		content, ok := candidate["content"].(map[string]any)
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}
		for _, partValue := range parts {
			part, ok := partValue.(map[string]any)
			if !ok {
				continue
			}
			text, ok := part["text"].(string)
			if !ok {
				continue
			}
			urls := extractGeminiMarkdownImageURLs(text)
			if len(urls) == 0 {
				continue
			}
			successfulURLs := make(map[string]struct{}, len(urls))
			for _, imageURL := range urls {
				fetchedMime, fetchedData, err := fetchNativeGeminiImage(imageURL, info)
				if err != nil {
					logger.LogWarn(context.Background(), fmt.Sprintf("native Gemini markdown image conversion failed: %v", err))
					continue
				}
				parts = append(parts, map[string]any{"inlineData": map[string]any{
					"mimeType": fetchedMime,
					"data":     fetchedData,
				}})
				successfulURLs[imageURL] = struct{}{}
				changed = true
			}
			if len(successfulURLs) == 0 {
				continue
			}
			cleaned := geminiMarkdownImageURLPattern.ReplaceAllStringFunc(text, func(match string) string {
				parts := geminiMarkdownImageURLPattern.FindStringSubmatch(match)
				if len(parts) > 1 {
					if _, ok := successfulURLs[parts[1]]; ok {
						return ""
					}
				}
				return match
			})
			if strings.TrimSpace(cleaned) == "" {
				delete(part, "text")
			} else {
				part["text"] = cleaned
			}
		}
		content["parts"] = parts
	}
	return changed
}

func appendNativeGeminiProviderImages(root map[string]any, info *relaycommon.RelayInfo, changed bool) bool {
	var imageParts []map[string]any
	for _, field := range []string{"data", "images", "outputs"} {
		items, ok := root[field].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			part, ok := nativeGeminiInlinePartFromProviderValue(item, info)
			if ok {
				imageParts = append(imageParts, part)
			}
		}
	}

	if len(imageParts) == 0 {
		return changed
	}

	candidates, _ := root["candidates"].([]any)
	if len(candidates) == 0 {
		candidates = []any{map[string]any{
			"content": map[string]any{
				"role":  "model",
				"parts": []any{},
			},
		}}
	}
	firstCandidate, ok := candidates[0].(map[string]any)
	if !ok {
		return changed
	}
	content, ok := firstCandidate["content"].(map[string]any)
	if !ok {
		content = map[string]any{"role": "model", "parts": []any{}}
		firstCandidate["content"] = content
	}
	parts, _ := content["parts"].([]any)
	for _, imagePart := range imageParts {
		parts = append(parts, imagePart)
	}
	content["parts"] = parts
	candidates[0] = firstCandidate
	root["candidates"] = candidates
	return true
}

func nativeGeminiInlinePartFromProviderValue(value any, info *relaycommon.RelayInfo) (map[string]any, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	mimeType := firstNativeGeminiString(item, "mimeType", "mime_type", "contentType", "content_type")
	base64Data := firstNativeGeminiString(item, "b64_json", "b64Json", "base64", "base64Encoded", "bytesBase64Encoded", "imageBase64")
	if base64Data == "" {
		if data := firstNativeGeminiString(item, "data"); strings.HasPrefix(strings.ToLower(data), "data:image/") {
			parsedMime, parsedData, err := service.DecodeBase64FileData(data)
			if err == nil {
				mimeType, base64Data = parsedMime, parsedData
			}
		} else if data != "" {
			base64Data = data
		}
	}
	if base64Data != "" {
		if mimeType == "" {
			mimeType = "image/png"
		}
		return map[string]any{"inlineData": map[string]any{
			"mimeType": mimeType,
			"data":     base64Data,
		}}, true
	}

	imageURL := firstNativeGeminiURL(item)
	if imageURL == "" {
		return nil, false
	}
	fetchedMime, fetchedData, err := fetchNativeGeminiImage(imageURL, info)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("native Gemini image conversion failed: %v", err))
		return nil, false
	}
	return map[string]any{"inlineData": map[string]any{
		"mimeType": fetchedMime,
		"data":     fetchedData,
	}}, true
}

func firstNativeGeminiString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func firstNativeGeminiURL(value map[string]any) string {
	for _, key := range []string{"url", "imageUrl", "image_url", "outputUrl", "output_url", "downloadUrl", "download_url"} {
		if text, ok := value[key].(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "http") {
			return strings.TrimSpace(text)
		}
	}
	for _, key := range []string{"imageUrl", "image_url"} {
		if nested, ok := value[key].(map[string]any); ok {
			if text, ok := nested["url"].(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "http") {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func fetchNativeGeminiImage(imageURL string, info *relaycommon.RelayInfo) (string, string, error) {
	if err := service.ValidateImageCacheFetchURL(imageURL); err != nil {
		return "", "", fmt.Errorf("image URL blocked: %w", err)
	}

	client := service.GetImageCacheHTTPClient()
	if info != nil {
		proxy := service.ResolveChannelProxy(info.ChannelSetting)
		if proxy != "" {
			proxyClient, err := service.GetImageCacheHTTPClientWithProxy(proxy)
			if err != nil {
				return "", "", fmt.Errorf("create image fetch proxy: %w", err)
			}
			client = proxyClient
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), nativeGeminiImageFetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("create image fetch request: %w", err)
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/*;q=0.9")
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("download image: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("download image returned HTTP %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxGeminiInlineImageBytes)+1))
	if err != nil {
		return "", "", fmt.Errorf("read image: %w", err)
	}
	if len(data) == 0 || len(data) > maxGeminiInlineImageBytes {
		return "", "", fmt.Errorf("image exceeds %d MB limit", maxGeminiInlineImageBytes/(1024*1024))
	}

	mimeType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		mimeType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		if parsed, parseErr := url.Parse(imageURL); parseErr == nil {
			mimeType = mime.TypeByExtension(path.Ext(parsed.Path))
		}
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return "", "", fmt.Errorf("downloaded content is not an image")
	}
	return mimeType, base64.StdEncoding.EncodeToString(data), nil
}

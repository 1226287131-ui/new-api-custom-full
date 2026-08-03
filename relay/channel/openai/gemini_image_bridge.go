package openai

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// mergeOpenAIChatImageFields preserves provider-specific image arrays such as
// choices[].message.images. Those fields are used by OpenRouter-compatible
// image models but are not part of dto.Message, so a normal unmarshal drops
// them before the Gemini response converter can create inlineData parts.
func mergeOpenAIChatImageFields(responseBody []byte, response *dto.OpenAITextResponse) {
	if response == nil || len(response.Choices) == 0 {
		return
	}
	var envelope struct {
		Choices []struct {
			Message map[string]any `json:"message"`
			Images  any            `json:"images"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(responseBody, &envelope); err != nil {
		return
	}

	for index := range response.Choices {
		if index >= len(envelope.Choices) {
			break
		}
		providerImages := make([]any, 0)
		for _, key := range []string{"images", "output_images", "generated_images"} {
			providerImages = appendOpenAIProviderImageValues(providerImages, envelope.Choices[index].Message[key])
		}
		providerImages = appendOpenAIProviderImageValues(providerImages, envelope.Choices[index].Images)
		if len(providerImages) == 0 {
			continue
		}

		contentItems := make([]any, 0, len(providerImages)+1)
		switch content := response.Choices[index].Message.Content.(type) {
		case string:
			if content != "" {
				contentItems = append(contentItems, map[string]any{"type": "text", "text": content})
			}
		case []any:
			contentItems = append(contentItems, content...)
		case []dto.MediaContent:
			for _, item := range content {
				contentItems = append(contentItems, item)
			}
		case nil:
		default:
			contentItems = append(contentItems, content)
		}
		for _, image := range providerImages {
			if source, ok := image.(string); ok {
				image = map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": source},
				}
			}
			contentItems = append(contentItems, image)
		}
		response.Choices[index].Message.Content = contentItems
	}
}

func appendOpenAIProviderImageValues(target []any, value any) []any {
	switch typed := value.(type) {
	case []any:
		return append(target, typed...)
	case nil:
		return target
	default:
		return append(target, typed)
	}
}

// openAIImageBodyAsChatResponse adapts an Images API-shaped upstream result
// to the chat response DTO used by the Gemini response converter. This is a
// fallback for gateways that ignore chat-completions and still return data[].
func openAIImageBodyAsChatResponse(responseBody []byte) (*dto.OpenAITextResponse, bool, error) {
	var envelope struct {
		Data    []dto.ImageData `json:"data"`
		Images  []dto.ImageData `json:"images"`
		Outputs []dto.ImageData `json:"outputs"`
		Usage   dto.Usage       `json:"usage"`
		Created int64           `json:"created"`
		Model   string          `json:"model"`
	}
	if err := common.Unmarshal(responseBody, &envelope); err != nil {
		return nil, false, nil
	}
	items := envelope.Data
	if len(items) == 0 {
		items = envelope.Images
	}
	if len(items) == 0 {
		items = envelope.Outputs
	}
	if len(items) == 0 {
		return nil, false, nil
	}

	content := make([]dto.MediaContent, 0, len(items))
	for _, item := range items {
		source := strings.TrimSpace(item.Url)
		if source == "" {
			data := strings.TrimSpace(item.B64Json)
			if data != "" {
				source = "data:image/png;base64," + data
			}
		}
		if source == "" {
			return nil, false, fmt.Errorf("upstream image result has no url or b64_json")
		}
		content = append(content, dto.MediaContent{
			Type: dto.ContentTypeImageURL,
			ImageUrl: &dto.MessageImageUrl{
				Url: source,
			},
		})
	}

	message := dto.Message{Role: "assistant"}
	message.SetMediaContent(content)
	return &dto.OpenAITextResponse{
		Object:  "chat.completion",
		Model:   envelope.Model,
		Created: envelope.Created,
		Choices: []dto.OpenAITextResponseChoice{{
			Index:        0,
			Message:      message,
			FinishReason: "stop",
		}},
		Usage: envelope.Usage,
	}, true, nil
}

package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

type geminiImageEndpointRequest struct {
	Model            string   `json:"model,omitempty"`
	Prompt           string   `json:"prompt"`
	N                *int     `json:"n,omitempty"`
	Size             string   `json:"size,omitempty"`
	AspectRatio      string   `json:"aspect_ratio,omitempty"`
	OutputResolution string   `json:"output_resolution,omitempty"`
	Images           []string `json:"images,omitempty"`
}

func buildGeminiImageEndpointRequest(geminiRequest *dto.GeminiChatRequest, openAIRequest *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) (*geminiImageEndpointRequest, bool, error) {
	if geminiRequest == nil || openAIRequest == nil || info == nil || info.ChannelType != constant.ChannelTypeOpenAI || info.IsStream {
		return nil, false, nil
	}
	if !isGeminiImageGenerationRequest(geminiRequest, info) {
		return nil, false, nil
	}

	prompt := collectGeminiImagePrompt(geminiRequest)
	if strings.TrimSpace(prompt) == "" {
		return nil, false, fmt.Errorf("prompt is required for Gemini image generation")
	}

	images, err := collectGeminiImageInputs(geminiRequest)
	if err != nil {
		return nil, false, err
	}

	payload := &geminiImageEndpointRequest{
		Model:            openAIRequest.Model,
		Prompt:           prompt,
		N:                openAIRequest.N,
		Size:             openAIRequest.Size,
		AspectRatio:      openAIRequest.AspectRatio,
		OutputResolution: openAIRequest.OutputResolution,
		Images:           images,
	}
	if payload.Model == "" {
		payload.Model = strings.TrimSpace(info.UpstreamModelName)
	}
	if payload.N != nil {
		if *payload.N <= 0 {
			payload.N = nil
		} else if *payload.N > dto.MaxImageN {
			return nil, false, fmt.Errorf("candidateCount must be between 1 and %d for Gemini image models", dto.MaxImageN)
		}
	}

	info.UseGeminiImageEndpoint = true
	info.GeminiImageEdit = len(images) > 0
	return payload, true, nil
}

func isGeminiImageGenerationRequest(request *dto.GeminiChatRequest, info *relaycommon.RelayInfo) bool {
	if info != nil {
		for _, modelName := range []string{info.UpstreamModelName, info.OriginModelName} {
			if model_setting.IsGeminiModelSupportImagine(modelName) {
				return true
			}
		}
	}
	if request == nil {
		return false
	}
	for _, modality := range request.GenerationConfig.ResponseModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "IMAGE") {
			return true
		}
	}
	return len(request.GenerationConfig.ImageConfig) > 0
}

func collectGeminiImagePrompt(request *dto.GeminiChatRequest) string {
	if request == nil {
		return ""
	}
	texts := make([]string, 0)
	appendText := func(content dto.GeminiChatContent) {
		for _, part := range content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				texts = append(texts, text)
			}
		}
	}
	if request.SystemInstructions != nil {
		appendText(*request.SystemInstructions)
	}
	for _, content := range request.Contents {
		appendText(content)
	}
	return strings.Join(texts, "\n")
}

func collectGeminiImageInputs(request *dto.GeminiChatRequest) ([]string, error) {
	if request == nil {
		return nil, nil
	}
	contents := append([]dto.GeminiChatContent{}, request.Contents...)
	if request.SystemInstructions != nil {
		contents = append(contents, *request.SystemInstructions)
	}
	images := make([]string, 0)
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.InlineData != nil {
				mimeType := strings.TrimSpace(part.InlineData.MimeType)
				if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
					return nil, fmt.Errorf("Gemini inlineData MIME type %q is not an image", mimeType)
				}
				data := strings.TrimSpace(part.InlineData.Data)
				if data == "" {
					return nil, fmt.Errorf("Gemini inlineData is empty")
				}
				images = append(images, "data:"+mimeType+";base64,"+data)
			}
			if part.FileData != nil {
				fileURI := strings.TrimSpace(part.FileData.FileUri)
				if fileURI == "" {
					return nil, fmt.Errorf("Gemini fileData.fileUri is empty")
				}
				images = append(images, fileURI)
			}
		}
	}
	return images, nil
}

func geminiImageEndpointRequestBody(request *geminiImageEndpointRequest) ([]byte, error) {
	if request == nil {
		return nil, fmt.Errorf("Gemini image endpoint request is nil")
	}
	return json.Marshal(request)
}

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
	normalizeOpenAIUsage(&envelope.Usage)
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

	choices := make([]dto.OpenAITextResponseChoice, 0, len(items))
	for index, item := range items {
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
		message := dto.Message{Role: "assistant"}
		message.SetMediaContent([]dto.MediaContent{{
			Type: dto.ContentTypeImageURL,
			ImageUrl: &dto.MessageImageUrl{
				Url: source,
			},
		}})
		choices = append(choices, dto.OpenAITextResponseChoice{
			Index:        index,
			Message:      message,
			FinishReason: "stop",
		})
	}

	return &dto.OpenAITextResponse{
		Object:  "chat.completion",
		Model:   envelope.Model,
		Created: envelope.Created,
		Choices: choices,
		Usage:   envelope.Usage,
	}, true, nil
}

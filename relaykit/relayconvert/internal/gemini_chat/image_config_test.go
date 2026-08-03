package geminichat

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiGenerateContentRequestToOpenAIChatPreservesImageOptions(t *testing.T) {
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "draw a vertical poster"}},
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageConfig:        json.RawMessage(`{"aspect_ratio":"9:16","image_size":"2K"}`),
		},
	}
	info := &convmeta.Values{
		OriginModelName:     "Nano Banana 2",
		UpstreamModelName:   "Nano Banana 2",
		ChannelMetaAttached: true,
	}

	converted, err := GeminiGenerateContentRequestToOpenAIChat(request, info)
	require.NoError(t, err)
	require.NotNil(t, converted)
	assert.Equal(t, "Nano Banana 2", converted.Model)
	assert.Equal(t, "9:16", converted.Size)
	assert.Equal(t, "9:16", converted.AspectRatio)
	assert.Equal(t, "9:16", converted.Ratio)
	assert.Equal(t, "2K", converted.ImageSize)
	assert.Equal(t, "2K", converted.Resolution)

	var modalities []string
	require.NoError(t, common.Unmarshal(converted.Modalities, &modalities))
	assert.Equal(t, []string{"text", "image"}, modalities)

	var extraBody map[string]any
	require.NoError(t, common.Unmarshal(converted.ExtraBody, &extraBody))
	googleBody, ok := extraBody["google"].(map[string]any)
	require.True(t, ok)
	imageConfig, ok := googleBody["image_config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "9:16", imageConfig["aspect_ratio"])
	assert.Equal(t, "2K", imageConfig["image_size"])
}

func TestGeminiGenerateContentRequestToOpenAIChatReadsResponseFormatImageOptions(t *testing.T) {
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "draw"}},
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseFormat: json.RawMessage(`{"image":{"aspectRatio":"3:4","resolution":"4K"}}`),
		},
	}
	info := &convmeta.Values{
		UpstreamModelName:   "Nano Banana Pro",
		ChannelMetaAttached: true,
	}

	converted, err := GeminiGenerateContentRequestToOpenAIChat(request, info)
	require.NoError(t, err)
	assert.Equal(t, "3:4", converted.AspectRatio)
	assert.Equal(t, "4K", converted.ImageSize)
	require.NotEmpty(t, converted.Modalities)
}

func TestGeminiGenerateContentRequestToOpenAIChatDerivesRatioFromDimensions(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		imageConfig string
		wantRatio   string
	}{
		{name: "portrait width and height", imageConfig: `{"width":1024,"height":1792}`, wantRatio: "9:16"},
		{name: "landscape size", imageConfig: `{"size":"1824x1024"}`, wantRatio: "16:9"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := &dto.GeminiChatRequest{
				Contents: []dto.GeminiChatContent{{
					Role:  "user",
					Parts: []dto.GeminiPart{{Text: "draw"}},
				}},
				GenerationConfig: dto.GeminiChatGenerationConfig{
					ResponseModalities: []string{"IMAGE"},
					ImageConfig:        json.RawMessage(testCase.imageConfig),
				},
			}
			info := &convmeta.Values{
				UpstreamModelName:   "Nano Banana 2",
				ChannelMetaAttached: true,
			}

			converted, err := GeminiGenerateContentRequestToOpenAIChat(request, info)

			require.NoError(t, err)
			assert.Equal(t, testCase.wantRatio, converted.AspectRatio)
			assert.Equal(t, testCase.wantRatio, converted.Ratio)
		})
	}
}

func TestGeminiGenerateContentRequestToOpenAIChatLeavesTextModelsUnchanged(t *testing.T) {
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "hello"}},
		}},
	}
	info := &convmeta.Values{
		UpstreamModelName:   "gemini-2.5-flash",
		ChannelMetaAttached: true,
	}

	converted, err := GeminiGenerateContentRequestToOpenAIChat(request, info)
	require.NoError(t, err)
	assert.Empty(t, converted.Modalities)
	assert.Empty(t, converted.ExtraBody)
	assert.Empty(t, converted.AspectRatio)
	assert.Empty(t, converted.ImageSize)
}

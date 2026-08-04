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
	assert.Equal(t, "2k", converted.Size)
	assert.Equal(t, "9:16", converted.AspectRatio)
	assert.Equal(t, "2K", converted.OutputResolution)
	assert.Empty(t, converted.Ratio)
	assert.Empty(t, converted.ImageSize)
	assert.Empty(t, converted.Resolution)

	var modalities []string
	require.NoError(t, common.Unmarshal(converted.Modalities, &modalities))
	assert.Equal(t, []string{"text", "image"}, modalities)
	assert.Empty(t, converted.ExtraBody)
}

func TestGeminiGenerateContentRequestToOpenAIChatDoesNotPutAspectRatioInSize(t *testing.T) {
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "draw a cinematic landscape"}},
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"IMAGE"},
			ImageConfig:        json.RawMessage(`{"aspectRatio":"16:9","imageSize":"4K"}`),
		},
	}

	converted, err := GeminiGenerateContentRequestToOpenAIChat(request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "Nano Banana 2"},
	})

	require.NoError(t, err)
	assert.Equal(t, "16:9", converted.AspectRatio)
	assert.Equal(t, "4k", converted.Size)
	assert.Equal(t, "4K", converted.OutputResolution)
	assert.NotEqual(t, "16:9", converted.Size)

	body, err := common.Marshal(converted)
	require.NoError(t, err)
	var encoded map[string]any
	require.NoError(t, common.Unmarshal(body, &encoded))
	assert.Equal(t, "16:9", encoded["aspect_ratio"])
	assert.Equal(t, "4k", encoded["size"])
	assert.Equal(t, "4K", encoded["output_resolution"])
	assert.NotContains(t, encoded, "ratio")
	assert.NotContains(t, encoded, "image_size")
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
	assert.Equal(t, "4k", converted.Size)
	assert.Equal(t, "4K", converted.OutputResolution)
	require.NotEmpty(t, converted.Modalities)
}

func TestGeminiGenerateContentRequestToOpenAIChatAcceptsBanana2ExtraRatios(t *testing.T) {
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "draw an ultra-tall poster"}},
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"IMAGE"},
			ImageConfig:        json.RawMessage(`{"aspectRatio":"1:8","imageSize":"1K"}`),
		},
	}

	converted, err := GeminiGenerateContentRequestToOpenAIChat(request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "Nano Banana 2"},
	})

	require.NoError(t, err)
	assert.Equal(t, "1:8", converted.AspectRatio)
	assert.Equal(t, "1k", converted.Size)
}

func TestGeminiGenerateContentRequestToOpenAIChatAcceptsDocumentedStandardRatios(t *testing.T) {
	for _, ratio := range []string{"4:5", "5:4"} {
		request := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{{
				Role:  "user",
				Parts: []dto.GeminiPart{{Text: "draw a poster"}},
			}},
			GenerationConfig: dto.GeminiChatGenerationConfig{
				ResponseModalities: []string{"IMAGE"},
				ImageConfig:        json.RawMessage(`{"aspectRatio":"` + ratio + `","imageSize":"1K"}`),
			},
		}
		converted, err := GeminiGenerateContentRequestToOpenAIChat(request, &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "Nano Banana 2"},
		})
		require.NoError(t, err)
		assert.Equal(t, ratio, converted.AspectRatio)
		assert.Equal(t, "1k", converted.Size)
	}
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
			assert.NotEqual(t, testCase.wantRatio, converted.Size)
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

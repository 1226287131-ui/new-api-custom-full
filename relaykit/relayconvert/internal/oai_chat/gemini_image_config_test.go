package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIChatGeminiImageConfigAcceptsGoogleCamelCase(t *testing.T) {
	extraBody, err := kitutil.Marshal(map[string]any{
		"google": map[string]any{
			"imageConfig": map[string]any{
				"aspectRatio": "3:4",
				"imageSize":   "2K",
			},
		},
	})
	require.NoError(t, err)

	request := dto.GeneralOpenAIRequest{
		Model:     "nano-banana-pro-preview",
		Messages:  []dto.Message{{Role: "user", Content: "make a portrait"}},
		ExtraBody: extraBody,
	}
	info := testGeminiImageMeta("nano-banana-pro-preview", "gemini-3-pro-image-preview")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	require.Equal(t, "3:4", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "imageSize").String())
	require.Equal(t, []string{"TEXT", "IMAGE"}, converted.GenerationConfig.ResponseModalities)
}

func TestOpenAIChatGeminiImageConfigAcceptsSizeRatio(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:    "nano-banana-2",
		Messages: []dto.Message{{Role: "user", Content: "make a portrait"}},
		Size:     "3:4",
	}
	info := testGeminiImageMeta("nano-banana-2", "gemini-3.1-flash-image-preview")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	require.Equal(t, "3:4", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
}

func TestOpenAIChatGeminiImageConfigMapsPortraitDimensions(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:    "banana-pro",
		Messages: []dto.Message{{Role: "user", Content: "make a portrait"}},
		Size:     "1024x1365",
	}
	info := testGeminiImageMeta("banana-pro", "gemini-3-pro-image-preview")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	require.Equal(t, "3:4", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "1K", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "imageSize").String())
}

func TestOpenAIChatGeminiImageConfigAcceptsTopLevelAliases(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:       "Nano Banana 2",
		Messages:    []dto.Message{{Role: "user", Content: "make a story portrait"}},
		AspectRatio: "9:16",
		Quality:     "high",
	}
	info := testGeminiImageMeta("Nano Banana 2", "Nano Banana 2")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	require.Equal(t, "9:16", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "imageSize").String())
	require.Equal(t, []string{"TEXT", "IMAGE"}, converted.GenerationConfig.ResponseModalities)
}

func TestOpenAIChatGeminiImageConfigMapsDocumentedResolutionAndAutoRatio(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:            "Nano Banana 2",
		Messages:         []dto.Message{{Role: "user", Content: "preserve the reference composition"}},
		Size:             "4k",
		AspectRatio:      "auto",
		OutputResolution: "4K",
	}
	info := testGeminiImageMeta("Nano Banana 2", "Nano Banana 2")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	require.Equal(t, "auto", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "4K", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "imageSize").String())
}

func TestOpenAIChatGeminiImageConfigAcceptsBanana2ExtraRatios(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:       "Nano Banana 2",
		Messages:    []dto.Message{{Role: "user", Content: "draw an ultra-wide banner"}},
		AspectRatio: "8:1",
	}
	info := testGeminiImageMeta("Nano Banana 2", "Nano Banana 2")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	require.Equal(t, "8:1", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
}

func TestOpenAIChatGeminiImageConfigAcceptsDocumentedStandardRatios(t *testing.T) {
	for _, ratio := range []string{"4:5", "5:4"} {
		request := dto.GeneralOpenAIRequest{
			Model:       "Nano Banana 2",
			Messages:    []dto.Message{{Role: "user", Content: "draw a poster"}},
			AspectRatio: ratio,
		}
		info := testGeminiImageMeta("Nano Banana 2", "Nano Banana 2")
		converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
		require.NoError(t, err)
		assert.Equal(t, ratio, gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
	}
}

func TestOpenAIChatGeminiImageConfigPrefersSizeOverQuality(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:    "Nano Banana 2",
		Messages: []dto.Message{{Role: "user", Content: "draw a landscape"}},
		Size:     "4k",
		Quality:  "auto",
	}
	info := testGeminiImageMeta("Nano Banana 2", "Nano Banana 2")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	assert.Equal(t, "4K", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "imageSize").String())
}

func TestOpenAIChatGeminiImageConfigPrefersOutputResolutionOverSize(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Model:            "Nano Banana 2",
		Messages:         []dto.Message{{Role: "user", Content: "draw a landscape"}},
		Size:             "4k",
		OutputResolution: "2K",
	}
	info := testGeminiImageMeta("Nano Banana 2", "Nano Banana 2")

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	assert.Equal(t, "2K", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "imageSize").String())
}

func testGeminiImageMeta(origin, upstream string) *convmeta.Values {
	return &convmeta.Values{
		OriginModelName:     origin,
		UpstreamModelName:   upstream,
		ChannelMetaAttached: true,
	}
}

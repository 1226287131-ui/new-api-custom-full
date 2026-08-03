package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIChatGeminiImageConfigAcceptsGoogleCamelCase(t *testing.T) {
	extraBody, err := common.Marshal(map[string]any{
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
	info := &relaycommon.RelayInfo{
		OriginModelName: "nano-banana-pro-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

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
	info := &relaycommon.RelayInfo{
		OriginModelName: "nano-banana-2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

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
	info := &relaycommon.RelayInfo{
		OriginModelName: "banana-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

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
	info := &relaycommon.RelayInfo{
		OriginModelName: "Nano Banana 2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "Nano Banana 2",
		},
	}

	converted, err := OpenAIChatRequestToGeminiGenerateContent(nil, request, info)
	require.NoError(t, err)
	require.Equal(t, "9:16", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(converted.GenerationConfig.ImageConfig, "imageSize").String())
	require.Equal(t, []string{"TEXT", "IMAGE"}, converted.GenerationConfig.ResponseModalities)
}

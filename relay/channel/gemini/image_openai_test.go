package gemini

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestConvertOpenAIImageRequestToGeminiPreservesAspectRatio(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "nano-banana-2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	converted, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:  "nano-banana-2",
		Prompt: "a mountain lake",
		Size:   "3:4",
	})
	require.NoError(t, err)
	request, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Equal(t, "3:4", gjson.GetBytes(request.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, []string{"TEXT", "IMAGE"}, request.GenerationConfig.ResponseModalities)
}

func TestConvertOpenAIImageRequestToGeminiMapsPortraitSize(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "nano-banana-2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	converted, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:  "nano-banana-2",
		Prompt: "a portrait poster",
		Size:   "1024x1365",
	})
	require.NoError(t, err)
	request, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Equal(t, "3:4", gjson.GetBytes(request.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "1K", gjson.GetBytes(request.GenerationConfig.ImageConfig, "imageSize").String())
}

func TestConvertOpenAIImageRequestToGeminiAcceptsGoogleOptionsAndReferences(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "nano-banana-pro-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	extra := map[string]json.RawMessage{
		"extra_body": json.RawMessage(`{"google":{"imageConfig":{"aspectRatio":"4:3","imageSize":"2K"}}}`),
	}
	converted, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:  "nano-banana-pro-preview",
		Prompt: "turn the references into a product photo",
		Image:  json.RawMessage(`"data:image/png;base64,aGVsbG8="`),
		Images: json.RawMessage(`["data:image/jpeg;base64,aGVsbG8="]`),
		Extra:  extra,
	})
	require.NoError(t, err)
	request, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Equal(t, "4:3", gjson.GetBytes(request.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(request.GenerationConfig.ImageConfig, "imageSize").String())
	require.Len(t, request.Contents, 1)
	require.Len(t, request.Contents[0].Parts, 3)
	require.Equal(t, "image/png", request.Contents[0].Parts[0].InlineData.MimeType)
	require.Equal(t, "image/jpeg", request.Contents[0].Parts[1].InlineData.MimeType)
	require.Equal(t, "turn the references into a product photo", request.Contents[0].Parts[2].Text)
}

func TestConvertOpenAIImageRequestUsesRatioAndResolutionAliases(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "Nano Banana 2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	converted, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:  "Nano Banana 2",
		Prompt: "a portrait poster",
		Size:   "2048x2048",
		Extra: map[string]json.RawMessage{
			"ratio":      json.RawMessage(`"9:16"`),
			"resolution": json.RawMessage(`"2K"`),
		},
	})
	require.NoError(t, err)
	request, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Equal(t, "9:16", gjson.GetBytes(request.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(request.GenerationConfig.ImageConfig, "imageSize").String())
}

func TestConvertGeminiRequestNormalizesNativeImageConfig(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "Nano Banana 2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "Nano Banana 2",
		},
	}
	request := &dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"TEXT"},
			ImageConfig:        json.RawMessage(`{"aspect_ratio":"9 : 16","image_size":"2048x2048"}`),
		},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Equal(t, []string{"TEXT", "IMAGE"}, convertedRequest.GenerationConfig.ResponseModalities)
	require.Equal(t, "9:16", gjson.GetBytes(convertedRequest.GenerationConfig.ImageConfig, "aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(convertedRequest.GenerationConfig.ImageConfig, "imageSize").String())
}

func TestConvertGeminiRequestNormalizesResponseFormatImageWrapper(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "Nano Banana 2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "Nano Banana 2",
		},
	}
	request := &dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ResponseFormat:     json.RawMessage(`{"image":{"aspectRatio":"9:16","imageSize":"2K"}}`),
		},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Equal(t, "9:16", gjson.GetBytes(convertedRequest.GenerationConfig.ResponseFormat, "image.aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(convertedRequest.GenerationConfig.ResponseFormat, "image.imageSize").String())
	require.Empty(t, convertedRequest.GenerationConfig.ImageConfig)
}

func TestConvertOpenAIImageRequestRejectsMultipleCandidates(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "nano-banana-2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	_, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:  "nano-banana-2",
		Prompt: "a cat",
		N:      common.GetPointer(uint(2)),
	})
	require.EqualError(t, err, "Gemini image models currently support n=1 only")
}

func TestGeminiNativeImageHandlerConvertsInlineData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	responseBody := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}},{"text":"done"}]}}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "nano-banana-2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
		RelayFormat: types.RelayFormatOpenAI,
	}

	usage, newAPIError := GeminiNativeImageHandler(c, info, resp)
	require.Nil(t, newAPIError)
	require.Equal(t, 258, usage.TotalTokens)
	require.Equal(t, "aGVsbG8=", gjson.Get(recorder.Body.String(), "data.0.b64_json").String())
}

func TestGeminiNativeImageHandlerConvertsMarkdownImageURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	imageURL := "https://images.example.test/result.png?signature=abc123"
	responseBody := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"![image](` + imageURL + `)"}]}}]}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "nano-banana-2",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
		RelayFormat: types.RelayFormatOpenAI,
	}

	usage, newAPIError := GeminiNativeImageHandler(c, info, resp)
	require.Nil(t, newAPIError)
	require.Equal(t, 258, usage.TotalTokens)
	require.Equal(t, imageURL, gjson.Get(recorder.Body.String(), "data.0.url").String())
}

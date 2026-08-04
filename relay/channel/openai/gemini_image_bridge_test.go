package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertGeminiNativeTextImageUsesOpenAIImagesEndpoint(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatGemini,
		RelayMode:       relayconstant.RelayModeGemini,
		OriginModelName: "Nano Banana 2",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://upstream.example",
			UpstreamModelName: "Nano Banana 2",
		},
	}
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role:  "user",
			Parts: []dto.GeminiPart{{Text: "A moonlit city"}},
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageConfig:        json.RawMessage(`{"aspectRatio":"16:9","imageSize":"4K"}`),
		},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	require.NoError(t, err)
	payload, ok := converted.(*geminiImageEndpointRequest)
	require.True(t, ok)
	assert.Equal(t, "Nano Banana 2", payload.Model)
	assert.Equal(t, "A moonlit city", payload.Prompt)
	assert.Equal(t, "16:9", payload.AspectRatio)
	assert.Equal(t, "4k", payload.Size)
	assert.Equal(t, "4K", payload.OutputResolution)
	assert.Empty(t, payload.Images)
	assert.True(t, info.UseGeminiImageEndpoint)
	assert.False(t, info.GeminiImageEdit)

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/images/generations", url)

	body, err := geminiImageEndpointRequestBody(payload)
	require.NoError(t, err)
	var encoded map[string]any
	require.NoError(t, json.Unmarshal(body, &encoded))
	assert.Equal(t, "16:9", encoded["aspect_ratio"])
	assert.Equal(t, "4k", encoded["size"])
	assert.Equal(t, "4K", encoded["output_resolution"])
	assert.NotContains(t, encoded, "messages")
}

func TestConvertGeminiNativeReferenceImageUsesOpenAIEditsEndpoint(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatGemini,
		RelayMode:       relayconstant.RelayModeGemini,
		OriginModelName: "Nano Banana 2",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://upstream.example",
			UpstreamModelName: "Nano Banana 2",
		},
	}
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role: "user",
			Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "aGVsbG8="}},
				{Text: "Turn this into watercolor"},
			},
		}},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ResponseModalities: []string{"IMAGE"},
			ImageConfig:        json.RawMessage(`{"aspectRatio":"auto","imageSize":"2K"}`),
		},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	require.NoError(t, err)
	payload, ok := converted.(*geminiImageEndpointRequest)
	require.True(t, ok)
	assert.Equal(t, "Turn this into watercolor", payload.Prompt)
	assert.Equal(t, "auto", payload.AspectRatio)
	assert.Equal(t, "2k", payload.Size)
	require.Len(t, payload.Images, 1)
	assert.Equal(t, "data:image/png;base64,aGVsbG8=", payload.Images[0])
	assert.True(t, info.GeminiImageEdit)

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/images/edits", url)
}

func TestConvertGeminiVisionRequestDoesNotUseImageEndpoint(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatGemini,
		RelayMode:       relayconstant.RelayModeGemini,
		OriginModelName: "gemini-2.5-flash",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://upstream.example",
			UpstreamModelName: "gemini-2.5-flash",
		},
	}
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Role: "user",
			Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "aGVsbG8="}},
				{Text: "What is in this image?"},
			},
		}},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	require.NoError(t, err)
	_, ok := converted.(*geminiImageEndpointRequest)
	assert.False(t, ok)
	assert.False(t, info.UseGeminiImageEndpoint)

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/chat/completions", url)
}

func TestOpenAIImageBodyAsChatResponseSupportsURLAndBase64(t *testing.T) {
	response, ok, err := openAIImageBodyAsChatResponse([]byte(`{
		"created":1710000000,
		"model":"Nano Banana 2",
		"data":[
			{"url":"https://cdn.example.test/one.png"},
			{"b64_json":"aGVsbG8="}
		]
	}`))

	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, response.Choices, 2)
	assert.Equal(t, "Nano Banana 2", response.Model)
	first := response.Choices[0].Message.ParseContent()[0].GetImageMedia()
	require.NotNil(t, first)
	assert.Equal(t, "https://cdn.example.test/one.png", first.Url)
	second := response.Choices[1].Message.ParseContent()[0].GetImageMedia()
	require.NotNil(t, second)
	assert.Equal(t, "data:image/png;base64,aGVsbG8=", second.Url)
	assert.Equal(t, dto.ContentTypeImageURL, response.Choices[0].Message.ParseContent()[0].Type)
}

func TestOpenaiHandlerReturnsGeminiInlineDataForImagesPayload(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/Nano%20Banana%202:generateContent", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aGVsbG8="}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)),
	}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatGemini,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "Nano Banana 2"},
	}

	usage, relayErr := OpenaiHandler(c, info, upstream)

	require.Nil(t, relayErr)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.TotalTokens)

	var response dto.GeminiChatResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Candidates, 1)
	assert.Equal(t, "model", response.Candidates[0].Content.Role)
	require.Len(t, response.Candidates[0].Content.Parts, 1)
	require.NotNil(t, response.Candidates[0].Content.Parts[0].InlineData)
	assert.Equal(t, "image/png", response.Candidates[0].Content.Parts[0].InlineData.MimeType)
	assert.Equal(t, "aGVsbG8=", response.Candidates[0].Content.Parts[0].InlineData.Data)
	assert.Equal(t, 3, response.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 4, response.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 7, response.UsageMetadata.TotalTokenCount)
}

func TestOpenaiHandlerPreservesGeminiImageDimensions(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	imageData := encodeSolidPNGBase64(t, 3840, 2160)
	upstreamBody, err := json.Marshal(map[string]any{
		"created": 1785200000,
		"model":   "Nano Banana 2",
		"data":    []map[string]string{{"b64_json": imageData}},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/Nano%20Banana%202:generateContent", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(upstreamBody))),
	}
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatGemini,
		UseGeminiImageEndpoint: true,
		ChannelMeta:            &relaycommon.ChannelMeta{UpstreamModelName: "Nano Banana 2"},
	}

	_, relayErr := OpenaiHandler(c, info, upstream)
	require.Nil(t, relayErr)

	var response dto.GeminiChatResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Candidates, 1)
	require.Len(t, response.Candidates[0].Content.Parts, 1)
	inlineData := response.Candidates[0].Content.Parts[0].InlineData
	require.NotNil(t, inlineData)
	decoded, err := base64.StdEncoding.DecodeString(inlineData.Data)
	require.NoError(t, err)
	config, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 3840, config.Width)
	assert.Equal(t, 2160, config.Height)
}

func TestOpenaiHandlerReturnsGeminiInlineDataForMessageImages(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/Nano%20Banana%202:generateContent", nil)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"choices":[{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"",
					"images":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]
				},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}
		}`)),
	}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatGemini,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "Nano Banana 2"},
	}

	usage, relayErr := OpenaiHandler(c, info, upstream)

	require.Nil(t, relayErr)
	require.NotNil(t, usage)
	var response dto.GeminiChatResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Candidates, 1)
	require.Len(t, response.Candidates[0].Content.Parts, 1)
	require.NotNil(t, response.Candidates[0].Content.Parts[0].InlineData)
	assert.Equal(t, "image/png", response.Candidates[0].Content.Parts[0].InlineData.MimeType)
	assert.Equal(t, "aGVsbG8=", response.Candidates[0].Content.Parts[0].InlineData.Data)
}

func TestOpenAIImageBodyAsChatResponseIgnoresOrdinaryChatBody(t *testing.T) {
	response, ok, err := openAIImageBodyAsChatResponse([]byte(`{
		"choices":[{"message":{"role":"assistant","content":"hello"}}]
	}`))

	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, response)
}

type solidGrayImage struct {
	bounds image.Rectangle
	value  color.Gray
}

func (i solidGrayImage) ColorModel() color.Model { return color.GrayModel }

func (i solidGrayImage) Bounds() image.Rectangle { return i.bounds }

func (i solidGrayImage) At(_, _ int) color.Color { return i.value }

func encodeSolidPNGBase64(t *testing.T, width, height int) string {
	t.Helper()
	var buffer bytes.Buffer
	imageData := solidGrayImage{
		bounds: image.Rect(0, 0, width, height),
		value:  color.Gray{Y: 128},
	}
	require.NoError(t, png.Encode(&buffer, imageData))
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

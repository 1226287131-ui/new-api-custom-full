package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	require.Len(t, response.Choices, 1)
	assert.Equal(t, "Nano Banana 2", response.Model)
	content := response.Choices[0].Message.ParseContent()
	require.Len(t, content, 2)
	first := content[0].GetImageMedia()
	require.NotNil(t, first)
	assert.Equal(t, "https://cdn.example.test/one.png", first.Url)
	second := content[1].GetImageMedia()
	require.NotNil(t, second)
	assert.Equal(t, "data:image/png;base64,aGVsbG8=", second.Url)
	assert.Equal(t, dto.ContentTypeImageURL, content[0].Type)
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

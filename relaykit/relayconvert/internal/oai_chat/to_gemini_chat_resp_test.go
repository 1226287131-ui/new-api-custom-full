package oaichat

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	relaymedia "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/media"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseOpenAI2GeminiMapsTextToolFinishReasonAndUsage(t *testing.T) {
	msg := dto.Message{
		Role:    "assistant",
		Content: "hello",
	}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_1",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "lookup",
				Arguments: `{"q":"x"}`,
			},
		},
	})

	resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index:        2,
				Message:      msg,
				FinishReason: "length",
			},
		},
		Usage: dto.Usage{
			PromptTokens:     11,
			CompletionTokens: 5,
			TotalTokens:      16,
		},
	}, nil)

	assert.Equal(t, 11, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 5, resp.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 16, resp.UsageMetadata.TotalTokenCount)
	require.NotNil(t, resp.UsageMetadata.BillingUsage)
	require.NotNil(t, resp.UsageMetadata.BillingUsage.OpenAIUsage)
	assert.Equal(t, dto.BillingUsageSourceOAIChat, resp.UsageMetadata.BillingUsage.Source)
	assert.Equal(t, dto.BillingUsageSemanticOpenAI, resp.UsageMetadata.BillingUsage.Semantic)
	assert.Equal(t, 11, resp.UsageMetadata.BillingUsage.OpenAIUsage.PromptTokens)
	assert.Equal(t, 5, resp.UsageMetadata.BillingUsage.OpenAIUsage.CompletionTokens)
	assert.Equal(t, 16, resp.UsageMetadata.BillingUsage.OpenAIUsage.TotalTokens)
	assert.Nil(t, resp.UsageMetadata.BillingUsage.OpenAIUsage.BillingUsage)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, int64(2), resp.Candidates[0].Index)
	require.NotNil(t, resp.Candidates[0].FinishReason)
	assert.Equal(t, "MAX_TOKENS", *resp.Candidates[0].FinishReason)
	require.Len(t, resp.Candidates[0].Content.Parts, 2)
	assert.Equal(t, "hello", resp.Candidates[0].Content.Parts[0].Text)
	require.NotNil(t, resp.Candidates[0].Content.Parts[1].FunctionCall)
	assert.Equal(t, "lookup", resp.Candidates[0].Content.Parts[1].FunctionCall.FunctionName)
	assert.Equal(t, map[string]interface{}{"q": "x"}, resp.Candidates[0].Content.Parts[1].FunctionCall.Arguments)
}

func TestResponseOpenAI2GeminiConvertsMarkdownImageURLToInlineData(t *testing.T) {
	relaymedia.SetMediaResolver(relaymedia.MediaResolver{
		GetBase64Data: func(_ context.Context, source types.FileSource, _ ...string) (string, string, error) {
			assert.Equal(t, "https://cdn.example.test/generated", source.GetRawData())
			return "aGVsbG8=", "image/png", nil
		},
	})
	t.Cleanup(func() {
		relaymedia.SetMediaResolver(relaymedia.MediaResolver{})
	})

	resp, err := ResponseOpenAI2GeminiWithContext(nil, &dto.OpenAITextResponse{
		Choices: []dto.OpenAITextResponseChoice{{
			Index: 0,
			Message: dto.Message{
				Role:    "assistant",
				Content: "done\n![image](https://cdn.example.test/generated)",
			},
			FinishReason: "stop",
		}},
	}, &convmeta.Values{
		UpstreamModelName:   "Nano Banana 2",
		ChannelMetaAttached: true,
	})

	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	require.Len(t, resp.Candidates[0].Content.Parts, 2)
	assert.Equal(t, "done\n", resp.Candidates[0].Content.Parts[0].Text)
	require.NotNil(t, resp.Candidates[0].Content.Parts[1].InlineData)
	assert.Equal(t, "image/png", resp.Candidates[0].Content.Parts[1].InlineData.MimeType)
	assert.Equal(t, "aGVsbG8=", resp.Candidates[0].Content.Parts[1].InlineData.Data)
}

func TestResponseOpenAI2GeminiConvertsImageContentItemsToInlineData(t *testing.T) {
	resp, err := ResponseOpenAI2GeminiWithContext(nil, &dto.OpenAITextResponse{
		Choices: []dto.OpenAITextResponseChoice{{
			Index: 0,
			Message: dto.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "data:image/jpeg;base64,aGVsbG8=",
						},
					},
					map[string]any{"type": "text", "text": "caption"},
				},
			},
			FinishReason: "stop",
		}},
	}, nil)

	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	require.Len(t, resp.Candidates[0].Content.Parts, 2)
	require.NotNil(t, resp.Candidates[0].Content.Parts[0].InlineData)
	assert.Equal(t, "image/jpeg", resp.Candidates[0].Content.Parts[0].InlineData.MimeType)
	assert.Equal(t, "aGVsbG8=", resp.Candidates[0].Content.Parts[0].InlineData.Data)
	assert.Equal(t, "caption", resp.Candidates[0].Content.Parts[1].Text)
}

func TestStreamResponseOpenAI2GeminiConvertsCompleteDataImageChunk(t *testing.T) {
	content := "![image](data:image/png;base64,aGVsbG8=)"
	resp, err := StreamResponseOpenAI2GeminiWithContext(nil, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Index: 0,
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
				Content: &content,
			},
		}},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Candidates, 1)
	require.Len(t, resp.Candidates[0].Content.Parts, 1)
	require.NotNil(t, resp.Candidates[0].Content.Parts[0].InlineData)
	assert.Equal(t, "image/png", resp.Candidates[0].Content.Parts[0].InlineData.MimeType)
}

func TestStreamResponseOpenAI2GeminiMapsToolCallFinishReasonAndUsage(t *testing.T) {
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index:        1,
				FinishReason: geminiRespPtr("tool_calls"),
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{
							Type: "function",
							Function: dto.FunctionResponse{
								Name:      "lookup",
								Arguments: `{"q":"x"}`,
							},
						},
					},
				},
			},
		},
		Usage: &dto.Usage{
			PromptTokens:     13,
			CompletionTokens: 8,
			TotalTokens:      21,
		},
	}, &convmeta.Values{})

	require.NotNil(t, resp)
	assert.Equal(t, 13, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 8, resp.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 21, resp.UsageMetadata.TotalTokenCount)
	require.NotNil(t, resp.UsageMetadata.BillingUsage)
	require.NotNil(t, resp.UsageMetadata.BillingUsage.OpenAIUsage)
	assert.Equal(t, 13, resp.UsageMetadata.BillingUsage.OpenAIUsage.PromptTokens)
	assert.Equal(t, 8, resp.UsageMetadata.BillingUsage.OpenAIUsage.CompletionTokens)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, int64(1), resp.Candidates[0].Index)
	require.NotNil(t, resp.Candidates[0].FinishReason)
	assert.Equal(t, "STOP", *resp.Candidates[0].FinishReason)
	require.Len(t, resp.Candidates[0].Content.Parts, 1)
	require.NotNil(t, resp.Candidates[0].Content.Parts[0].FunctionCall)
	assert.Equal(t, "lookup", resp.Candidates[0].Content.Parts[0].FunctionCall.FunctionName)
	assert.Equal(t, map[string]interface{}{"q": "x"}, resp.Candidates[0].Content.Parts[0].FunctionCall.Arguments)
}

func geminiRespPtr[T any](value T) *T {
	return &value
}

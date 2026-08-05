package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiChatGenerationConfigPreservesExplicitZeroValuesCamelCase(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{
			"topP":0,
			"topK":0,
			"maxOutputTokens":0,
			"candidateCount":0,
			"seed":0,
			"responseLogprobs":false
		}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))

	encoded, err := kitutil.Marshal(req)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, kitutil.Unmarshal(encoded, &out))

	generationConfig, ok := out["generationConfig"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, generationConfig, "topP")
	assert.Contains(t, generationConfig, "topK")
	assert.Contains(t, generationConfig, "maxOutputTokens")
	assert.Contains(t, generationConfig, "candidateCount")
	assert.Contains(t, generationConfig, "seed")
	assert.Contains(t, generationConfig, "responseLogprobs")

	assert.Equal(t, float64(0), generationConfig["topP"])
	assert.Equal(t, float64(0), generationConfig["topK"])
	assert.Equal(t, float64(0), generationConfig["maxOutputTokens"])
	assert.Equal(t, float64(0), generationConfig["candidateCount"])
	assert.Equal(t, float64(0), generationConfig["seed"])
	assert.Equal(t, false, generationConfig["responseLogprobs"])
}

func TestGeminiChatGenerationConfigPreservesExplicitZeroValuesSnakeCase(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{
			"top_p":0,
			"top_k":0,
			"max_output_tokens":0,
			"candidate_count":0,
			"seed":0,
			"response_logprobs":false
		}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))

	encoded, err := kitutil.Marshal(req)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, kitutil.Unmarshal(encoded, &out))

	generationConfig, ok := out["generationConfig"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, generationConfig, "topP")
	assert.Contains(t, generationConfig, "topK")
	assert.Contains(t, generationConfig, "maxOutputTokens")
	assert.Contains(t, generationConfig, "candidateCount")
	assert.Contains(t, generationConfig, "seed")
	assert.Contains(t, generationConfig, "responseLogprobs")

	assert.Equal(t, float64(0), generationConfig["topP"])
	assert.Equal(t, float64(0), generationConfig["topK"])
	assert.Equal(t, float64(0), generationConfig["maxOutputTokens"])
	assert.Equal(t, float64(0), generationConfig["candidateCount"])
	assert.Equal(t, float64(0), generationConfig["seed"])
	assert.Equal(t, false, generationConfig["responseLogprobs"])
}

func TestGeminiChatRequestAcceptsSnakeCaseGenerationConfig(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"make an image"}]}],
		"generation_config":{
			"response_modalities":["IMAGE"],
			"image_config":{
				"aspect_ratio":"9:16",
				"image_size":"2K"
			}
		}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))
	assert.Equal(t, []string{"IMAGE"}, req.GenerationConfig.ResponseModalities)

	var imageConfig map[string]any
	require.NoError(t, kitutil.Unmarshal(req.GenerationConfig.ImageConfig, &imageConfig))
	assert.Equal(t, "9:16", imageConfig["aspect_ratio"])
	assert.Equal(t, "2K", imageConfig["image_size"])
}

func TestGeminiChatRequestAcceptsResponseFormatImageWrapper(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"make an image"}]}],
		"generationConfig":{
			"responseModalities":["TEXT","IMAGE"],
			"responseFormat":{
				"image":{
					"aspectRatio":"9:16",
					"imageSize":"2K"
				}
			}
		}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))

	var responseFormat map[string]any
	require.NoError(t, kitutil.Unmarshal(req.GenerationConfig.ResponseFormat, &responseFormat))
	image, ok := responseFormat["image"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "9:16", image["aspectRatio"])
	assert.Equal(t, "2K", image["imageSize"])
}

func TestGeminiChatRequestNormalizesTopLevelImageAliases(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"make a landscape image"}]}],
		"responseModalities":["TEXT","IMAGE"],
		"aspectRatio":"16:9",
		"resolution":"4k",
		"n":1
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))
	assert.Equal(t, []string{"TEXT", "IMAGE"}, req.GenerationConfig.ResponseModalities)
	require.NotNil(t, req.GenerationConfig.CandidateCount)
	assert.Equal(t, 1, *req.GenerationConfig.CandidateCount)

	var imageConfig map[string]any
	require.NoError(t, kitutil.Unmarshal(req.GenerationConfig.ImageConfig, &imageConfig))
	assert.Equal(t, "16:9", imageConfig["aspectRatio"])
	assert.Equal(t, "4k", imageConfig["imageSize"])
	meta := req.GetTokenCountMeta()
	assert.Equal(t, "4k", meta.ImageResolution)
	assert.Equal(t, float64(1), meta.BillingRatios["n"])
}

func TestGeminiChatRequestMapsQualityWhenResolutionIsMissing(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"make a detailed image"}]}],
		"generationConfig":{"quality":"ultra","aspect_ratio":"9:16"}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))

	var imageConfig map[string]any
	require.NoError(t, kitutil.Unmarshal(req.GenerationConfig.ImageConfig, &imageConfig))
	assert.Equal(t, "9:16", imageConfig["aspectRatio"])
	assert.Equal(t, "4K", imageConfig["imageSize"])
}

func TestGeminiChatRequestExplicitResolutionWinsOverQualityAcrossLevels(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"make a 4K image"}]}],
		"generationConfig":{"quality":"standard"},
		"quality":"standard",
		"resolution":"4k",
		"aspectRatio":"16:9"
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))

	var imageConfig map[string]any
	require.NoError(t, kitutil.Unmarshal(req.GenerationConfig.ImageConfig, &imageConfig))
	assert.Equal(t, "16:9", imageConfig["aspectRatio"])
	assert.Equal(t, "4k", imageConfig["imageSize"])
	assert.Equal(t, "standard", imageConfig["quality"])
}

func TestGeminiChatRequestQualityAutoDoesNotForceOneK(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"make an image"}]}],
		"quality":"auto",
		"aspectRatio":"16:9"
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))

	var imageConfig map[string]any
	require.NoError(t, kitutil.Unmarshal(req.GenerationConfig.ImageConfig, &imageConfig))
	assert.Equal(t, "16:9", imageConfig["aspectRatio"])
	assert.Equal(t, "auto", imageConfig["quality"])
	assert.NotContains(t, imageConfig, "imageSize")
}

func TestGeminiChatRequestAcceptsTopLevelResponseFormatImage(t *testing.T) {
	raw := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"make an image"}]}],
		"response_format":{"image":{"image_size":"4K","aspect_ratio":"3:4"}}
	}`)

	var req GeminiChatRequest
	require.NoError(t, kitutil.Unmarshal(raw, &req))
	require.NotEmpty(t, req.GenerationConfig.ResponseFormat)

	var imageConfig map[string]any
	require.NoError(t, kitutil.Unmarshal(req.GenerationConfig.ImageConfig, &imageConfig))
	assert.Equal(t, "4K", imageConfig["image_size"])
	assert.Equal(t, "3:4", imageConfig["aspect_ratio"])
}

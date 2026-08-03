package gemini

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestNormalizeNativeGeminiImageResponseCanonicalizesSnakeCaseInlineData(t *testing.T) {
	body := []byte(`{"candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/png","data":"aGVsbG8="}}]}}],"usage_metadata":{"prompt_token_count":1}}`)

	normalized, err := normalizeNativeGeminiImageResponse(body, nil)
	require.NoError(t, err)
	require.Contains(t, string(normalized), `"inlineData"`)
	require.Contains(t, string(normalized), `"mimeType"`)
	require.NotContains(t, string(normalized), `"inline_data"`)
	require.NotContains(t, string(normalized), `"mime_type"`)

	var response map[string]any
	require.NoError(t, common.Unmarshal(normalized, &response))
	candidates := response["candidates"].([]any)
	part := candidates[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	inlineData := part["inlineData"].(map[string]any)
	require.Equal(t, "image/png", inlineData["mimeType"])
	require.Equal(t, "aGVsbG8=", inlineData["data"])
}

func TestNormalizeNativeGeminiImageResponseConvertsProviderBase64(t *testing.T) {
	body := []byte(`{"data":[{"b64_json":"aGVsbG8=","mime_type":"image/jpeg"}]}`)

	normalized, err := normalizeNativeGeminiImageResponse(body, nil)
	require.NoError(t, err)
	require.Contains(t, string(normalized), `"candidates"`)
	require.Contains(t, string(normalized), `"inlineData"`)
	require.Contains(t, string(normalized), `"image/jpeg"`)
}

func TestNormalizeNativeGeminiImageResponseRemovesMarkdownURLWhenInlineDataExists(t *testing.T) {
	imageURL := "https://upstream.example/image.png"
	body := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}},{"text":"![image](` + imageURL + `)"}]}}]}`)

	normalized, err := normalizeNativeGeminiImageResponse(body, nil)
	require.NoError(t, err)
	require.NotContains(t, string(normalized), imageURL)
	require.False(t, strings.Contains(string(normalized), "inline_data"))
}

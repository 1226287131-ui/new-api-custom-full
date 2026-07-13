package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestResolveImageResolutionBilling(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		meta      *types.TokenCountMeta
		wantTier  string
		wantCount float64
		wantError bool
	}{
		{
			name:      "missing resolution defaults to 1K",
			body:      `{}`,
			wantTier:  ratio_setting.ImageResolutionTier1K,
			wantCount: 1,
		},
		{
			name:      "OpenAI square size and count",
			body:      `{"size":"2048x2048","n":3}`,
			wantTier:  ratio_setting.ImageResolutionTier2K,
			wantCount: 3,
		},
		{
			name:      "OpenAI landscape 1536 size remains 1K by pixel area",
			body:      `{"size":"1536x1024"}`,
			wantTier:  ratio_setting.ImageResolutionTier1K,
			wantCount: 1,
		},
		{
			name:      "Gemini camel case image size",
			body:      `{"generationConfig":{"imageConfig":{"imageSize":"4K"},"candidateCount":2}}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 2,
		},
		{
			name:      "Gemini snake case image size",
			body:      `{"generation_config":{"image_config":{"image_size":"2k"}}}`,
			wantTier:  ratio_setting.ImageResolutionTier2K,
			wantCount: 1,
		},
		{
			name:      "Gemini Imagen parameters",
			body:      `{"parameters":{"imageSize":"2K","sampleCount":4}}`,
			wantTier:  ratio_setting.ImageResolutionTier2K,
			wantCount: 4,
		},
		{
			name:      "OpenAI Gemini compatibility extra body",
			body:      `{"extra_body":{"google":{"image_config":{"image_size":"4K"}}}}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 1,
		},
		{
			name:      "explicit dimensions fields",
			body:      `{"width":4096,"height":4096}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 1,
		},
		{
			name:      "aspect ratio alone defaults to 1K",
			body:      `{"size":"16:9"}`,
			wantTier:  ratio_setting.ImageResolutionTier1K,
			wantCount: 1,
		},
		{
			name:      "multipart metadata takes precedence",
			body:      `{}`,
			meta:      &types.TokenCountMeta{ImageResolution: "4096x4096", BillingRatios: map[string]float64{"n": 2}},
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 2,
		},
		{
			name:      "unsupported explicit tier is rejected",
			body:      `{"size":"8K"}`,
			wantError: true,
		},
		{
			name:      "oversized count is rejected",
			body:      `{"size":"1K","n":129}`,
			wantError: true,
		},
		{
			name:      "dimensions above 4K tier are rejected",
			body:      `{"size":"8192x8192"}`,
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			billing, err := resolveImageResolutionBilling(billingexpr.RequestInput{Body: []byte(test.body)}, test.meta)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantTier, billing.Tier)
			require.Equal(t, test.wantCount, billing.Count)
		})
	}
}

package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
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
			name:      "standard 2K landscape dimensions",
			body:      `{"size":"1920x1080"}`,
			wantTier:  ratio_setting.ImageResolutionTier2K,
			wantCount: 1,
		},
		{
			name:      "standard 2K portrait dimensions",
			body:      `{"size":"1080x1920"}`,
			wantTier:  ratio_setting.ImageResolutionTier2K,
			wantCount: 1,
		},
		{
			name:      "standard 4K landscape dimensions",
			body:      `{"size":"3840x2160"}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 1,
		},
		{
			name:      "standard 4K portrait dimensions",
			body:      `{"size":"2160x3840"}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 1,
		},
		{
			name:      "2K decimal pixel boundary",
			body:      `{"size":"2000x1000"}`,
			wantTier:  ratio_setting.ImageResolutionTier2K,
			wantCount: 1,
		},
		{
			name:      "4K decimal pixel boundary",
			body:      `{"size":"4000x2000"}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
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
			name:      "Ali parameter size and count",
			body:      `{"parameters":{"size":"3840*2160","n":3}}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 3,
		},
		{
			name:      "SiliconFlow aliases",
			body:      `{"image_size":"1920x1080","batch_size":4}`,
			wantTier:  ratio_setting.ImageResolutionTier2K,
			wantCount: 4,
		},
		{
			name:      "Replicate input aliases",
			body:      `{"input":{"width":2160,"height":3840,"num_outputs":2}}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 2,
		},
		{
			name:      "extra body generic aliases",
			body:      `{"extra_body":{"parameters":{"output_resolution":"4k","num_images":2}}}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 2,
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
			name:      "aspect ratio does not mask explicit resolution",
			body:      `{"size":"16:9","image_size":"4K"}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 1,
		},
		{
			name:      "redundant equivalent resolution parameters",
			body:      `{"size":"2160x3840","image_size":"4K"}`,
			wantTier:  ratio_setting.ImageResolutionTier4K,
			wantCount: 1,
		},
		{
			name:      "zero standard count defers to provider count",
			body:      `{"n":0,"batch_size":4}`,
			wantTier:  ratio_setting.ImageResolutionTier1K,
			wantCount: 4,
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
			name:      "conflicting resolution parameters are rejected",
			body:      `{"size":"1K","image_size":"4K"}`,
			wantError: true,
		},
		{
			name:      "conflicting count parameters are rejected",
			body:      `{"n":1,"batch_size":4}`,
			wantError: true,
		},
		{
			name:      "partial explicit dimensions are rejected",
			body:      `{"parameters":{"width":3840}}`,
			wantError: true,
		},
		{
			name:      "non string resolution is rejected",
			body:      `{"size":4096}`,
			wantError: true,
		},
		{
			name:      "non numeric count is rejected",
			body:      `{"batch_size":true}`,
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

func TestResolveImageResolutionBillingSupportsEveryResolutionPath(t *testing.T) {
	for _, path := range imageResolutionPaths {
		t.Run(path, func(t *testing.T) {
			body, err := sjson.Set(`{}`, path, "4K")
			require.NoError(t, err)

			billing, err := resolveImageResolutionBilling(billingexpr.RequestInput{Body: []byte(body)}, nil)
			require.NoError(t, err)
			require.Equal(t, ratio_setting.ImageResolutionTier4K, billing.Tier)
			require.True(t, billing.ExplicitResolution)
		})
	}
}

func TestResolveImageResolutionBillingSupportsEveryDimensionPath(t *testing.T) {
	for _, paths := range imageDimensionPaths {
		t.Run(paths[0], func(t *testing.T) {
			body, err := sjson.Set(`{}`, paths[0], 2160)
			require.NoError(t, err)
			body, err = sjson.Set(body, paths[1], 3840)
			require.NoError(t, err)

			billing, err := resolveImageResolutionBilling(billingexpr.RequestInput{Body: []byte(body)}, nil)
			require.NoError(t, err)
			require.Equal(t, ratio_setting.ImageResolutionTier4K, billing.Tier)
			require.True(t, billing.ExplicitResolution)
		})
	}
}

func TestResolveImageResolutionBillingSupportsEveryCountPath(t *testing.T) {
	for _, path := range imageCountPaths {
		t.Run(path, func(t *testing.T) {
			body, err := sjson.Set(`{}`, path, 3)
			require.NoError(t, err)

			billing, err := resolveImageResolutionBilling(billingexpr.RequestInput{Body: []byte(body)}, nil)
			require.NoError(t, err)
			require.Equal(t, float64(3), billing.Count)
			require.True(t, billing.ExplicitCount)
		})
	}
}

func TestValidateOutboundImageBilling(t *testing.T) {
	priceData := types.PriceData{
		ImageResolutionTier:  ratio_setting.ImageResolutionTier4K,
		ImageGenerationCount: 3,
	}
	priceData.AddOtherRatio("n", 3)

	tests := []struct {
		name      string
		body      string
		wantError bool
	}{
		{name: "matching converted parameters", body: `{"parameters":{"size":"3840*2160","n":3}}`},
		{name: "missing converted parameters are allowed", body: `{}`},
		{name: "resolution changed by channel", body: `{"image_size":"2K","batch_size":3}`, wantError: true},
		{name: "count changed by channel", body: `{"image_size":"4K","batch_size":4}`, wantError: true},
		{name: "conflicting converted parameters", body: `{"size":"1K","image_size":"4K"}`, wantError: true},
		{name: "unknown converted resolution", body: `{"resolution":"8K"}`, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateOutboundImageBilling([]byte(test.body), priceData)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateOutboundImageBillingUsesFrozenPreConsumeCount(t *testing.T) {
	priceData := types.PriceData{
		ImageResolutionTier:  ratio_setting.ImageResolutionTier4K,
		ImageGenerationCount: 3,
	}
	priceData.AddOtherRatio("n", 4)

	require.NoError(t, ValidateOutboundImageBilling([]byte(`{"size":"4K","n":3}`), priceData))
	require.Error(t, ValidateOutboundImageBilling([]byte(`{"size":"4K","n":4}`), priceData))
}

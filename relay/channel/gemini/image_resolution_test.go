package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequestUsesConfiguredResolutionPricing(t *testing.T) {
	saved := ratio_setting.ImageResolutionPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(saved))
	})
	require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(`{
		"priced-image-model": {"1K": 0.02, "2K": 0.05, "4K": 0.1}
	}`))

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "priced-image-model",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "imagen-4.0-generate-001",
		},
	}

	converted, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Prompt:  "test",
		Size:    "4096x4096",
		Quality: "low",
	})
	require.NoError(t, err)
	request, ok := converted.(dto.GeminiImageRequest)
	require.True(t, ok)
	require.Equal(t, ratio_setting.ImageResolutionTier4K, request.Parameters.ImageSize)

	converted, err = adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Prompt:  "test",
		Quality: "high",
	})
	require.NoError(t, err)
	request, ok = converted.(dto.GeminiImageRequest)
	require.True(t, ok)
	require.Equal(t, ratio_setting.ImageResolutionTier1K, request.Parameters.ImageSize)
}

func TestConvertImageRequestPreservesLegacyQualityMapping(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "legacy-image-model",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "imagen-4.0-generate-001",
		},
	}

	converted, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Prompt:  "test",
		Quality: "high",
	})
	require.NoError(t, err)
	request, ok := converted.(dto.GeminiImageRequest)
	require.True(t, ok)
	require.Equal(t, ratio_setting.ImageResolutionTier2K, request.Parameters.ImageSize)
}

package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateImageResolutionPriceByJSONString(t *testing.T) {
	saved := ImageResolutionPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateImageResolutionPriceByJSONString(saved))
	})

	require.NoError(t, UpdateImageResolutionPriceByJSONString(`{
		"image-model": {"1K": 0.02, "2K": 0.05, "4K": 0.1}
	}`))
	prices, ok := GetImageResolutionPrice("image-model")
	require.True(t, ok)
	require.Equal(t, 0.02, prices.OneK)
	require.Equal(t, 0.05, prices.TwoK)
	require.Equal(t, 0.1, prices.FourK)

	err := UpdateImageResolutionPriceByJSONString(`{
		"image-model": {"1K": 0.02, "2K": 0.05}
	}`)
	require.ErrorContains(t, err, "exactly 1K, 2K and 4K")

	pricesAfterError, ok := GetImageResolutionPrice("image-model")
	require.True(t, ok)
	require.Equal(t, prices, pricesAfterError)
}

func TestValidateImageResolutionPriceJSONStringRejectsInvalidPrices(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{
			name:  "negative price",
			value: `{"image-model":{"1K":0.02,"2K":-0.05,"4K":0.1}}`,
		},
		{
			name:  "unknown tier replaces required tier",
			value: `{"image-model":{"1K":0.02,"2K":0.05,"8K":0.1}}`,
		},
		{
			name:  "empty model name",
			value: `{"":{"1K":0.02,"2K":0.05,"4K":0.1}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, ValidateImageResolutionPriceJSONString(test.value))
		})
	}
}

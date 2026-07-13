package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperUsesImageResolutionPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	saved := ratio_setting.ImageResolutionPrice2JSONString()
	savedGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(saved))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(savedGroupGroupRatio))
	})
	require.NoError(t, ratio_setting.UpdateImageResolutionPriceByJSONString(`{
		"resolution-price-test": {"1K": 0.02, "2K": 0.05, "4K": 0.1}
	}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{
		"vip": {"image": 0.4}
	}`))

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "image")
	info := &relaycommon.RelayInfo{
		OriginModelName: "resolution-price-test",
		UserGroup:       "vip",
		UsingGroup:      "image",
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{"size":"2048x2048","quality":"high","n":3}`),
		},
	}
	meta := &types.TokenCountMeta{
		ImagePriceRatio: 9,
		BillingRatios:   map[string]float64{"n": 3},
	}

	priceData, err := ModelPriceHelper(ctx, info, 0, meta)
	require.NoError(t, err)
	require.True(t, priceData.UsePrice)
	require.Equal(t, 0.05, priceData.ModelPrice)
	require.Equal(t, ratio_setting.ImageResolutionTier2K, priceData.ImageResolutionTier)
	require.Equal(t, 3.0, priceData.OtherRatios()["n"])
	require.True(t, priceData.GroupRatioInfo.HasSpecialRatio)
	require.Equal(t, 0.4, priceData.GroupRatioInfo.GroupSpecialRatio)
	require.Equal(t, 0.4, priceData.GroupRatioInfo.GroupRatio)

	expectedQuota, err := common.QuotaFromFloatStrict(0.05 * common.QuotaPerUnit * 0.4 * 3)
	require.NoError(t, err)
	require.Equal(t, expectedQuota, priceData.QuotaToPreConsume)
	require.Equal(t, priceData, info.PriceData)
}

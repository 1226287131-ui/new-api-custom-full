package relay

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestApplyTaskBillingRatios(t *testing.T) {
	newInfo := func() *relaycommon.RelayInfo {
		info := &relaycommon.RelayInfo{
			PriceData: types.PriceData{Quota: 100},
		}
		info.PriceData.AddOtherRatio("seconds", 10)
		return info
	}

	t.Run("per request keeps fixed quota", func(t *testing.T) {
		info := newInfo()
		applyTaskBillingRatios(info, billing_setting.BillingModePerRequest)
		require.Equal(t, 100, info.PriceData.Quota)
	})

	t.Run("per second applies duration multiplier", func(t *testing.T) {
		info := newInfo()
		applyTaskBillingRatios(info, billing_setting.BillingModePerSecond)
		require.Equal(t, 1000, info.PriceData.Quota)
	})
}

func TestConfiguredTaskRatiosIgnoreResolutionMultiplier(t *testing.T) {
	ratios := map[string]float64{
		"seconds":    10,
		"resolution": 2,
	}

	require.Nil(t, normalizeConfiguredTaskRatios(ratios, billing_setting.BillingModePerRequest))
	filtered := normalizeConfiguredTaskRatios(ratios, billing_setting.BillingModePerSecond)
	require.Equal(t, map[string]float64{"seconds": 10}, filtered)
}

func TestRecalcQuotaFromRatiosKeepsScheduledDiscount(t *testing.T) {
	info := &relaycommon.RelayInfo{PriceData: types.PriceData{Quota: 800}}
	info.PriceData.AddOtherRatio(billing_setting.ScheduledDiscountRatioKey, 0.8)
	info.PriceData.AddOtherRatio("seconds", 10)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{"seconds": 12})
	require.True(t, ok)
	require.Equal(t, 960, quota)
	require.Equal(t, 0.8, info.PriceData.OtherRatios()[billing_setting.ScheduledDiscountRatioKey])
	require.Equal(t, 12.0, info.PriceData.OtherRatios()["seconds"])
}

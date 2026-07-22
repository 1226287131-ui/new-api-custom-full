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

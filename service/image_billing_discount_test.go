package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageReservationAndSettlementKeepFrozenDiscount(t *testing.T) {
	const expression = `tier("image", fixed(0.04)) * image_count`
	for _, tc := range []struct {
		name       string
		multiplier float64
		want       int
	}{
		{name: "scheduled discount", multiplier: 0.5, want: 15000},
		{name: "legacy snapshot without multiplier", want: 30000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalCount := 1
			snap := &billingexpr.BillingSnapshot{
				BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression),
				QuotaPerUnit: 500000, PriceMultiplier: tc.multiplier, GroupRatio: 0.5,
				EstimatedImageCount: &originalCount,
			}
			billing := &recordingBillingSettler{}
			info := &relaycommon.RelayInfo{
				TieredBillingSnapshot: snap, Billing: billing,
				PriceData:           types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 0.5}},
				BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{"n":1}`), ImageCount: &originalCount},
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			require.Nil(t, PrepareImageBillingForRequest(ctx, info, 3, false))
			assert.Equal(t, []int{tc.want}, billing.reserveTargets)
			assert.Equal(t, tc.want, snap.EstimatedQuotaAfterGroup)
			assert.Equal(t, tc.want, info.FinalPreConsumedQuota)
			assert.Equal(t, []byte(`{"n":1}`), info.BillingRequestInput.Body)
			assert.Equal(t, 1, *info.BillingRequestInput.ImageCount)
			actualCount := 3
			result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{ImageCount: &actualCount})
			require.NoError(t, err)
			assert.Equal(t, tc.want, result.ActualQuotaAfterGroup)
		})
	}
}

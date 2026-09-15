package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskUsageExpressionUsesFactsAndTaskQuotaConversion(t *testing.T) {
	expression := `tier("1080p", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2))`
	cost, trace, err := RunExprWithRequest(expression, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 10.0, "resolution": "1080p"}})
	require.NoError(t, err)
	assert.Equal(t, 4.0, cost)
	assert.Equal(t, "1080p", trace.MatchedTier)
	for _, tc := range []struct {
		name       string
		multiplier float64
		want       int
	}{
		{name: "legacy snapshot", want: 4_000_000},
		{name: "frozen scheduled discount", multiplier: 0.5, want: 2_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ComputeTieredQuotaWithRequest(&BillingSnapshot{ExprString: expression, ExprHash: ExprHashString(expression), GroupRatio: 2, PriceMultiplier: tc.multiplier, QuotaPerUnit: 500000, ExprVersion: 1, TaskUsageBilling: true}, TokenParams{}, RequestInput{Usage: map[string]any{"seconds": 10.0, "resolution": "1080p"}})
			require.NoError(t, err)
			assert.Equal(t, tc.want, result.ActualQuotaAfterGroup)
		})
	}
}

package constant

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiniMaxH3ResolutionTierForSizeUsesExactSizeTable(t *testing.T) {
	tiers := map[string]string{
		MiniMaxH3Resolution480P: `
			864x480 480x864 640x640 544x800 800x544 576x736 736x576 992x416
		`,
		MiniMaxH3Resolution768P: `
			1376x768 768x1376 1024x1024 832x1248 1248x832 896x1184 1184x896 1568x672
		`,
		MiniMaxH3Resolution1080P: `
			1920x1088 1088x1920 1440x1440 1184x1760 1760x1184 1248x1664 1664x1248 2208x960
		`,
	}

	expectedCount := 0
	for tier, values := range tiers {
		for _, size := range strings.Fields(values) {
			expectedCount++
			actual, supported := MiniMaxH3ResolutionTierForSize(size)
			require.Truef(t, supported, "size %s", size)
			assert.Equalf(t, tier, actual, "size %s", size)
		}
	}

	assert.Len(t, miniMaxH3ResolutionTierBySize, expectedCount)
	_, supported := MiniMaxH3ResolutionTierForSize("1280x736")
	assert.False(t, supported)
}

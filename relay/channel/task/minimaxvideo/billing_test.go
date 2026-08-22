package minimaxvideo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveH3BillingResolutionUsesDocumentedSizeTiers(t *testing.T) {
	lowTierSizes := make(map[string]struct{})
	for _, size := range strings.Fields(`
		448x448 576x576 640x640 736x736 800x800 864x864 928x928 960x960
		384x576 448x672 544x800 576x896 640x960 704x1056 736x1120 800x1184
		576x384 672x448 800x544 896x576 960x640 1056x704 1120x736 1184x800
		384x544 480x640 576x736 640x832 672x928 736x992 800x1056 832x1120
		544x384 640x480 736x576 832x640 928x672 992x736 1056x800 1120x832
		352x608 416x736 480x864 544x960 608x1056 640x1152 672x1216 736x1280
		608x352 736x416 864x480 960x544 1056x608 1152x640 1216x672 1280x736
		704x288 864x352 992x416 1120x480 1216x512 1312x576 1408x608 1472x640
	`) {
		lowTierSizes[size] = struct{}{}
	}
	require.Len(t, lowTierSizes, 64)
	require.Len(t, supportedSizes, 109)

	for size := range supportedSizes {
		want := h3BillingResolution1080
		if _, lowTier := lowTierSizes[size]; lowTier {
			want = h3BillingResolution768
		}
		got, err := resolveH3BillingResolution("", "", size, 0, false)
		require.NoErrorf(t, err, "size %s", size)
		assert.Equalf(t, want, got, "size %s", size)
	}

	got, err := resolveH3BillingResolution("", "", "1280x736", 0, false)
	require.NoError(t, err)
	assert.Equal(t, h3BillingResolution768, got)
}

func TestResolveH3BillingResolutionRejectsUndocumentedPixelSize(t *testing.T) {
	_, err := resolveH3BillingResolution("", "", "1280x720", 0, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "documented MiniMax-H3 dimensions")
}

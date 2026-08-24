package minimaxvideo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveH3BillingResolutionUsesDocumentedSizeTiers(t *testing.T) {
	tests := map[string]string{
		"864x480":   h3BillingResolution480,
		"480x864":   h3BillingResolution480,
		"640x640":   h3BillingResolution480,
		"544x800":   h3BillingResolution480,
		"800x544":   h3BillingResolution480,
		"576x736":   h3BillingResolution480,
		"736x576":   h3BillingResolution480,
		"992x416":   h3BillingResolution480,
		"1376x768":  h3BillingResolution768,
		"768x1376":  h3BillingResolution768,
		"1024x1024": h3BillingResolution768,
		"832x1248":  h3BillingResolution768,
		"1248x832":  h3BillingResolution768,
		"896x1184":  h3BillingResolution768,
		"1184x896":  h3BillingResolution768,
		"1568x672":  h3BillingResolution768,
		"1920x1088": h3BillingResolution1080,
		"1088x1920": h3BillingResolution1080,
		"1440x1440": h3BillingResolution1080,
		"1184x1760": h3BillingResolution1080,
		"1760x1184": h3BillingResolution1080,
		"1248x1664": h3BillingResolution1080,
		"1664x1248": h3BillingResolution1080,
		"2208x960":  h3BillingResolution1080,
	}
	require.Len(t, tests, 24)

	for size, want := range tests {
		got, err := resolveH3BillingResolution("", "", size, 0, false)
		require.NoErrorf(t, err, "size %s", size)
		assert.Equalf(t, want, got, "size %s", size)
	}
}

func TestResolveH3BillingResolutionRejectsUndocumentedPixelSize(t *testing.T) {
	_, err := resolveH3BillingResolution("", "", "1280x736", 0, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "documented MiniMax-H3 dimensions")
}

func TestResolveH3BillingResolutionRecognizesStandardAliases(t *testing.T) {
	tests := map[string]string{
		"480p":      h3BillingResolution480,
		"1280x720":  "720p",
		"768p":      h3BillingResolution768,
		"1920x1080": h3BillingResolution1080,
		"2K":        "1440p",
		"4K":        "4k",
	}

	for value, want := range tests {
		got, err := resolveH3BillingResolution(value, "", "", 0, false)
		require.NoErrorf(t, err, "value %s", value)
		assert.Equalf(t, want, got, "value %s", value)
	}
}

func TestResolveH3BillingResolutionRecognizesStandard2KAnd4KSizes(t *testing.T) {
	tests := map[string]string{
		"2048x1080": "1440p",
		"2048x858":  "1440p",
		"2560x1600": "1440p",
		"3440x1440": "1440p",
		"4096x2160": "4k",
		"4096x1716": "4k",
		"3840x1600": "4k",
	}

	for size, want := range tests {
		got, err := resolveH3BillingResolution("", "", size, 0, false)
		require.NoErrorf(t, err, "size %s", size)
		assert.Equalf(t, want, got, "size %s", size)
	}
}

package billing_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTaskBillingMode(t *testing.T) {
	tests := []struct {
		name             string
		configuredMode   string
		legacyPerRequest bool
		want             string
	}{
		{
			name:           "explicit per request overrides legacy default",
			configuredMode: BillingModePerRequest,
			want:           BillingModePerRequest,
		},
		{
			name:             "explicit per second",
			configuredMode:   BillingModePerSecond,
			legacyPerRequest: true,
			want:             BillingModePerSecond,
		},
		{
			name:             "legacy task patch",
			legacyPerRequest: true,
			want:             BillingModePerRequest,
		},
		{
			name: "unconfigured task defaults to per second",
			want: BillingModePerSecond,
		},
		{
			name:             "unknown mode uses compatibility default",
			configuredMode:   "unexpected",
			legacyPerRequest: true,
			want:             BillingModePerRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveTaskBillingMode(tt.configuredMode, tt.legacyPerRequest); got != tt.want {
				t.Fatalf("resolveTaskBillingMode(%q, %t) = %q, want %q", tt.configuredMode, tt.legacyPerRequest, got, tt.want)
			}
		})
	}
}

func TestResolveTaskBillingPriceByResolutionOnly(t *testing.T) {
	defaultPrice := 0.01
	saved := billingSetting.TaskBillingPricing
	billingSetting.TaskBillingPricing = map[string]TaskBillingPriceConfig{
		"video-per-second": {
			Mode:         BillingModePerSecond,
			DefaultPrice: &defaultPrice,
			ResolutionPrices: map[string]float64{
				"4k":    0.04,
				"720p":  0.01,
				"1080p": 0.02,
			},
		},
		"video-per-request": {
			Mode: BillingModePerRequest,
			ResolutionPrices: map[string]float64{
				"720p":  0.08,
				"1080p": 0.15,
			},
		},
	}
	t.Cleanup(func() { billingSetting.TaskBillingPricing = saved })

	selection, configured, err := ResolveTaskBillingPrice("video-per-second", "1080x1920")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, BillingModePerSecond, selection.Mode)
	assert.Equal(t, 0.02, selection.Price)
	assert.Equal(t, "1080p", selection.Resolution)

	_, configured, err = ResolveTaskBillingPrice("video-per-second", "480p")
	require.Error(t, err)
	assert.True(t, configured)
	assert.Contains(t, err.Error(), "480p")

	selection, configured, err = ResolveTaskBillingPrice("video-per-second", "2160p")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, 0.04, selection.Price)
	assert.Equal(t, "4k", selection.Resolution)

	selection, configured, err = ResolveTaskBillingPrice("video-per-request", "720p")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, BillingModePerRequest, selection.Mode)
	assert.Equal(t, 0.08, selection.Price)

	billingSetting.TaskBillingPricing["video-per-request"].ResolutionPrices["1440p"] = 0.3
	selection, configured, err = ResolveTaskBillingPrice("video-per-request", "2560x1440")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, 0.3, selection.Price)
	assert.Equal(t, "1440p", selection.Resolution)
}

func TestResolveTaskBillingPriceUsesDefaultWithoutResolutionTable(t *testing.T) {
	defaultPrice := 0.01
	saved := billingSetting.TaskBillingPricing
	billingSetting.TaskBillingPricing = map[string]TaskBillingPriceConfig{
		"legacy-video": {
			Mode:         BillingModePerSecond,
			DefaultPrice: &defaultPrice,
		},
	}
	t.Cleanup(func() { billingSetting.TaskBillingPricing = saved })

	selection, configured, err := ResolveTaskBillingPrice("legacy-video", "480p")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, defaultPrice, selection.Price)
	assert.Equal(t, "480p", selection.Resolution)
}

func TestResolveTaskBillingPriceRejectsMissingResolutionPrice(t *testing.T) {
	saved := billingSetting.TaskBillingPricing
	billingSetting.TaskBillingPricing = map[string]TaskBillingPriceConfig{
		"video-incomplete": {
			Mode:             BillingModePerSecond,
			ResolutionPrices: map[string]float64{"1080p": 0.02},
		},
	}
	t.Cleanup(func() { billingSetting.TaskBillingPricing = saved })

	_, configured, err := ResolveTaskBillingPrice("video-incomplete", "720p")
	require.Error(t, err)
	assert.True(t, configured)
	assert.Contains(t, err.Error(), "720p")
}

package billing_setting

import "testing"

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

package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeTokenGroup(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  string
		wantError string
	}{
		{
			name:     "single group",
			input:    "vip",
			expected: "vip",
		},
		{
			name:     "multiple groups preserve order and remove duplicates",
			input:    " vip, premium, vip ",
			expected: "vip,premium",
		},
		{
			name:     "empty group uses user group",
			input:    "",
			expected: "",
		},
		{
			name:      "auto cannot be combined",
			input:     "auto,vip",
			wantError: "auto",
		},
		{
			name:      "empty item is invalid",
			input:     "vip,,premium",
			wantError: "不能为空",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, err := NormalizeTokenGroup(tt.input)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, group)
		})
	}
}

func TestTokenGetGroups(t *testing.T) {
	token := &Token{Group: " vip, premium, vip "}
	require.Equal(t, []string{"vip", "premium"}, token.GetGroups())
}

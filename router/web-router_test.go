package router

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsFrontendAssetPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "hashed javascript", path: "/static/js/index.abc123.js", want: true},
		{name: "missing asset", path: "/static/js/old-route.js", want: true},
		{name: "public logo", path: "/logo.png", want: true},
		{name: "spa route", path: "/keys", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isFrontendAssetPath(tt.path))
		})
	}
}

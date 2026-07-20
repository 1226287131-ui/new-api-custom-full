package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVideoURLUsesUpstreamOrigin(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		videoURL string
		expect   bool
	}{
		{name: "same origin", baseURL: "https://upstream.example", videoURL: "https://upstream.example/video.mp4", expect: true},
		{name: "host comparison is case insensitive", baseURL: "https://UPSTREAM.example", videoURL: "https://upstream.example/video.mp4", expect: true},
		{name: "different host", baseURL: "https://upstream.example", videoURL: "https://cdn.example/video.mp4", expect: false},
		{name: "different scheme", baseURL: "https://upstream.example", videoURL: "http://upstream.example/video.mp4", expect: false},
		{name: "relative result", baseURL: "https://upstream.example", videoURL: "/video.mp4", expect: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expect, videoURLUsesUpstreamOrigin(test.baseURL, test.videoURL))
		})
	}
}

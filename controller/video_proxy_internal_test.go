package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
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

func TestIsLocalVideoProxyURL(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() { system_setting.ServerAddress = previousServerAddress })

	tests := []struct {
		name   string
		value  string
		expect bool
	}{
		{name: "relative public cache", value: "/video-cache/task_public.mp4", expect: true},
		{name: "same origin public cache", value: "https://api.example/video-cache/task_public.mp4", expect: true},
		{name: "same origin authenticated content", value: "https://api.example/v1/videos/task_public/content", expect: true},
		{name: "external content endpoint", value: "https://upstream.example/v1/videos/task_provider/content", expect: false},
		{name: "external video cache path", value: "https://upstream.example/video-cache/task_provider.mp4", expect: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expect, isLocalVideoProxyURL(test.value))
		})
	}
}

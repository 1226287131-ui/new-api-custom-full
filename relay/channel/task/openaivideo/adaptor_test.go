package openaivideo

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newOpenAIVideoRequestContext(t *testing.T, path, contentType string, body io.Reader) (*gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, body)
	c.Request.Header.Set("Content-Type", contentType)

	info := &relaycommon.RelayInfo{
		OriginModelName: "seedance-2.0",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-test",
			ChannelBaseUrl:    "https://upstream.example/",
			UpstreamModelName: "seedance-2.0",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	return c, adaptor, info
}

func buildOpenAIVideoRequestBody(t *testing.T, payload map[string]any) (map[string]any, *gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	t.Helper()
	requestBody, err := common.Marshal(payload)
	require.NoError(t, err)
	c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	var upstreamPayload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &upstreamPayload))
	return upstreamPayload, c, adaptor, info
}

func TestBuildRequestURLUsesOpenAIVideoEndpoint(t *testing.T) {
	adaptor := &TaskAdaptor{}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiKey:         "sk-test",
		ChannelBaseUrl: "https://upstream.example/",
	}})

	requestURL, err := adaptor.BuildRequestURL(nil)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos", requestURL)
	assert.Equal(t, "openai-video", (&TaskAdaptor{}).GetChannelName())
}

func TestBuildRequestURLSupportsConfiguredOpenAIVideoEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"/v1/videos",
		"/v1/video/generations",
		"/v1/videos/generations",
		"/unknown",
	} {
		t.Run(endpoint, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey:         "sk-test",
				ChannelBaseUrl: "https://upstream.example/",
				ChannelSetting: dto.ChannelSettings{OpenAIVideoEndpoint: endpoint},
			}}
			adaptor := &TaskAdaptor{}
			adaptor.Init(info)

			requestURL, err := adaptor.BuildRequestURL(info)
			require.NoError(t, err)
			wantEndpoint := endpoint
			if endpoint == "/unknown" {
				wantEndpoint = "/v1/videos"
			}
			assert.Equal(t, "https://upstream.example"+wantEndpoint, requestURL)
		})
	}
}

func TestBuildRequestBodyKeepsUnifiedRatioForGenerationsEndpoints(t *testing.T) {
	for _, endpoint := range []string{"/v1/video/generations", "/v1/videos/generations"} {
		t.Run(endpoint, func(t *testing.T) {
			upstreamPayload, c, adaptor, info := buildOpenAIVideoRequestBody(t, map[string]any{
				"model":  "seedance-2.0",
				"prompt": "animate this",
				"ratio":  "9:16",
			})
			info.ChannelSetting.OpenAIVideoEndpoint = endpoint

			body, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			encoded, err := io.ReadAll(body)
			require.NoError(t, err)
			require.NoError(t, common.Unmarshal(encoded, &upstreamPayload))
			assert.Equal(t, "9:16", upstreamPayload["ratio"])
			assert.NotContains(t, upstreamPayload, "aspect_ratio")
		})
	}
}

func TestBuildRequestBodyKeepsRatioForStandardEndpoint(t *testing.T) {
	upstreamPayload, c, adaptor, info := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":  "seedance-2.0",
		"prompt": "animate this",
		"ratio":  "9:16",
	})
	info.ChannelSetting.OpenAIVideoEndpoint = "/v1/videos"

	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(encoded, &upstreamPayload))
	assert.Equal(t, "9:16", upstreamPayload["ratio"])
	assert.NotContains(t, upstreamPayload, "aspect_ratio")
}

func TestBuildRequestBodyPreservesSeedanceMediaAndPrompt(t *testing.T) {
	prompt := strings.Repeat("animate the reference subject with a smooth camera move; ", 999) + "finish with a smooth camera move"
	upstreamPayload, c, adaptor, info := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":      "seedance-2.0",
		"prompt":     prompt,
		"images":     []string{"https://images.example/one.png", "https://images.example/two.png"},
		"videos":     []string{"https://videos.example/camera.mp4"},
		"audios":     []string{"https://audios.example/music.mp3"},
		"ratio":      "9:16",
		"duration":   10,
		"resolution": "720p",
		"metadata":   map[string]any{"client": "downstream"},
		"async":      true,
	})

	info.UpstreamModelName = "cvk-2.5-720"
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	remappedBody, err := io.ReadAll(body)
	require.NoError(t, err)
	var remapped map[string]any
	require.NoError(t, common.Unmarshal(remappedBody, &remapped))

	assert.Equal(t, "cvk-2.5-720", remapped["model"])
	assert.Equal(t, prompt, remapped["prompt"])
	assert.Equal(t, []any{"https://images.example/one.png", "https://images.example/two.png"}, upstreamPayload["images"])
	assert.Equal(t, []any{"https://videos.example/camera.mp4"}, upstreamPayload["videos"])
	assert.Equal(t, []any{"https://audios.example/music.mp3"}, upstreamPayload["audios"])
	assert.Equal(t, float64(10), upstreamPayload["duration"])
	assert.Equal(t, "9:16", upstreamPayload["ratio"])
	assert.Equal(t, "720p", upstreamPayload["resolution"])
	assert.NotContains(t, upstreamPayload, "metadata")
	assert.NotContains(t, upstreamPayload, "async")
	assert.Equal(t, float64(10), adaptor.EstimateBilling(c, info)["seconds"])
}

func TestDoRequestSendsUnifiedVideoFieldsAfterModelMapping(t *testing.T) {
	service.InitHttpClient()
	for _, endpoint := range []string{"/v1/videos", "/v1/video/generations", "/v1/videos/generations"} {
		t.Run(endpoint, func(t *testing.T) {
			type capturedRequest struct {
				body        []byte
				readErr     error
				path        string
				contentType string
			}
			captured := make(chan capturedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				captured <- capturedRequest{body: body, readErr: err, path: r.URL.Path, contentType: r.Header.Get("Content-Type")}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()

			const requestJSON = `{
				"model":"video-v3", "prompt":"  animate @图片1 with @视频1\n  ",
				"images":["https://assets.example/two.png","https://assets.example/one.png","https://assets.example/two.png"],
				"videos":["https://assets.example/reference.mp4"],
				"audios":["https://assets.example/music.mp3"],
				"ratio":"9:16", "duration":7, "resolution":"720p", "watermark":false,
				"notify_url":"https://client.example/video/callback?tag=a%2Bb", "notify_secret":"client-signing-secret",
				"generate_audio":true, "seed":42, "metadata":{"client":"test"}, "async":true
			}`
			c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", strings.NewReader(requestJSON))
			info.ChannelBaseUrl = server.URL
			info.ChannelSetting.OpenAIVideoEndpoint = endpoint
			info.OriginModelName = "video-v3"
			info.UpstreamModelName = "cvk-2.5-720"
			c.Set("model_mapping", `{"video-v3":"cvk-2.5-720"}`)
			adaptor.Init(info)
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			body, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			resp, err := adaptor.DoRequest(c, info, body)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusAccepted, resp.StatusCode)

			upstream := <-captured
			require.NoError(t, upstream.readErr)
			assert.Equal(t, endpoint, upstream.path)
			assert.Equal(t, "application/json", upstream.contentType)
			assert.JSONEq(t, `{
				"model":"cvk-2.5-720", "prompt":"  animate @图片1 with @视频1\n  ",
				"images":["https://assets.example/two.png","https://assets.example/one.png","https://assets.example/two.png"],
				"videos":["https://assets.example/reference.mp4"],
				"audios":["https://assets.example/music.mp3"],
				"ratio":"9:16", "duration":7, "resolution":"720p", "watermark":false,
				"notify_url":"https://client.example/video/callback?tag=a%2Bb", "notify_secret":"client-signing-secret"
			}`, string(upstream.body))
			request, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			assert.Equal(t, 7, request.Duration)
			assert.Equal(t, "720p", request.BillingResolution)
			assert.Equal(t, float64(7), adaptor.EstimateBilling(c, info)["seconds"])
		})
	}
}

func TestUnifiedVideoPreservesEmptyArraysAndBillingDefaults(t *testing.T) {
	upstream, c, adaptor, info := buildOpenAIVideoRequestBody(t, map[string]any{
		"model": "video-v3", "prompt": "animate this", "images": []string{}, "videos": []string{}, "audios": []string{},
	})
	for _, field := range []string{"images", "videos", "audios"} {
		assert.Equal(t, []any{}, upstream[field])
	}
	assert.NotContains(t, upstream, "watermark")
	assert.NotContains(t, upstream, "notify_url")
	assert.NotContains(t, upstream, "content")
	assert.Equal(t, float64(4), upstream["duration"])
	assert.Equal(t, "16:9", upstream["ratio"])
	assert.Equal(t, "720p", upstream["resolution"])
	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, 4, request.Duration)
	assert.Equal(t, "720p", request.BillingResolution)
	assert.Equal(t, float64(4), adaptor.EstimateBilling(c, info)["seconds"])
}

func TestBuildRequestBodyMapsLegacySecondsAndSize(t *testing.T) {
	upstreamPayload, _, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":   "seedance-2.0",
		"prompt":  "animate this",
		"seconds": "10",
		"size":    "720x1280",
		"quality": "hd",
		"image":   "https://images.example/reference.png",
	})

	assert.Equal(t, float64(10), upstreamPayload["duration"])
	assert.Equal(t, "9:16", upstreamPayload["ratio"])
	assert.Equal(t, "720p", upstreamPayload["resolution"])
	assert.Equal(t, []any{"https://images.example/reference.png"}, upstreamPayload["images"])
	assert.NotContains(t, upstreamPayload, "seconds")
	assert.NotContains(t, upstreamPayload, "size")
	assert.NotContains(t, upstreamPayload, "quality")
	assert.NotContains(t, upstreamPayload, "image")
}

func TestBuildRequestBodyAccepts2KAnd4KSizeAliases(t *testing.T) {
	tests := []struct {
		name              string
		size              string
		wantResolution    string
		wantCanonicalSize string
		wantBillingTier   string
	}{
		{name: "2K", size: "2K", wantResolution: "2k", wantCanonicalSize: "2560x1440", wantBillingTier: "1440p"},
		{name: "4K", size: "4K", wantResolution: "4k", wantCanonicalSize: "3840x2160", wantBillingTier: "4k"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			upstreamPayload, c, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
				"model":  "seedance-2.0",
				"prompt": "animate this",
				"size":   testCase.size,
			})

			assert.Equal(t, "16:9", upstreamPayload["ratio"])
			assert.Equal(t, testCase.wantResolution, upstreamPayload["resolution"])
			request, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			assert.Equal(t, testCase.wantCanonicalSize, request.Size)
			assert.Equal(t, testCase.wantBillingTier, request.BillingResolution)
		})
	}
}

func TestBuildRequestBodyMapsStandard2KAnd4KSizes(t *testing.T) {
	tests := []struct {
		name            string
		size            string
		wantRatio       string
		wantResolution  string
		wantBillingTier string
	}{
		{name: "DCI 2K", size: "2048×1080", wantRatio: "16:9", wantResolution: "2k", wantBillingTier: "1440p"},
		{name: "2K scope", size: "2048x858", wantRatio: "21:9", wantResolution: "2k", wantBillingTier: "1440p"},
		{name: "QHD", size: "2560x1440", wantRatio: "16:9", wantResolution: "2k", wantBillingTier: "1440p"},
		{name: "ultrawide 2K", size: "3440x1440", wantRatio: "21:9", wantResolution: "2k", wantBillingTier: "1440p"},
		{name: "UHD", size: "3840x2160", wantRatio: "16:9", wantResolution: "4k", wantBillingTier: "4k"},
		{name: "DCI 4K scope", size: "4096x1716", wantRatio: "21:9", wantResolution: "4k", wantBillingTier: "4k"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			upstreamPayload, c, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
				"model":  "seedance-2.0",
				"prompt": "animate this",
				"size":   testCase.size,
			})

			assert.Equal(t, testCase.wantRatio, upstreamPayload["ratio"])
			assert.Equal(t, testCase.wantResolution, upstreamPayload["resolution"])
			request, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			assert.Equal(t, testCase.wantBillingTier, request.BillingResolution)
		})
	}
}

func TestBuildRequestBodySupportsSeedanceReferenceAliasesAndAspectRatio(t *testing.T) {
	upstreamPayload, _, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":                "seedance-2.0",
		"prompt":               "animate the subject",
		"duration":             15,
		"aspect_ratio":         "21:9",
		"resolution":           "1080p",
		"reference_image_urls": []string{"https://images.example/one.png", "https://images.example/two.png"},
		"reference_videos":     []string{"https://videos.example/reference.mp4"},
		"reference_audios":     []string{"https://audios.example/reference.mp3"},
		"generate_audio":       true,
		"bypass_face_check":    true,
		"grid_strength":        0.5,
	})

	assert.Equal(t, "21:9", upstreamPayload["ratio"])
	assert.Equal(t, "1080p", upstreamPayload["resolution"])
	assert.Equal(t, float64(15), upstreamPayload["duration"])
	assert.Equal(t, []any{"https://images.example/one.png", "https://images.example/two.png"}, upstreamPayload["images"])
	assert.Equal(t, []any{"https://videos.example/reference.mp4"}, upstreamPayload["videos"])
	assert.Equal(t, []any{"https://audios.example/reference.mp3"}, upstreamPayload["audios"])
	assert.NotContains(t, upstreamPayload, "generate_audio")
	assert.NotContains(t, upstreamPayload, "bypass_face_check")
	assert.NotContains(t, upstreamPayload, "grid_strength")
	assert.NotContains(t, upstreamPayload, "aspect_ratio")
	assert.NotContains(t, upstreamPayload, "reference_image_urls")
}

func TestQYSeedance25SupportsMetaJingAliasesAndDefaults(t *testing.T) {
	upstreamPayload, c, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":  "qy-seedance-2.5",
		"prompt": "animate the subject",
		"params": map[string]any{
			"duration":    29,
			"aspectRatio": "1:1",
			"audio":       false,
		},
		"seed":          4294967295,
		"grid_strength": 0.5,
	})

	assert.Equal(t, float64(29), upstreamPayload["duration"])
	assert.Equal(t, "1:1", upstreamPayload["ratio"])
	assert.Equal(t, "480p", upstreamPayload["resolution"])
	assert.NotContains(t, upstreamPayload, "generate_audio")
	assert.NotContains(t, upstreamPayload, "seed")
	assert.NotContains(t, upstreamPayload, "grid_strength")
	assert.NotContains(t, upstreamPayload, "params")
	assert.NotContains(t, upstreamPayload, "aspectRatio")
	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, "29", request.Seconds)
	assert.Equal(t, "480x480", request.Size)
}

func TestQYSeedance25KeepsUnifiedReferenceVideos(t *testing.T) {
	upstreamPayload, _, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":        "qy-seedance-2.5",
		"prompt":       "animate the subject",
		"duration":     18,
		"resolution":   "720p",
		"aspect_ratio": "9:16",
		"videos":       []string{"https://videos.example/reference.mp4"},
	})

	assert.Equal(t, "720p", upstreamPayload["resolution"])
	assert.Equal(t, []any{"https://videos.example/reference.mp4"}, upstreamPayload["videos"])
	assert.NotContains(t, upstreamPayload, "content")
	assert.NotContains(t, upstreamPayload, "generate_audio")
}

func TestQYSeedance25RejectsUnsupportedCompatibilityValues(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		code string
	}{
		{name: "duration above 29", body: map[string]any{"model": "qy-seedance-2.5", "prompt": "x", "duration": 30}, code: "invalid_duration"},
		{name: "reference video duration above 18", body: map[string]any{"model": "qy-seedance-2.5", "prompt": "x", "duration": 19, "videos": []string{"https://videos.example/reference.mp4"}}, code: "invalid_duration"},
		{name: "frame with images", body: map[string]any{"model": "qy-seedance-2.5", "prompt": "x", "images": []string{"https://images.example/reference.png"}, "start_frame_url": "https://images.example/start.png"}, code: "invalid_frame_parameters"},
		{name: "end frame without start", body: map[string]any{"model": "qy-seedance-2.5", "prompt": "x", "end_frame_url": "https://images.example/end.png"}, code: "invalid_frame_parameters"},
		{name: "2K is unsupported", body: map[string]any{"model": "qy-seedance-2.5", "prompt": "x", "size": "2K"}, code: "invalid_video_format"},
		{name: "4K is unsupported", body: map[string]any{"model": "qy-seedance-2.5", "prompt": "x", "size": "3840x2160"}, code: "invalid_video_format"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestBody, err := common.Marshal(test.body)
			require.NoError(t, err)
			c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Equal(t, test.code, taskErr.Code)
		})
	}
}

func TestVideoV3PreservesUnifiedMediaAndExplicitResolution(t *testing.T) {
	images := make([]string, 30)
	for index := range images {
		images[index] = fmt.Sprintf("https://images.example/reference-%02d.png", index)
	}
	videos := make([]string, 10)
	audios := make([]string, 10)
	for index := range videos {
		videos[index] = fmt.Sprintf("https://videos.example/reference-%02d.mp4", index)
	}
	for index := range audios {
		audios[index] = fmt.Sprintf("https://audios.example/reference-%02d.mp3", index)
	}

	upstreamPayload, c, adaptor, _ := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":          "video-v3",
		"prompt":         "animate all reference media with a smooth camera move",
		"duration":       30,
		"size":           "1920x1080",
		"resolution":     "1080p",
		"images":         images,
		"videos":         videos,
		"audios":         audios,
		"generate_audio": false,
		"seed":           -1,
	})

	assert.Equal(t, float64(30), upstreamPayload["duration"])
	assert.Equal(t, "16:9", upstreamPayload["ratio"])
	assert.Equal(t, "1080p", upstreamPayload["resolution"])
	assert.NotContains(t, upstreamPayload, "generate_audio")
	assert.NotContains(t, upstreamPayload, "seed")
	assert.Equal(t, "animate all reference media with a smooth camera move", upstreamPayload["prompt"])
	for field, expected := range map[string][]string{"images": images, "videos": videos, "audios": audios} {
		actual, ok := upstreamPayload[field].([]any)
		require.True(t, ok, field)
		require.Len(t, actual, len(expected), field)
		for index, value := range expected {
			assert.Equal(t, value, actual[index], "%s[%d]", field, index)
		}
	}
	assert.NotContains(t, upstreamPayload, "content")

	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, 30, request.Duration)
	assert.Equal(t, "30", request.Seconds)
	assert.Equal(t, "1920x1080", request.Size)
	assert.Equal(t, "1080p", request.Metadata["resolution"])
	assert.Equal(t, "1080p", request.BillingResolution)
	assert.Equal(t, "seedance-2.5", request.Metadata["video_profile"])
	assert.Equal(t, float64(30), adaptor.EstimateBilling(c, nil)["seconds"])
}

func TestVideoV3NormalizesLegacyInputReferencesToUnifiedArrays(t *testing.T) {
	inputPayload := map[string]any{
		"model":           "video-v3",
		"prompt":          "animate the reference images",
		"seconds":         "4",
		"input_reference": []string{"https://images.example/one.png", "https://images.example/two.png", "https://videos.example/camera.mp4", "https://audios.example/music.mp3"},
		"resolution":      "720p",
		"ratio":           "9:16",
	}
	upstreamPayload, _, _, _ := buildOpenAIVideoRequestBody(t, inputPayload)
	assert.Equal(t, []any{"https://images.example/one.png", "https://images.example/two.png"}, upstreamPayload["images"])
	assert.Equal(t, []any{"https://videos.example/camera.mp4"}, upstreamPayload["videos"])
	assert.Equal(t, []any{"https://audios.example/music.mp3"}, upstreamPayload["audios"])
	assert.NotContains(t, upstreamPayload, "input_reference")
	assert.NotContains(t, upstreamPayload, "content")
}

func TestSeedance25ChannelProfileAcceptsCustomDownstreamModelName(t *testing.T) {
	requestBody, err := common.Marshal(map[string]any{
		"model":      "my-customer-video-model",
		"prompt":     "animate the reference subject",
		"duration":   30,
		"ratio":      "9:16",
		"resolution": "1080p",
		"images":     []string{"https://images.example/reference.png"},
	})
	require.NoError(t, err)

	c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
	info.ChannelSetting.OpenAIVideoProfile = "seedance-2.5"
	info.UpstreamModelName = "provider-sd25-deployment"
	c.Set("model_mapping", `{"my-customer-video-model":"provider-sd25-deployment"}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)

	var upstreamPayload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &upstreamPayload))
	assert.Equal(t, "provider-sd25-deployment", upstreamPayload["model"])
	assert.Equal(t, float64(30), upstreamPayload["duration"])
	assert.Equal(t, "9:16", upstreamPayload["ratio"])
	assert.Equal(t, "1080p", upstreamPayload["resolution"])
	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, "seedance-2.5", request.Metadata["video_profile"])
}

func TestVideoV3RejectsOutOfRangeDurationAndReferenceCounts(t *testing.T) {
	images := make([]string, 31)
	videos := make([]string, 11)
	audios := make([]string, 11)
	for index := range images {
		images[index] = fmt.Sprintf("https://images.example/%d.png", index)
	}
	for index := range videos {
		videos[index] = fmt.Sprintf("https://videos.example/%d.mp4", index)
	}
	for index := range audios {
		audios[index] = fmt.Sprintf("https://audios.example/%d.mp3", index)
	}

	tests := []struct {
		name string
		body map[string]any
		code string
	}{
		{name: "duration below minimum", body: map[string]any{"model": "video-v3", "prompt": "x", "duration": 3}, code: "invalid_duration"},
		{name: "duration above maximum", body: map[string]any{"model": "video-v3", "prompt": "x", "duration": 31}, code: "invalid_duration"},
		{name: "too many images", body: map[string]any{"model": "video-v3", "prompt": "x", "images": images}, code: "invalid_images"},
		{name: "too many videos", body: map[string]any{"model": "video-v3", "prompt": "x", "videos": videos}, code: "invalid_videos"},
		{name: "too many audios", body: map[string]any{"model": "video-v3", "prompt": "x", "audios": audios}, code: "invalid_audios"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestBody, err := common.Marshal(test.body)
			require.NoError(t, err)
			c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Equal(t, test.code, taskErr.Code)
		})
	}
}

func TestDefaultOpenAIVideoAllowsThirtyReferenceImages(t *testing.T) {
	images := make([]string, 31)
	for index := range images {
		images[index] = fmt.Sprintf("https://images.example/%d.png", index)
	}

	for _, test := range []struct {
		name       string
		imageCount int
		wantError  bool
	}{
		{name: "thirty accepted", imageCount: 30},
		{name: "thirty one rejected", imageCount: 31, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{
				"model":  "video-v3-480p",
				"prompt": "animate the reference images",
				"images": images[:test.imageCount],
			})
			require.NoError(t, err)
			c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(body))
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			if test.wantError {
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				assert.Equal(t, "invalid_images", taskErr.Code)
				assert.Equal(t, "images supports at most 30 reference images", taskErr.Message)
				return
			}
			require.Nil(t, taskErr)
		})
	}
}

func TestFast720pAllowsImageOnlyRequestsAndRemovesPromptWhenAbsent(t *testing.T) {
	upstreamPayload, _, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":        "seedance-2.0-fast-720p",
		"duration":     5,
		"aspect_ratio": "3:4",
		"images":       []string{"https://images.example/reference.png"},
	})

	assert.NotContains(t, upstreamPayload, "prompt")
	assert.Equal(t, "3:4", upstreamPayload["ratio"])
	assert.Equal(t, "720p", upstreamPayload["resolution"])
	assert.NotContains(t, upstreamPayload, "size")
}

func TestFast720pAppliesRestrictionsAfterModelMapping(t *testing.T) {
	requestBody, err := common.Marshal(map[string]any{
		"model":    "customer-video",
		"duration": 5,
		"images":   []string{"https://images.example/reference.png"},
	})
	require.NoError(t, err)
	c, adaptor, info := newOpenAIVideoRequestContext(
		t,
		"/v1/videos",
		"application/json",
		bytes.NewReader(requestBody),
	)
	c.Set("model_mapping", `{"customer-video":"cvk-2-fast-720"}`)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, true, request.Metadata["fast_720p"])
}

func TestFast720pRejectsUnsupportedInputs(t *testing.T) {
	tests := []map[string]any{
		{
			"model":    "seedance-2.0-fast-720p",
			"prompt":   "animate this",
			"duration": 5,
			"videos":   []string{"https://videos.example/reference.mp4"},
		},
		{
			"model":    "seedance-2.0-fast-720p",
			"duration": 5,
			"images":   []string{"https://images.example/reference.png"},
			"audios":   []string{"https://audios.example/reference.mp3"},
		},
		{
			"model":      "seedance-2.0-fast-720p",
			"duration":   5,
			"images":     []string{"https://images.example/reference.png"},
			"resolution": "1080p",
		},
	}

	for _, body := range tests {
		requestBody, err := common.Marshal(body)
		require.NoError(t, err)
		c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
		taskErr := adaptor.ValidateRequestAndSetAction(c, info)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	}
}

func TestValidateRequestAcceptsWholeSecondsFromFiveThroughThirty(t *testing.T) {
	for _, duration := range []int{5, 8, 10, 14, 15, 16, 29, 30} {
		requestBody, err := common.Marshal(map[string]any{
			"model": "seedance-2.0", "prompt": "animate this", "duration": duration,
		})
		require.NoError(t, err)
		c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	}

	for _, duration := range []any{0, 4, 31, 6.5, "5.5"} {
		requestBody, err := common.Marshal(map[string]any{
			"model": "seedance-2.0", "prompt": "animate this", "duration": duration,
		})
		require.NoError(t, err)
		c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
		taskErr := adaptor.ValidateRequestAndSetAction(c, info)
		require.NotNil(t, taskErr, "duration %v", duration)
		assert.Equal(t, "invalid_duration", taskErr.Code)
	}
}

func TestValidateRequestUsesGenerateActionForAnyReferenceMedia(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		value  []string
		expect string
	}{
		{name: "images", field: "images", value: []string{"https://images.example/reference.png"}, expect: constant.TaskActionGenerate},
		{name: "videos", field: "videos", value: []string{"https://videos.example/reference.mp4"}, expect: constant.TaskActionGenerate},
		{name: "audios", field: "audios", value: []string{"https://audios.example/reference.mp3"}, expect: constant.TaskActionGenerate},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestBody, err := common.Marshal(map[string]any{
				"model":    "seedance-2.0",
				"prompt":   "animate this",
				test.field: test.value,
			})
			require.NoError(t, err)
			c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			assert.Equal(t, test.expect, info.Action)
		})
	}

	requestBody, err := common.Marshal(map[string]any{"model": "seedance-2.0", "prompt": "text only"})
	require.NoError(t, err)
	c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionTextGenerate, info.Action)
}

func TestValidateRequestRejectsInvalidBillingAndMediaInputs(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		code string
	}{
		{
			name: "duration out of integer range",
			body: map[string]any{"model": "seedance-2.0", "prompt": "x", "duration": "999999999999999999999999"},
			code: "invalid_duration",
		},
		{
			name: "duration above billing bound",
			body: map[string]any{"model": "seedance-2.0", "prompt": "x", "duration": relaycommon.MaxTaskDurationSeconds + 1},
			code: "invalid_duration",
		},
		{
			name: "non string image item",
			body: map[string]any{"model": "seedance-2.0", "prompt": "x", "images": []any{"https://images.example/reference.png", 42}},
			code: "invalid_images",
		},
		{
			name: "invalid video URL",
			body: map[string]any{"model": "seedance-2.0", "prompt": "x", "videos": []string{"file:///tmp/reference.mp4"}},
			code: "invalid_videos",
		},
		{
			name: "native content is not silently dropped",
			body: map[string]any{"model": "video-v3", "prompt": "x", "content": []any{map[string]any{"type": "image_url", "image_url": "https://assets.example/one.png"}}},
			code: "invalid_content",
		},
		{
			name: "duplicate images count toward the reference limit",
			body: map[string]any{"model": "video-v3", "prompt": "x", "images": strings.Split(strings.Repeat("https://assets.example/one.png,", 30)+"https://assets.example/one.png", ",")},
			code: "invalid_images",
		},
		{
			name: "non string prompt",
			body: map[string]any{"model": "video-v3", "prompt": 42},
			code: "invalid_prompt",
		},
		{
			name: "invalid watermark",
			body: map[string]any{"model": "video-v3", "prompt": "x", "watermark": 1},
			code: "invalid_parameters",
		},
		{
			name: "non string callback URL",
			body: map[string]any{"model": "video-v3", "prompt": "x", "notify_url": []string{"https://client.example/callback"}},
			code: "invalid_parameters",
		},
		{
			name: "non string callback secret",
			body: map[string]any{"model": "video-v3", "prompt": "x", "notify_secret": 12345},
			code: "invalid_parameters",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestBody, err := common.Marshal(test.body)
			require.NoError(t, err)
			c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", "application/json", bytes.NewReader(requestBody))
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Equal(t, test.code, taskErr.Code)
		})
	}
}

func TestMultipartInputReferenceBecomesImageArray(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_INPUT_CACHE_DIR", cacheDir)
	t.Setenv("VIDEO_INPUT_CACHE_PUBLIC_BASE_URL", "https://api.example")

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	require.NoError(t, writer.WriteField("model", "seedance-2.0"))
	require.NoError(t, writer.WriteField("prompt", "animate the uploaded subject"))
	require.NoError(t, writer.WriteField("duration", "5"))
	filePart, err := writer.CreateFormFile("input_reference", "reference.png")
	require.NoError(t, err)
	_, err = filePart.Write([]byte{'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n', 0, 0, 0, 0})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, adaptor, info := newOpenAIVideoRequestContext(t, "/v1/videos", writer.FormDataContentType(), bytes.NewReader(requestBody.Bytes()))
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))

	images, ok := payload["images"].([]any)
	require.True(t, ok)
	require.Len(t, images, 1)
	imageURL, ok := images[0].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(imageURL, "https://api.example/video-input-cache/"))
	assert.NotContains(t, payload, "input_reference")
	files, err := filepath.Glob(cacheDir + "/*.png")
	require.NoError(t, err)
	assert.Len(t, files, 1)
}

func TestParseTaskResultHandlesNestedResultAndIgnoresInputURL(t *testing.T) {
	adaptor := &TaskAdaptor{}

	processing, err := adaptor.ParseTaskResult([]byte(`{
		"id":"task_upstream",
		"status":"processing",
		"data":{"input":{"url":"https://images.example/reference.png"}}
	}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, processing.Status)
	assert.Empty(t, processing.Url)

	completed, err := adaptor.ParseTaskResult([]byte(`{
		"id":"task_upstream",
		"status":"completed",
		"data":{"input":{"url":"https://images.example/reference.png"},"result":{"data":[{"url":"https://videos.example/final.mp4"}]}}
	}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, completed.Status)
	assert.Equal(t, "https://videos.example/final.mp4", completed.Url)
}

func TestParseTaskResultUsesCompletedMetadataURL(t *testing.T) {
	adaptor := &TaskAdaptor{}

	queued, err := adaptor.ParseTaskResult([]byte(`{
		"id":"task_upstream",
		"status":"queued",
		"metadata":{"url":"https://videos.example/not-ready.mp4"}
	}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusQueued, queued.Status)
	assert.Empty(t, queued.Url)

	completed, err := adaptor.ParseTaskResult([]byte(`{
		"id":"task_upstream",
		"status":"completed",
		"metadata":{"url":"https://videos.example/final.mp4"}
	}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, completed.Status)
	assert.Equal(t, "https://videos.example/final.mp4", completed.Url)
}

func TestParseTaskResultHandlesStatusesAndErrors(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		expect string
		reason string
	}{
		{name: "queued", body: `{"status":"queued"}`, expect: model.TaskStatusQueued},
		{name: "running", body: `{"status":"running","progress":42,"message":"still working"}`, expect: model.TaskStatusInProgress},
		{name: "completed without URL", body: `{"status":"completed"}`, expect: model.TaskStatusSuccess},
		{name: "failed", body: `{"status":"failed","error":{"code":"generation_failed","message":"provider rejected the request"}}`, expect: model.TaskStatusFailure, reason: "provider rejected the request"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(test.body))
			require.NoError(t, err)
			assert.Equal(t, test.expect, result.Status)
			assert.Equal(t, test.reason, result.Reason)
		})
	}
}

func TestDoResponseUsesPublicTaskIDForNestedUpstreamTask(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "seedance-2.0",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-test",
			ChannelBaseUrl:    "https://upstream.example",
			UpstreamModelName: "seedance-2.0",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	adaptor.Init(info)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Set("task_request", relaycommon.TaskSubmitReq{Seconds: "10", Size: "1280x720"})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"data":{"task_id":"task_upstream","status":"queued"}}`)),
	}

	taskID, _, taskErr := adaptor.DoResponse(c, resp, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "task_upstream", taskID)
	var video dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &video))
	assert.Equal(t, "task_public", video.ID)
	assert.Equal(t, "task_public", video.TaskID)
	assert.Equal(t, "10", video.Seconds)
	assert.Equal(t, "1280x720", video.Size)
}

func TestConvertToOpenAIVideoReturnsPublicCacheURL(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() { system_setting.ServerAddress = previousServerAddress })

	task := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		CreatedAt: time.Now().Unix() - 10,
		UpdatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "seedance-2.0",
			VideoSeconds:    "5",
			VideoSize:       "1280x720",
		},
	}

	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var video dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &video))
	assert.Equal(t, dto.VideoStatusCompleted, video.Status)
	assert.Equal(t, "https://api.example/video-cache/task_public.mp4", video.ResultURL)
	assert.Equal(t, video.ResultURL, video.Metadata["url"])
}

func TestFetchTaskUsesOpenAIVideoStatusEndpoint(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/videos/task_upstream", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(server.URL, "sk-test", map[string]any{"task_id": "task_upstream"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestFetchTaskUsesConfiguredOpenAIVideoStatusEndpoint(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/video/generations/task_upstream", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiKey:         "sk-test",
		ChannelBaseUrl: server.URL,
		ChannelSetting: dto.ChannelSettings{OpenAIVideoEndpoint: "/v1/video/generations"},
	}}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	resp, err := adaptor.FetchTask(server.URL, "sk-test", map[string]any{"task_id": "task_upstream"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

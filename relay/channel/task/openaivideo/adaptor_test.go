package openaivideo

import (
	"bytes"
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
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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

	info.UpstreamModelName = "cvk-2-fast-720"
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	remappedBody, err := io.ReadAll(body)
	require.NoError(t, err)
	var remapped map[string]any
	require.NoError(t, common.Unmarshal(remappedBody, &remapped))

	assert.Equal(t, "cvk-2-fast-720", remapped["model"])
	assert.Equal(t, prompt, remapped["prompt"])
	assert.Equal(t, []any{"https://images.example/one.png", "https://images.example/two.png"}, upstreamPayload["images"])
	assert.Equal(t, []any{"https://videos.example/camera.mp4"}, upstreamPayload["videos"])
	assert.Equal(t, []any{"https://audios.example/music.mp3"}, upstreamPayload["audios"])
	assert.Equal(t, float64(10), upstreamPayload["duration"])
	assert.Equal(t, "9:16", upstreamPayload["ratio"])
	assert.Equal(t, "720p", upstreamPayload["resolution"])
	assert.Equal(t, map[string]any{"client": "downstream"}, upstreamPayload["metadata"])
	assert.NotContains(t, upstreamPayload, "async")
	assert.Equal(t, float64(10), adaptor.EstimateBilling(c, info)["seconds"])
}

func TestBuildRequestBodyMapsLegacySecondsAndSize(t *testing.T) {
	upstreamPayload, _, _, _ := buildOpenAIVideoRequestBody(t, map[string]any{
		"model":   "seedance-2.0",
		"prompt":  "animate this",
		"seconds": "8",
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

func TestConvertToOpenAIVideoUsesAuthenticatedProxyURL(t *testing.T) {
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
	assert.Equal(t, "https://api.example/v1/videos/task_public/content", video.ResultURL)
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

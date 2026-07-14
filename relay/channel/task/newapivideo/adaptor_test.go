package newapivideo

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newVideoRequestContext(t *testing.T, path, contentType string, body io.Reader) (*gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", path, body)
	c.Request.Header.Set("Content-Type", contentType)
	info := &relaycommon.RelayInfo{
		OriginModelName: "video-v1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-test",
			ChannelBaseUrl:    "https://upstream.example",
			UpstreamModelName: "video-v1",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	return c, adaptor, info
}

func normalizedJSONPayload(t *testing.T, path, requestBody string) (map[string]interface{}, map[string]float64) {
	t.Helper()
	c, adaptor, info := newVideoRequestContext(t, path, "application/json", strings.NewReader(requestBody))
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload map[string]interface{}
	require.NoError(t, common.Unmarshal(encoded, &payload))
	return payload, adaptor.EstimateBilling(c, info)
}

func TestBuildRequestURLUsesNewAPIVideoEndpoint(t *testing.T) {
	adaptor := &TaskAdaptor{}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiKey:         "sk-test",
		ChannelBaseUrl: "https://upstream.example/",
	}})

	requestURL, err := adaptor.BuildRequestURL(nil)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/video/generations", requestURL)
}

func TestParseTaskResultSupportsNewAPIResponse(t *testing.T) {
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"data": {
			"status": "SUCCESS",
			"progress": 100,
			"result_url": "https://upstream.example/video.mp4"
		}
	}`))

	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, result.Status)
	assert.Equal(t, "100%", result.Progress)
	assert.Equal(t, "https://upstream.example/video.mp4", result.Url)
}

func TestBuildRequestBodyMapsSoraDurationsAndSizes(t *testing.T) {
	tests := []struct {
		name             string
		seconds          string
		size             string
		expectedDuration float64
		expectedRatio    string
	}{
		{name: "landscape 4 seconds", seconds: "4", size: "1280x720", expectedDuration: 5, expectedRatio: "16:9"},
		{name: "portrait 8 seconds", seconds: "8", size: "720x1280", expectedDuration: 10, expectedRatio: "9:16"},
		{name: "square 12 seconds", seconds: "12", size: "1024x1024", expectedDuration: 15, expectedRatio: "1:1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, billing := normalizedJSONPayload(t, "/v1/videos", `{
				"model":"video-v1",
				"prompt":"test video",
				"seconds":"`+test.seconds+`",
				"size":"`+test.size+`"
			}`)

			assert.Equal(t, "video-v1", payload["model"])
			assert.Equal(t, test.expectedDuration, payload["duration"])
			assert.Equal(t, test.expectedRatio, payload["ratio"])
			assert.Equal(t, "hd", payload["quality"])
			assert.Equal(t, true, payload["async"])
			assert.NotContains(t, payload, "seconds")
			assert.NotContains(t, payload, "size")
			assert.Equal(t, test.expectedDuration, billing["seconds"])
		})
	}
}

func TestBuildRequestBodyPreservesLegacyVideoV1Contract(t *testing.T) {
	payload, billing := normalizedJSONPayload(t, "/v1/video/generations", `{
		"model":"video-v1",
		"prompt":"legacy request",
		"duration":10,
		"ratio":"16:9",
		"quality":"sd",
		"async":true,
		"image":"https://images.example/reference.jpg"
	}`)

	assert.Equal(t, float64(10), payload["duration"])
	assert.Equal(t, "16:9", payload["ratio"])
	assert.Equal(t, "sd", payload["quality"])
	assert.Equal(t, "https://images.example/reference.jpg", payload["image"])
	assert.Equal(t, float64(10), billing["seconds"])
}

func TestValidateRequestRejectsUnsupportedSoraParameters(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
	}{
		{name: "seconds", body: `{"model":"video-v1","prompt":"x","seconds":"7","size":"1280x720"}`, code: "invalid_seconds"},
		{name: "size", body: `{"model":"video-v1","prompt":"x","seconds":"4","size":"800x600"}`, code: "invalid_size"},
		{name: "quality", body: `{"model":"video-v1","prompt":"x","seconds":"4","size":"1280x720","quality":"4k"}`, code: "invalid_quality"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, adaptor, info := newVideoRequestContext(t, "/v1/videos", "application/json", strings.NewReader(test.body))
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, 400, taskErr.StatusCode)
			assert.Equal(t, test.code, taskErr.Code)
		})
	}
}

func TestMultipartInputReferenceBecomesTemporaryPublicImageURL(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("VIDEO_INPUT_CACHE_DIR", cacheDir)
	t.Setenv("VIDEO_INPUT_CACHE_PUBLIC_BASE_URL", "https://api.example")

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	require.NoError(t, writer.WriteField("model", "video-v1"))
	require.NoError(t, writer.WriteField("prompt", "animate this image"))
	require.NoError(t, writer.WriteField("seconds", "4"))
	require.NoError(t, writer.WriteField("size", "1280x720"))
	filePart, err := writer.CreateFormFile("input_reference", "reference.png")
	require.NoError(t, err)
	_, err = filePart.Write([]byte{'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n', 0, 0, 0, 0})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, adaptor, info := newVideoRequestContext(t, "/v1/videos", writer.FormDataContentType(), bytes.NewReader(requestBody.Bytes()))
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload map[string]interface{}
	require.NoError(t, common.Unmarshal(encoded, &payload))

	imageURL, ok := payload["image"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(imageURL, "https://api.example/video-input-cache/"))
	assert.Equal(t, ".png", filepath.Ext(imageURL))
	assert.NotContains(t, payload, "input_reference")
	files, err := filepath.Glob(filepath.Join(cacheDir, "*.png"))
	require.NoError(t, err)
	assert.Len(t, files, 1)
}

func TestConvertToOpenAIVideoReturnsPublicResultURL(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() {
		system_setting.ServerAddress = previousServerAddress
	})

	now := time.Now().Unix()
	task := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		CreatedAt: now - 10,
		UpdatedAt: now,
		Properties: model.Properties{
			OriginModelName: "video-v1",
			VideoSeconds:    "5",
			VideoSize:       "1280x720",
		},
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://api.example/v1/videos/task_public/content",
		},
	}

	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var video dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &video))
	assert.Equal(t, "task_public", video.ID)
	assert.Equal(t, dto.VideoStatusCompleted, video.Status)
	assert.Equal(t, "5", video.Seconds)
	assert.Equal(t, "1280x720", video.Size)
	assert.Equal(t, "https://api.example/video-cache/task_public.mp4", video.ResultURL)
	assert.Equal(t, "https://api.example/video-cache/task_public.mp4", video.Metadata["url"])
}

func TestConvertToOpenAIVideoReturnsQueuedBeforeFirstPoll(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_pending",
		Status:    model.TaskStatusNotStart,
		Progress:  "0%",
		CreatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "video-v1",
			VideoSeconds:    "5",
			VideoSize:       "1280x720",
		},
	}

	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	var video dto.OpenAIVideo
	require.NoError(t, common.Unmarshal(body, &video))
	assert.Equal(t, dto.VideoStatusQueued, video.Status)
	assert.Zero(t, video.Progress)
}

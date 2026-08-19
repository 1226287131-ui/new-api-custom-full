package minimaxvideo

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMiniMaxVideoJSONContext(t *testing.T, payload string) (*gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	return newMiniMaxVideoFixture(c)
}

func newMiniMaxVideoMultipartContext(t *testing.T, body *bytes.Buffer, contentType string) (*gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", body)
	c.Request.Header.Set("Content-Type", contentType)
	return newMiniMaxVideoFixture(c)
}

func newMiniMaxVideoFixture(c *gin.Context) (*gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "MiniMax-H3-933-1440P-GF",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-test",
			ChannelBaseUrl:    "https://upstream.example/",
			UpstreamModelName: "MiniMax-H3-933-1440P-GF",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	return c, adaptor, info
}

func readJSONBody(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	return payload
}

func TestMiniMaxVideoBuildsStrictH3TextPayload(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoJSONContext(t, `{
  "model": "MiniMax-H3",
  "prompt": "清晨的海边公路",
  "duration": 8,
  "size": "1920x1088"
}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	info.UpstreamModelName = "minimax_h3"

	assert.Equal(t, map[string]any{
		"model":   "minimax_h3",
		"prompt":  "清晨的海边公路",
		"seconds": float64(8),
		"size":    "1920x1088",
	}, readJSONBody(t, mustBuildBody(t, adaptor, c, info)))
}

func TestMiniMaxVideoStripsLegacyConflictingFields(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoJSONContext(t, `{
  "model": "MiniMax-H3",
  "prompt": "参考素材驱动人物跳舞",
  "duration": 12,
  "seconds": 5,
  "size": "1088x1920",
  "audio": true,
  "mode": "first_last_frame",
  "prompt_enhance": true,
  "resolution": "1080p",
  "clarity": "high",
  "aspect_ratio": "9:16",
  "megapixels": 2,
  "metadata": {"multiple": 16},
  "input_reference": "https://example.com/person.png",
  "reference_video": "https://example.com/motion.mp4",
  "reference_video_audio": "https://example.com/motion.mp3",
  "reference_audio": "https://example.com/music.mp3"
}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	upstream := readJSONBody(t, mustBuildBody(t, adaptor, c, info))
	assert.Equal(t, float64(12), upstream["seconds"])
	assert.Equal(t, []any{"https://example.com/person.png"}, upstream["images"])
	assert.Equal(t, []any{"https://example.com/motion.mp4"}, upstream["reference_videos"])
	assert.Equal(t, []any{
		"https://example.com/motion.mp3",
		"https://example.com/music.mp3",
	}, upstream["reference_audios"])
	for _, field := range []string{
		"duration", "audio", "mode", "prompt_enhance", "resolution", "clarity",
		"aspect_ratio", "megapixels", "metadata", "reference_video_audios",
	} {
		assert.NotContains(t, upstream, field)
	}
}

func TestMiniMaxVideoRequiresDocumentedSizeAndValidMediaURLs(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "missing size",
			payload: `{"model":"MiniMax-H3","prompt":"scene","seconds":5}`,
			want:    "size field is required",
		},
		{
			name:    "unsupported size",
			payload: `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"1920x1080"}`,
			want:    "documented MiniMax-H3 dimensions",
		},
		{
			name:    "invalid reference",
			payload: `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"1920x1088","images":["blob:https://video.kkone.vip/id"]}`,
			want:    "must be an HTTP or HTTPS URL",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c, adaptor, info := newMiniMaxVideoJSONContext(t, testCase.payload)
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			require.NotNil(t, taskErr.Error)
			assert.Contains(t, taskErr.Error.Error(), testCase.want)
		})
	}
}

func TestMiniMaxVideoMultipartStripsConflictingFields(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "MiniMax-H3"))
	require.NoError(t, writer.WriteField("prompt", "参考图片生成视频"))
	require.NoError(t, writer.WriteField("duration", "12"))
	require.NoError(t, writer.WriteField("size", "1920x1088"))
	require.NoError(t, writer.WriteField("audio", "true"))
	require.NoError(t, writer.WriteField("images", "https://example.com/person.png"))
	require.NoError(t, writer.Close())

	c, adaptor, info := newMiniMaxVideoMultipartContext(t, &body, writer.FormDataContentType())
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	upstreamRequest := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/videos", nil)
	require.NoError(t, adaptor.BuildRequestHeader(c, upstreamRequest, info))
	_, params, err := mime.ParseMediaType(upstreamRequest.Header.Get("Content-Type"))
	require.NoError(t, err)
	form, err := multipart.NewReader(bytes.NewReader(encoded), params["boundary"]).ReadForm(4 * 1024 * 1024)
	require.NoError(t, err)
	defer form.RemoveAll()

	assert.Equal(t, []string{"12"}, form.Value["seconds"])
	assert.Equal(t, []string{"1920x1088"}, form.Value["size"])
	assert.Equal(t, []string{"https://example.com/person.png"}, form.Value["images"])
	assert.NotContains(t, form.Value, "duration")
	assert.NotContains(t, form.Value, "audio")
}

func TestMiniMaxVideoParsesCompletedTaskAndReturnsLocalVideoURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	completed, err := adaptor.ParseTaskResult([]byte(`{
  "id": "provider-task",
  "status": "completed",
  "progress": 100
}`))
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, completed.Status)
	assert.Equal(t, "100%", completed.Progress)

	converted, err := adaptor.ConvertToOpenAIVideo(&model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		CreatedAt: 100,
		UpdatedAt: 200,
		Properties: model.Properties{
			OriginModelName: "MiniMax-H3-933-1440P-GF",
			VideoSeconds:    "5",
			VideoSize:       "1920x1088",
		},
	})
	require.NoError(t, err)
	assert.Contains(t, string(converted), "/video-cache/task_public.mp4")
}

func TestMiniMaxVideoUsesNativeEndpointAndJSONHeaders(t *testing.T) {
	adaptor := &TaskAdaptor{}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiKey:         "sk-test",
		ChannelBaseUrl: "https://upstream.example/",
	}})
	requestURL, err := adaptor.BuildRequestURL(nil)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos", requestURL)
	assert.Equal(t, "minimax-video", adaptor.GetChannelName())
	assert.True(t, strings.Contains(strings.Join(adaptor.GetModelList(), ","), "MiniMax-H3"))
}

func mustBuildBody(t *testing.T, adaptor *TaskAdaptor, c *gin.Context, info *relaycommon.RelayInfo) io.Reader {
	t.Helper()
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	return body
}

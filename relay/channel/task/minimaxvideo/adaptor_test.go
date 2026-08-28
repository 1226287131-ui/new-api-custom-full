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
	"github.com/QuantumNous/new-api/relaykit/dto"
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
		"model":          "minimax_h3",
		"prompt":         "清晨的海边公路",
		"prompt_enhance": "false",
		"seconds":        float64(8),
		"size":           "1920x1088",
		"workflow_id":    "multi-reference",
	}, readJSONBody(t, mustBuildBody(t, adaptor, c, info)))
}

func TestMiniMaxVideoPassesThroughWorkflowID(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoJSONContext(t, `{
  "model": "MiniMax-H3",
  "prompt": "首尾帧转场",
  "seconds": 5,
  "size": "1920x1088",
  "workflow_id": "fl2v",
  "images": ["https://example.com/first.png", "https://example.com/last.png"]
}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	upstream := readJSONBody(t, mustBuildBody(t, adaptor, c, info))
	assert.Equal(t, "fl2v", upstream["workflow_id"])
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
		"duration", "audio", "mode", "resolution", "clarity",
		"megapixels", "metadata", "reference_video_audios",
	} {
		assert.NotContains(t, upstream, field)
	}
	assert.Equal(t, "false", upstream["prompt_enhance"])
}

func TestMiniMaxVideoForcesPromptEnhancementSetting(t *testing.T) {
	tests := []struct {
		name             string
		configured       bool
		downstreamField  string
		expectedUpstream string
	}{
		{name: "disabled when omitted", expectedUpstream: "false"},
		{name: "disabled overrides true", downstreamField: `,"prompt_enhance":true`, expectedUpstream: "false"},
		{name: "enabled when omitted", configured: true, expectedUpstream: "true"},
		{name: "enabled overrides false", configured: true, downstreamField: `,"prompt_enhance":false`, expectedUpstream: "true"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c, adaptor, info := newMiniMaxVideoJSONContext(t, `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"1920x1088"`+testCase.downstreamField+`}`)
			info.ChannelSetting = dto.ChannelSettings{MiniMaxVideoPromptEnhance: testCase.configured}
			adaptor.Init(info)
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			upstream := readJSONBody(t, mustBuildBody(t, adaptor, c, info))
			assert.Equal(t, testCase.expectedUpstream, upstream["prompt_enhance"])
		})
	}
}

func TestMiniMaxVideoRequiresRecognizedSizeAndValidMediaURLs(t *testing.T) {
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
			payload: `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"1280x736"}`,
			want:    "documented MiniMax-H3 dimension or a standard video resolution",
		},
		{
			name:    "invalid reference",
			payload: `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"1920x1088","images":["blob:https://video.kkone.vip/id"]}`,
			want:    "must be an HTTP or HTTPS URL",
		},
		{
			name:    "invalid workflow",
			payload: `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"1920x1088","workflow_id":"unknown"}`,
			want:    "workflow_id must be one of",
		},
		{
			name:    "missing super resolution aspect ratio",
			payload: `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"4K","workflow_id":"cf-multi-reference"}`,
			want:    "aspect_ratio is required when size is 2K or 4K",
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

func TestMiniMaxVideoCanonicalizesDocumentedSize(t *testing.T) {
	size, err := normalizeSize(map[string]any{"size": "1920 × 1088"})
	require.NoError(t, err)
	assert.Equal(t, "1920x1088", size)
}

func TestMiniMaxVideoAcceptsStandard2KAnd4KSizes(t *testing.T) {
	tests := []struct {
		name           string
		size           string
		normalizedSize string
		upstreamSize   string
		billingTier    string
		aspectRatio    string
	}{
		{name: "2K uppercase alias", size: "2K", normalizedSize: "2k", upstreamSize: "2K", billingTier: "1440p", aspectRatio: "16:9"},
		{name: "2K lowercase alias", size: "2k", normalizedSize: "2k", upstreamSize: "2K", billingTier: "1440p", aspectRatio: "16:9"},
		{name: "4K uppercase alias", size: "4K", normalizedSize: "4k", upstreamSize: "4K", billingTier: "4k", aspectRatio: "9:16"},
		{name: "4K lowercase alias", size: "4k", normalizedSize: "4k", upstreamSize: "4K", billingTier: "4k", aspectRatio: "9:16"},
		{name: "2K DCI", size: "2048×858", normalizedSize: "2048x858", upstreamSize: "2048x858", billingTier: "1440p"},
		{name: "4K DCI", size: "4096x1716", normalizedSize: "4096x1716", upstreamSize: "4096x1716", billingTier: "4k"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			payload := `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"` + testCase.size + `"}`
			if testCase.aspectRatio != "" {
				payload = `{"model":"MiniMax-H3","prompt":"scene","seconds":5,"size":"` + testCase.size + `","aspect_ratio":"` + testCase.aspectRatio + `"}`
			}
			c, adaptor, info := newMiniMaxVideoJSONContext(t, payload)
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

			request, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			assert.Equal(t, testCase.normalizedSize, request.Size)
			assert.Equal(t, testCase.billingTier, request.BillingResolution)

			upstream := readJSONBody(t, mustBuildBody(t, adaptor, c, info))
			assert.Equal(t, testCase.upstreamSize, upstream["size"])
			if testCase.aspectRatio != "" {
				assert.Equal(t, testCase.aspectRatio, upstream["aspect_ratio"])
			} else {
				assert.NotContains(t, upstream, "aspect_ratio")
			}
		})
	}
}

func TestMiniMaxVideoMultipartStripsConflictingFields(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "MiniMax-H3"))
	require.NoError(t, writer.WriteField("prompt", "参考图片生成视频"))
	require.NoError(t, writer.WriteField("duration", "12"))
	require.NoError(t, writer.WriteField("size", "2k"))
	require.NoError(t, writer.WriteField("workflow_id", "mj"))
	require.NoError(t, writer.WriteField("aspect_ratio", "16:9"))
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
	assert.Equal(t, []string{"false"}, form.Value["prompt_enhance"])
	assert.Equal(t, []string{"2K"}, form.Value["size"])
	assert.Equal(t, []string{"mj"}, form.Value["workflow_id"])
	assert.Equal(t, []string{"16:9"}, form.Value["aspect_ratio"])
	assert.Equal(t, []string{"https://example.com/person.png"}, form.Value["images"])
	assert.NotContains(t, form.Value, "duration")
	assert.NotContains(t, form.Value, "audio")
}

func TestMiniMaxVideoMultipartForcesPromptEnhancementSetting(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "MiniMax-H3"))
	require.NoError(t, writer.WriteField("prompt", "scene"))
	require.NoError(t, writer.WriteField("seconds", "5"))
	require.NoError(t, writer.WriteField("size", "1920x1088"))
	require.NoError(t, writer.WriteField("prompt_enhance", "false"))
	require.NoError(t, writer.Close())

	c, adaptor, info := newMiniMaxVideoMultipartContext(t, &body, writer.FormDataContentType())
	info.ChannelSetting = dto.ChannelSettings{MiniMaxVideoPromptEnhance: true}
	adaptor.Init(info)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/videos", nil)
	require.NoError(t, adaptor.BuildRequestHeader(c, request, info))
	_, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	require.NoError(t, err)
	form, err := multipart.NewReader(bytes.NewReader(encoded), params["boundary"]).ReadForm(4 * 1024 * 1024)
	require.NoError(t, err)
	defer form.RemoveAll()

	assert.Equal(t, []string{"true"}, form.Value["prompt_enhance"])
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

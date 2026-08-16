package minimaxvideo

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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

func TestMiniMaxVideoJSONForwardsAllReferenceMediaTogether(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoJSONContext(t, `{
  "model": "minimax_h3",
  "prompt": "让人物根据参考素材自然起舞",
  "seconds": 5,
  "duration": 10,
  "size": "1920x1080",
  "prompt_enhance": true,
  "resolution": "1080P",
  "clarity": "2.0",
  "aspect_ratio": "16:9",
  "megapixels": 2.0,
  "input_reference": "https://example.com/person.png",
  "images": ["https://example.com/person.png", "https://example.com/outfit.png"],
  "reference_images": ["https://example.com/stage.png"],
  "reference_video": "https://example.com/motion.mp4",
  "reference_videos": ["https://example.com/motion-2.mp4"],
  "reference_video_audio": "https://example.com/motion.mp3",
  "reference_video_audios": ["https://example.com/motion-2.mp3"],
  "reference_audio": "https://example.com/music.mp3",
  "reference_audios": ["https://example.com/voice.mp3"],
  "metadata": {"multiple": 16, "reference_audios": ["https://example.com/voice-2.mp3"]}
}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
	assert.Equal(t, map[string]float64{"seconds": 10}, adaptor.EstimateBilling(c, info))

	info.UpstreamModelName = "provider-minimax-h3"
	upstream := readJSONBody(t, mustBuildBody(t, adaptor, c, info))
	assert.Equal(t, "provider-minimax-h3", upstream["model"])
	assert.Equal(t, "让人物根据参考素材自然起舞", upstream["prompt"])
	assert.Equal(t, "10", upstream["seconds"])
	assert.Equal(t, float64(10), upstream["duration"])
	assert.Equal(t, "1920x1080", upstream["size"])
	assert.Equal(t, true, upstream["prompt_enhance"])
	assert.Equal(t, "1080P", upstream["resolution"])
	assert.Equal(t, "2.0", upstream["clarity"])
	assert.Equal(t, "16:9", upstream["aspect_ratio"])
	assert.Equal(t, 2.0, upstream["megapixels"])
	assert.Equal(t, []any{
		"https://example.com/person.png",
		"https://example.com/outfit.png",
		"https://example.com/stage.png",
	}, upstream["images"])
	assert.Equal(t, []any{
		"https://example.com/motion.mp4",
		"https://example.com/motion-2.mp4",
	}, upstream["reference_videos"])
	assert.Equal(t, []any{
		"https://example.com/motion.mp3",
		"https://example.com/motion-2.mp3",
	}, upstream["reference_video_audios"])
	assert.Equal(t, []any{
		"https://example.com/music.mp3",
		"https://example.com/voice.mp3",
		"https://example.com/voice-2.mp3",
	}, upstream["reference_audios"])

	metadata, ok := upstream["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(16), metadata["multiple"])

	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, 3, request.Metadata["reference_image_count"])
	assert.Equal(t, 2, request.Metadata["reference_video_count"])
	assert.Equal(t, 2, request.Metadata["reference_video_audio_count"])
	assert.Equal(t, 3, request.Metadata["reference_audio_count"])
}

func TestMiniMaxVideoDurationPriorityAndDefaults(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoJSONContext(t, `{
  "model": "minimax_h3",
  "prompt": "A calm sunrise.",
  "seconds": 12,
  "metadata": {"duration": 20}
}`)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, 12, mustTaskRequest(t, c).Duration)

	c, adaptor, info = newMiniMaxVideoJSONContext(t, `{"model":"minimax_h3","prompt":"A calm sunrise."}`)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, 5, mustTaskRequest(t, c).Duration)
	assert.Equal(t, constant.TaskActionTextGenerate, info.Action)

	body := readJSONBody(t, mustBuildBody(t, adaptor, c, info))
	assert.Equal(t, "5", body["seconds"])
	assert.NotContains(t, body, "duration")
	assert.Equal(t, true, body["audio"])
}

func TestMiniMaxVideoAcceptsReferenceAliasesAndDeduplicates(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoJSONContext(t, `{
  "model":"minimax_h3",
  "prompt":"scene",
  "input_reference":["https://example.com/a.png", "https://example.com/a.png"],
  "video_urls":"https://example.com/ref.mp4",
  "audios":["https://example.com/ref.mp3", "https://example.com/ref.mp3"]
}`)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	body := readJSONBody(t, mustBuildBody(t, adaptor, c, info))
	assert.Equal(t, []any{"https://example.com/a.png"}, body["images"])
	assert.Equal(t, []any{"https://example.com/ref.mp4"}, body["reference_videos"])
	assert.Equal(t, []any{"https://example.com/ref.mp3"}, body["reference_audios"])
}

func TestMiniMaxVideoMultipartForwardsMixedReferenceFiles(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "minimax_h3"))
	require.NoError(t, writer.WriteField("prompt", "把参考素材融合成舞蹈视频"))
	require.NoError(t, writer.WriteField("seconds", "5"))
	require.NoError(t, writer.WriteField("duration", "12"))
	require.NoError(t, writer.WriteField("size", "1920x1080"))
	require.NoError(t, writer.WriteField("metadata", `{"multiple":16}`))
	writeMultipartTestFile(t, writer, "input_reference", "person.png", []byte("image-bytes"))
	writeMultipartTestFile(t, writer, "reference_video", "motion.mp4", []byte("video-bytes"))
	writeMultipartTestFile(t, writer, "reference_video_audio", "motion.mp3", []byte("video-audio-bytes"))
	writeMultipartTestFile(t, writer, "reference_audio", "music.mp3", []byte("audio-bytes"))
	require.NoError(t, writer.Close())

	c, adaptor, info := newMiniMaxVideoMultipartContext(t, &body, writer.FormDataContentType())
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
	assert.Equal(t, 12, mustTaskRequest(t, c).Duration)

	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	upstreamRequest, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/videos", nil)
	require.NoError(t, err)
	require.NoError(t, adaptor.BuildRequestHeader(c, upstreamRequest, info))
	_, params, err := mime.ParseMediaType(upstreamRequest.Header.Get("Content-Type"))
	require.NoError(t, err)
	form, err := multipart.NewReader(bytes.NewReader(encoded), params["boundary"]).ReadForm(4 * 1024 * 1024)
	require.NoError(t, err)
	defer form.RemoveAll()

	assert.Equal(t, []string{"12"}, form.Value["seconds"])
	assert.Equal(t, []string{"12"}, form.Value["duration"])
	assert.Equal(t, []string{"1920x1080"}, form.Value["size"])
	var metadata map[string]any
	require.Len(t, form.Value["metadata"], 1)
	require.NoError(t, common.Unmarshal([]byte(form.Value["metadata"][0]), &metadata))
	assert.Equal(t, float64(16), metadata["multiple"])
	for _, field := range []string{"input_reference", "reference_video", "reference_video_audio", "reference_audio"} {
		require.Len(t, form.File[field], 1, field)
	}
	assertMultipartFileContent(t, form, "input_reference", []byte("image-bytes"))
	assertMultipartFileContent(t, form, "reference_video", []byte("video-bytes"))
	assertMultipartFileContent(t, form, "reference_video_audio", []byte("video-audio-bytes"))
	assertMultipartFileContent(t, form, "reference_audio", []byte("audio-bytes"))
}

func TestMiniMaxVideoRejectsReferenceLimitsAndInvalidParameters(t *testing.T) {
	images := make([]string, 10)
	for index := range images {
		images[index] = fmt.Sprintf("https://cdn.example/image-%d.png", index)
	}
	videos := []string{"https://cdn.example/1.mp4", "https://cdn.example/2.mp4", "https://cdn.example/3.mp4", "https://cdn.example/4.mp4"}
	audios := []string{"https://cdn.example/1.mp3", "https://cdn.example/2.mp3", "https://cdn.example/3.mp3", "https://cdn.example/4.mp3"}
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{name: "too many images", payload: map[string]any{"images": images}, want: "reference images support at most 9"},
		{name: "too many videos", payload: map[string]any{"reference_videos": videos}, want: "reference videos support at most 3"},
		{name: "too many video audios", payload: map[string]any{"reference_video_audios": audios}, want: "reference video audios support at most 3"},
		{name: "too many audios", payload: map[string]any{"reference_audios": audios}, want: "reference audios support at most 3"},
		{name: "duration below minimum", payload: map[string]any{"seconds": 0}, want: "between 1 and 300"},
		{name: "duration above maximum", payload: map[string]any{"duration": 301}, want: "between 1 and 300"},
		{name: "invalid multiple", payload: map[string]any{"metadata": map[string]any{"multiple": 10}}, want: "metadata.multiple"},
		{name: "private URL", payload: map[string]any{"reference_audio": "http://127.0.0.1/file.mp3"}, want: "private IP address"},
		{name: "unknown field", payload: map[string]any{"file_urls": []string{"https://example.com/reference.pdf"}}, want: "file_urls is not supported"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := map[string]any{"model": "minimax_h3", "prompt": "scene"}
			for key, value := range testCase.payload {
				payload[key] = value
			}
			encoded, err := common.Marshal(payload)
			require.NoError(t, err)
			c, adaptor, info := newMiniMaxVideoJSONContext(t, string(encoded))
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			require.NotNil(t, taskErr.Error)
			assert.Contains(t, taskErr.Error.Error(), testCase.want)
		})
	}
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
			VideoSize:       "1920x1080",
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

func mustTaskRequest(t *testing.T, c *gin.Context) relaycommon.TaskSubmitReq {
	t.Helper()
	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	return request
}

func writeMultipartTestFile(t *testing.T, writer *multipart.Writer, field, filename string, content []byte) {
	t.Helper()
	part, err := writer.CreateFormFile(field, filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
}

func assertMultipartFileContent(t *testing.T, form *multipart.Form, field string, want []byte) {
	t.Helper()
	file, err := form.File[field][0].Open()
	require.NoError(t, err)
	got, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, want, got)
}

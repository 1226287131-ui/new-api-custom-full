package grokvideo

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGrokVideoRequestContext(t *testing.T, body *bytes.Buffer, contentType string) (*gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", body)
	c.Request.Header.Set("Content-Type", contentType)

	info := &relaycommon.RelayInfo{
		OriginModelName: "grok-imagine-video",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-test",
			ChannelBaseUrl:    "https://upstream.example/",
			UpstreamModelName: "grok-imagine-video",
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	return c, adaptor, info
}

func grokMultipartBody(t *testing.T, fields map[string]string, inputReference []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}
	if inputReference != nil {
		part, err := writer.CreateFormFile("input_reference", "reference.png")
		require.NoError(t, err)
		_, err = part.Write(inputReference)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return &body, writer.FormDataContentType()
}

func TestGrokVideoBuildRequestForwardsNativeMultipartContract(t *testing.T) {
	body, contentType := grokMultipartBody(t, map[string]string{
		"model":        "grok-imagine-video",
		"prompt":       "Animate the reference image with a smooth camera move.",
		"aspect_ratio": "9:16",
		"seconds":      "8",
		"resolution":   "1080p",
	}, []byte("png-reference-data"))
	c, adaptor, info := newGrokVideoRequestContext(t, body, contentType)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
	assert.Equal(t, map[string]float64{"seconds": 8}, adaptor.EstimateBilling(c, info))

	info.UpstreamModelName = "provider-grok-video"
	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)

	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/videos", nil)
	require.NoError(t, err)
	require.NoError(t, adaptor.BuildRequestHeader(c, request, info))
	assert.Equal(t, "Bearer sk-test", request.Header.Get("Authorization"))

	_, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	require.NoError(t, err)
	form, err := multipart.NewReader(bytes.NewReader(encoded), params["boundary"]).ReadForm(1024 * 1024)
	require.NoError(t, err)
	defer form.RemoveAll()

	assert.Equal(t, []string{"provider-grok-video"}, form.Value["model"])
	assert.Equal(t, []string{"Animate the reference image with a smooth camera move."}, form.Value["prompt"])
	assert.Equal(t, []string{"9:16"}, form.Value["aspect_ratio"])
	assert.Equal(t, []string{"8"}, form.Value["seconds"])
	assert.Equal(t, []string{"1080p"}, form.Value["resolution"])
	require.Len(t, form.File["input_reference"], 1)
	file, err := form.File["input_reference"][0].Open()
	require.NoError(t, err)
	contents, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, []byte("png-reference-data"), contents)

	requestData, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, 8, requestData.Duration)
	assert.Equal(t, "608x1080", requestData.Size)
	assert.Equal(t, "1080p", requestData.Metadata["resolution"])
}

func TestGrokVideoValidationRejectsUnsupportedDuration(t *testing.T) {
	body, contentType := grokMultipartBody(t, map[string]string{
		"model":   "grok-imagine-video",
		"prompt":  "Animate this scene.",
		"seconds": "16",
	}, nil)
	c, adaptor, info := newGrokVideoRequestContext(t, body, contentType)

	taskErr := adaptor.ValidateRequestAndSetAction(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	require.NotNil(t, taskErr.Error)
	assert.Contains(t, taskErr.Error.Error(), "seconds must be an integer between 1 and 15")
}

func TestGrokVideoParsesCompletedAndFailedTasks(t *testing.T) {
	adaptor := &TaskAdaptor{}

	completed, err := adaptor.ParseTaskResult([]byte(`{
  "id": "video_provider",
  "status": "completed",
  "progress": 100,
  "seconds": "8",
  "size": "720x1280"
}`))
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", string(completed.Status))
	assert.Equal(t, "100%", completed.Progress)
	assert.Empty(t, completed.Url)

	failed, err := adaptor.ParseTaskResult([]byte(`{
  "id": "video_provider",
  "status": "failed",
  "error": {"code": "content_policy", "message": "generation rejected"}
}`))
	require.NoError(t, err)
	assert.Equal(t, "FAILURE", string(failed.Status))
	assert.Equal(t, "100%", failed.Progress)
	assert.Equal(t, "generation rejected", failed.Reason)
}

func TestGrokVideoUsesNativeEndpoints(t *testing.T) {
	adaptor := &TaskAdaptor{}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiKey:         "sk-test",
		ChannelBaseUrl: "https://upstream.example/",
	}})

	requestURL, err := adaptor.BuildRequestURL(nil)
	require.NoError(t, err)
	assert.Equal(t, "https://upstream.example/v1/videos", requestURL)
	assert.Equal(t, "grok-video", adaptor.GetChannelName())
	assert.True(t, strings.Contains(strings.Join(adaptor.GetModelList(), ","), "grok-imagine-video"))
}

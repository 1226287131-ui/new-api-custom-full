package minimaxvideo

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMiniMaxVideoRequestContext(t *testing.T, body string) (*gin.Context, *TaskAdaptor, *relaycommon.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")

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

func TestMiniMaxVideoBuildRequestForwardsNativeJSONContract(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoRequestContext(t, `{
  "model": "MiniMax-H3-933-1440P-GF",
  "prompt": "Animate the reference subject with a smooth forward camera move.",
  "seconds": 5,
  "size": "2560x1440",
  "audio": false,
  "images": ["https://cdn.example/reference.png"]
}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionGenerate, info.Action)
	assert.Equal(t, map[string]float64{"seconds": 5}, adaptor.EstimateBilling(c, info))

	info.UpstreamModelName = "provider-minimax-video"
	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)

	var upstream map[string]any
	require.NoError(t, common.Unmarshal(encoded, &upstream))
	assert.Equal(t, "provider-minimax-video", upstream["model"])
	assert.Equal(t, "Animate the reference subject with a smooth forward camera move.", upstream["prompt"])
	assert.Equal(t, "5", upstream["seconds"])
	assert.Equal(t, "2560x1440", upstream["size"])
	assert.Equal(t, false, upstream["audio"])
	assert.Equal(t, []any{"https://cdn.example/reference.png"}, upstream["images"])

	request, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, 5, request.Duration)
	assert.Equal(t, "5", request.Seconds)
	assert.Equal(t, "2560x1440", request.Size)
	assert.Equal(t, "1440p", request.Metadata["resolution"])
}

func TestMiniMaxVideoDefaultsAudioToTrue(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoRequestContext(t, `{
  "model": "MiniMax-H3-933-1440P-GF",
  "prompt": "A calm sunrise over the ocean.",
  "seconds": 8,
  "size": "1440x1920"
}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, constant.TaskActionTextGenerate, info.Action)
	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)

	var upstream map[string]any
	require.NoError(t, common.Unmarshal(encoded, &upstream))
	assert.Equal(t, true, upstream["audio"])
	assert.NotContains(t, upstream, "images")
}

func TestMiniMaxVideoAcceptsStringDurationForNativeClients(t *testing.T) {
	c, adaptor, info := newMiniMaxVideoRequestContext(t, `{
  "model": "MiniMax-H3-933-1440P-GF",
  "prompt": "A calm sunrise over the ocean.",
  "seconds": "10",
  "size": "1440x1920"
}`)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)

	var upstream map[string]any
	require.NoError(t, common.Unmarshal(encoded, &upstream))
	assert.Equal(t, "10", upstream["seconds"])
}

func TestMiniMaxVideoAcceptsEverySupportedSize(t *testing.T) {
	for _, size := range []string{
		"3360x1440",
		"2560x1440",
		"1920x1440",
		"1440x1440",
		"1440x1920",
		"1440x2560",
	} {
		t.Run(size, func(t *testing.T) {
			c, adaptor, info := newMiniMaxVideoRequestContext(t, `{
  "model": "MiniMax-H3-933-1440P-GF",
  "prompt": "A cinematic scene.",
  "seconds": 5,
  "size": "`+size+`"
}`)
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			request, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			assert.Equal(t, size, request.Size)
		})
	}
}

func TestMiniMaxVideoValidationRejectsInvalidDurationAndReferences(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "duration below minimum",
			body: `{"model":"MiniMax-H3-933-1440P-GF","prompt":"scene","seconds":4,"size":"2560x1440"}`,
			want: "seconds must be between 5 and 15",
		},
		{
			name: "duration above maximum",
			body: `{"model":"MiniMax-H3-933-1440P-GF","prompt":"scene","seconds":16,"size":"2560x1440"}`,
			want: "seconds must be between 5 and 15",
		},
		{
			name: "duration must be an integer",
			body: `{"model":"MiniMax-H3-933-1440P-GF","prompt":"scene","seconds":5.5,"size":"2560x1440"}`,
			want: "seconds must be an integer between 5 and 15",
		},
		{
			name: "too many images",
			body: `{"model":"MiniMax-H3-933-1440P-GF","prompt":"scene","seconds":5,"size":"2560x1440","images":["https://cdn.example/1.png","https://cdn.example/2.png","https://cdn.example/3.png","https://cdn.example/4.png","https://cdn.example/5.png","https://cdn.example/6.png"]}`,
			want: "images supports at most 5 reference images",
		},
		{
			name: "reference video is unsupported",
			body: `{"model":"MiniMax-H3-933-1440P-GF","prompt":"scene","seconds":5,"size":"2560x1440","video_urls":["https://cdn.example/reference.mp4"]}`,
			want: "video_urls is not supported",
		},
		{
			name: "reference audio is unsupported",
			body: `{"model":"MiniMax-H3-933-1440P-GF","prompt":"scene","seconds":5,"size":"2560x1440","audio_urls":["https://cdn.example/reference.mp3"]}`,
			want: "audio_urls is not supported",
		},
		{
			name: "reference file is unsupported",
			body: `{"model":"MiniMax-H3-933-1440P-GF","prompt":"scene","seconds":5,"size":"2560x1440","file_urls":["https://cdn.example/reference.pdf"]}`,
			want: "file_urls is not supported",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			c, adaptor, info := newMiniMaxVideoRequestContext(t, testCase.body)
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
			VideoSize:       "2560x1440",
		},
	})
	require.NoError(t, err)
	assert.Contains(t, string(converted), "/video-cache/task_public.mp4")
}

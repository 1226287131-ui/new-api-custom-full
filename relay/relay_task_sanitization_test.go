package relay

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskModel2DtoSanitizesNewAPIVideoProviderData(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() {
		system_setting.ServerAddress = previousServerAddress
	})

	task := &model.Task{
		TaskID:   "task_public",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeNewAPIVideo)),
		Status:   model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "provider_task_123",
			ResultURL:      "https://upstream.example/video.mp4",
		},
		Data: json.RawMessage(`{
			"task_id":"provider_task_123",
			"result_url":"https://upstream.example/video.mp4"
		}`),
	}

	dto := TaskModel2Dto(task)
	assert.Equal(t, "https://api.example/video-cache/task_public.mp4", dto.ResultURL)
	assert.NotContains(t, string(dto.Data), "upstream.example")
	assert.NotContains(t, string(dto.Data), "provider_task_123")
	assert.Contains(t, string(dto.Data), "task_public")
}

func TestTaskModel2DtoSanitizesOpenAIVideoProviderData(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() {
		system_setting.ServerAddress = previousServerAddress
	})

	task := &model.Task{
		TaskID:   "task_public",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeOpenAIVideo)),
		Status:   model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "provider_task_123",
			ResultURL:      "https://upstream.example/video.mp4",
		},
		Data: json.RawMessage(`{
			"task_id":"provider_task_123",
			"result_url":"https://upstream.example/video.mp4"
		}`),
	}

	dto := TaskModel2Dto(task)
	require.NotNil(t, dto)
	assert.Equal(t, "https://api.example/video-cache/task_public.mp4", dto.ResultURL)
	assert.NotContains(t, string(dto.Data), "upstream.example")
	assert.NotContains(t, string(dto.Data), "provider_task_123")
	assert.Contains(t, string(dto.Data), "task_public")
}

func TestTaskModel2DtoHidesExpiredVideoResultURL(t *testing.T) {
	previousServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example"
	t.Cleanup(func() {
		system_setting.ServerAddress = previousServerAddress
	})

	task := &model.Task{
		TaskID:     "task_expired",
		Platform:   constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeNewAPIVideo)),
		Status:     model.TaskStatusSuccess,
		FinishTime: time.Now().Add(-49 * time.Hour).Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "provider_task_expired",
			ResultURL:      "https://upstream.example/video.mp4",
		},
		Data: json.RawMessage(`{"result_url":"https://upstream.example/video.mp4"}`),
	}

	dto := TaskModel2Dto(task)
	require.NotNil(t, dto)
	assert.Empty(t, dto.ResultURL)
	assert.NotContains(t, string(dto.Data), "upstream.example")
}

func TestTaskModel2DtoDoesNotExposeRequestBody(t *testing.T) {
	task := &model.Task{
		TaskID: "task_private",
		Properties: model.Properties{
			OriginModelName:   "video-v3",
			UpstreamModelName: "upstream-video-v3",
		},
		RequestBody:         json.RawMessage(`{"prompt":"secret prompt","model":"video-v3"}`),
		RequestBodyComplete: true,
	}

	result := TaskModel2Dto(task)
	assert.Equal(t, "video-v3", result.ModelName)
	assert.Equal(t, "upstream-video-v3", result.UpstreamModelName)
	assert.Empty(t, result.RequestBody)
}

func TestTaskModel2DtoForAdminIncludesRequestBody(t *testing.T) {
	body := json.RawMessage(`{"prompt":"secret prompt","model":"video-v3"}`)
	task := &model.Task{
		TaskID: "task_admin",
		Properties: model.Properties{
			OriginModelName: "video-v3",
		},
		RequestBody:         body,
		RequestBodyComplete: true,
	}

	result := TaskModel2DtoForAdmin(task)
	assert.Equal(t, "video-v3", result.ModelName)
	assert.JSONEq(t, string(body), string(result.RequestBody))
	assert.True(t, result.RequestBodyComplete)
}

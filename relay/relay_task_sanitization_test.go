package relay

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
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

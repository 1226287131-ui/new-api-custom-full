package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyVideoSubmitThirtySecondsPersistsOnceBeforePublication(t *testing.T) {
	service.InitHttpClient()
	events := []string{}
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLog := common.LogConsumeEnabled
	previousPrices := ratio_setting.ModelPrice2JSONString()
	common.LogConsumeEnabled = false
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"legacy-video-merge":0.01}`))
	t.Cleanup(func() {
		common.LogConsumeEnabled = previousLog
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
	})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/videos", r.URL.Path)
		assert.Equal(t, "Bearer legacy-test-key", r.Header.Get("Authorization"))
		var request map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &request))
		assert.Equal(t, float64(30), request["duration"])
		assert.Equal(t, "1080p", request["resolution"])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"id":"provider-private-id","status":"queued"}`)
	}))
	defer upstream.Close()
	ch := &model.Channel{Id: 601, Type: constant.ChannelTypeOpenAIVideo, Name: "legacy video", Key: "legacy-test-key", BaseURL: &upstream.URL, Status: common.ChannelStatusEnabled}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	const requestBody = `{"model":"legacy-video-merge","prompt":"a quiet ocean","seconds":"30","size":"1920x1080"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyUserId, 1)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, ch, "legacy-video-merge"))
	billing := &taskSubmissionTestBilling{events: &events, onSettle: func() {
		var count int64
		require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
		assert.Equal(t, int64(1), count)
		assert.False(t, c.Writer.Written(), "success must wait for settlement")
	}}
	info := taskSubmissionRelayInfo(billing)
	info.OriginModelName = "legacy-video-merge"
	info.LockedChannel = ch
	outcome, taskErr := executeTaskSubmissionWith(c, info, relay.RelayTaskSubmit)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.False(t, c.Writer.Written())
	var persisted model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&persisted).Error)
	assert.Equal(t, constant.TaskPlatform("60"), persisted.Platform)
	assert.Equal(t, "provider-private-id", persisted.PrivateData.UpstreamTaskID)
	assert.Equal(t, "30", persisted.Properties.VideoSeconds)
	assert.Equal(t, "1920x1080", persisted.Properties.VideoSize)
	assert.JSONEq(t, requestBody, string(persisted.RequestBody))
	assert.True(t, persisted.RequestBodyComplete)
	assert.NotContains(t, string(persisted.Data), "provider-private-id")
	presentTaskSubmission(c, outcome)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "task_public")
	assert.NotContains(t, recorder.Body.String(), "provider-private-id")
}

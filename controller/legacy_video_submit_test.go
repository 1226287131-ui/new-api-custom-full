package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyVideoSubmitThirtySecondsPersistsOnceBeforePublication(t *testing.T) {
	service.InitHttpClient()
	previousTimeout := common.RelayTimeout
	common.RelayTimeout = 60 * 60
	t.Cleanup(func() { common.RelayTimeout = previousTimeout })
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
	started := time.Now()
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		deadline, bounded := info.SubmissionContext.Deadline()
		assert.True(t, bounded)
		assert.False(t, deadline.Before(started.Add(time.Hour)), "an explicit relay timeout longer than 30 minutes must remain effective")
		return relay.RelayTaskSubmit(c, info)
	})
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

func TestLegacyVideoAcceptedDisconnectDatabase(t *testing.T) {
	service.InitHttpClient()
	kind := os.Getenv("TEST_TASK_DB_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	dsn := ""
	switch kind {
	case "mysql":
		dsn = os.Getenv("TEST_MYSQL_DSN")
		require.NotEmpty(t, dsn)
	case "postgres":
		dsn = os.Getenv("TEST_POSTGRES_DSN")
		require.NotEmpty(t, dsn)
	case "sqlite":
	default:
		t.Fatalf("unsupported test database %q", kind)
	}
	database, _ := newAuditTestDatabase(t, kind, dsn)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.GroupUserRatio{}))
	versionSQL := "SELECT version()"
	if kind == "sqlite" {
		versionSQL = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, database.Raw(versionSQL).Scan(&version).Error)
	t.Logf("database: %s %s", kind, version)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	previousPrices := ratio_setting.ModelPrice2JSONString()
	model.DB, model.LOG_DB = database, database
	common.SetDatabaseTypes(common.DatabaseType(kind), common.DatabaseType(kind))
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"accepted-video":0.01}`))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
	})
	for index, flushHeaders := range []bool{false, true} {
		t.Run(fmt.Sprintf("headers_sent=%t", flushHeaders), func(t *testing.T) {
			accepted, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				if flushHeaders {
					w.WriteHeader(http.StatusAccepted)
					w.(http.Flusher).Flush()
				}
				close(accepted)
				<-release
				_, _ = io.WriteString(w, `{"id":"accepted-private-id","status":"queued"}`)
			}))
			defer upstream.Close()
			initialQuota := common.QuotaRound(20 * common.QuotaPerUnit)
			user := &model.User{Username: fmt.Sprintf("disconnect_%d", index), AffCode: fmt.Sprintf("disconnect_%d", index), Quota: initialQuota}
			require.NoError(t, database.Create(user).Error)
			ch := &model.Channel{Type: constant.ChannelTypeOpenAIVideo, Key: "test-key", BaseURL: &upstream.URL, Status: common.ChannelStatusEnabled}
			require.NoError(t, database.Create(ch).Error)
			c := taskSubmissionTestContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"accepted-video","prompt":"scene","seconds":5}`))
			c.Request.Header.Set("Content-Type", "application/json")
			clientContext, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(clientContext)
			common.SetContextKey(c, constant.ContextKeyUserId, user.Id)
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, ch, "accepted-video"))
			info := taskSubmissionRelayInfo(nil)
			info.UserId, info.UserGroup, info.OriginModelName = user.Id, "default", "accepted-video"
			info.UserQuota = initialQuota
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID, info.LockedChannel = model.GenerateTaskID(), ch
			var outcome *taskSubmissionOutcome
			var taskErr *dto.TaskError
			done := make(chan struct{})
			go func() {
				defer close(done)
				outcome, taskErr = executeTaskSubmission(c, info)
			}()
			select {
			case <-accepted:
			case <-done:
				close(release)
				t.Fatalf("submission failed before acceptance: %+v", taskErr)
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("upstream did not receive submission")
			}
			cancel()
			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("accepted submission did not finish")
			}
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.Equal(t, int32(1), calls.Load())
			assert.False(t, c.Writer.Written())
			var tasks []model.Task
			require.NoError(t, database.Where("user_id = ?", user.Id).Find(&tasks).Error)
			require.Len(t, tasks, 1)
			assert.Equal(t, "accepted-private-id", tasks[0].PrivateData.UpstreamTaskID)
			want := common.QuotaRound(5 * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, tasks[0].Quota)
			var stored model.User
			require.NoError(t, database.First(&stored, user.Id).Error)
			assert.Equal(t, initialQuota-want, stored.Quota)
			assert.Equal(t, want, stored.UsedQuota)
			var logs []model.Log
			require.NoError(t, database.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			info.Billing.Refund(c)
			require.NoError(t, info.Billing.Settle(want))
			require.NoError(t, database.First(&stored, user.Id).Error)
			assert.Equal(t, initialQuota-want, stored.Quota, "accepted work must not be refunded or charged twice")
		})
	}
}

func TestLegacyVideoDisconnectBeforeSendDoesNotSubmitOrRetry(t *testing.T) {
	previousRetries := common.RetryTimes
	common.RetryTimes = 2
	t.Cleanup(func() { common.RetryTimes = previousRetries })
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	previousPrices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"legacy-video":0.01}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices)) })
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	c := taskSubmissionTestContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"legacy-video","prompt":"scene","seconds":5}`))
	c.Request.Header.Set("Content-Type", "application/json")
	clientContext, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(clientContext)
	ch := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAIVideo, BaseURL: &upstream.URL}
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, ch, "legacy-video"))
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)
	info.LockedChannel, info.OriginModelName = ch, "legacy-video"
	attempts := 0
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		attempts++
		cancel()
		return relay.RelayTaskSubmit(c, info)
	})
	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, 1, attempts)
	assert.Zero(t, calls.Load())
	assert.Equal(t, []string{"refund"}, events)
}

func TestLegacyVideoSubmissionDeadlineStopsBodyReadWithoutRetry(t *testing.T) {
	service.InitHttpClient()
	previousTimeout, previousRetries := common.RelayTimeout, common.RetryTimes
	common.RelayTimeout, common.RetryTimes = 1, 2
	t.Cleanup(func() { common.RelayTimeout, common.RetryTimes = previousTimeout, previousRetries })
	events := []string{}
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousPrices := ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"legacy-video":0.01}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices)) })
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	c := taskSubmissionTestContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"legacy-video","prompt":"scene","seconds":5}`))
	c.Request.Header.Set("Content-Type", "application/json")
	ch := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAIVideo, BaseURL: &upstream.URL}
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, ch, "legacy-video"))
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)
	info.LockedChannel, info.OriginModelName = ch, "legacy-video"
	outcome, taskErr := executeTaskSubmissionWith(c, info, relay.RelayTaskSubmit)
	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_submission_timeout", taskErr.Code)
	assert.Equal(t, http.StatusGatewayTimeout, taskErr.StatusCode)
	assert.NoError(t, c.Request.Context().Err(), "the operation deadline must not cancel the client")
	assert.Equal(t, int32(1), calls.Load(), "an uncertain accepted submission must not be retried")
	assert.Equal(t, []string{"refund"}, events)
	var taskCount int64
	require.NoError(t, database.Model(&model.Task{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
}

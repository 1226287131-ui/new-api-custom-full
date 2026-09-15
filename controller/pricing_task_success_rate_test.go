package controller

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPricingTaskSuccessRatesDatabaseMatrix(t *testing.T) {
	const pluginKey = "task-success-rate"
	_, err := jsplugin.DefaultRegistry.Register(`
export const meta = {
  apiVersion: 1, key: "task-success-rate", name: "Task Success Rate", version: "1.0.0",
  author: {name: "Test"}, models: ["async-text"], fetchMode: "per_task"
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(pluginKey) })
	previousGroups, err := common.Marshal(setting.GetUserUsableGroupsCopy())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(string(previousGroups))) })

	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skipf("%s is not configured", dialect.env)
			}
			db := modelManagementDB(t, dialect.kind, os.Getenv(dialect.env))
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","retired":"Retired"}`))
			require.NoError(t, db.AutoMigrate(&model.Task{}))
			t.Cleanup(model.InvalidatePricingCache)
			channels := []model.Channel{
				{Name: "native", Type: constant.ChannelTypeOpenAIVideo, Key: "private-channel-key", Group: "default", Status: common.ChannelStatusEnabled, Models: "site-alias,no-history,hidden,only-failure,null"},
				{Name: "mixed", Type: constant.ChannelTypeGemini, Key: "private-channel-key", Group: "default", Status: common.ChannelStatusEnabled, Models: "gemini-chat,background-client,provider-internal,async-text,plugin-alias,veo-unregistered", ModelMapping: common.GetPointer(`{"plugin-alias":"async-text"}`)},
				{Name: "private", Type: constant.ChannelTypeOpenAIVideo, Key: "private-channel-key", Group: "private", Status: common.ChannelStatusEnabled, Models: "group-private,site-alias"},
			}
			for i := range channels {
				require.NoError(t, channels[i].Insert())
			}
			hiddenModel := model.Model{ModelName: "hidden"}
			require.NoError(t, db.Create(&hiddenModel).Error)
			require.NoError(t, db.Model(&hiddenModel).Update("status", 0).Error)
			model.RefreshPricing()
			byName := make(map[string]model.Pricing)
			for _, item := range model.GetPricing() {
				byName[item.ModelName] = item
			}
			assert.True(t, byName["no-history"].IsTaskModel)
			assert.True(t, byName["async-text"].IsTaskModel)
			assert.True(t, byName["plugin-alias"].IsTaskModel)
			assert.True(t, byName["veo-unregistered"].IsTaskModel)
			assert.False(t, byName["gemini-chat"].IsTaskModel, "a mixed provider's chat models are not asynchronous tasks")
			assert.NotContains(t, byName, "hidden")

			now := time.Now().Unix()
			start := now - 3600
			tasks := []model.Task{
				{TaskID: "private-task-success", Group: "default", ChannelId: channels[0].Id, SubmitTime: now - 120, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "site-alias", UpstreamModelName: "provider-internal"}},
				{Group: "default", ChannelId: channels[1].Id, SubmitTime: now - 60, Status: model.TaskStatusFailure, Properties: model.Properties{OriginModelName: "site-alias"}},
				{Group: "default", SubmitTime: now - 80, Status: model.TaskStatusInProgress, Properties: model.Properties{OriginModelName: "site-alias"}},
				{Group: "default", SubmitTime: now - 90, Status: model.TaskStatusUnknown, Properties: model.Properties{OriginModelName: "site-alias"}},
				{Group: "default", SubmitTime: now - 100, Status: model.TaskStatusSuccess, Properties: model.Properties{UpstreamModelName: "provider-internal"}, PrivateData: model.TaskPrivateData{Key: "private-channel-key", BillingContext: &model.TaskBillingContext{OriginModelName: "site-alias"}}},
				{Group: "default", SubmitTime: now - 100, Status: model.TaskStatusSuccess, Properties: model.Properties{UpstreamModelName: "provider-internal"}},
				{Group: "private", SubmitTime: now - 60, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "site-alias"}},
				{Group: "private", SubmitTime: now - 60, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "group-private"}},
				{Group: "default", SubmitTime: now - 60, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "hidden"}},
				{Group: "default", SubmitTime: now - 60, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "background-client"}},
				{Group: "default", SubmitTime: now - 60, Status: model.TaskStatusQueued, Properties: model.Properties{OriginModelName: "async-text"}},
				{Group: "retired", SubmitTime: now - 60, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "site-alias"}},
				{Group: "default", SubmitTime: now - 60, Status: model.TaskStatusFailure, Properties: model.Properties{OriginModelName: "only-failure"}},
				{Group: "default", SubmitTime: now - 60, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "null"}},
				{Group: "default", SubmitTime: start - 1, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "boundary"}},
				{Group: "default", SubmitTime: start, Status: model.TaskStatusFailure, Properties: model.Properties{OriginModelName: "boundary"}},
				{Group: "default", SubmitTime: now, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "boundary"}},
				{Group: "default", SubmitTime: now + 1, Status: model.TaskStatusFailure, Properties: model.Properties{OriginModelName: "boundary"}},
			}
			require.NoError(t, db.Create(&tasks).Error)
			nullIdentityTask := model.Task{Group: "default", SubmitTime: now - 60, Status: model.TaskStatusSuccess, PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{OriginModelName: "json-null-fallback"}}}
			require.NoError(t, db.Create(&nullIdentityTask).Error)
			require.NoError(t, db.Model(&nullIdentityTask).Update("properties", `{"origin_model_name":null}`).Error)
			snapshot, err := model.QueryTaskSuccessRates(context.Background(), start, now)
			require.NoError(t, err)
			assert.Equal(t, model.TaskOutcomeCounts{Total: 2, Success: 1, Failure: 1}, snapshot.Models["boundary"]["default"])
			assert.Equal(t, model.TaskOutcomeCounts{Total: 5, Success: 2, Failure: 1, Pending: 2}, snapshot.Models["site-alias"]["default"])
			assert.NotContains(t, snapshot.Models, "provider-internal", "an upstream name cannot substitute for a client model")
			assert.EqualValues(t, 1, snapshot.Models["null"]["default"].Success)
			assert.EqualValues(t, 1, snapshot.Models["json-null-fallback"]["default"].Success)

			var response struct {
				Success bool `json:"success"`
				Data    struct {
					WindowStart int64 `json:"window_start"`
					WindowEnd   int64 `json:"window_end"`
					Models      map[string]struct {
						model.TaskOutcomeCounts
						SuccessRate *float64 `json:"success_rate"`
					} `json:"models"`
				} `json:"data"`
			}
			recorder := modelManagementRequest(t, GetPricingTaskSuccessRates, http.MethodGet, "/api/pricing/task-success-rates", nil, &response)
			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
			require.True(t, response.Success)
			assert.EqualValues(t, 3600, response.Data.WindowEnd-response.Data.WindowStart)
			assert.Equal(t, model.TaskOutcomeCounts{Total: 5, Success: 2, Failure: 1, Pending: 2}, response.Data.Models["site-alias"].TaskOutcomeCounts)
			require.NotNil(t, response.Data.Models["site-alias"].SuccessRate)
			assert.InDelta(t, 200.0/3.0, *response.Data.Models["site-alias"].SuccessRate, 0.000001)
			assert.Nil(t, response.Data.Models["no-history"].SuccessRate)
			assert.Zero(t, response.Data.Models["no-history"].Total)
			require.NotNil(t, response.Data.Models["only-failure"].SuccessRate)
			assert.Zero(t, *response.Data.Models["only-failure"].SuccessRate)
			assert.EqualValues(t, 1, response.Data.Models["async-text"].Pending)
			assert.Nil(t, response.Data.Models["async-text"].SuccessRate)
			require.NotNil(t, response.Data.Models["background-client"].SuccessRate)
			assert.Equal(t, 100.0, *response.Data.Models["background-client"].SuccessRate)
			for _, excluded := range []string{"hidden", "group-private", "provider-internal", "gemini-chat", "boundary"} {
				assert.NotContains(t, response.Data.Models, excluded)
			}
			for _, secret := range []string{"channel_id", "user_id", "private-channel-key", "private-task-success", "private_data"} {
				assert.NotContains(t, recorder.Body.String(), secret)
			}

			// Repeated plaza requests share the same aggregate snapshot rather
			// than querying once per model or exposing fresh partial data.
			cached, err := model.GetTaskSuccessRates(context.Background())
			require.NoError(t, err)
			require.NoError(t, db.Create(&model.Task{Group: "default", SubmitTime: now - 10, Status: model.TaskStatusFailure, Properties: model.Properties{OriginModelName: "site-alias"}}).Error)
			again, err := model.GetTaskSuccessRates(context.Background())
			require.NoError(t, err)
			assert.Equal(t, cached, again)
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"private":"Private"}`))
			response.Data.Models = nil
			modelManagementRequest(t, GetPricingTaskSuccessRates, http.MethodGet, "/api/pricing/task-success-rates", nil, &response)
			assert.EqualValues(t, 1, response.Data.Models["site-alias"].Total)
			require.NotNil(t, response.Data.Models["site-alias"].SuccessRate)
			assert.Equal(t, 100.0, *response.Data.Models["site-alias"].SuccessRate)
			assert.Contains(t, response.Data.Models, "group-private")
			assert.NotContains(t, response.Data.Models, "no-history")
			queryContext, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = model.QueryTaskSuccessRates(queryContext, start, now)
			assert.ErrorIs(t, err, context.Canceled)
		})
	}
}

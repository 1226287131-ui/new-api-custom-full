package jsplugin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel"
	vertexcore "github.com/QuantumNous/new-api/relay/channel/vertex"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuth2JWTUsesSelectedChannelEgressAcrossTaskLifecycle(t *testing.T) {
	original := acquireAccessToken
	t.Cleanup(func() { acquireAccessToken = original; pluginAuthCache = sync.Map{} })
	credentials, err := common.Marshal(vertexcore.Credentials{ProjectID: "project", ClientEmail: "test@example.com", PrivateKey: "private-key"})
	require.NoError(t, err)
	plugin, err := pluginruntime.NewRegistry().Register(`
export const meta = {
  apiVersion:1,key:"egress-auth",name:"Egress Auth",version:"1.0.0",author:{name:"Test"},models:["m"],fetchMode:"per_task",
  auth:{type:"oauth2_jwt"},usageSchema:{seconds:{type:"number",unit:"second",description:"Duration"}}
};
function checkAuth(ctx) {
  if (ctx.apiKey !== undefined || ctx.authHeader !== "Bearer access-token") throw new Error("invalid auth context");
}
export function buildSubmitRequest(ctx) {checkAuth(ctx);return {url:ctx.baseUrl+"/submit",headers:{Authorization:ctx.authHeader}};}
export function parseSubmitResponse() {return {taskId:"upstream-1",immediate:{status:"SUCCESS"}};}
export function buildQueryRequest(ctx) {checkAuth(ctx);return {url:ctx.baseUrl+"/query"};}
export function parseTaskResult(ctx) {checkAuth(ctx);return {status:"SUCCESS"};}
export function extractUsageOnComplete(ctx) {checkAuth(ctx);return {seconds:2};}
export function listArtifacts() {return [];}
export function buildContentRequest(ctx) {checkAuth(ctx);return {url:ctx.baseUrl+"/content",headers:{Authorization:ctx.authHeader}};}
`, pluginruntime.Options{})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, egress, want string
		enabled            bool
	}{
		{name: "opted in", enabled: true, egress: "http://egress.example:8888", want: "http://egress.example:8888"},
		{name: "opted out", egress: "http://egress.example:8888", want: "http://channel.example:8888"},
		{name: "missing deployment egress", enabled: true, want: "http://channel.example:8888"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("UPSTREAM_EGRESS_PROXY", tc.egress)
			pluginAuthCache = sync.Map{}
			var tokenProxies []string
			acquireAccessToken = func(_ vertexcore.Credentials, proxy string) (string, error) {
				tokenProxies = append(tokenProxies, proxy)
				return "access-token", nil
			}
			info := &relaycommon.RelayInfo{
				OriginModelName: "m", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "public-1"},
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: "https://provider.example", ApiKey: string(credentials), UpstreamModelName: "m",
					ChannelSetting: dto.ChannelSettings{Proxy: "http://channel.example:8888", UpstreamEgressProxyEnabled: tc.enabled},
				},
			}
			adaptor := New(plugin)
			adaptor.Init(info)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			ctx.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "prompt"})
			require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
			response, taskErr := adaptor.ParseResponse(ctx, &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, info)
			require.Nil(t, taskErr)
			require.NotNil(t, response.Immediate)
			task := &model.Task{TaskID: "public-1", Properties: model.Properties{OriginModelName: "m", UpstreamModelName: "m"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream-1"}}
			result, err := adaptor.ParseTaskResult(task, nil, []byte(`{}`))
			require.NoError(t, err)
			assert.Equal(t, string(model.TaskStatusSuccess), result.Status)
			content, err := adaptor.BuildContentRequest(task, "video", channel.TaskArtifactClientRequest{Method: http.MethodGet})
			require.NoError(t, err)
			assert.Equal(t, "Bearer access-token", content.Headers["Authorization"])
			assert.Equal(t, []string{tc.want}, tokenProxies, "submit, immediate completion, polling, and artifacts must share the selected OAuth egress")
		})
	}
}

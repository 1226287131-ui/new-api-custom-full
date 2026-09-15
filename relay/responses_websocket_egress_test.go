package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesWebSocketUsesSelectedChannelEgress(t *testing.T) {
	requests := make(chan string, 3)
	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- "configured:" + r.Method + ":" + r.Host
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer configured.Close()
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- "dedicated:" + r.Method + ":" + r.Host
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer dedicated.Close()
	for _, tc := range []struct {
		name, egress, want string
		enabled            bool
	}{
		{name: "opted in", enabled: true, egress: dedicated.URL, want: "dedicated"},
		{name: "opted out", egress: dedicated.URL, want: "configured"},
		{name: "missing deployment egress", enabled: true, want: "configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("UPSTREAM_EGRESS_PROXY", tc.egress)
			info := &relaycommon.RelayInfo{
				RequestURLPath: "/v1/responses", RelayFormat: types.RelayFormatOpenAIResponses,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "http://provider.invalid", ApiKey: "test-key",
					ChannelSetting: dto.ChannelSettings{Proxy: configured.URL, UpstreamEgressProxyEnabled: tc.enabled},
				},
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			connection, apiErr := dialResponsesWebSocketUpstream(ctx, &openai.Adaptor{}, info)
			assert.Nil(t, connection)
			require.NotNil(t, apiErr, "the selected proxy deliberately rejects CONNECT")
			select {
			case request := <-requests:
				assert.Equal(t, tc.want+":CONNECT:provider.invalid:80", request)
			default:
				t.Fatal("the selected channel proxy did not receive the WebSocket connection")
			}
		})
	}
}

package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvancedCustomBalanceUsesSelectedChannelEgress(t *testing.T) {
	database := modelManagementDB(t, "sqlite", "")
	requests := make(chan string, 3)
	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- "configured:" + r.URL.String()
		_, _ = w.Write([]byte(`{"object":"credit_summary","total_available":12.5}`))
	}))
	defer configured.Close()
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- "dedicated:" + r.URL.String()
		_, _ = w.Write([]byte(`{"object":"credit_summary","total_available":12.5}`))
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
			channel := &model.Channel{Type: constant.ChannelTypeAdvancedCustom, Key: "test-key", BaseURL: common.GetPointer("http://provider.invalid")}
			channel.SetSetting(dto.ChannelSettings{Proxy: configured.URL, UpstreamEgressProxyEnabled: tc.enabled})
			channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
				Routes: []dto.AdvancedCustomRoute{{IncomingPath: dto.AdvancedCustomBalancePath, UpstreamPath: "/balance"}},
			}})
			require.NoError(t, database.Create(channel).Error)
			result, err := fetchAdvancedCustomBalance(channel)
			require.NoError(t, err)
			assert.Equal(t, 12.5, result.Balance)
			var stored model.Channel
			require.NoError(t, database.First(&stored, channel.Id).Error)
			assert.Equal(t, 12.5, stored.Balance)
			select {
			case request := <-requests:
				assert.Equal(t, tc.want+":http://provider.invalid/balance", request)
			default:
				t.Fatal("the selected channel proxy did not receive the balance request")
			}
		})
	}
}

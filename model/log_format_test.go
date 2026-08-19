package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// TestFormatUserLogsStripsQuotaSaturation verifies the admin-only quota
// saturation marker (nested under other.admin_info) is removed for non-admin
// log views, since formatUserLogs strips the whole admin_info object.
func TestFormatUserLogsStripsQuotaSaturation(t *testing.T) {
	other := common.MapToJsonStr(map[string]interface{}{
		"model_price": 0.004,
		"admin_info": map[string]interface{}{
			"quota_saturation": map[string]interface{}{
				"op":      "QuotaFromDecimal",
				"kind":    "overflow",
				"clamped": common.MaxQuota,
			},
		},
	})
	logs := []*Log{{Other: other}}

	formatUserLogs(logs, 0)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	_, hasAdminInfo := parsed["admin_info"]
	require.False(t, hasAdminInfo, "admin_info (and nested quota_saturation) must be stripped for non-admin views")
	// Non-admin billing fields remain visible.
	require.Contains(t, parsed, "model_price")
}

func TestSanitizeLogOtherStripsUpstreamModelForAdminAndUserViews(t *testing.T) {
	other := common.MapToJsonStr(map[string]interface{}{
		"model_price":        0.004,
		"is_model_mapped":    true,
		"upstream_model_name": "provider-video-v3",
		"admin_info": map[string]interface{}{
			"use_channel": []string{"channel-1"},
		},
	})
	log := &Log{Other: other}

	sanitizeLogOther(log, false)
	parsed, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	_, hasMapped := parsed["is_model_mapped"]
	_, hasUpstream := parsed["upstream_model_name"]
	require.False(t, hasMapped)
	require.False(t, hasUpstream)
	require.Contains(t, parsed, "admin_info")

	log.Other = other
	sanitizeLogOther(log, true)
	parsed, err = common.StrToMap(log.Other)
	require.NoError(t, err)
	_, hasAdmin := parsed["admin_info"]
	require.False(t, hasAdmin)
}

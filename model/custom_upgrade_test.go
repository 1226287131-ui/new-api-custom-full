package model

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run this same fixture against the pre-merge custom source and the latest
// released source (create), then against the merged source (verify). Only
// explicitly supplied, loopback-only scratch databases may be used.
func TestCustomUpgradePreservesStoredConfiguration(t *testing.T) {
	mode := os.Getenv("NEWAPI_UPGRADE_FIXTURE")
	if mode == "" {
		t.Skip("set NEWAPI_UPGRADE_FIXTURE=create or verify with isolated local databases")
	}
	require.Contains(t, []string{"create", "verify"}, mode)
	dsn := os.Getenv("SQL_DSN")
	require.True(t, dsn == "local" || strings.Contains(dsn, "127.0.0.1:"), "only isolated loopback databases are allowed")
	logDSN := os.Getenv("LOG_SQL_DSN")
	require.True(t, logDSN == "" || strings.Contains(logDSN, "127.0.0.1:"), "only isolated loopback log databases are allowed")
	savedDB, savedLogDB := DB, LOG_DB
	savedMainType, savedLogType := common.MainDatabaseType(), common.LogDatabaseType()
	savedSQLitePath := common.SQLitePath
	savedMaster, savedMemoryCache, savedRedis := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled
	t.Cleanup(func() {
		if DB != nil && DB != savedDB {
			if LOG_DB != nil && LOG_DB != DB && LOG_DB != savedLogDB {
				require.NoError(t, closeDB(LOG_DB))
			}
			require.NoError(t, closeDB(DB))
		}
		DB, LOG_DB = savedDB, savedLogDB
		common.SetMainDatabaseType(savedMainType)
		common.SetLogDatabaseType(savedLogType)
		common.SQLitePath = savedSQLitePath
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = savedMaster, savedMemoryCache, savedRedis
		initCol()
	})
	if dsn == "local" {
		common.SQLitePath = os.Getenv("NEWAPI_UPGRADE_SQLITE")
		require.NotEmpty(t, common.SQLitePath)
	}
	common.IsMasterNode = true
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	require.NoError(t, InitDB())
	require.NoError(t, InitLogDB())

	channelSettings := `{"openai_video_endpoint":"/v1/video/generations","openai_video_profile":"seedance-2.5","minimax_video_prompt_enhance":true,"video_cache_proxy_enabled":true,"upstream_egress_proxy_enabled":true}`
	privateData := `{"upstream_task_id":"provider-private-fixture","video_cached_at":1234567890,"upstream_result_url":"https://upstream.invalid/result.mp4","billing_context":{"model_price":0.12,"group_ratio":0.07}}`
	options := map[string]string{
		"ImageResolutionPrice":                 `{"custom-image":{"1K":0.01,"2K":0.02,"4K":0.04}}`,
		"billing_setting.task_billing_pricing": `{"custom-video":{"mode":"per-second","resolution_prices":{"720p":0.01,"768p":0.02,"1080p":0.03,"2k":0.04,"4k":0.08}}}`,
		"billing_setting.scheduled_discount":   `{"custom-video":{"enabled":true,"discount":0.5,"start":"01:00","end":"05:00"}}`,
	}
	if mode == "create" {
		require.NoError(t, DB.Create(&User{Id: 701, Username: "upgrade-fixture", Password: "fixture-hash", Group: "default", Quota: 456789, UsedQuota: 12345, Status: 1}).Error)
		require.NoError(t, DB.Create(&Token{Id: 702, UserId: 701, Key: "upgrade-fixture-token-not-a-real-credential", Name: "two-groups", Group: "default,vip", RemainQuota: 345678, Status: 1}).Error)
		for _, channelType := range []int{59, 60, 61, 62, 63, 64} {
			require.NoError(t, DB.Create(&Channel{Id: 800 + channelType, Type: channelType, Key: "fixture-not-a-real-credential", Name: "upgrade-channel", Models: "custom-video", Group: "default", Status: 1, Setting: &channelSettings}).Error)
		}
		for key, value := range options {
			require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error)
		}
		require.NoError(t, DB.Table("tasks").Create(&map[string]any{"task_id": "task_upgrade_fixture", "platform": "60", "user_id": 701, "channel_id": 860, "status": "IN_PROGRESS", "quota": 120, "private_data": privateData, "data": `{"status":"processing"}`}).Error)
		require.NoError(t, LOG_DB.Create(&Log{UserId: 701, Type: LogTypeConsume, ModelName: "custom-video", Quota: 120, TokenName: "two-groups", Group: "default"}).Error)
		if os.Getenv("NEWAPI_UPGRADE_SOURCE") == "custom" || os.Getenv("NEWAPI_UPGRADE_SOURCE") == "merged" {
			require.NoError(t, DB.Table("group_user_ratios").Create(&map[string]any{"user_id": 701, "group": "default", "ratio": 0.07}).Error)
		}
		return
	}
	// A second migration must preserve every seeded value and uniqueness guard.
	require.NoError(t, migrateDB())
	require.NoError(t, migrateLOGDB())
	var user User
	require.NoError(t, DB.First(&user, 701).Error)
	assert.EqualValues(t, 456789, user.Quota)
	assert.EqualValues(t, 12345, user.UsedQuota)
	var token Token
	require.NoError(t, DB.First(&token, 702).Error)
	assert.Equal(t, "default,vip", token.Group)
	assert.Equal(t, 345678, token.RemainQuota)
	for _, channelType := range []int{59, 60, 61, 62, 63, 64} {
		var channel Channel
		require.NoError(t, DB.First(&channel, 800+channelType).Error)
		assert.Equal(t, channelType, channel.Type)
		require.NotNil(t, channel.Setting)
		assert.JSONEq(t, channelSettings, *channel.Setting)
	}
	for key, value := range options {
		var option Option
		require.NoError(t, DB.Where(&Option{Key: key}).First(&option).Error)
		assert.JSONEq(t, value, option.Value)
	}
	var stored struct {
		PrivateData string
		Quota       int
		Status      string
	}
	require.NoError(t, DB.Table("tasks").Where("task_id = ?", "task_upgrade_fixture").Take(&stored).Error)
	assert.JSONEq(t, privateData, stored.PrivateData)
	assert.Equal(t, "IN_PROGRESS", stored.Status)
	assert.Equal(t, 120, stored.Quota)
	var logCount int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("user_id = ?", 701).Count(&logCount).Error)
	assert.EqualValues(t, 1, logCount)
	if os.Getenv("NEWAPI_UPGRADE_SOURCE") == "custom" || os.Getenv("NEWAPI_UPGRADE_SOURCE") == "merged" {
		var row struct{ Ratio float64 }
		require.NoError(t, DB.Table("group_user_ratios").Where("user_id = ?", 701).Take(&row).Error)
		assert.InDelta(t, 0.07, row.Ratio, 1e-12)
		assert.Error(t, DB.Table("group_user_ratios").Create(&map[string]any{"user_id": 701, "group": "default", "ratio": 0.2}).Error)
	}
	assert.Error(t, DB.Create(&Token{UserId: 701, Key: token.Key, Name: "duplicate"}).Error)
}

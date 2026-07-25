package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetTodayChannelUsageAggregatesConsumeLogsByChannel(t *testing.T) {
	truncateTables(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
	now := time.Now()
	createdAt := now.Unix()
	startOfDay := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		now.Location(),
	)
	require.NoError(t, DB.Create(&Channel{Id: 1, Name: "primary"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 2, Name: "backup"}).Error)
	require.NoError(t, DB.Create(&Log{
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		ChannelId:        1,
		Quota:            100,
		PromptTokens:     60,
		CompletionTokens: 40,
	}).Error)
	require.NoError(t, DB.Create(&Log{
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		ChannelId:        1,
		Quota:            25,
		PromptTokens:     10,
		CompletionTokens: 5,
	}).Error)
	require.NoError(t, DB.Create(&Log{
		CreatedAt:        createdAt,
		Type:             LogTypeConsume,
		ChannelId:        2,
		Quota:            50,
		PromptTokens:     20,
		CompletionTokens: 10,
	}).Error)
	require.NoError(t, DB.Create(&Log{
		CreatedAt:        startOfDay.Unix() - 1,
		Type:             LogTypeConsume,
		ChannelId:        1,
		Quota:            999,
	}).Error)
	require.NoError(t, DB.Create(&Log{
		CreatedAt:        createdAt,
		Type:             LogTypeError,
		ChannelId:        1,
		Quota:            500,
	}).Error)

	usage, err := GetTodayChannelUsage()
	require.NoError(t, err)
	require.Equal(t, now.Format("2006-01-02"), usage.Date)
	require.Equal(t, int64(3), usage.TotalCount)
	require.Equal(t, int64(175), usage.TotalQuota)
	require.Equal(t, int64(135), usage.TotalTokenUsed)
	require.Len(t, usage.Channels, 2)
	require.Equal(t, "primary", usage.Channels[0].ChannelName)
	require.Equal(t, int64(125), usage.Channels[0].Quota)
	require.Equal(t, int64(2), usage.Channels[0].Count)
	require.Equal(t, "backup", usage.Channels[1].ChannelName)
	require.Equal(t, int64(50), usage.Channels[1].Quota)
}

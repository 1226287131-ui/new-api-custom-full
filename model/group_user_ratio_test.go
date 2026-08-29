package model

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ensureGroupUserRatioTable(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&GroupUserRatio{}))
	t.Cleanup(func() {
		DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&GroupUserRatio{})
		DB.Unscoped().Delete(&User{}, 100901)
	})
}

func TestValidateGroupUserRatio(t *testing.T) {
	for _, value := range []float64{-0.01, math.NaN(), math.Inf(1), math.Inf(-1), MaxGroupUserRatio + 0.01} {
		assert.Error(t, ValidateGroupUserRatio(value))
	}

	for _, value := range []float64{0, 0.5, 1, MaxGroupUserRatio} {
		assert.NoError(t, ValidateGroupUserRatio(value))
	}
}

func TestGroupUserRatioCRUDAndUpsert(t *testing.T) {
	ensureGroupUserRatioTable(t)

	const userID = 100901
	invalidateGroupUserRatioCache(userID, "vip")
	t.Cleanup(func() { invalidateGroupUserRatioCache(userID, "vip") })
	require.NoError(t, DB.Create(&User{
		Id:          userID,
		Username:    "ratio-user",
		DisplayName: "Ratio User",
		Email:       "ratio-user@example.com",
		Password:    "password",
	}).Error)

	entry, err := UpsertGroupUserRatio(userID, "vip", 0.35)
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, userID, entry.UserID)
	assert.Equal(t, "vip", entry.Group)
	assert.Equal(t, 0.35, entry.Ratio)
	assert.Equal(t, "ratio-user@example.com", entry.Email)

	ratio, configured, err := GetGroupUserRatio(userID, "vip")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, 0.35, ratio)

	updated, err := UpsertGroupUserRatio(userID, "vip", 0.6)
	require.NoError(t, err)
	assert.Equal(t, 0.6, updated.Ratio)

	ratio, configured, err = GetGroupUserRatio(userID, "vip")
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, 0.6, ratio)

	entries, err := ListGroupUserRatios("vip")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 0.6, entries[0].Ratio)

	require.NoError(t, DeleteGroupUserRatio("vip", userID))
	_, configured, err = GetGroupUserRatio(userID, "vip")
	require.NoError(t, err)
	assert.False(t, configured)
}

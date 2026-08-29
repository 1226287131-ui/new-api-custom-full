package model

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxGroupUserRatio = 1000

const groupUserRatioCacheNamespace = "new-api:group_user_ratio:v1"

type groupUserRatioCacheValue struct {
	Ratio      float64 `json:"ratio"`
	Configured bool    `json:"configured"`
}

var (
	groupUserRatioCacheOnce sync.Once
	groupUserRatioCache     *cachex.HybridCache[groupUserRatioCacheValue]
)

func getGroupUserRatioCache() *cachex.HybridCache[groupUserRatioCacheValue] {
	groupUserRatioCacheOnce.Do(func() {
		groupUserRatioCache = cachex.NewHybridCache(cachex.HybridCacheConfig[groupUserRatioCacheValue]{
			Namespace: cachex.Namespace(groupUserRatioCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[groupUserRatioCacheValue]{},
			Memory: func() *hot.HotCache[string, groupUserRatioCacheValue] {
				return hot.NewHotCache[string, groupUserRatioCacheValue](hot.LRU, 10000).
					WithTTL(time.Minute).
					WithJanitor().
					Build()
			},
		})
	})
	return groupUserRatioCache
}

func groupUserRatioCacheKey(userID int, group string) string {
	if userID <= 0 || group == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s", userID, group)
}

func invalidateGroupUserRatioCache(userID int, group string) {
	key := groupUserRatioCacheKey(userID, group)
	if key == "" {
		return
	}
	if _, err := getGroupUserRatioCache().DeleteMany([]string{key}); err != nil {
		common.SysError("failed to invalidate user-specific group ratio cache: " + err.Error())
	}
}

// GroupUserRatio overrides the billing ratio for one user in one billing group.
// The composite unique index prevents duplicate rules when multiple admins edit
// the same user concurrently.
type GroupUserRatio struct {
	ID        int     `json:"id" gorm:"primaryKey"`
	UserID    int     `json:"user_id" gorm:"not null;uniqueIndex:idx_group_user_ratio_user_group,priority:1;index"`
	Group     string  `json:"group" gorm:"type:varchar(64);not null;uniqueIndex:idx_group_user_ratio_user_group,priority:2"`
	Ratio     float64 `json:"ratio" gorm:"not null"`
	CreatedAt int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64   `json:"updated_at" gorm:"autoUpdateTime"`
}

type GroupUserRatioView struct {
	GroupUserRatio
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

func ValidateGroupUserRatio(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > MaxGroupUserRatio {
		return errors.New("ratio must be a finite number between 0 and 1000")
	}
	return nil
}

func ListGroupUserRatios(group string) ([]GroupUserRatioView, error) {
	var entries []GroupUserRatioView
	err := DB.Model(&GroupUserRatio{}).
		Select("group_user_ratios.*, users.username, users.email, users.display_name").
		Joins("JOIN users ON users.id = group_user_ratios.user_id AND users.deleted_at IS NULL").
		Where(&GroupUserRatio{Group: group}).
		Order("group_user_ratios.id ASC").
		Scan(&entries).Error
	return entries, err
}

func UpsertGroupUserRatio(userID int, group string, ratio float64) (*GroupUserRatioView, error) {
	if userID <= 0 {
		return nil, errors.New("user_id must be positive")
	}
	if err := ValidateGroupUserRatio(ratio); err != nil {
		return nil, err
	}
	entry := &GroupUserRatio{UserID: userID, Group: group, Ratio: ratio}
	err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "group"}},
		DoUpdates: clause.AssignmentColumns([]string{"ratio", "updated_at"}),
	}).Create(entry).Error
	if err != nil {
		return nil, err
	}
	invalidateGroupUserRatioCache(userID, group)
	var view GroupUserRatioView
	err = DB.Model(&GroupUserRatio{}).
		Select("group_user_ratios.*, users.username, users.email, users.display_name").
		Joins("JOIN users ON users.id = group_user_ratios.user_id AND users.deleted_at IS NULL").
		Where(&GroupUserRatio{UserID: userID, Group: group}).
		First(&view).Error
	return &view, err
}

func DeleteGroupUserRatio(group string, userID int) error {
	result := DB.Where(&GroupUserRatio{Group: group, UserID: userID}).Delete(&GroupUserRatio{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	invalidateGroupUserRatioCache(userID, group)
	return nil
}

func GetGroupUserRatio(userID int, group string) (float64, bool, error) {
	// Some pure relay tests run without a database initialized. Treat that as
	// no override; production starts the database before handling requests.
	if DB == nil || userID <= 0 || group == "" {
		return 0, false, nil
	}
	cacheKey := groupUserRatioCacheKey(userID, group)
	cache := getGroupUserRatioCache()
	if cached, found, err := cache.Get(cacheKey); err != nil {
		common.SysError("failed to load user-specific group ratio cache: " + err.Error())
	} else if found {
		return cached.Ratio, cached.Configured, nil
	}
	var entry GroupUserRatio
	err := DB.Where(&GroupUserRatio{UserID: userID, Group: group}).First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if cacheErr := cache.SetWithTTL(cacheKey, groupUserRatioCacheValue{}, time.Minute); cacheErr != nil {
			common.SysError("failed to cache missing user-specific group ratio: " + cacheErr.Error())
		}
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if cacheErr := cache.SetWithTTL(cacheKey, groupUserRatioCacheValue{Ratio: entry.Ratio, Configured: true}, time.Minute); cacheErr != nil {
		common.SysError("failed to cache user-specific group ratio: " + cacheErr.Error())
	}
	return entry.Ratio, true, nil
}

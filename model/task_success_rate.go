package model

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// TaskOutcomeCounts counts accepted tasks, not HTTP submission attempts.
// Pending tasks are excluded from the success-rate denominator.
type TaskOutcomeCounts struct {
	Total   int64 `json:"total"`
	Success int64 `json:"success"`
	Failure int64 `json:"failure"`
	Pending int64 `json:"pending"`
}

// TaskSuccessRateSnapshot is shared internally. Callers must filter models and
// groups for the viewer and must not mutate the cached maps.
type TaskSuccessRateSnapshot struct {
	WindowStart int64
	WindowEnd   int64
	Models      map[string]map[string]TaskOutcomeCounts
}

type cachedTaskSuccessRates struct {
	database  *gorm.DB
	expiresAt time.Time
	snapshot  *TaskSuccessRateSnapshot
	err       error
}

var (
	taskSuccessRatesCache   atomic.Pointer[cachedTaskSuccessRates]
	taskSuccessRatesRefresh singleflight.Group
)

// QueryTaskSuccessRates uses the indexed submission window and aggregates in
// the primary database. Only the two client-model identity fields are read;
// provider model names, task payloads and credentials never enter the result.
func QueryTaskSuccessRates(ctx context.Context, start, end int64) (*TaskSuccessRateSnapshot, error) {
	queryContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	origin := "json_extract(properties, '$.origin_model_name')"
	legacyOrigin := "json_extract(private_data, '$.billing_context.origin_model_name')"
	switch {
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		origin = "CASE WHEN JSON_TYPE(JSON_EXTRACT(properties, '$.origin_model_name')) = 'STRING' THEN JSON_UNQUOTE(JSON_EXTRACT(properties, '$.origin_model_name')) END"
		legacyOrigin = "CASE WHEN JSON_TYPE(JSON_EXTRACT(private_data, '$.billing_context.origin_model_name')) = 'STRING' THEN JSON_UNQUOTE(JSON_EXTRACT(private_data, '$.billing_context.origin_model_name')) END"
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		origin = "properties->>'origin_model_name'"
		legacyOrigin = "private_data->'billing_context'->>'origin_model_name'"
	}
	identity := fmt.Sprintf("COALESCE(NULLIF(%s, ''), NULLIF(%s, ''), '')", origin, legacyOrigin)
	var rows []struct {
		ModelName string
		Group     string
		Status    TaskStatus
		Count     int64
	}
	err := DB.WithContext(queryContext).Model(&Task{}).
		Select(identity+" AS model_name, "+commonGroupCol+", status, COUNT(*) AS count").
		Where("submit_time >= ? AND submit_time <= ?", start, end).
		Group("model_name, " + commonGroupCol + ", status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := &TaskSuccessRateSnapshot{WindowStart: start, WindowEnd: end, Models: make(map[string]map[string]TaskOutcomeCounts)}
	for _, row := range rows {
		if row.ModelName == "" {
			continue
		}
		groups := result.Models[row.ModelName]
		if groups == nil {
			groups = make(map[string]TaskOutcomeCounts)
			result.Models[row.ModelName] = groups
		}
		counts := groups[row.Group]
		counts.Total += row.Count
		switch row.Status {
		case TaskStatusSuccess:
			counts.Success += row.Count
		case TaskStatusFailure:
			counts.Failure += row.Count
		default:
			counts.Pending += row.Count
		}
		groups[row.Group] = counts
	}
	return result, nil
}

// GetTaskSuccessRates shares one bounded refresh across all viewers. A caller
// disconnecting stops its wait without cancelling other viewers' refresh.
func GetTaskSuccessRates(ctx context.Context) (*TaskSuccessRateSnapshot, error) {
	database := DB
	if cached := taskSuccessRatesCache.Load(); cached != nil && cached.database == database && time.Now().Before(cached.expiresAt) {
		return cached.snapshot, cached.err
	}
	refresh := taskSuccessRatesRefresh.DoChan(fmt.Sprintf("%p", database), func() (any, error) {
		if cached := taskSuccessRatesCache.Load(); cached != nil && cached.database == database && time.Now().Before(cached.expiresAt) {
			return cached.snapshot, cached.err
		}
		now := time.Now().Unix()
		snapshot, err := QueryTaskSuccessRates(context.WithoutCancel(ctx), now-3600, now)
		if err != nil {
			common.SysError("failed to refresh task success rates: " + err.Error())
			taskSuccessRatesCache.Store(&cachedTaskSuccessRates{database: database, expiresAt: time.Now().Add(5 * time.Second), err: err})
			return nil, err
		}
		taskSuccessRatesCache.Store(&cachedTaskSuccessRates{database: database, expiresAt: time.Now().Add(30 * time.Second), snapshot: snapshot})
		return snapshot, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-refresh:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(*TaskSuccessRateSnapshot), nil
	}
}

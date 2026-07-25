/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package model

import (
	"fmt"
	"time"
)

type ChannelUsage struct {
	ChannelID   int    `json:"channel_id" gorm:"column:channel_id"`
	ChannelName string `json:"channel_name" gorm:"-"`
	Count       int64  `json:"count" gorm:"column:count"`
	Quota       int64  `json:"quota" gorm:"column:quota"`
	TokenUsed   int64  `json:"token_used" gorm:"column:token_used"`
}

type TodayChannelUsage struct {
	Date           string          `json:"date"`
	StartTimestamp int64           `json:"start_timestamp"`
	UpdatedAt      int64           `json:"updated_at"`
	TotalCount     int64           `json:"total_count"`
	TotalQuota     int64           `json:"total_quota"`
	TotalTokenUsed int64           `json:"total_token_used"`
	Channels       []*ChannelUsage `json:"channels"`
}

// GetTodayChannelUsage reads consume logs directly so the dashboard does not
// wait for the periodic quota_data export task to flush its five-minute cache.
func GetTodayChannelUsage() (*TodayChannelUsage, error) {
	now := time.Now()
	startOfDay := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		now.Location(),
	)
	startTimestamp := startOfDay.Unix()
	updatedAt := now.Unix()

	channels := make([]*ChannelUsage, 0)
	err := LOG_DB.Table("logs").
		Select("channel_id, count(*) as count, COALESCE(sum(quota), 0) as quota, COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0) as token_used").
		Where("type = ? and created_at >= ? and created_at <= ?", LogTypeConsume, startTimestamp, updatedAt).
		Group("channel_id").
		Order("quota DESC").
		Find(&channels).Error
	if err != nil {
		return nil, err
	}

	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel.ChannelID > 0 {
			channelIDs = append(channelIDs, channel.ChannelID)
		}
	}
	channelNameByID, err := getChannelNames(channelIDs)
	if err != nil {
		return nil, err
	}

	result := &TodayChannelUsage{
		Date:           startOfDay.Format("2006-01-02"),
		StartTimestamp: startTimestamp,
		UpdatedAt:      updatedAt,
		Channels:       channels,
	}
	for _, channel := range channels {
		channel.ChannelName = channelNameByID[channel.ChannelID]
		if channel.ChannelName == "" {
			if channel.ChannelID > 0 {
				channel.ChannelName = fmt.Sprintf("channel-%d", channel.ChannelID)
			} else {
				channel.ChannelName = "unknown"
			}
		}
		result.TotalCount += channel.Count
		result.TotalQuota += channel.Quota
		result.TotalTokenUsed += channel.TokenUsed
	}
	return result, nil
}

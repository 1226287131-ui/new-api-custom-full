package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidAgentRewardPreference = errors.New("invalid team reward preference")

// AgentRewardPreference overrides only the default mode for future orders.
// It intentionally lives outside User so profile updates cannot overwrite it.
type AgentRewardPreference struct {
	UserID    int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Mode      string `json:"mode" gorm:"type:varchar(16)"`
	UpdatedAt int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
}

// An empty Mode means the user has not chosen and follows AgentPolicy.Mode.
func GetAgentRewardPreference(userID int) (*AgentRewardPreference, error) {
	if userID <= 0 {
		return nil, ErrInvalidAgentRewardPreference
	}
	preference := &AgentRewardPreference{UserID: userID}
	err := DB.First(preference, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return preference, nil
	}
	return preference, err
}

func SaveAgentRewardPreference(userID int, mode string) error {
	if userID <= 0 || (mode != AgentModeCredit && mode != AgentModeCash) {
		return ErrInvalidAgentRewardPreference
	}
	preference := &AgentRewardPreference{UserID: userID, Mode: mode, UpdatedAt: time.Now().Unix()}
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "status").First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrInvalidAgentRewardPreference
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"mode", "updated_at"}),
		}).Create(preference).Error
	})
}

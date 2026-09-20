package model

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAgentReferralInvalid   = errors.New("invalid team referral change")
	ErrAgentReferralForbidden = errors.New("cannot change referrals for this user role")
	ErrAgentReferralConflict  = errors.New("team referral changed; refresh before retrying")
	ErrAgentReferralCycle     = errors.New("team referral would create a cycle")
)

// AgentReferralGuard serializes hierarchy changes across application instances.
type AgentReferralGuard struct {
	ID       int `gorm:"primaryKey;autoIncrement:false"`
	Revision int64
}

// AgentReferralChange is retained in the primary database in the same
// transaction as the relationship change, independently of usage-log cleanup.
type AgentReferralChange struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	UserID           int    `json:"user_id" gorm:"index"`
	Username         string `json:"username" gorm:"type:varchar(64)"`
	OldInviterID     int    `json:"previous_inviter_id"`
	NewInviterID     int    `json:"inviter_id"`
	OperatorID       int    `json:"actor_id" gorm:"index"`
	OperatorUsername string `json:"operator_username" gorm:"type:varchar(64)"`
	OperatorRole     int    `json:"operator_role"`
	Reason           string `json:"reason" gorm:"type:varchar(500)"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime:false"`
}

type AgentReferral struct {
	UserID          int    `json:"user_id"`
	Username        string `json:"username"`
	InviterID       int    `json:"inviter_id"`
	InviterUsername string `json:"inviter_username"`
}

func GetAgentReferral(userID int) (*AgentReferral, error) {
	if userID <= 0 {
		return nil, ErrAgentReferralInvalid
	}
	var user User
	if err := DB.Select("id", "username", "inviter_id").First(&user, userID).Error; err != nil {
		return nil, err
	}
	referral := &AgentReferral{UserID: user.Id, Username: user.Username, InviterID: user.InviterId}
	if user.InviterId > 0 {
		var inviter User
		if err := DB.Select("id", "username").First(&inviter, user.InviterId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		referral.InviterUsername = inviter.Username
	}
	return referral, nil
}

// ChangeAgentReferrer affects only future order snapshots. Neither historical
// rewards nor the legacy signup-reward counters are rewritten.
func ChangeAgentReferrer(operatorID, userID, expectedInviterID, newInviterID int, reason string) (*AgentReferralChange, error) {
	reason = strings.TrimSpace(reason)
	if operatorID <= 0 || userID <= 0 || expectedInviterID < 0 || newInviterID < 0 ||
		newInviterID == userID || reason == "" || utf8.RuneCountInString(reason) > 200 {
		return nil, ErrAgentReferralInvalid
	}
	var change AgentReferralChange
	err := DB.Transaction(func(tx *gorm.DB) error {
		// The write happens before reading any hierarchy state. It also acquires
		// SQLite's write lock, where SELECT FOR UPDATE is unavailable.
		guard := AgentReferralGuard{ID: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&guard).Error; err != nil {
			return err
		}
		if err := tx.Model(&AgentReferralGuard{}).Where("id = ?", 1).
			UpdateColumn("revision", gorm.Expr("revision + 1")).Error; err != nil {
			return err
		}
		var operator, user User
		if err := lockForUpdate(tx).Select("id", "username", "role", "status").First(&operator, operatorID).Error; err != nil {
			return err
		}
		if operator.Role < common.RoleAdminUser || operator.Status != common.UserStatusEnabled {
			return ErrAgentReferralForbidden
		}
		if err := lockForUpdate(tx).Select("id", "username", "role", "inviter_id").First(&user, userID).Error; err != nil {
			return err
		}
		if operator.Role != common.RoleRootUser && operator.Role <= user.Role {
			return ErrAgentReferralForbidden
		}
		if user.InviterId != expectedInviterID {
			return ErrAgentReferralConflict
		}
		if user.InviterId == newInviterID {
			return ErrAgentReferralInvalid
		}
		visited := map[int]struct{}{userID: {}}
		for ancestorID := newInviterID; ancestorID > 0; {
			if _, exists := visited[ancestorID]; exists {
				return ErrAgentReferralCycle
			}
			visited[ancestorID] = struct{}{}
			var ancestor User
			if err := lockForUpdate(tx).Select("id", "inviter_id", "status").First(&ancestor, ancestorID).Error; err != nil {
				return err
			}
			if ancestorID == newInviterID && ancestor.Status != common.UserStatusEnabled {
				return ErrAgentReferralInvalid
			}
			ancestorID = ancestor.InviterId
		}
		result := tx.Model(&User{}).Where("id = ? AND inviter_id = ?", userID, expectedInviterID).UpdateColumn("inviter_id", newInviterID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAgentReferralConflict
		}
		change = AgentReferralChange{
			UserID: user.Id, Username: user.Username, OldInviterID: user.InviterId, NewInviterID: newInviterID,
			OperatorID: operator.Id, OperatorUsername: operator.Username, OperatorRole: operator.Role,
			Reason: reason, CreatedAt: time.Now().Unix(),
		}
		return tx.Create(&change).Error
	})
	if err != nil {
		return nil, err
	}
	return &change, nil
}

func GetAgentReferralAudits(userID int, pageInfo *common.PageInfo) ([]AgentReferralChange, int64, error) {
	query := DB.Model(&AgentReferralChange{})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var changes []AgentReferralChange
	err := query.Order("id desc").Offset(pageInfo.GetStartIdx()).Limit(pageInfo.GetPageSize()).Find(&changes).Error
	return changes, count, err
}

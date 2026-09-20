package model

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AgentWithdrawalPending  = "pending"
	AgentWithdrawalApproved = "approved"
	AgentWithdrawalPaid     = "paid"
	AgentWithdrawalRejected = "rejected"
	// Keep monetary values well inside both signed SQL BIGINT and exact JSON integers.
	MaxAgentCashCents int64 = 1_000_000_000_000
)

var (
	ErrAgentInsufficientCash          = errors.New("insufficient available commission balance")
	ErrAgentWithdrawalDisabled        = errors.New("agent withdrawals are disabled")
	ErrAgentWithdrawalState           = errors.New("withdrawal status does not allow this operation")
	ErrAgentWithdrawalRequestConflict = errors.New("withdrawal request id was already used with different parameters")
	ErrAgentPayoutAccountRequired     = errors.New("bind an Alipay payout account first")
	ErrAgentInvalidCashAmount         = errors.New("invalid commission cash amount")
)

type AgentWallet struct {
	UserID         int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	AvailableCents int64 `json:"available_cents" gorm:"not null"`
	FrozenCents    int64 `json:"frozen_cents" gorm:"not null"`
	PaidCents      int64 `json:"paid_cents" gorm:"not null"`
	UpdatedAt      int64 `json:"updated_at" gorm:"autoUpdateTime:false"`
}

type AgentPayoutAccount struct {
	UserID            int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	AccountCiphertext string `json:"-" gorm:"type:text;not null"`
	NameCiphertext    string `json:"-" gorm:"type:text;not null"`
	AccountMasked     string `json:"account_masked" gorm:"size:128;not null"`
	NameMasked        string `json:"name_masked" gorm:"size:128;not null"`
	UpdatedAt         int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
}

type AgentWithdrawal struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	UserID            int    `json:"user_id" gorm:"uniqueIndex:idx_agent_withdrawal_request,priority:1;index:idx_agent_withdrawal_user_created,priority:1;not null"`
	RequestID         string `json:"request_id" gorm:"size:64;uniqueIndex:idx_agent_withdrawal_request,priority:2;not null"`
	AmountCents       int64  `json:"amount_cents" gorm:"not null"`
	Status            string `json:"status" gorm:"size:16;index;not null"`
	AccountCiphertext string `json:"-" gorm:"type:text;not null"`
	NameCiphertext    string `json:"-" gorm:"type:text;not null"`
	AccountMasked     string `json:"account_masked" gorm:"size:128;not null"`
	NameMasked        string `json:"name_masked" gorm:"size:128;not null"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime:false;index:idx_agent_withdrawal_user_created,priority:2"`
	UpdatedAt         int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
	ReviewedBy        int    `json:"reviewed_by"`
	ReviewedAt        int64  `json:"reviewed_at"`
	PaidBy            int    `json:"paid_by"`
	PaidAt            int64  `json:"paid_at"`
	RejectedBy        int    `json:"rejected_by"`
	RejectedAt        int64  `json:"rejected_at"`
	PaymentReference  string `json:"payment_reference" gorm:"size:128"`
	RejectionReason   string `json:"rejection_reason" gorm:"size:512"`
}

type AgentWithdrawalDetail struct {
	AgentWithdrawal
	Account string `json:"account"`
	Name    string `json:"name"`
}

func GetAgentWallet(userID int) (*AgentWallet, error) {
	if userID <= 0 {
		return nil, errors.New("invalid user id")
	}
	wallet := &AgentWallet{UserID: userID}
	err := DB.Where("user_id = ?", userID).Take(wallet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return wallet, nil
	}
	return wallet, err
}

// The caller's commission event transaction supplies the idempotency boundary.
func creditAgentCash(tx *gorm.DB, userID int, cents int64) error {
	if tx == nil || userID <= 0 || cents <= 0 || cents > MaxAgentCashCents {
		return ErrAgentInvalidCashAmount
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentWallet{UserID: userID}).Error; err != nil {
		return err
	}
	result := tx.Model(&AgentWallet{}).Where("user_id = ? AND available_cents >= 0 AND available_cents <= ?", userID, MaxAgentCashCents-cents).Updates(map[string]any{
		"available_cents": gorm.Expr("available_cents + ?", cents), "updated_at": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentInvalidCashAmount
	}
	return nil
}

func GetAgentPayoutAccount(userID int) (*AgentPayoutAccount, error) {
	if userID <= 0 {
		return nil, errors.New("invalid user id")
	}
	var account AgentPayoutAccount
	err := DB.Select("user_id", "account_masked", "name_masked", "updated_at").Where("user_id = ?", userID).Take(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &account, err
}

func SaveAgentPayoutAccount(userID int, account, name string, authorization ...*AuthFlowAuthorization) (*AgentPayoutAccount, error) {
	account, name = strings.TrimSpace(account), strings.TrimSpace(name)
	if userID <= 0 || !utf8.ValidString(account) || !utf8.ValidString(name) || utf8.RuneCountInString(account) < 3 || utf8.RuneCountInString(account) > 128 || utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 128 {
		return nil, errors.New("invalid Alipay account or name")
	}
	if strings.IndexFunc(account+name, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid Alipay account or name")
	}
	accountCipher, err := common.EncryptAgentPayoutText(account, fmt.Sprintf("%d:account", userID))
	if err != nil {
		return nil, err
	}
	nameCipher, err := common.EncryptAgentPayoutText(name, fmt.Sprintf("%d:name", userID))
	if err != nil {
		return nil, err
	}
	accountRunes, nameRunes := []rune(account), []rune(name)
	maskedAccount := string(accountRunes[:1]) + "***" + string(accountRunes[len(accountRunes)-1:])
	if len(accountRunes) > 7 {
		maskedAccount = string(accountRunes[:3]) + "***" + string(accountRunes[len(accountRunes)-3:])
	}
	maskedName := string(nameRunes[:1]) + "***"
	record := &AgentPayoutAccount{UserID: userID, AccountCiphertext: accountCipher, NameCiphertext: nameCipher, AccountMasked: maskedAccount, NameMasked: maskedName, UpdatedAt: common.GetTimestamp()}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if len(authorization) > 0 {
			if err := tx.Model(&User{}).Where("id = ?", userID).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
				return err
			}
			if err := ValidateTeamAuthorizationWithTx(tx, authorization[0], userID, "team.payout.write"); err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"account_ciphertext", "name_ciphertext", "account_masked", "name_masked", "updated_at"})}).Create(record).Error
	})
	if err != nil {
		return nil, err
	}
	record.AccountCiphertext, record.NameCiphertext = "", ""
	return record, nil
}

func RequestAgentWithdrawal(userID int, cents int64, requestID string) (*AgentWithdrawal, error) {
	requestID = strings.TrimSpace(requestID)
	if userID <= 0 || len(requestID) < 8 || len(requestID) > 64 || strings.IndexFunc(requestID, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) >= 0 {
		return nil, errors.New("invalid withdrawal request id")
	}
	if cents <= 0 || cents > MaxAgentCashCents {
		return nil, ErrAgentInvalidCashAmount
	}
	policy, err := GetAgentPolicy()
	if err != nil {
		return nil, err
	}
	var record AgentWithdrawal
	err = DB.Transaction(func(tx *gorm.DB) error {
		var wallet AgentWallet
		if err := lockForUpdate(tx).Where("user_id = ?", userID).Take(&wallet).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentInsufficientCash
			}
			return err
		}
		err := tx.Where("user_id = ? AND request_id = ?", userID, requestID).Take(&record).Error
		if err == nil {
			if record.AmountCents != cents {
				return ErrAgentWithdrawalRequestConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if !policy.Enabled {
			return ErrAgentWithdrawalDisabled
		}
		if policy.MinimumWithdrawalCents <= 0 || cents < policy.MinimumWithdrawalCents {
			return ErrAgentInvalidCashAmount
		}
		var payout AgentPayoutAccount
		if err := tx.Where("user_id = ?", userID).Take(&payout).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentPayoutAccountRequired
			}
			return err
		}
		// Verify ciphertext before freezing money; a missing/rotated key must fail closed.
		if _, err := common.DecryptAgentPayoutText(payout.AccountCiphertext, fmt.Sprintf("%d:account", userID)); err != nil {
			return err
		}
		if _, err := common.DecryptAgentPayoutText(payout.NameCiphertext, fmt.Sprintf("%d:name", userID)); err != nil {
			return err
		}
		result := tx.Model(&AgentWallet{}).Where("user_id = ? AND available_cents >= ? AND frozen_cents >= 0 AND frozen_cents <= ?", userID, cents, MaxAgentCashCents-cents).Updates(map[string]any{
			"available_cents": gorm.Expr("available_cents - ?", cents), "frozen_cents": gorm.Expr("frozen_cents + ?", cents), "updated_at": common.GetTimestamp(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAgentInsufficientCash
		}
		now := common.GetTimestamp()
		record = AgentWithdrawal{UserID: userID, RequestID: requestID, AmountCents: cents, Status: AgentWithdrawalPending, AccountCiphertext: payout.AccountCiphertext, NameCiphertext: payout.NameCiphertext, AccountMasked: payout.AccountMasked, NameMasked: payout.NameMasked, CreatedAt: now, UpdatedAt: now}
		return tx.Create(&record).Error
	})
	if err != nil {
		return nil, err
	}
	record.AccountCiphertext, record.NameCiphertext = "", ""
	return &record, nil
}

func ListAgentWithdrawals(userID int, status string, offset, limit int) ([]AgentWithdrawal, int64, error) {
	if userID < 0 || offset < 0 || limit <= 0 || limit > 100 {
		return nil, 0, errors.New("invalid withdrawal pagination")
	}
	query := DB.Model(&AgentWithdrawal{})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if status != "" {
		if status != AgentWithdrawalPending && status != AgentWithdrawalApproved && status != AgentWithdrawalPaid && status != AgentWithdrawalRejected {
			return nil, 0, errors.New("invalid withdrawal status")
		}
		query = query.Where("status = ?", status)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var records []AgentWithdrawal
	err := query.Omit("account_ciphertext", "name_ciphertext").Order("id DESC").Offset(offset).Limit(limit).Find(&records).Error
	return records, count, err
}

func GetAgentWithdrawalForAdmin(id int64) (*AgentWithdrawalDetail, error) {
	if id <= 0 {
		return nil, errors.New("invalid withdrawal id")
	}
	var record AgentWithdrawal
	if err := DB.First(&record, id).Error; err != nil {
		return nil, err
	}
	account, err := common.DecryptAgentPayoutText(record.AccountCiphertext, fmt.Sprintf("%d:account", record.UserID))
	if err != nil {
		return nil, err
	}
	name, err := common.DecryptAgentPayoutText(record.NameCiphertext, fmt.Sprintf("%d:name", record.UserID))
	if err != nil {
		return nil, err
	}
	record.AccountCiphertext, record.NameCiphertext = "", ""
	return &AgentWithdrawalDetail{AgentWithdrawal: record, Account: account, Name: name}, nil
}

// action is approve, paid, or reject. Repeated identical actions are idempotent.
func ReviewAgentWithdrawal(id int64, adminID int, action, paymentReference, reason string) (*AgentWithdrawal, error) {
	paymentReference, reason = strings.TrimSpace(paymentReference), strings.TrimSpace(reason)
	if id <= 0 || adminID <= 0 || len(paymentReference) > 128 || len(reason) > 512 || strings.IndexFunc(paymentReference+reason, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid withdrawal review")
	}
	if action != "approve" && action != "paid" && action != "reject" {
		return nil, errors.New("invalid withdrawal action")
	}
	if action == "paid" && paymentReference == "" {
		return nil, errors.New("payment reference is required")
	}
	if action == "reject" && reason == "" {
		return nil, errors.New("rejection reason is required")
	}
	var record AgentWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		// All withdrawal operations lock wallet before withdrawal to avoid lock-order cycles.
		var owner AgentWithdrawal
		if err := tx.Select("user_id").First(&owner, id).Error; err != nil {
			return err
		}
		var wallet AgentWallet
		if err := lockForUpdate(tx).Where("user_id = ?", owner.UserID).Take(&wallet).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&record, id).Error; err != nil {
			return err
		}
		if record.AmountCents <= 0 || record.AmountCents > MaxAgentCashCents {
			return ErrAgentInvalidCashAmount
		}
		now := common.GetTimestamp()
		previousStatus := record.Status
		updates := map[string]any{"updated_at": now}
		switch action {
		case "approve":
			if record.Status == AgentWithdrawalApproved {
				return nil
			}
			if record.Status != AgentWithdrawalPending {
				return ErrAgentWithdrawalState
			}
			record.Status, record.ReviewedBy, record.ReviewedAt = AgentWithdrawalApproved, adminID, now
			updates["status"], updates["reviewed_by"], updates["reviewed_at"] = record.Status, adminID, now
		case "paid":
			if record.Status == AgentWithdrawalPaid {
				if record.PaymentReference != paymentReference {
					return ErrAgentWithdrawalRequestConflict
				}
				return nil
			}
			if record.Status != AgentWithdrawalApproved {
				return ErrAgentWithdrawalState
			}
			result := tx.Model(&AgentWallet{}).Where("user_id = ? AND frozen_cents >= ? AND paid_cents >= 0 AND paid_cents <= ?", record.UserID, record.AmountCents, MaxAgentCashCents-record.AmountCents).Updates(map[string]any{"frozen_cents": gorm.Expr("frozen_cents - ?", record.AmountCents), "paid_cents": gorm.Expr("paid_cents + ?", record.AmountCents), "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrAgentInvalidCashAmount
			}
			record.Status, record.PaymentReference, record.PaidBy, record.PaidAt = AgentWithdrawalPaid, paymentReference, adminID, now
			updates["status"], updates["payment_reference"], updates["paid_by"], updates["paid_at"] = record.Status, paymentReference, adminID, now
		case "reject":
			if record.Status == AgentWithdrawalRejected {
				if record.RejectionReason != reason {
					return ErrAgentWithdrawalRequestConflict
				}
				return nil
			}
			if record.Status != AgentWithdrawalPending && record.Status != AgentWithdrawalApproved {
				return ErrAgentWithdrawalState
			}
			result := tx.Model(&AgentWallet{}).Where("user_id = ? AND frozen_cents >= ? AND available_cents >= 0 AND available_cents <= ?", record.UserID, record.AmountCents, MaxAgentCashCents-record.AmountCents).Updates(map[string]any{"frozen_cents": gorm.Expr("frozen_cents - ?", record.AmountCents), "available_cents": gorm.Expr("available_cents + ?", record.AmountCents), "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrAgentInvalidCashAmount
			}
			record.Status, record.RejectionReason, record.RejectedBy, record.RejectedAt = AgentWithdrawalRejected, reason, adminID, now
			updates["status"], updates["rejection_reason"], updates["rejected_by"], updates["rejected_at"] = record.Status, reason, adminID, now
		}
		result := tx.Model(&AgentWithdrawal{}).Where("id = ? AND status = ?", id, previousStatus).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAgentWithdrawalState
		}
		record.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	record.AccountCiphertext, record.NameCiphertext = "", ""
	return &record, nil
}

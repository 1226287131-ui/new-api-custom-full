package model

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AgentModeCredit               = "credit"
	AgentModeCash                 = "cash"
	AgentCommissionPending        = "pending"
	AgentCommissionSettled        = "settled"
	maxAgentPaymentCents    int64 = 100000000000
	maxUserQuotaCreditTotal int64 = 1<<53 - 1
)

var ErrEpayQuotaCapacity = errors.New("recharge exceeds the remaining quota balance capacity")

// AgentPolicy applies only to newly created, verified Epay CNY recharge orders.
type AgentPolicy struct {
	ID                     int    `json:"id" gorm:"primaryKey"`
	Enabled                bool   `json:"enabled"`
	Mode                   string `json:"mode" gorm:"type:varchar(16)"`
	CreditRateBPS          int    `json:"credit_rate_bps"`
	CashRateBPS            int    `json:"cash_rate_bps"`
	FreezeHours            int    `json:"freeze_hours"`
	MinimumWithdrawalCents int64  `json:"minimum_withdrawal_cents"`
	UpdatedAt              int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
}

func GetAgentPolicy() (*AgentPolicy, error) {
	policy := &AgentPolicy{}
	err := DB.First(policy, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &AgentPolicy{ID: 1, Mode: AgentModeCredit, CreditRateBPS: 500, CashRateBPS: 500, FreezeHours: 24, MinimumWithdrawalCents: 1000}, nil
	}
	return policy, err
}

func SaveAgentPolicy(policy *AgentPolicy) error {
	if policy == nil || (policy.Mode != AgentModeCredit && policy.Mode != AgentModeCash) ||
		policy.CreditRateBPS < 0 || policy.CreditRateBPS > 10000 ||
		policy.CashRateBPS < 0 || policy.CashRateBPS > 10000 ||
		policy.FreezeHours < 0 || policy.FreezeHours > 24*365 ||
		policy.MinimumWithdrawalCents < 1 || policy.MinimumWithdrawalCents > maxAgentPaymentCents {
		return errors.New("invalid agent policy")
	}
	policy.ID = 1
	policy.UpdatedAt = time.Now().Unix()
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "mode", "credit_rate_bps", "cash_rate_bps", "freeze_hours", "minimum_withdrawal_cents", "updated_at"}),
	}).Create(policy).Error
}

type agentOrderSnapshot struct {
	Version      int    `json:"version"`
	UserID       int    `json:"user_id"`
	ReferrerID   int    `json:"referrer_id"`
	Mode         string `json:"mode"`
	RateBPS      int    `json:"rate_bps"`
	Price        string `json:"price"`
	QuotaPerUnit string `json:"quota_per_unit"`
	FreezeHours  int    `json:"freeze_hours"`
}

type AgentCommission struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	TopUpID       int    `json:"top_up_id" gorm:"uniqueIndex"`
	TradeNo       string `json:"trade_no" gorm:"type:varchar(255);uniqueIndex"`
	UserID        int    `json:"user_id" gorm:"index"`
	ReferrerID    int    `json:"referrer_id" gorm:"index"`
	Mode          string `json:"mode" gorm:"type:varchar(16)"`
	RateBPS       int    `json:"rate_bps"`
	PaidCents     int64  `json:"paid_cents"`
	CreditQuota   int    `json:"credit_quota"`
	CashCents     int64  `json:"cash_cents"`
	Price         string `json:"price" gorm:"type:varchar(64)"`
	QuotaPerUnit  string `json:"quota_per_unit" gorm:"type:varchar(64)"`
	Status        string `json:"status" gorm:"type:varchar(16);index:idx_agent_commission_due,priority:1"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime:false"`
	ReadyAt       int64  `json:"ready_at" gorm:"index:idx_agent_commission_due,priority:2"`
	SettledAt     int64  `json:"settled_at"`
	Attempts      int    `json:"attempts"`
	NextAttemptAt int64  `json:"next_attempt_at"`
	LastError     string `json:"last_error,omitempty" gorm:"type:varchar(500)"`
}

var epayMoneyPattern = regexp.MustCompile(`^[0-9]{1,10}(\.[0-9]{1,2})?$`)

// ParseEpayPaidCents never accepts fractional cents, exponents or signed values.
func ParseEpayPaidCents(value string) (int64, error) {
	if !epayMoneyPattern.MatchString(value) {
		return 0, errors.New("invalid Epay payment amount")
	}
	money, err := decimal.NewFromString(value)
	if err != nil {
		return 0, errors.New("invalid Epay payment amount")
	}
	cents := money.Mul(decimal.NewFromInt(100))
	if !cents.IsPositive() || cents.GreaterThan(decimal.NewFromInt(maxAgentPaymentCents)) {
		return 0, errors.New("Epay payment amount out of range")
	}
	return cents.IntPart(), nil
}

// SnapshotEpayAgentPolicy is called explicitly by the online order creator,
// never by generic TopUp.Insert or any balance adjustment path.
func SnapshotEpayAgentPolicy(topUp *TopUp, price, quotaPerUnit float64) error {
	if topUp == nil || topUp.PaymentProvider != PaymentProviderEpay ||
		math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 ||
		math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) || quotaPerUnit <= 0 {
		return errors.New("invalid Epay order conversion")
	}
	topUp.AgentSnapshot = ""
	cents, err := ParseEpayPaidCents(strconv.FormatFloat(topUp.Money, 'f', 2, 64))
	if err != nil {
		return err
	}
	quota, err := epayTopUpQuota(topUp.Amount, decimal.NewFromFloat(quotaPerUnit))
	if err != nil {
		return err
	}
	var user User
	if err := DB.Select("id", "inviter_id", "quota", "quota_credit_total").First(&user, topUp.UserId).Error; err != nil {
		return err
	}
	if user.Quota > common.MaxWalletQuota-quota || user.QuotaCreditTotal > maxUserQuotaCreditTotal-int64(quota) {
		return ErrEpayQuotaCapacity
	}
	topUp.ExpectedPaidCents = cents
	topUp.EpayQuotaPerUnit = decimal.NewFromFloat(quotaPerUnit).String()
	policy, err := GetAgentPolicy()
	if err != nil {
		return err
	}
	if !policy.Enabled {
		return nil
	}
	if user.InviterId <= 0 || user.InviterId == user.Id {
		return nil
	}
	var referrer User
	if err := DB.Select("id").First(&referrer, user.InviterId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	rate := policy.CreditRateBPS
	if policy.Mode == AgentModeCash {
		rate = policy.CashRateBPS
	}
	snapshot := agentOrderSnapshot{
		Version: 1, UserID: user.Id, ReferrerID: user.InviterId, Mode: policy.Mode,
		RateBPS: rate, Price: decimal.NewFromFloat(price).String(),
		QuotaPerUnit: topUp.EpayQuotaPerUnit, FreezeHours: policy.FreezeHours,
	}
	if _, _, err := calculateAgentCommission(snapshot, cents); err != nil {
		return err
	}
	encoded, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	topUp.AgentSnapshot = string(encoded)
	return nil
}

func calculateAgentCommission(snapshot agentOrderSnapshot, cents int64) (int, int64, error) {
	if snapshot.Version != 1 || snapshot.UserID <= 0 || snapshot.ReferrerID <= 0 || snapshot.UserID == snapshot.ReferrerID ||
		snapshot.RateBPS < 0 || snapshot.RateBPS > 10000 || cents <= 0 || cents > maxAgentPaymentCents ||
		snapshot.FreezeHours < 0 || snapshot.FreezeHours > 24*365 {
		return 0, 0, errors.New("invalid agent commission snapshot")
	}
	paid := decimal.NewFromInt(cents)
	rebateCents := paid.Mul(decimal.NewFromInt(int64(snapshot.RateBPS))).Div(decimal.NewFromInt(10000))
	if snapshot.Mode == AgentModeCash {
		return 0, rebateCents.Round(0).IntPart(), nil
	}
	if snapshot.Mode != AgentModeCredit {
		return 0, 0, errors.New("invalid agent commission mode")
	}
	price, err := decimal.NewFromString(snapshot.Price)
	if err != nil || !price.IsPositive() {
		return 0, 0, errors.New("invalid agent commission price")
	}
	quotaPerUnit, err := decimal.NewFromString(snapshot.QuotaPerUnit)
	if err != nil || !quotaPerUnit.IsPositive() {
		return 0, 0, errors.New("invalid agent quota conversion")
	}
	quota, err := common.WalletQuotaFromDecimalStrict(rebateCents.Div(decimal.NewFromInt(100)).Div(price).Mul(quotaPerUnit))
	if err != nil {
		return 0, 0, err
	}
	return quota, 0, nil
}

func epayTopUpQuota(amount int64, quotaPerUnit decimal.Decimal) (int, error) {
	if amount <= 0 || !quotaPerUnit.IsPositive() {
		return 0, errors.New("invalid recharge quota")
	}
	quota, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(amount).Mul(quotaPerUnit).Truncate(0))
	if err != nil {
		return 0, err
	}
	if quota <= 0 {
		return 0, errors.New("invalid recharge quota")
	}
	return quota, nil
}

func creditAgentQuota(tx *gorm.DB, userID, quota int) (int64, error) {
	if quota < 0 || quota > common.MaxWalletQuota {
		return 0, errors.New("invalid agent quota credit")
	}
	if quota == 0 {
		return 0, nil
	}
	// The watermark is read-only on User so unrelated profile updates cannot
	// overwrite it. Only this transaction writes it alongside the actual credit.
	result := tx.Table("users").Where("id = ? AND deleted_at IS NULL AND quota <= ? AND quota_credit_total <= ?", userID, common.MaxWalletQuota-quota, maxUserQuotaCreditTotal-int64(quota)).
		Updates(map[string]interface{}{"quota": gorm.Expr("quota + ?", quota), "quota_credit_total": gorm.Expr("quota_credit_total + ?", quota)})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, ErrEpayQuotaCapacity
	}
	var user User
	if err := tx.Select("quota_credit_total").First(&user, userID).Error; err != nil {
		return 0, err
	}
	return user.QuotaCreditTotal, nil
}

// ProcessPendingAgentCommissions processes only durable events created by
// verified payment settlement. Each grant and its settled marker share a tx.
func ProcessPendingAgentCommissions(limit int) error {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := time.Now().Unix()
	var events []AgentCommission
	if err := DB.Where("status = ? AND ready_at <= ? AND next_attempt_at <= ?", AgentCommissionPending, now, now).
		Order("id").Limit(limit).Find(&events).Error; err != nil {
		return err
	}
	var failures []error
	for _, candidate := range events {
		var settled AgentCommission
		var creditTotal int64
		err := DB.Transaction(func(tx *gorm.DB) error {
			var event AgentCommission
			if err := lockForUpdate(tx).First(&event, candidate.ID).Error; err != nil {
				return err
			}
			if event.Status != AgentCommissionPending || event.ReadyAt > now || event.NextAttemptAt > now {
				return nil
			}
			if event.Mode == AgentModeCredit {
				var err error
				creditTotal, err = creditAgentQuota(tx, event.ReferrerID, event.CreditQuota)
				if err != nil {
					return err
				}
			} else if event.Mode == AgentModeCash {
				if event.CashCents > 0 {
					if err := creditAgentCash(tx, event.ReferrerID, event.CashCents); err != nil {
						return err
					}
				}
			} else {
				return errors.New("invalid agent commission mode")
			}
			if err := tx.Model(&event).Updates(map[string]interface{}{
				"status": AgentCommissionSettled, "settled_at": now, "last_error": "", "next_attempt_at": 0,
			}).Error; err != nil {
				return err
			}
			settled = event
			return nil
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("commission %d: %w", candidate.ID, err))
			message := err.Error()
			if len(message) > 480 {
				message = message[:480]
			}
			if retryErr := DB.Model(&AgentCommission{}).Where("id = ? AND status = ?", candidate.ID, AgentCommissionPending).
				Updates(map[string]interface{}{"attempts": gorm.Expr("attempts + 1"), "last_error": message, "next_attempt_at": now + 60}).Error; retryErr != nil {
				failures = append(failures, retryErr)
			}
			continue
		}
		if settled.ID != 0 && settled.Mode == AgentModeCredit {
			if err := syncCommittedUserQuotaCredit(settled.ReferrerID, creditTotal); err != nil {
				common.SysError(fmt.Sprintf("agent commission %d cache credit synchronization: %s", settled.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func GetAgentCommissions(referrerID int, pageInfo *common.PageInfo) ([]AgentCommission, int64, error) {
	query := DB.Model(&AgentCommission{})
	if referrerID > 0 {
		query = query.Where("referrer_id = ?", referrerID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AgentCommission
	err := query.Order("id desc").Offset(pageInfo.GetStartIdx()).Limit(pageInfo.GetPageSize()).Find(&rows).Error
	return rows, total, err
}

type AgentCommissionSummary struct {
	ReferredUsers      int64 `json:"referred_users"`
	OrderCount         int64 `json:"order_count"`
	PendingCreditQuota int64 `json:"pending_credit_quota"`
	SettledCreditQuota int64 `json:"settled_credit_quota"`
	PendingCashCents   int64 `json:"pending_cash_cents"`
	SettledCashCents   int64 `json:"settled_cash_cents"`
}

func GetAgentCommissionSummary(referrerID int) (*AgentCommissionSummary, error) {
	if referrerID <= 0 {
		return nil, errors.New("invalid referrer")
	}
	summary := &AgentCommissionSummary{}
	if err := DB.Model(&User{}).Where("inviter_id = ?", referrerID).Count(&summary.ReferredUsers).Error; err != nil {
		return nil, err
	}
	var groups []struct {
		Status      string
		Count       int64
		CreditQuota int64
		CashCents   int64
	}
	if err := DB.Model(&AgentCommission{}).Select("status, COUNT(*) AS count, COALESCE(SUM(credit_quota), 0) AS credit_quota, COALESCE(SUM(cash_cents), 0) AS cash_cents").
		Where("referrer_id = ?", referrerID).Group("status").Scan(&groups).Error; err != nil {
		return nil, err
	}
	for _, group := range groups {
		summary.OrderCount += group.Count
		if group.Status == AgentCommissionSettled {
			summary.SettledCreditQuota += group.CreditQuota
			summary.SettledCashCents += group.CashCents
		} else if group.Status == AgentCommissionPending {
			summary.PendingCreditQuota += group.CreditQuota
			summary.PendingCashCents += group.CashCents
		}
	}
	return summary, nil
}

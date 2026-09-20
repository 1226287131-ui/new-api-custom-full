package model

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentCommissionDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &AgentPolicy{}, &AgentCommission{}, &AgentWallet{}, &Log{}))
	oldDB, oldLogDB, oldQuota, oldRedis, oldBatch := DB, LOG_DB, common.QuotaPerUnit, common.RedisEnabled, common.BatchUpdateEnabled
	DB, LOG_DB, common.QuotaPerUnit, common.RedisEnabled, common.BatchUpdateEnabled = db, db, 500000, false, false
	t.Cleanup(func() {
		DB, LOG_DB, common.QuotaPerUnit, common.RedisEnabled, common.BatchUpdateEnabled = oldDB, oldLogDB, oldQuota, oldRedis, oldBatch
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, DB.Create(&User{Id: 1, Username: "referrer", AffCode: "referrer", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, DB.Create(&User{Id: 2, Username: "customer", AffCode: "customer", InviterId: 1, Status: common.UserStatusEnabled}).Error)
}

func createAgentTestOrder(t *testing.T, tradeNo string, price float64, amount int64) *TopUp {
	t.Helper()
	order := &TopUp{UserId: 2, TradeNo: tradeNo, Amount: amount, Money: 100, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}
	require.NoError(t, SnapshotEpayAgentPolicy(order, price, common.QuotaPerUnit))
	require.NoError(t, order.Insert())
	return order
}

func TestAgentCommissionCreditAndCashExamples(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		price      float64
		amount     int64
		wantCredit int
		wantCash   int64
	}{
		{"hong-kong-credit", AgentModeCredit, 0.1, 1000, 25000000, 0},
		{"us-credit", AgentModeCredit, 1, 100, 2500000, 0},
		{"cash-not-converted-by-price", AgentModeCash, 0.1, 1000, 0, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupAgentCommissionDB(t)
			require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: tc.mode, CreditRateBPS: 500, CashRateBPS: 500, MinimumWithdrawalCents: 1000}))
			order := createAgentTestOrder(t, tc.name, tc.price, tc.amount)
			_, _, completed, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
			require.NoError(t, err)
			require.True(t, completed)
			var pending AgentCommission
			require.NoError(t, DB.First(&pending).Error)
			assert.Equal(t, tc.wantCredit, pending.CreditQuota)
			assert.Equal(t, tc.wantCash, pending.CashCents)
			require.NoError(t, ProcessPendingAgentCommissions(100))
			require.NoError(t, ProcessPendingAgentCommissions(100))
			_, _, completed, err = CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
			require.NoError(t, err)
			assert.False(t, completed)
			var customer, referrer User
			require.NoError(t, DB.First(&customer, 2).Error)
			require.NoError(t, DB.First(&referrer, 1).Error)
			assert.Equal(t, int(tc.amount)*500000, customer.Quota)
			assert.Equal(t, tc.wantCredit, referrer.Quota)
			wallet, err := GetAgentWallet(1)
			require.NoError(t, err)
			assert.Equal(t, tc.wantCash, wallet.AvailableCents)
			var count int64
			require.NoError(t, DB.Model(&AgentCommission{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
			summary, err := GetAgentCommissionSummary(1)
			require.NoError(t, err)
			assert.EqualValues(t, 1, summary.OrderCount)
			assert.EqualValues(t, tc.wantCredit, summary.SettledCreditQuota)
			assert.Equal(t, tc.wantCash, summary.SettledCashCents)
		})
	}
}

func TestAgentOrderSnapshotPreservesPolicyAndReferrer(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, CashRateBPS: 1000, FreezeHours: 24, MinimumWithdrawalCents: 1000}))
	order := createAgentTestOrder(t, "policy-snapshot", 0.1, 1000)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: false, Mode: AgentModeCash, CreditRateBPS: 9000, CashRateBPS: 9000, MinimumWithdrawalCents: 1000}))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("inviter_id", 0).Error)
	common.QuotaPerUnit = 1
	_, quota, _, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	assert.Equal(t, 500000000, quota)
	var event AgentCommission
	require.NoError(t, DB.First(&event).Error)
	assert.Equal(t, 1, event.ReferrerID)
	assert.Equal(t, AgentModeCredit, event.Mode)
	assert.Equal(t, 25000000, event.CreditQuota)
	assert.EqualValues(t, 24*3600, event.ReadyAt-event.CreatedAt)
	require.NoError(t, ProcessPendingAgentCommissions(100))
	var referrer User
	require.NoError(t, DB.First(&referrer, 1).Error)
	assert.Zero(t, referrer.Quota)
	require.NoError(t, DB.Model(&event).Update("ready_at", time.Now().Unix()-1).Error)
	require.NoError(t, ProcessPendingAgentCommissions(100))
	require.NoError(t, DB.First(&referrer, 1).Error)
	assert.Equal(t, 25000000, referrer.Quota)
}

func TestAgentManualAndHistoricalCreditsDoNotEarn(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
	manual := createAgentTestOrder(t, "manual", 1, 100)
	require.NoError(t, ManualCompleteTopUp(manual.TradeNo, "127.0.0.1"))
	_, _, completed, err := CompleteVerifiedEpayTopUp(manual.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	assert.False(t, completed)
	require.NoError(t, IncreaseUserQuota(2, 100, true))
	historical := &TopUp{UserId: 2, TradeNo: "historical", Amount: 100, Money: 100, PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending}
	require.NoError(t, historical.Insert())
	_, _, completed, err = CompleteVerifiedEpayTopUp(historical.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	assert.True(t, completed)
	require.NoError(t, ProcessPendingAgentCommissions(100))
	var count int64
	require.NoError(t, DB.Model(&AgentCommission{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAgentPaymentCommitFailureRollsBackBalanceAndOrder(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
	order := createAgentTestOrder(t, "payment-rollback", 1, 100)
	const callback = "test:commission-persistence-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_commissions" {
			tx.AddError(errors.New("simulated durable event failure"))
		}
	}))
	_, _, completed, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.Error(t, err)
	assert.False(t, completed)
	var persisted TopUp
	require.NoError(t, DB.First(&persisted, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, persisted.Status)
	var customer User
	require.NoError(t, DB.First(&customer, 2).Error)
	assert.Zero(t, customer.Quota)
	require.NoError(t, DB.Callback().Create().Remove(callback))
	_, _, completed, err = CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	assert.True(t, completed)
}

func TestAgentGrantFailureRetriesWithoutDoubleCredit(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
	order := createAgentTestOrder(t, "grant-retry", 1, 100)
	_, _, _, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	const callback = "test:commission-state-failure"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "agent_commissions" {
			return
		}
		updates, ok := tx.Statement.Dest.(map[string]interface{})
		if ok && updates["status"] == AgentCommissionSettled {
			tx.AddError(errors.New("simulated settlement marker failure"))
		}
	}))
	require.Error(t, ProcessPendingAgentCommissions(100))
	var referrer User
	require.NoError(t, DB.First(&referrer, 1).Error)
	assert.Zero(t, referrer.Quota)
	var event AgentCommission
	require.NoError(t, DB.First(&event).Error)
	assert.Equal(t, AgentCommissionPending, event.Status)
	assert.Equal(t, 1, event.Attempts)
	require.NoError(t, DB.Callback().Update().Remove(callback))
	require.NoError(t, DB.Model(&event).Update("next_attempt_at", 0).Error)
	require.NoError(t, ProcessPendingAgentCommissions(100))
	require.NoError(t, ProcessPendingAgentCommissions(100))
	require.NoError(t, DB.First(&referrer, 1).Error)
	assert.Equal(t, 2500000, referrer.Quota)
}

func TestAgentVerifiedPaymentRejectsMismatchWithoutMutating(t *testing.T) {
	setupAgentCommissionDB(t)
	order := createAgentTestOrder(t, "amount-mismatch", 1, 100)
	_, _, _, err := CompleteVerifiedEpayTopUp(order.TradeNo, 9999, "alipay")
	require.Error(t, err)
	var persisted TopUp
	require.NoError(t, DB.First(&persisted, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, persisted.Status)
	require.NoError(t, DB.Model(&TopUp{}).Where("id = ?", order.Id).Update("payment_provider", PaymentProviderStripe).Error)
	_, _, _, err = CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	assert.ErrorIs(t, err, ErrPaymentMethodMismatch)
	var customer User
	require.NoError(t, DB.First(&customer, 2).Error)
	assert.Zero(t, customer.Quota)
}

func TestAgentRejectsInvalidAmountsAndQuotaOverflow(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "NaN", "1e5", "1.001", " 1.00", "100000000000000"} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseEpayPaidCents(value)
			require.Error(t, err)
		})
	}
	for _, tc := range []struct {
		value string
		want  int64
	}{{"100", 10000}, {"100.0", 10000}, {"100.01", 10001}, {"0.01", 1}} {
		got, err := ParseEpayPaidCents(tc.value)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	setupAgentCommissionDB(t)
	order := &TopUp{UserId: 2, TradeNo: "overflow", Amount: math.MaxInt64, Money: 100, PaymentProvider: PaymentProviderEpay}
	require.Error(t, SnapshotEpayAgentPolicy(order, 1, common.QuotaPerUnit))
	order.Amount = 100
	require.Error(t, SnapshotEpayAgentPolicy(order, math.NaN(), common.QuotaPerUnit))
	valid := createAgentTestOrder(t, "balance-overflow", 1, 100)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", common.MaxQuota-1).Error)
	_, _, _, err := CompleteVerifiedEpayTopUp(valid.TradeNo, 10000, "alipay")
	require.Error(t, err)
	var persisted TopUp
	require.NoError(t, DB.First(&persisted, valid.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, persisted.Status)
}

func TestAgentConcurrentCallbacksAndWorkersAwardOnce(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCash, CashRateBPS: 500, MinimumWithdrawalCents: 1000}))
	order := createAgentTestOrder(t, "concurrent-callbacks", 1, 100)
	start := make(chan struct{})
	var wait sync.WaitGroup
	results := make(chan bool, 2)
	errorsFound := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, _, completed, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
			results <- completed
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	completedCount := 0
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errorsFound)
		if <-results {
			completedCount++
		}
	}
	assert.Equal(t, 1, completedCount)
	start = make(chan struct{})
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errorsFound <- ProcessPendingAgentCommissions(100)
		}()
	}
	close(start)
	wait.Wait()
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errorsFound)
	}
	wallet, err := GetAgentWallet(1)
	require.NoError(t, err)
	assert.EqualValues(t, 500, wallet.AvailableCents)
	var customer User
	require.NoError(t, DB.First(&customer, 2).Error)
	assert.Equal(t, 50000000, customer.Quota)
}

func TestAgentDisabledAndSelfReferralDoNotCreateReward(t *testing.T) {
	setupAgentCommissionDB(t)
	policy, err := GetAgentPolicy()
	require.NoError(t, err)
	assert.False(t, policy.Enabled)
	disabled := createAgentTestOrder(t, "disabled-order", 1, 100)
	assert.Empty(t, disabled.AgentSnapshot)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("inviter_id", 2).Error)
	self := createAgentTestOrder(t, "self-invitation", 1, 100)
	assert.Empty(t, self.AgentSnapshot)
	for _, order := range []*TopUp{disabled, self} {
		_, _, _, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&AgentCommission{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAgentRechargeAndRewardPreserveUnflushedCachedDebits(t *testing.T) {
	setupAgentCommissionDB(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	require.NoError(t, DB.Model(&User{}).Where("id IN ?", []int{1, 2}).Update("quota", 1000000).Error)
	for _, id := range []int{1, 2} {
		_, err := GetUserCache(id)
		require.NoError(t, err)
		// This is the cache-side state before the batch debit is persisted.
		require.NoError(t, cacheDecrUserQuota(id, 300000))
	}
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
	order := createAgentTestOrder(t, "cache-debits", 1, 100)
	_, _, completed, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	require.True(t, completed)
	require.NoError(t, ProcessPendingAgentCommissions(100))
	_, _, completed, err = CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	assert.False(t, completed)
	require.NoError(t, ProcessPendingAgentCommissions(100))
	for _, tc := range []struct {
		id     int
		credit int
	}{{1, 2500000}, {2, 50000000}} {
		cached, err := GetUserCache(tc.id)
		require.NoError(t, err)
		assert.Equal(t, 700000+tc.credit, cached.Quota)
		var persisted User
		require.NoError(t, DB.First(&persisted, tc.id).Error)
		assert.Equal(t, 1000000+tc.credit, persisted.Quota)
		assert.EqualValues(t, tc.credit, persisted.QuotaCreditTotal)
	}
}

func TestAgentCommittedCreditCacheRebuildsAndRetriesDoNotDoubleCredit(t *testing.T) {
	for _, tc := range []struct {
		name               string
		populateBeforeSync bool
	}{
		{"cache-rebuilt-before-sync", true}, {"sync-before-cache-rebuild", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupAgentCommissionDB(t)
			useUserCacheMiniRedis(t)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", 1000000).Error)
			var before User
			require.NoError(t, DB.First(&before, 2).Error)
			var total int64
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				var err error
				total, err = creditAgentQuota(tx, 2, 500000)
				return err
			}))
			if tc.populateBeforeSync {
				cached, err := GetUserCache(2)
				require.NoError(t, err)
				assert.Equal(t, 1500000, cached.Quota)
			}
			require.NoError(t, syncCommittedUserQuotaCredit(2, total))
			if !tc.populateBeforeSync {
				assert.ErrorIs(t, populateUserCache(before), errUserQuotaCacheSnapshotStale)
			}
			cached, err := GetUserCache(2)
			require.NoError(t, err)
			assert.Equal(t, 1500000, cached.Quota)
			require.NoError(t, cacheDecrUserQuota(2, 100000))
			require.NoError(t, syncCommittedUserQuotaCredit(2, total))
			require.NoError(t, syncCommittedUserQuotaCredit(2, total-1))
			cached, err = GetUserCache(2)
			require.NoError(t, err)
			assert.Equal(t, 1400000, cached.Quota)
		})
	}
}

func TestAgentQuotaCreditWatermarkSurvivesProfileUpdates(t *testing.T) {
	setupAgentCommissionDB(t)
	var stale User
	require.NoError(t, DB.First(&stale, 2).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := creditAgentQuota(tx, 2, 500000)
		return err
	}))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Updates(map[string]interface{}{"email": "updated@example.com", "quota_credit_total": 0}).Error)
	var current User
	require.NoError(t, DB.First(&current, 2).Error)
	assert.EqualValues(t, 500000, current.QuotaCreditTotal)
	assert.Equal(t, "updated@example.com", current.Email)
}

func TestAgentOrderCreationRejectsBalanceCapacityBeforePayment(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", common.MaxQuota-500000+1).Error)
	order := &TopUp{UserId: 2, Amount: 1, Money: 1, PaymentProvider: PaymentProviderEpay}
	assert.ErrorIs(t, SnapshotEpayAgentPolicy(order, 1, common.QuotaPerUnit), ErrEpayQuotaCapacity)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", common.MaxQuota-500000).Error)
	require.NoError(t, SnapshotEpayAgentPolicy(order, 1, common.QuotaPerUnit))
}

func TestAgentRedisSyncFailureRecoversWithoutErasingUnflushedDebits(t *testing.T) {
	setupAgentCommissionDB(t)
	server := useUserCacheMiniRedis(t)
	require.NoError(t, DB.Model(&User{}).Where("id IN ?", []int{1, 2}).Update("quota", 1000000).Error)
	for _, id := range []int{1, 2} {
		_, err := GetUserCache(id)
		require.NoError(t, err)
		require.NoError(t, cacheDecrUserQuota(id, 300000))
	}
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
	order := createAgentTestOrder(t, "cache-sync-failure", 1, 100)
	server.SetError("ERR injected cache outage")
	_, _, completed, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	require.True(t, completed)
	require.NoError(t, ProcessPendingAgentCommissions(100))
	server.SetError("")
	for _, id := range []int{1, 2} {
		cached, err := GetUserCache(id)
		require.NoError(t, err)
		assert.Equal(t, 700000, cached.Quota)
	}
	// A callback retry can repair a committed recharge even after process loss.
	_, _, completed, err = CompleteVerifiedEpayTopUp(order.TradeNo, 10000, "alipay")
	require.NoError(t, err)
	assert.False(t, completed)
	customer, err := GetUserCache(2)
	require.NoError(t, err)
	assert.Equal(t, 50700000, customer.Quota)
	// A fresh database snapshot also heals missed reward synchronization by
	// applying only the watermark delta to the still-debited cached quota.
	var referrer User
	require.NoError(t, DB.First(&referrer, 1).Error)
	require.NoError(t, populateUserCache(referrer))
	require.NoError(t, populateUserCache(referrer))
	require.NoError(t, syncCommittedUserQuotaCredit(1, referrer.QuotaCreditTotal))
	cached, err := GetUserCache(1)
	require.NoError(t, err)
	assert.Equal(t, 3200000, cached.Quota)
}

func TestAgentDatabaseQuotaReadRepairsCreditsWithoutAbsoluteBalanceOverwrite(t *testing.T) {
	setupAgentCommissionDB(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", 1000000).Error)
	_, err := GetUserCache(2)
	require.NoError(t, err)
	require.NoError(t, cacheDecrUserQuota(2, 300000))
	var total int64
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		total, err = creditAgentQuota(tx, 2, 500000)
		return err
	}))
	// A DB quota read before the normal post-commit synchronization must repair
	// the watermark and delta together, never copy its absolute 1.5m balance.
	quota, err := GetUserQuota(2, true)
	require.NoError(t, err)
	assert.Equal(t, 1500000, quota)
	cached, err := cacheGetUserBase(2)
	require.NoError(t, err)
	assert.Equal(t, 1200000, cached.Quota)
	assert.Equal(t, total, cached.QuotaCreditTotal)
	require.NoError(t, syncCommittedUserQuotaCredit(2, total))
	quota, err = GetUserQuota(2, true)
	require.NoError(t, err)
	assert.Equal(t, 1500000, quota)
	cached, err = cacheGetUserBase(2)
	require.NoError(t, err)
	assert.Equal(t, 1200000, cached.Quota)
	assert.Equal(t, total, cached.QuotaCreditTotal)
}

func TestAgentDelayedDatabaseQuotaReadCannotOverwriteNewCredit(t *testing.T) {
	setupAgentCommissionDB(t)
	useUserCacheMiniRedis(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", 1000000).Error)
	_, err := GetUserCache(2)
	require.NoError(t, err)
	require.NoError(t, cacheDecrUserQuota(2, 300000))
	var total int64
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		total, err = creditAgentQuota(tx, 2, 500000)
		return err
	}))
	require.NoError(t, syncCommittedUserQuotaCredit(2, total))
	const callback = "test:credit-after-quota-snapshot"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "users" || len(tx.Statement.Selects) != 2 || tx.Statement.Selects[0] != "quota" {
			return
		}
		require.NoError(t, DB.Transaction(func(creditTX *gorm.DB) error {
			var err error
			total, err = creditAgentQuota(creditTX, 2, 200000)
			return err
		}))
		require.NoError(t, syncCommittedUserQuotaCredit(2, total))
	}))
	t.Cleanup(func() { assert.NoError(t, DB.Callback().Query().Remove(callback)) })
	// The SELECT has captured the old quota and watermark; the controlled query
	// hook commits the next reward before the fallback can update Redis.
	quota, err := GetUserQuota(2, true)
	require.NoError(t, err)
	assert.Equal(t, 1500000, quota)
	cached, err := cacheGetUserBase(2)
	require.NoError(t, err)
	assert.Equal(t, 1400000, cached.Quota)
	assert.EqualValues(t, 700000, cached.QuotaCreditTotal)
	require.NoError(t, syncCommittedUserQuotaCredit(2, 500000))
	cached, err = cacheGetUserBase(2)
	require.NoError(t, err)
	assert.Equal(t, 1400000, cached.Quota)
}

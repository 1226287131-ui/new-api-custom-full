package model

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mysqlgorm "gorm.io/driver/mysql"
	postgresgorm "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupAgentCommissionDB(t *testing.T) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "team.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &AgentPolicy{}, &AgentCommission{}, &AgentWallet{}, &AgentRewardPreference{}, &AgentReferralGuard{}, &AgentReferralChange{}, &Log{}))
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

func TestAgentRewardPreferenceOnlyChangesFutureReferrerSnapshots(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, CashRateBPS: 800, MinimumWithdrawalCents: 1000}))
	preference, err := GetAgentRewardPreference(1)
	require.NoError(t, err)
	assert.Empty(t, preference.Mode)
	oldOrder := createAgentTestOrder(t, "preference-before", 0.1, 1000)
	err = SaveAgentRewardPreference(1, AgentModeCash)
	require.NoError(t, err)
	// The paying customer's choice must never select the referrer's reward mode.
	err = SaveAgentRewardPreference(2, AgentModeCredit)
	require.NoError(t, err)
	newOrder := createAgentTestOrder(t, "preference-after", 0.1, 1000)
	var oldSnapshot, newSnapshot agentOrderSnapshot
	require.NoError(t, common.UnmarshalJsonStr(oldOrder.AgentSnapshot, &oldSnapshot))
	require.NoError(t, common.UnmarshalJsonStr(newOrder.AgentSnapshot, &newSnapshot))
	assert.Equal(t, AgentModeCredit, oldSnapshot.Mode)
	assert.Equal(t, 500, oldSnapshot.RateBPS)
	assert.Equal(t, AgentModeCash, newSnapshot.Mode)
	assert.Equal(t, 800, newSnapshot.RateBPS)
	err = SaveAgentRewardPreference(1, AgentModeCredit)
	require.NoError(t, err)
	var stored TopUp
	require.NoError(t, DB.First(&stored, newOrder.Id).Error)
	assert.Equal(t, newOrder.AgentSnapshot, stored.AgentSnapshot)
	err = SaveAgentRewardPreference(1, "arbitrary")
	assert.ErrorIs(t, err, ErrInvalidAgentRewardPreference)
	err = SaveAgentRewardPreference(9999, AgentModeCash)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAgentReferralChangeIsAuditedAndDoesNotRewriteOrdersOrBalances(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, DB.Create(&User{Id: 3, Username: "root", AffCode: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, DB.Create(&User{Id: 4, Username: "new-referrer", AffCode: "new-referrer", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
	oldOrder := createAgentTestOrder(t, "referral-before", 1, 100)
	change, err := ChangeAgentReferrer(3, 2, 1, 4, "customer requested correction")
	require.NoError(t, err)
	assert.Equal(t, 1, change.OldInviterID)
	assert.Equal(t, 4, change.NewInviterID)
	assert.Equal(t, "root", change.OperatorUsername)
	referral, err := GetAgentReferral(2)
	require.NoError(t, err)
	assert.Equal(t, "new-referrer", referral.InviterUsername)
	newOrder := createAgentTestOrder(t, "referral-after", 1, 100)
	var stored TopUp
	require.NoError(t, DB.First(&stored, oldOrder.Id).Error)
	assert.Equal(t, oldOrder.AgentSnapshot, stored.AgentSnapshot)
	var snapshot agentOrderSnapshot
	require.NoError(t, common.UnmarshalJsonStr(newOrder.AgentSnapshot, &snapshot))
	assert.Equal(t, 4, snapshot.ReferrerID)
	_, err = ChangeAgentReferrer(3, 2, 1, 0, "stale form")
	assert.ErrorIs(t, err, ErrAgentReferralConflict)
	_, err = ChangeAgentReferrer(3, 2, 4, 0, "unlink")
	require.NoError(t, err)
	var count int64
	require.NoError(t, DB.Model(&AgentReferralChange{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	var user User
	require.NoError(t, DB.First(&user, 2).Error)
	assert.Zero(t, user.InviterId)
	assert.Zero(t, user.Quota)
	assert.Zero(t, user.AffCount)
	assert.Zero(t, user.QuotaCreditTotal)
}

func TestAgentReferralRejectsUnauthorizedAndCyclicChanges(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, DB.Create(&User{Id: 3, Username: "root", AffCode: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, DB.Create(&User{Id: 4, Username: "admin", AffCode: "admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}).Error)
	for _, tc := range []struct {
		name                                   string
		operator, user, oldInviter, newInviter int
		want                                   error
	}{
		{"ordinary-user", 1, 2, 1, 0, ErrAgentReferralForbidden},
		{"protected-admin", 4, 3, 0, 1, ErrAgentReferralForbidden},
		{"self-invitation", 3, 2, 1, 2, ErrAgentReferralInvalid},
		{"ancestor-cycle", 3, 1, 0, 2, ErrAgentReferralCycle},
		{"missing-inviter", 3, 2, 1, 9999, gorm.ErrRecordNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ChangeAgentReferrer(tc.operator, tc.user, tc.oldInviter, tc.newInviter, "test correction")
			assert.ErrorIs(t, err, tc.want)
		})
	}
	var count int64
	require.NoError(t, DB.Model(&AgentReferralChange{}).Count(&count).Error)
	assert.Zero(t, count)
	// Generic profile changes cannot bypass the audited hierarchy operation.
	user := &User{Id: 2, DisplayName: "renamed", InviterId: 4}
	require.NoError(t, user.Update(false))
	assert.Equal(t, 1, user.InviterId)
}

func TestAgentReferralAuditFailureRollsBackBinding(t *testing.T) {
	setupAgentCommissionDB(t)
	require.NoError(t, DB.Create(&User{Id: 3, Username: "root", AffCode: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error)
	const callback = "test:reject-referral-audit"
	failure := errors.New("audit write unavailable")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_referral_changes" {
			tx.AddError(failure)
		}
	}))
	t.Cleanup(func() { assert.NoError(t, DB.Callback().Create().Remove(callback)) })
	_, err := ChangeAgentReferrer(3, 2, 1, 0, "unlink")
	assert.ErrorIs(t, err, failure)
	referral, err := GetAgentReferral(2)
	require.NoError(t, err)
	assert.Equal(t, 1, referral.InviterID)
}

func TestAgentReferralConcurrentChangesCannotCreateCycle(t *testing.T) {
	setupAgentCommissionDB(t)
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).UpdateColumn("inviter_id", 0).Error)
	require.NoError(t, DB.Create(&User{Id: 3, Username: "root", AffCode: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, DB.Create(&User{Id: 4, Username: "other-root", AffCode: "other-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, pair := range [][3]int{{1, 2, 3}, {2, 1, 4}} {
		go func() {
			<-start
			_, err := ChangeAgentReferrer(pair[2], pair[0], 0, pair[1], "concurrent correction")
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first == nil {
		assert.ErrorIs(t, second, ErrAgentReferralCycle)
	} else {
		assert.ErrorIs(t, first, ErrAgentReferralCycle)
		assert.NoError(t, second)
	}
	var count int64
	require.NoError(t, DB.Model(&AgentReferralChange{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

// External DSNs must point at disposable, empty scratch databases. No tables
// or databases are dropped, so failed migrations remain available to inspect.
func TestAgentRewardReferralDatabaseCompatibility(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		for _, scenario := range []string{"fresh", "upgrade"} {
			t.Run(string(dialect)+"-"+scenario, func(t *testing.T) {
				var driver gorm.Dialector
				if dialect == common.DatabaseTypeSQLite {
					driver = sqlite.Open(filepath.Join(t.TempDir(), "matrix.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
				} else {
					dsn := os.Getenv("NEWAPI_TEAM_TEST_" + strings.ToUpper(string(dialect)) + "_" + strings.ToUpper(scenario) + "_DSN")
					if dsn == "" {
						t.Skip("set the isolated scratch database DSN to run this engine")
					}
					if dialect == common.DatabaseTypeMySQL {
						driver = mysqlgorm.Open(dsn)
					} else {
						driver = postgresgorm.Open(dsn)
					}
				}
				db, err := gorm.Open(driver, &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(4)
				t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
				tables, err := db.Migrator().GetTables()
				require.NoError(t, err)
				require.Empty(t, tables, "refusing to use a nonempty scratch database")
				var version string
				versionSQL := "SELECT VERSION()"
				if dialect == common.DatabaseTypeSQLite {
					versionSQL = "SELECT sqlite_version()"
				}
				require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
				t.Logf("database version: %s", version)
				previousDB, previousLogDB, previousType := DB, LOG_DB, common.MainDatabaseType()
				previousRedis, previousBatch := common.RedisEnabled, common.BatchUpdateEnabled
				DB, LOG_DB = db, db
				common.SetMainDatabaseType(dialect)
				common.RedisEnabled, common.BatchUpdateEnabled = false, false
				t.Cleanup(func() {
					DB, LOG_DB = previousDB, previousLogDB
					common.SetMainDatabaseType(previousType)
					common.RedisEnabled, common.BatchUpdateEnabled = previousRedis, previousBatch
				})
				legacyModels := []any{&User{}, &TopUp{}, &AgentPolicy{}, &AgentCommission{}, &AgentWallet{}, &AgentPayoutAccount{}, &AgentWithdrawal{}}
				allModels := append(append([]any{}, legacyModels...), &AgentRewardPreference{}, &AgentReferralGuard{}, &AgentReferralChange{})
				if scenario == "upgrade" {
					require.NoError(t, db.AutoMigrate(legacyModels...))
				} else {
					require.NoError(t, db.AutoMigrate(allModels...))
				}
				users := []User{
					{Id: 1, Username: "referrer", AffCode: "matrix-parent", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 700000, QuotaCreditTotal: 100000},
					{Id: 2, Username: "customer", AffCode: "matrix-child", InviterId: 1, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 900000},
					{Id: 3, Username: "replacement", AffCode: "matrix-replacement", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
					{Id: 5, Username: "left", AffCode: "matrix-left", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
					{Id: 6, Username: "right", AffCode: "matrix-right", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
					{Id: 10, Username: "root-a", AffCode: "matrix-root-a", Role: common.RoleRootUser, Status: common.UserStatusEnabled},
					{Id: 11, Username: "root-b", AffCode: "matrix-root-b", Role: common.RoleRootUser, Status: common.UserStatusEnabled},
				}
				require.NoError(t, db.Create(&users).Error)
				require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCredit, CreditRateBPS: 500, CashRateBPS: 800, MinimumWithdrawalCents: 100}))
				encoded, err := common.Marshal(agentOrderSnapshot{Version: 1, UserID: 2, ReferrerID: 1, Mode: AgentModeCash, RateBPS: 1000, Price: "1", QuotaPerUnit: "500000"})
				require.NoError(t, err)
				order := TopUp{UserId: 2, TradeNo: "matrix-old-order", Amount: 100, Money: 100, PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess, AgentSnapshot: string(encoded), ExpectedPaidCents: 10000, EpayQuotaPerUnit: "500000"}
				require.NoError(t, db.Create(&order).Error)
				commission := AgentCommission{TopUpID: order.Id, TradeNo: order.TradeNo, UserID: 2, ReferrerID: 1, Mode: AgentModeCash, RateBPS: 1000, PaidCents: 10000, CashCents: 1000, Status: AgentCommissionSettled, Price: "1", QuotaPerUnit: "500000"}
				wallet := AgentWallet{UserID: 1, AvailableCents: 900, FrozenCents: 100}
				withdrawal := AgentWithdrawal{UserID: 1, RequestID: "matrix-old-withdrawal", AmountCents: 100, Status: AgentWithdrawalPending, AccountCiphertext: "existing-encrypted-account", NameCiphertext: "existing-encrypted-name"}
				require.NoError(t, db.Create(&commission).Error)
				require.NoError(t, db.Create(&wallet).Error)
				require.NoError(t, db.Create(&withdrawal).Error)
				var beforeUsers []User
				require.NoError(t, db.Order("id").Find(&beforeUsers).Error)
				// Repeated startup migrations must preserve all previously stored funds
				// and frozen order, commission and withdrawal records.
				for range 2 {
					require.NoError(t, db.AutoMigrate(allModels...))
				}
				var afterUsers []User
				require.NoError(t, db.Order("id").Find(&afterUsers).Error)
				assert.Equal(t, beforeUsers, afterUsers)
				preference, err := GetAgentRewardPreference(1)
				require.NoError(t, err)
				assert.Empty(t, preference.Mode)
				require.NoError(t, SaveAgentRewardPreference(1, AgentModeCash))
				require.NoError(t, SaveAgentRewardPreference(1, AgentModeCredit))
				require.NoError(t, SaveAgentRewardPreference(1, AgentModeCash))
				var preferenceCount int64
				require.NoError(t, db.Model(&AgentRewardPreference{}).Where("user_id = ?", 1).Count(&preferenceCount).Error)
				assert.EqualValues(t, 1, preferenceCount)
				newOrder := &TopUp{UserId: 2, Amount: 100, Money: 100, PaymentProvider: PaymentProviderEpay}
				require.NoError(t, SnapshotEpayAgentPolicy(newOrder, 1, 500000))
				var snapshot agentOrderSnapshot
				require.NoError(t, common.UnmarshalJsonStr(newOrder.AgentSnapshot, &snapshot))
				assert.Equal(t, AgentModeCash, snapshot.Mode)
				assert.Equal(t, 800, snapshot.RateBPS)
				_, err = ChangeAgentReferrer(10, 2, 1, 3, strings.Repeat("改", 201))
				assert.ErrorIs(t, err, ErrAgentReferralInvalid)
				_, err = ChangeAgentReferrer(10, 2, 1, 3, strings.Repeat("改", 200))
				require.NoError(t, err)
				_, err = ChangeAgentReferrer(11, 2, 1, 0, "stale form")
				assert.ErrorIs(t, err, ErrAgentReferralConflict)
				require.NoError(t, SnapshotEpayAgentPolicy(newOrder, 1, 500000))
				require.NoError(t, common.UnmarshalJsonStr(newOrder.AgentSnapshot, &snapshot))
				assert.Equal(t, 3, snapshot.ReferrerID)
				assert.Equal(t, AgentModeCredit, snapshot.Mode)
				assert.Equal(t, 500, snapshot.RateBPS)
				start, results := make(chan struct{}), make(chan error, 2)
				for _, change := range [][3]int{{10, 5, 6}, {11, 6, 5}} {
					go func() {
						<-start
						_, err := ChangeAgentReferrer(change[0], change[1], 0, change[2], "concurrent rebind")
						results <- err
					}()
				}
				close(start)
				first, second := <-results, <-results
				if first == nil {
					assert.ErrorIs(t, second, ErrAgentReferralCycle)
				} else {
					assert.ErrorIs(t, first, ErrAgentReferralCycle)
					assert.NoError(t, second)
				}
				const callback = "test:matrix-reject-audit"
				failure := errors.New("audit write unavailable")
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "agent_referral_changes" {
						tx.AddError(failure)
					}
				}))
				_, err = ChangeAgentReferrer(10, 2, 3, 0, "unlink")
				assert.ErrorIs(t, err, failure)
				require.NoError(t, db.Callback().Create().Remove(callback))
				referral, err := GetAgentReferral(2)
				require.NoError(t, err)
				assert.Equal(t, 3, referral.InviterID)
				var storedOrder TopUp
				var storedCommission AgentCommission
				var storedWallet AgentWallet
				var storedWithdrawal AgentWithdrawal
				require.NoError(t, db.First(&storedOrder, order.Id).Error)
				require.NoError(t, db.First(&storedCommission, commission.ID).Error)
				require.NoError(t, db.First(&storedWallet, "user_id = ?", 1).Error)
				require.NoError(t, db.First(&storedWithdrawal, withdrawal.ID).Error)
				assert.Equal(t, order, storedOrder)
				assert.Equal(t, commission, storedCommission)
				assert.Equal(t, wallet, storedWallet)
				assert.Equal(t, withdrawal, storedWithdrawal)
			})
		}
	}
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

func TestAgentRechargeAndRewardPreserveBigintWalletCapacity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "rewards-disabled", true: "rewards-enabled"}[enabled], func(t *testing.T) {
			setupAgentCommissionDB(t)
			require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: enabled, Mode: AgentModeCredit, CreditRateBPS: 500, MinimumWithdrawalCents: 1000}))
			order := &TopUp{UserId: 2, TradeNo: "bigint-wallet", Amount: 100000, Money: 100000, PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}
			require.NoError(t, SnapshotEpayAgentPolicy(order, 1, common.QuotaPerUnit))
			require.NoError(t, order.Insert())
			_, quota, completed, err := CompleteVerifiedEpayTopUp(order.TradeNo, 10000000, "alipay")
			require.NoError(t, err)
			require.True(t, completed)
			assert.Equal(t, 50000000000, quota)
			require.NoError(t, ProcessPendingAgentCommissions(100))
			_, _, completed, err = CompleteVerifiedEpayTopUp(order.TradeNo, 10000000, "alipay")
			require.NoError(t, err)
			assert.False(t, completed)
			require.NoError(t, ProcessPendingAgentCommissions(100))
			var customer, referrer User
			require.NoError(t, DB.First(&customer, 2).Error)
			require.NoError(t, DB.First(&referrer, 1).Error)
			assert.Equal(t, 50000000000, customer.Quota)
			assert.EqualValues(t, 50000000000, customer.QuotaCreditTotal)
			if enabled {
				assert.Equal(t, 2500000000, referrer.Quota)
				assert.EqualValues(t, 2500000000, referrer.QuotaCreditTotal)
			} else {
				assert.Zero(t, referrer.Quota)
				assert.Zero(t, referrer.QuotaCreditTotal)
			}
		})
	}
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
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", common.MaxWalletQuota-1).Error)
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
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", common.MaxWalletQuota-500000+1).Error)
	order := &TopUp{UserId: 2, Amount: 1, Money: 1, PaymentProvider: PaymentProviderEpay}
	assert.ErrorIs(t, SnapshotEpayAgentPolicy(order, 1, common.QuotaPerUnit), ErrEpayQuotaCapacity)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 2).Update("quota", common.MaxWalletQuota-500000).Error)
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

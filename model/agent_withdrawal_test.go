package model

import (
	"encoding/base64"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupAgentWithdrawalTest(t *testing.T, available int64) {
	t.Helper()
	previousDB := DB
	previousDBType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "withdrawals.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() { DB = previousDB; common.SetMainDatabaseType(previousDBType); assert.NoError(t, sqlDB.Close()) })
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("w", 32))))
	require.NoError(t, DB.AutoMigrate(&AgentPolicy{}, &AgentWallet{}, &AgentPayoutAccount{}, &AgentWithdrawal{}))
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Enabled: true, Mode: AgentModeCash, MinimumWithdrawalCents: 100}))
	require.NoError(t, DB.Create(&AgentWallet{UserID: 42, AvailableCents: available}).Error)
	_, err = SaveAgentPayoutAccount(42, "recipient@example.com", "Alice Example")
	require.NoError(t, err)
}

func TestAgentWithdrawalCashCreditIsBoundedAndTransactional(t *testing.T) {
	setupAgentWithdrawalTest(t, MaxAgentCashCents-5)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return creditAgentCash(tx, 42, 5) }))
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return creditAgentCash(tx, 42, 1) }), ErrAgentInvalidCashAmount)
	for _, cents := range []int64{-1, 0, math.MaxInt64} {
		require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error { return creditAgentCash(tx, 42, cents) }), ErrAgentInvalidCashAmount)
	}
	wallet, err := GetAgentWallet(42)
	require.NoError(t, err)
	assert.Equal(t, MaxAgentCashCents, wallet.AvailableCents)
	assert.Zero(t, wallet.FrozenCents)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return creditAgentCash(tx, 43, 150) }))
	wallet, err = GetAgentWallet(43)
	require.NoError(t, err)
	assert.Equal(t, int64(150), wallet.AvailableCents)
}

func TestAgentWithdrawalRejectsInvalidAndInsufficientAmounts(t *testing.T) {
	setupAgentWithdrawalTest(t, 1000)
	for _, cents := range []int64{-1, 0, 99, math.MaxInt64} {
		_, err := RequestAgentWithdrawal(42, cents, "request-invalid")
		require.ErrorIs(t, err, ErrAgentInvalidCashAmount)
	}
	_, err := RequestAgentWithdrawal(42, 1100, "request-insufficient")
	require.ErrorIs(t, err, ErrAgentInsufficientCash)
	wallet, err := GetAgentWallet(42)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), wallet.AvailableCents)
	assert.Zero(t, wallet.FrozenCents)
	var count int64
	require.NoError(t, DB.Model(&AgentWithdrawal{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAgentWithdrawalRequestIdempotencyAndAccountSnapshot(t *testing.T) {
	setupAgentWithdrawalTest(t, 1000)
	first, err := RequestAgentWithdrawal(42, 600, "request-original")
	require.NoError(t, err)
	second, err := RequestAgentWithdrawal(42, 600, "request-original")
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	_, err = RequestAgentWithdrawal(42, 500, "request-original")
	require.ErrorIs(t, err, ErrAgentWithdrawalRequestConflict)
	_, err = SaveAgentPayoutAccount(42, "changed@example.com", "Changed Recipient")
	require.NoError(t, err)
	detail, err := GetAgentWithdrawalForAdmin(first.ID)
	require.NoError(t, err)
	assert.Equal(t, "recipient@example.com", detail.Account)
	assert.Equal(t, "Alice Example", detail.Name)
	account, err := GetAgentPayoutAccount(42)
	require.NoError(t, err)
	assert.Empty(t, account.AccountCiphertext)
	assert.NotContains(t, account.AccountMasked, "changed@example.com")
	rows, count, err := ListAgentWithdrawals(42, "", 0, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	require.Len(t, rows, 1)
	assert.Empty(t, rows[0].AccountCiphertext)
	encoded, err := common.Marshal(rows)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "recipient@example.com")
	assert.NotContains(t, string(encoded), "Alice Example")
	wallet, err := GetAgentWallet(42)
	require.NoError(t, err)
	assert.Equal(t, int64(400), wallet.AvailableCents)
	assert.Equal(t, int64(600), wallet.FrozenCents)
}

func TestAgentWithdrawalReviewDoesNotDoubleReleaseOrPay(t *testing.T) {
	setupAgentWithdrawalTest(t, 2000)
	withdrawal, err := RequestAgentWithdrawal(42, 600, "request-to-pay")
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "paid", "transfer-123", "")
	require.ErrorIs(t, err, ErrAgentWithdrawalState)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "approve", "", "")
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 2, "approve", "", "")
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "paid", "", "")
	require.Error(t, err)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "paid", "transfer-123", "")
	require.NoError(t, err)
	paid, err := ReviewAgentWithdrawal(withdrawal.ID, 2, "paid", "transfer-123", "")
	require.NoError(t, err)
	assert.Equal(t, 1, paid.PaidBy)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "paid", "another-transfer", "")
	require.ErrorIs(t, err, ErrAgentWithdrawalRequestConflict)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "reject", "", "too late")
	require.ErrorIs(t, err, ErrAgentWithdrawalState)
	rejected, err := RequestAgentWithdrawal(42, 700, "request-to-reject")
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(rejected.ID, 1, "approve", "", "")
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(rejected.ID, 1, "reject", "", "Invalid recipient")
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(rejected.ID, 2, "reject", "", "Invalid recipient")
	require.NoError(t, err)
	wallet, err := GetAgentWallet(42)
	require.NoError(t, err)
	assert.Equal(t, int64(1400), wallet.AvailableCents)
	assert.Zero(t, wallet.FrozenCents)
	assert.Equal(t, int64(600), wallet.PaidCents)
}

func TestAgentWithdrawalDisabledPolicyStillAllowsAuditAndSettlement(t *testing.T) {
	setupAgentWithdrawalTest(t, 1000)
	withdrawal, err := RequestAgentWithdrawal(42, 400, "request-before-disable")
	require.NoError(t, err)
	require.NoError(t, SaveAgentPolicy(&AgentPolicy{Mode: AgentModeCredit, MinimumWithdrawalCents: 100}))
	_, err = RequestAgentWithdrawal(42, 100, "request-after-disable")
	require.ErrorIs(t, err, ErrAgentWithdrawalDisabled)
	duplicate, err := RequestAgentWithdrawal(42, 400, "request-before-disable")
	require.NoError(t, err)
	assert.Equal(t, withdrawal.ID, duplicate.ID)
	_, err = GetAgentWithdrawalForAdmin(withdrawal.ID)
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "approve", "", "")
	require.NoError(t, err)
	_, err = ReviewAgentWithdrawal(withdrawal.ID, 1, "paid", "completed-manually", "")
	require.NoError(t, err)
}

func TestAgentWithdrawalEncryptionFailureDoesNotFreezeMoneyOrOverwriteAccount(t *testing.T) {
	setupAgentWithdrawalTest(t, 1000)
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", "")
	_, err := SaveAgentPayoutAccount(42, "replacement@example.com", "Replacement Recipient")
	require.ErrorIs(t, err, common.ErrAgentPayoutEncryptionUnavailable)
	_, err = RequestAgentWithdrawal(42, 400, "request-no-key")
	require.ErrorIs(t, err, common.ErrAgentPayoutEncryptionUnavailable)
	wallet, err := GetAgentWallet(42)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), wallet.AvailableCents)
	assert.Zero(t, wallet.FrozenCents)
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("w", 32))))
	record, err := RequestAgentWithdrawal(42, 400, "request-restored-key")
	require.NoError(t, err)
	detail, err := GetAgentWithdrawalForAdmin(record.ID)
	require.NoError(t, err)
	assert.Equal(t, "recipient@example.com", detail.Account)
}

func TestAgentConcurrentWithdrawalsCannotOverdraw(t *testing.T) {
	setupAgentWithdrawalTest(t, 1000)
	start := make(chan struct{})
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for _, requestID := range []string{"concurrent-first", "concurrent-second"} {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			<-start
			_, err := RequestAgentWithdrawal(42, 700, id)
			errors <- err
		}(requestID)
	}
	close(start)
	workers.Wait()
	close(errors)
	successes, insufficient := 0, 0
	for err := range errors {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrAgentInsufficientCash)
			insufficient++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, insufficient)
	wallet, err := GetAgentWallet(42)
	require.NoError(t, err)
	assert.Equal(t, int64(300), wallet.AvailableCents)
	assert.Equal(t, int64(700), wallet.FrozenCents)
	_, count, err := ListAgentWithdrawals(42, "", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

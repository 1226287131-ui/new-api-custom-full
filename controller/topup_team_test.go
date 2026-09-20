package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTeamEpayCallbackTest(t *testing.T) *model.TopUp {
	t.Helper()
	gin.SetMode(gin.TestMode)
	confirmPaymentComplianceForTest(t)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousBatch, previousQuota := common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit
	previousAddress, previousMerchant, previousKey := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey
	previousMethods := operation_setting.PayMethods
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "epay-callback.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = false, false, 500000
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://pay.example.invalid", "merchant-test", "epay-test-signing-key"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = previousRedis, previousBatch, previousQuota
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = previousAddress, previousMerchant, previousKey
		operation_setting.PayMethods = previousMethods
		assert.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.AgentPolicy{}, &model.AgentCommission{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{Id: 41, Username: "epay-referrer", AffCode: "epay-ref", Quota: 17, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 42, Username: "epay-customer", AffCode: "epay-own", Quota: 7, InviterId: 41, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, model.SaveAgentPolicy(&model.AgentPolicy{Enabled: true, Mode: model.AgentModeCredit,
		CreditRateBPS: 500, CashRateBPS: 500, MinimumWithdrawalCents: 1000}))
	order := &model.TopUp{UserId: 42, Amount: 1000, Money: 100, TradeNo: "epay-team-order", PaymentProvider: model.PaymentProviderEpay,
		PaymentMethod: "alipay", Status: common.TopUpStatusPending}
	require.NoError(t, model.SnapshotEpayAgentPolicy(order, 0.1, common.QuotaPerUnit))
	require.NotEmpty(t, order.AgentSnapshot)
	require.NoError(t, order.Insert())
	return order
}

func signedTeamEpayCallback(tradeNo, merchant, money string) map[string]string {
	return epay.GenerateParams(map[string]string{
		"pid": merchant, "type": "alipay", "trade_no": "provider-order-123", "out_trade_no": tradeNo,
		"name": "Account recharge", "money": money, "trade_status": epay.StatusTradeSuccess,
	}, operation_setting.EpayKey)
}

func invokeTeamEpayCallback(method string, params map[string]string) *httptest.ResponseRecorder {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	path, body := "/api/user/epay/notify", ""
	if method == http.MethodGet {
		path += "?" + values.Encode()
	} else {
		body = values.Encode()
	}
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	EpayNotify(context)
	return response
}

func TestTeamEpayCallbackRejectsInvalidPaymentWithoutCredit(t *testing.T) {
	for _, test := range []struct {
		name, merchant, money string
		tamperSignature       bool
	}{
		{"invalid signature", "merchant-test", "100.00", true},
		{"signed wrong merchant", "other-merchant", "100.00", false},
		{"signed underpayment", "merchant-test", "99.99", false},
		{"signed overpayment", "merchant-test", "100.01", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := setupTeamEpayCallbackTest(t)
			params := signedTeamEpayCallback(order.TradeNo, test.merchant, test.money)
			if test.tamperSignature {
				params["sign"] = strings.Repeat("0", 32)
			}
			response := invokeTeamEpayCallback(http.MethodPost, params)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "fail", response.Body.String())
			var stored model.TopUp
			require.NoError(t, model.DB.First(&stored, order.Id).Error)
			assert.Equal(t, common.TopUpStatusPending, stored.Status)
			assert.Zero(t, stored.CompleteTime)
			var customer, referrer model.User
			require.NoError(t, model.DB.First(&customer, 42).Error)
			require.NoError(t, model.DB.First(&referrer, 41).Error)
			assert.Equal(t, 7, customer.Quota)
			assert.Zero(t, customer.QuotaCreditTotal)
			assert.Equal(t, 17, referrer.Quota)
			var events, logs int64
			require.NoError(t, model.DB.Model(&model.AgentCommission{}).Count(&events).Error)
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeTopup).Count(&logs).Error)
			assert.Zero(t, events)
			assert.Zero(t, logs)
		})
	}
}

func TestTeamEpayCallbackTransactionFailureIsRetryableAndAtomic(t *testing.T) {
	order := setupTeamEpayCallbackTest(t)
	injected := errors.New("injected commission storage failure")
	const callbackName = "test:fail_team_commission_insert"
	// Fail the final durable event insert, after the balance and order updates.
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*model.AgentCommission); ok {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove(callbackName) })
	params := signedTeamEpayCallback(order.TradeNo, operation_setting.EpayId, "100.00")
	response := invokeTeamEpayCallback(http.MethodPost, params)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "fail", response.Body.String())
	assert.NotContains(t, response.Body.String(), injected.Error())
	var customer model.User
	require.NoError(t, model.DB.First(&customer, 42).Error)
	assert.Equal(t, 7, customer.Quota)
	assert.Zero(t, customer.QuotaCreditTotal)
	var stored model.TopUp
	require.NoError(t, model.DB.First(&stored, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, stored.Status)
	assert.Zero(t, stored.CompleteTime)
	var events, logs int64
	require.NoError(t, model.DB.Model(&model.AgentCommission{}).Count(&events).Error)
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("type = ?", model.LogTypeTopup).Count(&logs).Error)
	assert.Zero(t, events)
	assert.Zero(t, logs)
	require.NoError(t, model.DB.Callback().Create().Remove(callbackName))
	response = invokeTeamEpayCallback(http.MethodPost, params)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "success", response.Body.String())
	require.NoError(t, model.DB.First(&customer, 42).Error)
	assert.Equal(t, 500000007, customer.Quota)
	require.NoError(t, model.DB.First(&stored, order.Id).Error)
	assert.Equal(t, common.TopUpStatusSuccess, stored.Status)
	require.NoError(t, model.DB.Model(&model.AgentCommission{}).Count(&events).Error)
	assert.Equal(t, int64(1), events)
}

func TestTeamEpayCallbackDuplicateGetAndPostCreditOnlyOnce(t *testing.T) {
	order := setupTeamEpayCallbackTest(t)
	params := signedTeamEpayCallback(order.TradeNo, operation_setting.EpayId, "100.00")
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		response := invokeTeamEpayCallback(method, params)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "success", response.Body.String())
	}
	var customer, referrer model.User
	require.NoError(t, model.DB.First(&customer, 42).Error)
	require.NoError(t, model.DB.First(&referrer, 41).Error)
	assert.Equal(t, 500000007, customer.Quota)
	assert.Equal(t, int64(500000000), customer.QuotaCreditTotal)
	assert.Equal(t, 17, referrer.Quota)
	var events []model.AgentCommission
	require.NoError(t, model.DB.Find(&events).Error)
	require.Len(t, events, 1)
	assert.Equal(t, model.AgentCommissionPending, events[0].Status)
	assert.Equal(t, 41, events[0].ReferrerID)
	assert.Equal(t, order.Id, events[0].TopUpID)
	assert.Equal(t, int64(10000), events[0].PaidCents)
	assert.Equal(t, 25000000, events[0].CreditQuota)
	require.NoError(t, model.ProcessPendingAgentCommissions(100))
	require.NoError(t, model.ProcessPendingAgentCommissions(100))
	response := invokeTeamEpayCallback(http.MethodPost, params)
	require.Equal(t, "success", response.Body.String())
	require.NoError(t, model.DB.First(&customer, 42).Error)
	require.NoError(t, model.DB.First(&referrer, 41).Error)
	assert.Equal(t, 500000007, customer.Quota)
	assert.Equal(t, 25000017, referrer.Quota)
	assert.Equal(t, int64(25000000), referrer.QuotaCreditTotal)
	var stored model.TopUp
	require.NoError(t, model.DB.First(&stored, order.Id).Error)
	assert.Equal(t, common.TopUpStatusSuccess, stored.Status)
	assert.Positive(t, stored.CompleteTime)
	require.NoError(t, model.DB.Find(&events).Error)
	require.Len(t, events, 1)
	assert.Equal(t, model.AgentCommissionSettled, events[0].Status)
	var logs int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", 42, model.LogTypeTopup).Count(&logs).Error)
	assert.Equal(t, int64(1), logs)
}

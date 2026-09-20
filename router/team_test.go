package router

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type teamRouteFixture struct {
	engine   *gin.Engine
	tokens   map[int]string
	identity map[int]service.AuthIdentity
}

func setupTeamRouteTest(t *testing.T) teamRouteFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
	previousRateLimit := common.CriticalRateLimitEnable
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "team-routes.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "team-route-security-test-secret"
	common.CriticalRateLimitEnable = false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		common.CriticalRateLimitEnable = previousRateLimit
		assert.NoError(t, sqlDB.Close())
	})
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32))))
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Log{},
		&model.AgentPolicy{}, &model.AgentCommission{}, &model.AgentWallet{}, &model.AgentPayoutAccount{}, &model.AgentWithdrawal{}))
	require.NoError(t, model.SaveAgentPolicy(&model.AgentPolicy{Enabled: true, Mode: model.AgentModeCash, MinimumWithdrawalCents: 100}))
	fixture := teamRouteFixture{engine: gin.New(), tokens: map[int]string{}, identity: map[int]service.AuthIdentity{}}
	for _, account := range []struct{ id, role int }{{42, common.RoleCommonUser}, {43, common.RoleCommonUser}, {44, common.RoleAdminUser}} {
		user := &model.User{Id: account.id, Username: fmt.Sprintf("team-user-%d", account.id), Password: "placeholder",
			Role: account.role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: fmt.Sprintf("team%d", account.id)}
		require.NoError(t, db.Create(user).Error)
		bundle, err := service.CreateLoginSession(user.Id, "password", "192.0.2.42", "team-test")
		require.NoError(t, err)
		identity, err := service.ParseAccessToken(bundle.AccessToken)
		require.NoError(t, err)
		fixture.tokens[user.Id], fixture.identity[user.Id] = bundle.AccessToken, identity
	}
	for _, id := range []int{42, 43} {
		require.NoError(t, db.Create(&model.AgentWallet{UserID: id, AvailableCents: int64(id * 100)}).Error)
		_, err := model.SaveAgentPayoutAccount(id, fmt.Sprintf("recipient-%d@example.com", id), fmt.Sprintf("Recipient %d", id))
		require.NoError(t, err)
		_, err = model.RequestAgentWithdrawal(id, 100, fmt.Sprintf("request-initial-%d", id))
		require.NoError(t, err)
		require.NoError(t, db.Create(&model.AgentCommission{TopUpID: id, TradeNo: fmt.Sprintf("private-order-%d", id),
			UserID: id + 100, ReferrerID: id, Status: model.AgentCommissionPending, Mode: model.AgentModeCash,
			CashCents: int64(id), LastError: "private-database-error"}).Error)
	}
	registerTeamRoutes(fixture.engine.Group("/api"))
	return fixture
}

func (fixture teamRouteFixture) request(method, path, body string, userID int, proof string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token := fixture.tokens[userID]; token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if proof != "" {
		request.Header.Set("X-Security-Proof", proof)
	}
	response := httptest.NewRecorder()
	fixture.engine.ServeHTTP(response, request)
	return response
}

func TestTeamRoutesEnforceDashboardRoles(t *testing.T) {
	fixture := setupTeamRouteTest(t)
	response := fixture.request(http.MethodGet, "/api/team/self", "", 0, "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/team/admin/policy"},
		{http.MethodGet, "/api/team/admin/commissions"},
		{http.MethodGet, "/api/team/admin/withdrawals"},
		{http.MethodPost, "/api/team/admin/withdrawals/1/payout"},
		{http.MethodPost, "/api/team/admin/withdrawals/1/review"},
		{http.MethodPut, "/api/team/admin/policy"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			response := fixture.request(route.method, route.path, `{}`, 42, "")
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Contains(t, response.Body.String(), "AUTH_INSUFFICIENT_PRIVILEGE")
		})
	}
	response = fixture.request(http.MethodGet, "/api/team/admin/policy", "", 44, "")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"success":true`)
	response = fixture.request(http.MethodPut, "/api/team/admin/policy", `{"enabled":false}`, 44, "")
	assert.Equal(t, http.StatusForbidden, response.Code)
	policy, err := model.GetAgentPolicy()
	require.NoError(t, err)
	assert.True(t, policy.Enabled)
}

func TestTeamSelfRoutesScopeRowsAndHidePayoutSecrets(t *testing.T) {
	fixture := setupTeamRouteTest(t)
	response := fixture.request(http.MethodGet, "/api/team/self?user_id=43&referrer_id=43", "", 42, "")
	require.Equal(t, http.StatusOK, response.Code)
	var self struct {
		Success bool `json:"success"`
		Data    struct {
			Wallet  model.AgentWallet            `json:"wallet"`
			Summary model.AgentCommissionSummary `json:"summary"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &self))
	require.True(t, self.Success, response.Body.String())
	assert.Equal(t, 42, self.Data.Wallet.UserID)
	assert.Equal(t, int64(4100), self.Data.Wallet.AvailableCents)
	assert.Equal(t, int64(42), self.Data.Summary.PendingCashCents)
	assert.NotContains(t, response.Body.String(), "recipient-42@example.com")
	assert.NotContains(t, response.Body.String(), "Recipient 42")

	response = fixture.request(http.MethodGet, "/api/team/withdrawals?user_id=43", "", 42, "")
	var withdrawals struct {
		Success bool `json:"success"`
		Data    struct {
			Total int                     `json:"total"`
			Items []model.AgentWithdrawal `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &withdrawals))
	require.True(t, withdrawals.Success, response.Body.String())
	assert.Equal(t, 1, withdrawals.Data.Total)
	require.Len(t, withdrawals.Data.Items, 1)
	assert.Equal(t, 42, withdrawals.Data.Items[0].UserID)
	assert.NotEmpty(t, withdrawals.Data.Items[0].AccountMasked)
	assert.NotContains(t, response.Body.String(), "recipient-42@example.com")
	assert.NotContains(t, response.Body.String(), "Recipient 42")
	assert.NotContains(t, response.Body.String(), "ciphertext")

	response = fixture.request(http.MethodGet, "/api/team/commissions?user_id=43&referrer_id=43", "", 42, "")
	var commissions struct {
		Success bool `json:"success"`
		Data    struct {
			Total int                     `json:"total"`
			Items []model.AgentCommission `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &commissions))
	require.True(t, commissions.Success, response.Body.String())
	assert.Equal(t, 1, commissions.Data.Total)
	require.Len(t, commissions.Data.Items, 1)
	assert.Equal(t, 42, commissions.Data.Items[0].ReferrerID)
	assert.Zero(t, commissions.Data.Items[0].TopUpID)
	assert.Empty(t, commissions.Data.Items[0].TradeNo)
	assert.Empty(t, commissions.Data.Items[0].LastError)
}

func TestTeamWritesIgnoreForgedUserID(t *testing.T) {
	fixture := setupTeamRouteTest(t)
	payoutProof, _, err := service.IssueSecurityProof(fixture.identity[42], "2fa", []string{"team.payout.write"})
	require.NoError(t, err)
	response := fixture.request(http.MethodPut, "/api/team/payout?user_id=43",
		`{"user_id":43,"account":"new-owner@example.com","name":"New Owner"}`, 42, payoutProof)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"success":true`)
	assert.NotContains(t, response.Body.String(), "new-owner@example.com")
	withdrawalProof, _, err := service.IssueSecurityProof(fixture.identity[42], "passkey", []string{"team.withdrawal.write"})
	require.NoError(t, err)
	response = fixture.request(http.MethodPost, "/api/team/withdrawals?user_id=43",
		`{"user_id":43,"amount_cents":200,"request_id":"request-forged-user"}`, 42, withdrawalProof)
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Success bool                  `json:"success"`
		Data    model.AgentWithdrawal `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	assert.Equal(t, 42, result.Data.UserID)
	detail, err := model.GetAgentWithdrawalForAdmin(result.Data.ID)
	require.NoError(t, err)
	assert.Equal(t, "new-owner@example.com", detail.Account)
	wallet, err := model.GetAgentWallet(42)
	require.NoError(t, err)
	assert.Equal(t, int64(3900), wallet.AvailableCents)
	other, err := model.GetAgentWallet(43)
	require.NoError(t, err)
	assert.Equal(t, int64(4200), other.AvailableCents)
	otherAccount, err := model.GetAgentPayoutAccount(43)
	require.NoError(t, err)
	assert.NotEqual(t, result.Data.AccountMasked, otherAccount.AccountMasked)
}

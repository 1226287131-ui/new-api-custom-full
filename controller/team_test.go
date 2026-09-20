package controller

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
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

func setupTeamProofTest(t *testing.T) (service.AuthIdentity, *model.AgentWithdrawal) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "team-proof.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "team-controller-proof-test-secret"
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		assert.NoError(t, sqlDB.Close())
	})
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.Log{}, &model.AgentPolicy{},
		&model.AgentWallet{}, &model.AgentPayoutAccount{}, &model.AgentWithdrawal{}))
	require.NoError(t, model.SaveAgentPolicy(&model.AgentPolicy{Enabled: true, Mode: model.AgentModeCash, MinimumWithdrawalCents: 100}))
	require.NoError(t, db.Create(&model.User{Id: 42, Username: "proof-user", Password: "placeholder", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}).Error)
	require.NoError(t, db.Create(&model.TwoFA{UserId: 42, Secret: "fixture-enrolled-secret", IsEnabled: true}).Error)
	bundle, err := service.CreateLoginSession(42, "2fa", "192.0.2.42", "team-proof-test")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.AgentWallet{UserID: 42, AvailableCents: 1000}).Error)
	_, err = model.SaveAgentPayoutAccount(42, "proof-recipient@example.com", "Proof Recipient")
	require.NoError(t, err)
	withdrawal, err := model.RequestAgentWithdrawal(42, 300, "request-initial-proof")
	require.NoError(t, err)
	require.NoError(t, db.First(withdrawal, withdrawal.ID).Error)
	require.NotEmpty(t, withdrawal.AccountCiphertext)
	return identity, withdrawal
}

func issueTeamProof(t *testing.T, identity service.AuthIdentity, method, scope string) string {
	t.Helper()
	operation := service.VerificationOperation{Scope: scope}
	if scope == service.VerificationScopeChannelKeyRead {
		operation.Context = []byte(`{"channel_id":123}`)
	}
	binding, err := service.BindVerificationOperation(operation)
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, method, binding)
	require.NoError(t, err)
	return proof
}

func teamProofContext(identity service.AuthIdentity, proof, body string, withdrawalID int64) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/team", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Request.Header.Set("X-Security-Proof", proof)
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)
	context.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(withdrawalID, 10)}}
	return context, response
}

func TestTeamSensitiveOperationsRejectInvalidProofWithoutMutationOrDisclosure(t *testing.T) {
	identity, withdrawal := setupTeamProofTest(t)
	for _, operation := range []struct {
		name, scope, body string
		handler           gin.HandlerFunc
	}{
		{"bind payout", securityProofScopeTeamPayoutWrite, `{"account":"attacker@example.com","name":"Attacker"}`, SaveTeamPayoutAccount},
		{"request withdrawal", securityProofScopeTeamWithdrawalWrite, `{"amount_cents":200,"request_id":"request-without-proof"}`, RequestTeamWithdrawal},
		{"review withdrawal", securityProofScopeTeamWithdrawalReview, `{"action":"reject","reason":"Unauthorized rejection"}`, AdminReviewTeamWithdrawal},
		{"read payout", securityProofScopeTeamWithdrawalRead, `{}`, AdminReadTeamWithdrawal},
	} {
		t.Run(operation.name, func(t *testing.T) {
			wrongScope := issueTeamProof(t, identity, "2fa", "channel.key.read")
			otherSession := identity
			otherSession.SessionID = "another-session"
			wrongSession := issueTeamProof(t, otherSession, "2fa", operation.scope)
			wrongMethod := issueTeamProof(t, identity, "password", operation.scope)
			for _, test := range []struct{ name, proof, code string }{
				{"missing proof", "", "SECURITY_PROOF_REQUIRED"},
				{"wrong scope", wrongScope, "SECURITY_PROOF_SCOPE_MISMATCH"},
				{"wrong session", wrongSession, "SECURITY_PROOF_INVALID"},
				{"wrong verification method", wrongMethod, "SECURITY_PROOF_METHOD_MISMATCH"},
			} {
				t.Run(test.name, func(t *testing.T) {
					context, response := teamProofContext(identity, test.proof, operation.body, withdrawal.ID)
					operation.handler(context)
					require.Equal(t, http.StatusForbidden, response.Code)
					assert.Contains(t, response.Body.String(), test.code)
					assert.NotContains(t, response.Body.String(), "proof-recipient@example.com")
					assert.NotContains(t, response.Body.String(), "Proof Recipient")
					assert.NotContains(t, response.Body.String(), withdrawal.AccountCiphertext)
					wallet, err := model.GetAgentWallet(42)
					require.NoError(t, err)
					assert.Equal(t, int64(700), wallet.AvailableCents)
					assert.Equal(t, int64(300), wallet.FrozenCents)
					rows, count, err := model.ListAgentWithdrawals(42, "", 0, 20)
					require.NoError(t, err)
					assert.Equal(t, int64(1), count)
					require.Len(t, rows, 1)
					assert.Equal(t, model.AgentWithdrawalPending, rows[0].Status)
					var payout model.AgentPayoutAccount
					require.NoError(t, model.DB.Where("user_id = ?", 42).Take(&payout).Error)
					account, err := common.DecryptAgentPayoutText(payout.AccountCiphertext, "42:account")
					require.NoError(t, err)
					assert.Equal(t, "proof-recipient@example.com", account)
				})
			}
		})
	}
}

func TestTeamPayoutDisclosureRequiresCorrectSessionAndReadScope(t *testing.T) {
	identity, withdrawal := setupTeamProofTest(t)
	proof := issueTeamProof(t, identity, "2fa", securityProofScopeTeamWithdrawalRead)
	context, response := teamProofContext(identity, proof, `{}`, withdrawal.ID)
	AdminReadTeamWithdrawal(context)
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Success bool                        `json:"success"`
		Data    model.AgentWithdrawalDetail `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	assert.Equal(t, "proof-recipient@example.com", result.Data.Account)
	assert.Equal(t, "Proof Recipient", result.Data.Name)
	assert.NotContains(t, response.Body.String(), withdrawal.AccountCiphertext)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	require.NotEmpty(t, logs)
	encoded, err := common.Marshal(logs)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "proof-recipient@example.com")
	assert.NotContains(t, string(encoded), "Proof Recipient")
	context, response = teamProofContext(identity, proof, `{}`, withdrawal.ID)
	AdminReadTeamWithdrawal(context)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONSUMED")
}

func TestTeamCashPolicyCannotEnableWithoutEncryption(t *testing.T) {
	identity, _ := setupTeamProofTest(t)
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", "")
	context, response := teamProofContext(identity, "", `{"enabled":true,"mode":"cash","credit_rate_bps":500,"cash_rate_bps":500,"minimum_withdrawal_cents":100}`, 0)
	AdminSaveTeamPolicy(context)
	var result struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.False(t, result.Success)
	assert.Equal(t, "Payout account encryption is not configured", result.Message)
	policy, err := model.GetAgentPolicy()
	require.NoError(t, err)
	assert.Zero(t, policy.CashRateBPS, "failed settings write must leave the stored policy unchanged")
}

func TestTeamPageQueryBoundsUntrustedPagination(t *testing.T) {
	for _, tc := range []struct {
		query      string
		page, size int
	}{
		{"?p=-4&page_size=-2", 1, 20},
		{"?p=2147483647&page_size=2147483647", 1000000, 100},
		{"?p=2&page_size=15", 2, 15},
	} {
		t.Run(tc.query, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodGet, "/api/team/commissions"+tc.query, nil)
			page := teamPageQuery(context)
			assert.Equal(t, tc.page, page.Page)
			assert.Equal(t, tc.size, page.PageSize)
		})
	}
}

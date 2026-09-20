package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTeamVerificationPolicy(t *testing.T) {
	for _, scope := range []string{VerificationScopeTeamPayoutWrite, VerificationScopeTeamWithdrawalWrite, VerificationScopeTeamWithdrawalReview, VerificationScopeTeamWithdrawalRead, VerificationScopeTeamReferralWrite} {
		t.Run(scope, func(t *testing.T) {
			operation := VerificationOperation{Scope: scope}
			if scope == VerificationScopeTeamReferralWrite {
				operation.Context = []byte(`{"user_id":42,"expected_inviter_id":0,"inviter_id":43,"reason":"Correction"}`)
			}
			if scope == VerificationScopeTeamPayoutWrite {
				operation.Context = []byte(`{"account":"owner@example.com","name":"Owner"}`)
			}
			binding, err := BindVerificationOperation(operation)
			require.NoError(t, err)
			assert.Equal(t, scope, binding.Scope)
			assert.NotEmpty(t, binding.ContextHash)
			_, err = BindVerificationOperation(VerificationOperation{Scope: scope, Context: []byte(`{"unexpected":true}`)})
			assert.ErrorIs(t, err, ErrVerificationContextInvalid)
			methods, err := securityVerificationPolicy(scope, model.UserVerificationState{HasPassword: true})
			require.NoError(t, err)
			emailScope := scope == VerificationScopeTeamPayoutWrite || scope == VerificationScopeTeamReferralWrite
			if emailScope {
				require.Len(t, methods, 1)
				assert.Equal(t, VerificationMethodEmail, methods[0].Method)
				assert.False(t, methods[0].Available)
				assert.Equal(t, ErrSecurityEmailRequired.Error(), methods[0].Reason)
			} else {
				assert.Empty(t, methods, "a password must not authorize a withdrawal")
			}
			methods, err = securityVerificationPolicy(scope, model.UserVerificationState{HasPassword: true, HasTwoFA: true})
			require.NoError(t, err)
			if emailScope {
				require.Len(t, methods, 2)
				methods = methods[1:]
			}
			require.Len(t, methods, 1)
			assert.Equal(t, VerificationMethodTwoFA, methods[0].Method)
			assert.True(t, methods[0].Available)
			methods, err = securityVerificationPolicy(scope, model.UserVerificationState{HasTwoFA: true, TwoFALocked: true})
			require.NoError(t, err)
			if emailScope {
				require.Len(t, methods, 2)
				methods = methods[1:]
			}
			require.Len(t, methods, 1)
			assert.False(t, methods[0].Available)
		})
	}
}

func TestTeamVerificationAdminScopesRejectOrdinaryUsers(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.TwoFA{}, &model.PasskeyCredential{}))
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "fixture-enrolled-secret", IsEnabled: true}).Error)
	identity := AuthIdentity{UserID: user.Id, UserAuthVersion: user.AuthVersion}
	for _, scope := range []string{VerificationScopeTeamWithdrawalRead, VerificationScopeTeamWithdrawalReview, VerificationScopeTeamReferralWrite} {
		_, err := GetVerificationRequirements(identity, scope)
		assert.ErrorIs(t, err, ErrVerificationForbidden)
	}
	_, err := GetVerificationRequirements(identity, VerificationScopeTeamPayoutWrite)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleAdminUser).Error)
	for _, scope := range []string{VerificationScopeTeamWithdrawalRead, VerificationScopeTeamWithdrawalReview, VerificationScopeTeamReferralWrite} {
		_, err := GetVerificationRequirements(identity, scope)
		require.NoError(t, err)
	}
}

func TestTeamReferralVerificationContextRejectsMissingOrUnexpectedFields(t *testing.T) {
	for _, context := range []string{
		`{}`, `null`,
		`{"user_id":42,"expected_inviter_id":0,"inviter_id":43,"other":"x"}`,
		`{"user_id":42,"expected_inviter_id":0,"other":43,"reason":"Correction"}`,
		`{"user_id":42,"expected_inviter_id":null,"inviter_id":43,"reason":"Correction"}`,
		`{"user_id":42,"expected_inviter_id":0,"inviter_id":null,"reason":"Correction"}`,
		`{"user_id":42,"expected_inviter_id":0,"inviter_id":42,"reason":"Self"}`,
		`{"user_id":42,"expected_inviter_id":-1,"inviter_id":43,"reason":"Correction"}`,
		`{"user_id":42,"expected_inviter_id":0,"inviter_id":43,"reason":"  "}`,
		`{"user_id":42,"expected_inviter_id":0,"inviter_id":43,"reason":"` + strings.Repeat("x", 201) + `"}`,
	} {
		_, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeTeamReferralWrite, Context: []byte(context)})
		assert.ErrorIs(t, err, ErrVerificationContextInvalid, context)
	}
	canonical, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeTeamReferralWrite,
		Context: []byte(`{"user_id":42,"expected_inviter_id":43,"inviter_id":0,"reason":"Correction"}`)})
	require.NoError(t, err)
	spaced, err := BindVerificationOperation(VerificationOperation{Scope: VerificationScopeTeamReferralWrite,
		Context: []byte(`{"reason":" Correction ","inviter_id":0,"expected_inviter_id":43,"user_id":42}`)})
	require.NoError(t, err)
	assert.Equal(t, canonical, spaced)
}

// External DSNs must point at dedicated, disposable test databases.
func TestTeamEmailVerificationDatabaseMatrix(t *testing.T) {
	useTestSessionSecret(t)
	oldServer, oldPort, oldFrom := common.SMTPServer, common.SMTPPort, common.SMTPFrom
	common.SMTPServer, common.SMTPPort, common.SMTPFrom = "127.0.0.1", 25, "sender@example.com"
	t.Cleanup(func() { common.SMTPServer, common.SMTPPort, common.SMTPFrom = oldServer, oldPort, oldFrom })
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""},
		{common.DatabaseTypeMySQL, "TEST_SECURITY_EMAIL_MYSQL_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_SECURITY_EMAIL_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect.env != "" {
				dsn := os.Getenv(dialect.env)
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				if dialect.kind == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			if dialect.kind == common.DatabaseTypeSQLite {
				sqlDB.SetMaxOpenConns(1)
			}
			oldDB, oldRedis := model.DB, common.RedisEnabled
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB, common.RedisEnabled = db, false
			common.SetDatabaseTypes(dialect.kind, dialect.kind)
			t.Cleanup(func() {
				model.DB, common.RedisEnabled = oldDB, oldRedis
				common.SetDatabaseTypes(oldMain, oldLog)
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.AgentPayoutAccount{}, &model.AgentReferralGuard{}, &model.AgentReferralChange{}))
			query := "select version()"
			if dialect.kind == common.DatabaseTypeSQLite {
				query = "select sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)
			runTeamEmailVerificationCases(t)
		})
	}
}

type teamEmailTestFixture struct {
	user       *model.User
	identity   AuthIdentity
	operation  VerificationOperation
	binding    VerificationBinding
	token      string
	flow       *model.AuthFlow
	code, hash string
}

func newTeamEmailTestFixture(t *testing.T) teamEmailTestFixture {
	t.Helper()
	user := &model.User{Username: "email-" + common.GetUUID()[:12], AffCode: common.GetUUID()[:12], Email: "owner@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, model.DB.Create(user).Error)
	bundle, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "email-test")
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	operation := VerificationOperation{Scope: VerificationScopeTeamPayoutWrite, Context: []byte(`{"account":"owner@example.com","name":"Owner"}`)}
	binding, err := BindVerificationOperation(operation)
	require.NoError(t, err)
	code := "628491"
	hash, err := common.Password2Hash(code)
	require.NoError(t, err)
	token, flow, _, err := model.SaveSecurityEmailChallenge(identity, "", binding.Scope, binding.ContextHash, user.Email, hash)
	require.NoError(t, err)
	require.NoError(t, model.DB.First(flow, flow.Id).Error)
	require.NoError(t, model.ActivateSecurityEmailChallenge(identity, token, binding.Scope, binding.ContextHash, user.Email, hash))
	return teamEmailTestFixture{user: user, identity: identity, operation: operation, binding: binding, token: token, flow: flow, code: code, hash: hash}
}

func (f teamEmailTestFixture) verify(code string) (*SecurityProof, error) {
	return VerifySecurityInput(f.identity, VerificationInput{Method: VerificationMethodEmail, Scope: f.operation.Scope, Context: f.operation.Context, FlowToken: f.token, Code: code})
}

func (f teamEmailTestFixture) allowResend(t *testing.T) {
	t.Helper()
	var flow model.AuthFlow
	require.NoError(t, model.DB.Where("purpose = ? AND user_id = ?", model.AuthFlowPurposeSecurityEmailRate, f.user.Id).Order("id DESC").First(&flow).Error)
	var payload map[string]any
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	payload["resend_at"] = time.Now().Add(-time.Second).Unix()
	encoded, err := common.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&flow).Update("payload", string(encoded)).Error)
}

func runTeamEmailVerificationCases(t *testing.T) {
	t.Run("admin-referral-proof-and-role-recheck", func(t *testing.T) {
		for _, revokeRole := range []bool{false, true} {
			t.Run(fmt.Sprint(revokeRole), func(t *testing.T) {
				f := newTeamEmailTestFixture(t)
				require.NoError(t, model.DB.Model(f.user).Update("role", common.RoleAdminUser).Error)
				member := &model.User{Username: "member-" + common.GetUUID()[:12], AffCode: common.GetUUID()[:12], Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
				inviter := &model.User{Username: "inviter-" + common.GetUUID()[:12], AffCode: common.GetUUID()[:12], Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
				require.NoError(t, model.DB.Create(member).Error)
				require.NoError(t, model.DB.Create(inviter).Error)
				context, err := common.Marshal(TeamReferralWriteContext{UserID: member.Id, ExpectedInviterID: 0, InviterID: inviter.Id, Reason: "Correction"})
				require.NoError(t, err)
				f.operation = VerificationOperation{Scope: VerificationScopeTeamReferralWrite, Context: context}
				f.binding, err = BindVerificationOperation(f.operation)
				require.NoError(t, err)
				f.allowResend(t)
				f.token, _, _, err = model.SaveSecurityEmailChallenge(f.identity, "", f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash)
				require.NoError(t, err)
				require.NoError(t, model.ActivateSecurityEmailChallenge(f.identity, f.token, f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash))
				proof, err := f.verify(f.code)
				require.NoError(t, err)
				authorization, err := ConsumeOperationProof(proof.ProofToken, f.identity, f.operation)
				require.NoError(t, err)
				assert.Equal(t, f.user.Email, authorization.EmailSnapshot)
				if revokeRole {
					require.NoError(t, model.DB.Model(f.user).Update("role", common.RoleCommonUser).Error)
				}
				change, err := model.ChangeAgentReferrer(f.user.Id, member.Id, 0, inviter.Id, "Correction", authorization)
				if revokeRole {
					assert.ErrorIs(t, err, model.ErrAgentReferralForbidden)
				} else {
					require.NoError(t, err)
					assert.Equal(t, inviter.Id, change.NewInviterID)
					assert.Equal(t, f.user.Id, change.OperatorID)
				}
				require.NoError(t, model.DB.First(member, member.Id).Error)
				var count int64
				require.NoError(t, model.DB.Model(&model.AgentReferralChange{}).Where("user_id = ?", member.Id).Count(&count).Error)
				if revokeRole {
					assert.Zero(t, member.InviterId)
					assert.Zero(t, count)
				} else {
					assert.Equal(t, inviter.Id, member.InviterId)
					assert.EqualValues(t, 1, count)
				}
			})
		}
	})
	t.Run("single-use-bound-proof-and-payout", func(t *testing.T) {
		f := newTeamEmailTestFixture(t)
		requirements, err := GetVerificationRequirements(f.identity, f.operation.Scope)
		require.NoError(t, err)
		assert.Equal(t, common.MaskEmail(f.user.Email), requirements.Email)
		require.NotEmpty(t, requirements.Methods)
		assert.Equal(t, VerificationMethodEmail, requirements.Methods[0].Method)
		assert.True(t, requirements.Methods[0].Available)
		proof, err := f.verify(f.code)
		require.NoError(t, err)
		_, err = f.verify(f.code)
		assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
		changed := f.operation
		changed.Context = []byte(`{"account":"other@example.com","name":"Owner"}`)
		_, err = ConsumeOperationProof(proof.ProofToken, f.identity, changed)
		assert.ErrorIs(t, err, ErrProofContext)
		authorization, err := ConsumeOperationProof(proof.ProofToken, f.identity, f.operation)
		require.NoError(t, err)
		_, err = ConsumeOperationProof(proof.ProofToken, f.identity, f.operation)
		assert.ErrorIs(t, err, ErrProofConsumed)
		account, err := model.SaveAgentPayoutAccount(f.user.Id, "owner@example.com", "Owner", authorization)
		require.NoError(t, err)
		assert.Equal(t, f.user.Id, account.UserID)
		assert.Empty(t, account.AccountCiphertext)
		assert.NotEqual(t, "owner@example.com", account.AccountMasked)
	})
	t.Run("binding-expiry-and-session", func(t *testing.T) {
		f := newTeamEmailTestFixture(t)
		for _, changed := range []struct {
			identity              AuthIdentity
			scope, context, email string
		}{
			{AuthIdentity{UserID: f.identity.UserID + 1000000, SessionID: f.identity.SessionID, UserAuthVersion: 1, SessionVersion: 1}, f.binding.Scope, f.binding.ContextHash, f.user.Email},
			{AuthIdentity{UserID: f.identity.UserID, SessionID: "other-session", UserAuthVersion: 1, SessionVersion: 1}, f.binding.Scope, f.binding.ContextHash, f.user.Email},
			{f.identity, VerificationScopeTeamWithdrawalWrite, f.binding.ContextHash, f.user.Email},
			{f.identity, f.binding.Scope, "other-context", f.user.Email},
			{f.identity, f.binding.Scope, f.binding.ContextHash, "other@example.com"},
		} {
			assert.Error(t, model.CompleteSecurityEmailChallenge(changed.identity, f.token, changed.scope, changed.context, changed.email, f.code))
		}
		var stored model.AuthFlow
		require.NoError(t, model.DB.First(&stored, f.flow.Id).Error)
		assert.NotContains(t, stored.Payload, f.code)
		assert.NotEqual(t, f.token, stored.TokenHash)
		require.NoError(t, model.DB.Model(&stored).Update("expires_at", time.Now().Add(-time.Second)).Error)
		_, err := f.verify(f.code)
		assert.ErrorIs(t, err, model.ErrAuthFlowExpired)
	})
	t.Run("mailbox-rechecked-at-each-boundary", func(t *testing.T) {
		for _, boundary := range []string{"code", "proof", "mutation", "revoked-session"} {
			t.Run(boundary, func(t *testing.T) {
				f := newTeamEmailTestFixture(t)
				var proof *SecurityProof
				var authorization *model.AuthFlowAuthorization
				var err error
				if boundary != "code" {
					proof, err = f.verify(f.code)
					require.NoError(t, err)
				}
				if boundary == "mutation" || boundary == "revoked-session" {
					authorization, err = ConsumeOperationProof(proof.ProofToken, f.identity, f.operation)
					require.NoError(t, err)
				}
				if boundary == "revoked-session" {
					require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", f.identity.SessionID).Update("revoked_at", time.Now().Unix()).Error)
				} else {
					require.NoError(t, model.DB.Model(f.user).Update("email", "changed@example.com").Error)
				}
				switch boundary {
				case "code":
					_, err = f.verify(f.code)
				case "proof":
					_, err = ConsumeOperationProof(proof.ProofToken, f.identity, f.operation)
				default:
					_, err = model.SaveAgentPayoutAccount(f.user.Id, "owner@example.com", "Owner", authorization)
				}
				assert.Error(t, err)
				var count int64
				require.NoError(t, model.DB.Model(&model.AgentPayoutAccount{}).Where("user_id = ?", f.user.Id).Count(&count).Error)
				assert.Zero(t, count)
			})
		}
	})
	t.Run("attempt-budget-survives-resends-and-new-flows", func(t *testing.T) {
		f := newTeamEmailTestFixture(t)
		for range 2 {
			_, err := f.verify("000000")
			assert.ErrorIs(t, err, model.ErrSecurityEmailInvalid)
		}
		f.allowResend(t)
		_, flow, _, err := model.SaveSecurityEmailChallenge(f.identity, f.token, f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash)
		require.NoError(t, err)
		assert.True(t, flow.ExpiresAt.Equal(f.flow.ExpiresAt))
		require.NoError(t, model.ActivateSecurityEmailChallenge(f.identity, f.token, f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash))
		_, err = f.verify("000000")
		assert.ErrorIs(t, err, model.ErrSecurityEmailInvalid)
		f.allowResend(t)
		f.token, flow, _, err = model.SaveSecurityEmailChallenge(f.identity, "", f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash)
		require.NoError(t, err)
		assert.True(t, flow.ExpiresAt.Equal(f.flow.ExpiresAt))
		require.NoError(t, model.ActivateSecurityEmailChallenge(f.identity, f.token, f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash))
		_, err = f.verify("000000")
		assert.ErrorIs(t, err, model.ErrSecurityEmailInvalid)
		_, err = f.verify("000000")
		assert.ErrorIs(t, err, model.ErrSecurityEmailLocked)
		_, err = f.verify(f.code)
		assert.ErrorIs(t, err, model.ErrSecurityEmailLocked)
		f.allowResend(t)
		_, _, _, err = model.SaveSecurityEmailChallenge(f.identity, "", f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash)
		assert.ErrorIs(t, err, model.ErrSecurityEmailLocked)
	})
	t.Run("smtp-failure-preserves-delivered-code", func(t *testing.T) {
		for _, resend := range []bool{false, true} {
			t.Run(fmt.Sprint(resend), func(t *testing.T) {
				f := newTeamEmailTestFixture(t)
				f.allowResend(t)
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				port := listener.Addr().(*net.TCPAddr).Port
				require.NoError(t, listener.Close())
				oldPort := common.SMTPPort
				common.SMTPPort = port
				t.Cleanup(func() { common.SMTPPort = oldPort })
				token := ""
				if resend {
					token = f.token
				}
				_, err = SendSecurityVerificationEmail(f.identity, f.operation, token)
				assert.ErrorIs(t, err, ErrSecurityEmailDelivery)
				_, err = f.verify(f.code)
				require.NoError(t, err)
			})
		}
	})
	t.Run("replacement-code-only-valid-after-delivery", func(t *testing.T) {
		f := newTeamEmailTestFixture(t)
		f.allowResend(t)
		hash, err := common.Password2Hash("718295")
		require.NoError(t, err)
		_, _, _, err = model.SaveSecurityEmailChallenge(f.identity, f.token, f.binding.Scope, f.binding.ContextHash, f.user.Email, hash)
		require.NoError(t, err)
		_, err = f.verify("718295")
		assert.ErrorIs(t, err, model.ErrSecurityEmailInvalid)
		require.NoError(t, model.ActivateSecurityEmailChallenge(f.identity, f.token, f.binding.Scope, f.binding.ContextHash, f.user.Email, hash))
		_, err = f.verify(f.code)
		assert.ErrorIs(t, err, model.ErrSecurityEmailInvalid)
		_, err = f.verify("718295")
		require.NoError(t, err)
	})
	t.Run("concurrent-send-and-consume", func(t *testing.T) {
		f := newTeamEmailTestFixture(t)
		f.allowResend(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				<-start
				_, _, _, err := model.SaveSecurityEmailChallenge(f.identity, "", f.binding.Scope, f.binding.ContextHash, f.user.Email, f.hash)
				results <- err
			})
		}
		close(start)
		workers.Wait()
		close(results)
		successes, cooldowns := 0, 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, model.ErrSecurityEmailCooldown) {
				cooldowns++
			} else {
				require.NoError(t, err)
			}
		}
		assert.Equal(t, 1, successes)
		assert.Equal(t, 1, cooldowns)
		start = make(chan struct{})
		results = make(chan error, 2)
		for range 2 {
			workers.Go(func() {
				<-start
				results <- model.CompleteSecurityEmailChallenge(f.identity, f.token, f.binding.Scope, f.binding.ContextHash, f.user.Email, f.code)
			})
		}
		close(start)
		workers.Wait()
		close(results)
		successes, consumed := 0, 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, model.ErrAuthFlowConsumed) {
				consumed++
			} else {
				require.NoError(t, err)
			}
		}
		assert.Equal(t, 1, successes)
		assert.Equal(t, 1, consumed)
	})
	t.Run("availability-and-non-team-scopes", func(t *testing.T) {
		f := newTeamEmailTestFixture(t)
		for _, scope := range []string{VerificationScopeChannelKeyRead, VerificationScopeTwoFASetup, VerificationScopeAccountDelete, VerificationScopeTeamWithdrawalWrite, VerificationScopeTeamWithdrawalRead, VerificationScopeTeamWithdrawalReview} {
			_, err := RequireVerificationMethod(f.identity, scope, VerificationMethodEmail)
			assert.Error(t, err, scope)
		}
		require.NoError(t, model.DB.Model(f.user).Update("email", "").Error)
		_, err := SendSecurityVerificationEmail(f.identity, f.operation, "")
		assert.ErrorIs(t, err, ErrSecurityEmailRequired)
		require.NoError(t, model.DB.Model(f.user).Update("email", "owner@example.com").Error)
		oldServer := common.SMTPServer
		common.SMTPServer = ""
		defer func() { common.SMTPServer = oldServer }()
		_, err = SendSecurityVerificationEmail(f.identity, f.operation, "")
		assert.ErrorIs(t, err, ErrSecurityEmailUnavailable)
	})
}

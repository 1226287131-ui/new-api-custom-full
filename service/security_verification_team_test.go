package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamVerificationRequiresEnrolledStrongFactor(t *testing.T) {
	for _, scope := range []string{VerificationScopeTeamPayoutWrite, VerificationScopeTeamWithdrawalWrite, VerificationScopeTeamWithdrawalReview, VerificationScopeTeamWithdrawalRead, VerificationScopeTeamReferralWrite} {
		t.Run(scope, func(t *testing.T) {
			operation := VerificationOperation{Scope: scope}
			if scope == VerificationScopeTeamReferralWrite {
				operation.Context = []byte(`{"user_id":42,"expected_inviter_id":0,"inviter_id":43,"reason":"Correction"}`)
			}
			binding, err := BindVerificationOperation(operation)
			require.NoError(t, err)
			assert.Equal(t, scope, binding.Scope)
			assert.NotEmpty(t, binding.ContextHash)
			_, err = BindVerificationOperation(VerificationOperation{Scope: scope, Context: []byte(`{"unexpected":true}`)})
			assert.ErrorIs(t, err, ErrVerificationContextInvalid)
			methods, err := securityVerificationPolicy(scope, model.UserVerificationState{HasPassword: true})
			require.NoError(t, err)
			assert.Empty(t, methods, "a password must not authorize a team payout operation")
			methods, err = securityVerificationPolicy(scope, model.UserVerificationState{HasPassword: true, HasTwoFA: true})
			require.NoError(t, err)
			require.Len(t, methods, 1)
			assert.Equal(t, VerificationMethodTwoFA, methods[0].Method)
			assert.True(t, methods[0].Available)
			methods, err = securityVerificationPolicy(scope, model.UserVerificationState{HasTwoFA: true, TwoFALocked: true})
			require.NoError(t, err)
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

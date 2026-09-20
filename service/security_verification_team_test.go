package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamVerificationRequiresEnrolledStrongFactor(t *testing.T) {
	for _, scope := range []string{VerificationScopeTeamPayoutWrite, VerificationScopeTeamWithdrawalWrite, VerificationScopeTeamWithdrawalReview, VerificationScopeTeamWithdrawalRead} {
		t.Run(scope, func(t *testing.T) {
			binding, err := BindVerificationOperation(VerificationOperation{Scope: scope})
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
	for _, scope := range []string{VerificationScopeTeamWithdrawalRead, VerificationScopeTeamWithdrawalReview} {
		_, err := GetVerificationRequirements(identity, scope)
		assert.ErrorIs(t, err, ErrVerificationForbidden)
	}
	_, err := GetVerificationRequirements(identity, VerificationScopeTeamPayoutWrite)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleAdminUser).Error)
	for _, scope := range []string{VerificationScopeTeamWithdrawalRead, VerificationScopeTeamWithdrawalReview} {
		_, err := GetVerificationRequirements(identity, scope)
		require.NoError(t, err)
	}
}

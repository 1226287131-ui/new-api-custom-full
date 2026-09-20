package model

import (
	"crypto/hmac"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	SecurityEmailTTL         = 10 * time.Minute
	SecurityEmailResendDelay = time.Minute
	SecurityEmailMaxAttempts = 5
)

var (
	ErrSecurityEmailCooldown = errors.New("Please wait before requesting another verification email.")
	ErrSecurityEmailInvalid  = errors.New("Email verification code is incorrect.")
	ErrSecurityEmailLocked   = errors.New("Too many incorrect codes. Try again after the verification window expires.")
)

type SecurityEmailState struct {
	Identity        AuthSessionIdentity `json:"identity"`
	Scope           string              `json:"scope"`
	ContextHash     string              `json:"context_hash"`
	Email           string              `json:"email"`
	CodeHash        string              `json:"code_hash"`
	PendingCodeHash string              `json:"pending_code_hash,omitempty"`
	BudgetID        int64               `json:"budget_id"`
	Delivered       bool                `json:"delivered"`
}

type securityEmailBudget struct {
	FailedAttempts int   `json:"failed_attempts"`
	ResendAt       int64 `json:"resend_at"`
	PendingFlowID  int64 `json:"pending_flow_id"`
}

// The account lock protects the shared send/attempt budget across sessions,
// instances, resends and newly started flows. No usable code is persisted.
func SaveSecurityEmailChallenge(identity AuthSessionIdentity, token, scope, contextHash, email, codeHash string) (string, *AuthFlow, int64, error) {
	var flow *AuthFlow
	var resendAt int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockSecurityEmailAccount(tx, identity, scope, email); err != nil {
			return err
		}
		now := time.Now()
		var budgetFlow AuthFlow
		var budget securityEmailBudget
		err := tx.Where("purpose = ? AND user_id = ? AND expires_at > ?", AuthFlowPurposeSecurityEmailRate, identity.UserID, now).Order("id DESC").First(&budgetFlow).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_, created, createErr := createAuthFlowWithTx(tx, AuthFlowCreate{Purpose: AuthFlowPurposeSecurityEmailRate, UserId: identity.UserID, Payload: "{}", ExpiresAt: now.Add(SecurityEmailTTL)})
			if createErr != nil {
				return createErr
			}
			budgetFlow = *created
		} else if err != nil {
			return err
		} else if common.UnmarshalJsonStr(budgetFlow.Payload, &budget) != nil {
			return ErrAuthFlowInvalid
		}
		if budget.FailedAttempts >= SecurityEmailMaxAttempts {
			return ErrSecurityEmailLocked
		}
		if budget.ResendAt > now.Unix() {
			return ErrSecurityEmailCooldown
		}
		if codeHash == "" || contextHash == "" {
			return ErrAuthFlowInvalid
		}
		state := SecurityEmailState{Identity: identity, Scope: scope, ContextHash: contextHash, Email: email, PendingCodeHash: codeHash, BudgetID: budgetFlow.Id}
		if token != "" {
			var oldState *SecurityEmailState
			flow, oldState, err = getSecurityEmailChallengeWithTx(tx, identity, token, scope, contextHash, email)
			if err != nil {
				return err
			}
			if oldState.BudgetID != budgetFlow.Id {
				return ErrAuthFlowExpired
			}
			state = *oldState
			state.PendingCodeHash = codeHash
		}
		payload, err := common.Marshal(state)
		if err != nil {
			return err
		}
		if token == "" {
			token, flow, err = createAuthFlowWithTx(tx, AuthFlowCreate{Purpose: AuthFlowPurposeSecurityEmail, UserId: identity.UserID, SessionId: identity.SessionID, Payload: string(payload), ExpiresAt: budgetFlow.ExpiresAt})
			if err != nil {
				return err
			}
		} else if err := tx.Model(flow).Update("payload", string(payload)).Error; err != nil {
			return err
		}
		budget.ResendAt = now.Add(SecurityEmailResendDelay).Unix()
		budget.PendingFlowID = flow.Id
		resendAt = budget.ResendAt
		payload, err = common.Marshal(budget)
		if err != nil {
			return err
		}
		return tx.Model(&budgetFlow).Update("payload", string(payload)).Error
	})
	return token, flow, resendAt, err
}

func CompleteSecurityEmailChallenge(identity AuthSessionIdentity, token, scope, contextHash, email, code string) error {
	var verificationErr error
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockSecurityEmailAccount(tx, identity, scope, email); err != nil {
			return err
		}
		flow, state, err := getSecurityEmailChallengeWithTx(tx, identity, token, scope, contextHash, email)
		if err != nil {
			return err
		}
		if !state.Delivered {
			return ErrAuthFlowInvalid
		}
		var budgetFlow AuthFlow
		if err := tx.Where("id = ? AND purpose = ? AND user_id = ? AND expires_at > ?", state.BudgetID, AuthFlowPurposeSecurityEmailRate, identity.UserID, time.Now()).First(&budgetFlow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthFlowExpired
			}
			return err
		}
		var budget securityEmailBudget
		if common.UnmarshalJsonStr(budgetFlow.Payload, &budget) != nil {
			return ErrAuthFlowInvalid
		}
		if budget.FailedAttempts >= SecurityEmailMaxAttempts {
			return ErrSecurityEmailLocked
		}
		numeric, codeErr := common.ValidateNumericCode(code)
		if codeErr != nil || !common.ValidatePasswordAndHash(numeric, state.CodeHash) {
			budget.FailedAttempts++
			verificationErr = ErrSecurityEmailInvalid
			if budget.FailedAttempts >= SecurityEmailMaxAttempts {
				verificationErr = ErrSecurityEmailLocked
			}
			payload, err := common.Marshal(budget)
			if err != nil {
				return err
			}
			return tx.Model(&budgetFlow).Update("payload", string(payload)).Error
		}
		return tx.Model(flow).Update("consumed_at", time.Now()).Error
	})
	if err != nil {
		return err
	}
	return verificationErr
}

func ActivateSecurityEmailChallenge(identity AuthSessionIdentity, token, scope, contextHash, email, codeHash string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockSecurityEmailAccount(tx, identity, scope, email); err != nil {
			return err
		}
		flow, state, err := getSecurityEmailChallengeWithTx(tx, identity, token, scope, contextHash, email)
		if err != nil {
			return err
		}
		if state.PendingCodeHash != codeHash {
			return ErrAuthFlowInvalid
		}
		var budgetFlow AuthFlow
		if err := tx.Where("id = ? AND purpose = ? AND user_id = ? AND expires_at > ?", state.BudgetID, AuthFlowPurposeSecurityEmailRate, identity.UserID, time.Now()).First(&budgetFlow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthFlowExpired
			}
			return err
		}
		var budget securityEmailBudget
		if common.UnmarshalJsonStr(budgetFlow.Payload, &budget) != nil || budget.PendingFlowID != flow.Id {
			return ErrAuthFlowInvalid
		}
		if budget.FailedAttempts >= SecurityEmailMaxAttempts {
			return ErrSecurityEmailLocked
		}
		state.CodeHash, state.PendingCodeHash = codeHash, ""
		state.Delivered = true
		payload, err := common.Marshal(state)
		if err != nil {
			return err
		}
		// Only successful delivery replaces an existing usable challenge.
		if err := tx.Model(&AuthFlow{}).Where("purpose = ? AND user_id = ? AND id <> ? AND consumed_at IS NULL", AuthFlowPurposeSecurityEmail, identity.UserID, flow.Id).Update("consumed_at", time.Now()).Error; err != nil {
			return err
		}
		return tx.Model(flow).Update("payload", string(payload)).Error
	})
}

func getSecurityEmailChallengeWithTx(tx *gorm.DB, identity AuthSessionIdentity, token, scope, contextHash, email string) (*AuthFlow, *SecurityEmailState, error) {
	if token == "" {
		return nil, nil, ErrAuthFlowInvalid
	}
	var flow AuthFlow
	err := applyAuthFlowMatch(tx, token, AuthFlowMatch{Purpose: AuthFlowPurposeSecurityEmail, UserId: identity.UserID, SessionId: identity.SessionID}).First(&flow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrAuthFlowInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	if flow.ConsumedAt != nil {
		return nil, nil, ErrAuthFlowConsumed
	}
	if !flow.ExpiresAt.After(time.Now()) {
		return nil, nil, ErrAuthFlowExpired
	}
	var state SecurityEmailState
	if common.UnmarshalJsonStr(flow.Payload, &state) != nil || state.Identity != identity || state.Scope != scope || state.Email != email || (state.CodeHash == "" && state.PendingCodeHash == "") || !hmac.Equal([]byte(state.ContextHash), []byte(contextHash)) {
		return nil, nil, ErrAuthFlowInvalid
	}
	return &flow, &state, nil
}

func lockSecurityEmailAccount(tx *gorm.DB, identity AuthSessionIdentity, scope, email string) error {
	if scope != "team.payout.write" && scope != "team.referral.write" {
		return ErrAuthFlowInvalid
	}
	if identity.UserID <= 0 || email == "" || email != NormalizeEmail(email) {
		return ErrAuthFlowInvalid
	}
	// A first write avoids a deferred-read transaction upgrade on SQLite.
	if err := tx.Model(&User{}).Where("id = ?", identity.UserID).UpdateColumn("auth_version", gorm.Expr("auth_version")).Error; err != nil {
		return err
	}
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return err
	}
	var user User
	if err := tx.Select("email", "role").First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if NormalizeEmail(user.Email) != email {
		return ErrAccountBindingChanged
	}
	if scope == "team.referral.write" && user.Role < common.RoleAdminUser {
		return ErrAgentReferralForbidden
	}
	return nil
}

// Recheck the consumed authorization in the same transaction as the mutation.
// An email or session change between proof consumption and execution fails closed.
func ValidateTeamAuthorizationWithTx(tx *gorm.DB, authorization *AuthFlowAuthorization, userID int, scope string) error {
	if authorization == nil || authorization.UserID != userID || authorization.Scope != scope || authorization.ProofID <= 0 {
		return ErrAuthFlowInvalid
	}
	if err := ValidateAuthSessionWithTx(tx, authorization.AuthSessionIdentity); err != nil {
		return err
	}
	if authorization.Method == "email" {
		var user User
		if err := tx.Select("email").First(&user, userID).Error; err != nil {
			return err
		}
		if authorization.EmailSnapshot == "" || NormalizeEmail(user.Email) != authorization.EmailSnapshot {
			return ErrAccountBindingChanged
		}
	}
	return nil
}

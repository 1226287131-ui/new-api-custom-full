package service

import (
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var (
	ErrSecurityEmailRequired    = errors.New("Bind an email address in account settings before using email verification.")
	ErrSecurityEmailUnavailable = errors.New("Email verification is unavailable. Contact the administrator to configure SMTP.")
	ErrSecurityEmailDelivery    = errors.New("Verification email could not be sent. Please try again later.")
)

type SecurityEmailData struct {
	FlowToken string `json:"flow_token"`
	ExpiresAt int64  `json:"expires_at"`
	ResendAt  int64  `json:"resend_at"`
	Email     string `json:"email"`
}

func securityEmailAvailability(email string) error {
	if common.Validate.Var(model.NormalizeEmail(email), "required,email,max=50") != nil {
		return ErrSecurityEmailRequired
	}
	if strings.TrimSpace(common.SMTPServer) == "" || common.SMTPPort <= 0 || (strings.TrimSpace(common.SMTPFrom) == "" && strings.TrimSpace(common.SMTPAccount) == "") {
		return ErrSecurityEmailUnavailable
	}
	return nil
}

func SendSecurityVerificationEmail(identity AuthIdentity, operation VerificationOperation, token string) (*SecurityEmailData, error) {
	binding, err := BindVerificationOperation(operation)
	if err != nil {
		return nil, err
	}
	if _, err := RequireVerificationMethod(identity, binding.Scope, VerificationMethodEmail); err != nil {
		return nil, err
	}
	user, err := model.GetUserById(identity.UserID, false)
	if err != nil {
		return nil, err
	}
	email := model.NormalizeEmail(user.Email)
	if err := securityEmailAvailability(email); err != nil {
		return nil, err
	}
	codes, err := generateEmailBindingCodes(false)
	if err != nil {
		return nil, err
	}
	token, flow, resendAt, err := model.SaveSecurityEmailChallenge(identity, token, binding.Scope, binding.ContextHash, email, codes.NewHash)
	if err != nil {
		return nil, err
	}
	action := "Confirm changing your Alipay payout account"
	var detail string
	if binding.Scope == VerificationScopeTeamPayoutWrite {
		var context TeamPayoutWriteContext
		if err := common.Unmarshal(operation.Context, &context); err != nil {
			return nil, err
		}
		detail = fmt.Sprintf("Alipay account: %s; name: %s", html.EscapeString(strings.TrimSpace(context.Account)), html.EscapeString(strings.TrimSpace(context.Name)))
	} else {
		action = "Confirm changing a team referral relationship"
		var context TeamReferralWriteContext
		if err := common.Unmarshal(operation.Context, &context); err != nil {
			return nil, err
		}
		detail = fmt.Sprintf("Member ID: %d; previous inviter ID: %d; new inviter ID: %d; reason: %s", context.UserID, context.ExpectedInviterID, context.InviterID, html.EscapeString(strings.TrimSpace(context.Reason)))
	}
	content := fmt.Sprintf("<p>%s</p><p>%s</p><p>Verification code: <strong>%s</strong></p><p>This code is only valid for this action and expires within 10 minutes. Do not share it. If you did not request this action, do not use this code.</p>", action, detail, html.EscapeString(codes.New))
	if err := common.SendEmail(common.SystemName+" - "+action, email, content); err != nil {
		return nil, ErrSecurityEmailDelivery
	}
	if err := model.ActivateSecurityEmailChallenge(identity, token, binding.Scope, binding.ContextHash, email, codes.NewHash); err != nil {
		return nil, err
	}
	return &SecurityEmailData{FlowToken: token, ExpiresAt: flow.ExpiresAt.Unix(), ResendAt: resendAt, Email: common.MaskEmail(email)}, nil
}

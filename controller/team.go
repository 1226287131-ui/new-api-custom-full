package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	securityProofScopeTeamPayoutWrite      = service.VerificationScopeTeamPayoutWrite
	securityProofScopeTeamWithdrawalWrite  = service.VerificationScopeTeamWithdrawalWrite
	securityProofScopeTeamWithdrawalReview = service.VerificationScopeTeamWithdrawalReview
	securityProofScopeTeamWithdrawalRead   = service.VerificationScopeTeamWithdrawalRead
)

func GetTeamSelf(c *gin.Context) {
	userID := c.GetInt("id")
	policy, err := model.GetAgentPolicy()
	if err != nil {
		teamAPIError(c, err)
		return
	}
	wallet, err := model.GetAgentWallet(userID)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	payout, err := model.GetAgentPayoutAccount(userID)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	summary, err := model.GetAgentCommissionSummary(userID)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	preference, err := model.GetAgentRewardPreference(userID)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	mode := preference.Mode
	if mode == "" {
		mode = policy.Mode
	}
	payoutView := gin.H{"bound": false, "account_masked": "", "name_masked": "", "updated_at": 0}
	if payout != nil {
		payoutView = gin.H{"bound": true, "account_masked": payout.AccountMasked, "name_masked": payout.NameMasked, "updated_at": payout.UpdatedAt}
	}
	common.ApiSuccess(c, gin.H{"policy": policy, "wallet": wallet, "payout": payoutView,
		"payout_ready": common.AgentPayoutEncryptionReady(), "summary": summary,
		"reward_preference": preference, "effective_reward_mode": mode})
}

func SaveTeamRewardPreference(c *gin.Context) {
	var request struct {
		Mode string `json:"mode"`
	}
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &request); err != nil ||
		(request.Mode != model.AgentModeCredit && request.Mode != model.AgentModeCash) {
		common.ApiErrorMsg(c, "Invalid reward preference")
		return
	}
	if request.Mode == model.AgentModeCash && !common.AgentPayoutEncryptionReady() {
		teamAPIError(c, common.ErrAgentPayoutEncryptionUnavailable)
		return
	}
	if err := model.SaveAgentRewardPreference(c.GetInt("id"), request.Mode); err != nil {
		teamAPIError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"mode": request.Mode})
}

func AdminGetTeamReferral(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		common.ApiErrorMsg(c, "Invalid referral relationship")
		return
	}
	relation, err := model.GetAgentReferral(userID)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	common.ApiSuccess(c, relation)
}

func AdminChangeTeamReferral(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		common.ApiErrorMsg(c, "Invalid referral relationship")
		return
	}
	var request struct {
		ExpectedInviterID *int   `json:"expected_inviter_id"`
		InviterID         *int   `json:"inviter_id"`
		Reason            string `json:"reason"`
	}
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &request); err != nil ||
		request.ExpectedInviterID == nil || request.InviterID == nil {
		common.ApiErrorMsg(c, "Invalid referral relationship")
		return
	}
	context, err := common.Marshal(service.TeamReferralWriteContext{UserID: userID,
		ExpectedInviterID: *request.ExpectedInviterID, InviterID: *request.InviterID, Reason: request.Reason})
	if err != nil {
		teamAPIError(c, err)
		return
	}
	operation := service.VerificationOperation{Scope: service.VerificationScopeTeamReferralWrite, Context: context}
	if _, err := service.BindVerificationOperation(operation); err != nil {
		common.ApiErrorMsg(c, "Invalid referral relationship")
		return
	}
	if middleware.RequireSecurityProof(c, operation) == nil {
		return
	}
	audit, err := model.ChangeAgentReferrer(c.GetInt("id"), userID, *request.ExpectedInviterID, *request.InviterID, request.Reason)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	common.ApiSuccess(c, audit)
}

func AdminGetTeamReferralAudits(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		common.ApiErrorMsg(c, "Invalid referral relationship")
		return
	}
	page := teamPageQuery(c)
	items, total, err := model.GetAgentReferralAudits(userID, page)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetTeamCommissions(c *gin.Context) {
	getTeamCommissions(c, c.GetInt("id"))
}

func AdminGetTeamCommissions(c *gin.Context) {
	getTeamCommissions(c, 0)
}

func getTeamCommissions(c *gin.Context, referrerID int) {
	page := teamPageQuery(c)
	items, total, err := model.GetAgentCommissions(referrerID, page)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	if referrerID != 0 {
		for i := range items {
			// Operational errors and payment-provider order IDs stay administrator-only.
			items[i].LastError = ""
			items[i].TradeNo = ""
			items[i].TopUpID = 0
		}
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func SaveTeamPayoutAccount(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: securityProofScopeTeamPayoutWrite}) == nil {
		return
	}
	var request struct {
		Account string `json:"account"`
		Name    string `json:"name"`
	}
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &request); err != nil {
		common.ApiErrorMsg(c, "Invalid payout account")
		return
	}
	account, err := model.SaveAgentPayoutAccount(c.GetInt("id"), request.Account, request.Name)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	model.RecordLog(c.GetInt("id"), model.LogTypeSystem, "Team payout account updated")
	common.ApiSuccess(c, account)
}

func RequestTeamWithdrawal(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: securityProofScopeTeamWithdrawalWrite}) == nil {
		return
	}
	var request struct {
		AmountCents int64  `json:"amount_cents"`
		RequestID   string `json:"request_id"`
	}
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &request); err != nil {
		common.ApiErrorMsg(c, "Invalid withdrawal request")
		return
	}
	withdrawal, err := model.RequestAgentWithdrawal(c.GetInt("id"), request.AmountCents, request.RequestID)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	common.ApiSuccess(c, withdrawal)
}

func GetTeamWithdrawals(c *gin.Context) {
	getTeamWithdrawals(c, c.GetInt("id"))
}

func AdminGetTeamWithdrawals(c *gin.Context) {
	getTeamWithdrawals(c, 0)
}

func getTeamWithdrawals(c *gin.Context, userID int) {
	status := c.Query("status")
	switch status {
	case "", model.AgentWithdrawalPending, model.AgentWithdrawalApproved, model.AgentWithdrawalPaid, model.AgentWithdrawalRejected:
	default:
		common.ApiErrorMsg(c, "Invalid withdrawal status")
		return
	}
	page := teamPageQuery(c)
	items, total, err := model.ListAgentWithdrawals(userID, status, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		teamAPIError(c, err)
		return
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func AdminReadTeamWithdrawal(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: securityProofScopeTeamWithdrawalRead}) == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "Invalid withdrawal request")
		return
	}
	withdrawal, err := model.GetAgentWithdrawalForAdmin(id)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	model.RecordLog(c.GetInt("id"), model.LogTypeSystem, fmt.Sprintf("Team withdrawal payout details accessed: %d", id))
	common.ApiSuccess(c, withdrawal)
}

func AdminReviewTeamWithdrawal(c *gin.Context) {
	if middleware.RequireSecurityProof(c, service.VerificationOperation{Scope: securityProofScopeTeamWithdrawalReview}) == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "Invalid withdrawal request")
		return
	}
	var request struct {
		Action           string `json:"action"`
		PaymentReference string `json:"reference"`
		Reason           string `json:"reason"`
	}
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &request); err != nil {
		common.ApiErrorMsg(c, "Invalid withdrawal review")
		return
	}
	withdrawal, err := model.ReviewAgentWithdrawal(id, c.GetInt("id"), request.Action, request.PaymentReference, request.Reason)
	if err != nil {
		teamAPIError(c, err)
		return
	}
	common.ApiSuccess(c, withdrawal)
}

func AdminGetTeamPolicy(c *gin.Context) {
	policy, err := model.GetAgentPolicy()
	if err != nil {
		teamAPIError(c, err)
		return
	}
	common.ApiSuccess(c, policy)
}

func AdminSaveTeamPolicy(c *gin.Context) {
	var policy model.AgentPolicy
	if err := common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &policy); err != nil {
		common.ApiErrorMsg(c, "Invalid invitation reward settings")
		return
	}
	if policy.Enabled && policy.Mode == model.AgentModeCash && !common.AgentPayoutEncryptionReady() {
		teamAPIError(c, common.ErrAgentPayoutEncryptionUnavailable)
		return
	}
	if err := model.SaveAgentPolicy(&policy); err != nil {
		common.SysError("save team policy: " + err.Error())
		common.ApiErrorMsg(c, "Invalid invitation reward settings")
		return
	}
	common.ApiSuccess(c, policy)
}

func teamPageQuery(c *gin.Context) *common.PageInfo {
	page := common.GetPageQuery(c)
	if page.Page < 1 {
		page.Page = 1
	}
	if page.Page > 1000000 {
		page.Page = 1000000
	}
	if page.PageSize < 1 {
		page.PageSize = 20
	}
	return page
}

// Do not expose SQL errors or encryption details through account endpoints.
func teamAPIError(c *gin.Context, err error) {
	message := "Unable to complete this team request"
	switch {
	case errors.Is(err, model.ErrAgentReferralConflict):
		message = "The referral relationship changed. Refresh and try again."
	case errors.Is(err, model.ErrAgentReferralForbidden):
		message = "You cannot change this user's referral relationship"
	case errors.Is(err, model.ErrAgentReferralCycle):
		message = "This referral relationship would create a cycle"
	case errors.Is(err, model.ErrAgentReferralInvalid):
		message = "Invalid referral relationship"
	case errors.Is(err, common.ErrAgentPayoutEncryptionUnavailable):
		message = "Payout account encryption is not configured"
	case errors.Is(err, model.ErrAgentInsufficientCash):
		message = "Insufficient available rewards"
	case errors.Is(err, model.ErrAgentWithdrawalDisabled):
		message = "Withdrawals are currently disabled"
	case errors.Is(err, model.ErrAgentWithdrawalState):
		message = "This withdrawal status does not allow this action"
	case errors.Is(err, model.ErrAgentWithdrawalRequestConflict):
		message = "This withdrawal request has already been submitted with a different amount"
	case errors.Is(err, model.ErrAgentPayoutAccountRequired):
		message = "Bind an Alipay account first"
	case errors.Is(err, model.ErrAgentInvalidCashAmount):
		message = "Invalid withdrawal amount or below the minimum"
	default:
		common.SysError("team request: " + err.Error())
	}
	common.ApiErrorMsg(c, message)
}

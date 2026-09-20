package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func registerTeamRoutes(api *gin.RouterGroup) {
	team := api.Group("/team", middleware.UserAuth(), middleware.DisableCache())
	team.GET("/self", controller.GetTeamSelf)
	team.GET("/commissions", controller.GetTeamCommissions)
	team.GET("/withdrawals", controller.GetTeamWithdrawals)
	team.PUT("/reward-preference", middleware.UserCriticalRateLimit("team-reward-preference"), controller.SaveTeamRewardPreference)
	team.PUT("/payout", middleware.UserCriticalRateLimit("team-payout"), controller.SaveTeamPayoutAccount)
	team.POST("/withdrawals", middleware.UserCriticalRateLimit("team-withdrawal"), controller.RequestTeamWithdrawal)

	admin := api.Group("/team/admin", middleware.AdminAuth(), middleware.DisableCache())
	admin.GET("/policy", controller.AdminGetTeamPolicy)
	admin.GET("/commissions", controller.AdminGetTeamCommissions)
	admin.GET("/withdrawals", controller.AdminGetTeamWithdrawals)
	admin.GET("/referrals/:id", controller.AdminGetTeamReferral)
	admin.GET("/referrals/:id/audits", controller.AdminGetTeamReferralAudits)
	admin.PUT("/referrals/:id", middleware.UserCriticalRateLimit("team-referral"), controller.AdminChangeTeamReferral)
	admin.POST("/withdrawals/:id/payout", middleware.UserCriticalRateLimit("team-withdrawal-payout-read"), controller.AdminReadTeamWithdrawal)
	admin.POST("/withdrawals/:id/review", middleware.UserCriticalRateLimit("team-withdrawal-review"), controller.AdminReviewTeamWithdrawal)

	settings := api.Group("/team/admin", middleware.RootAuth(), middleware.DisableCache())
	settings.PUT("/policy", middleware.UserCriticalRateLimit("team-policy"), controller.AdminSaveTeamPolicy)
}

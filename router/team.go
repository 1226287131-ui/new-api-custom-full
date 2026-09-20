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
	team.PUT("/payout", middleware.CriticalRateLimit(), controller.SaveTeamPayoutAccount)
	team.POST("/withdrawals", middleware.CriticalRateLimit(), controller.RequestTeamWithdrawal)

	admin := api.Group("/team/admin", middleware.AdminAuth(), middleware.DisableCache())
	admin.GET("/policy", controller.AdminGetTeamPolicy)
	admin.GET("/commissions", controller.AdminGetTeamCommissions)
	admin.GET("/withdrawals", controller.AdminGetTeamWithdrawals)
	admin.POST("/withdrawals/:id/payout", middleware.CriticalRateLimit(), controller.AdminReadTeamWithdrawal)
	admin.POST("/withdrawals/:id/review", middleware.CriticalRateLimit(), controller.AdminReviewTeamWithdrawal)

	settings := api.Group("/team/admin", middleware.RootAuth(), middleware.DisableCache())
	settings.PUT("/policy", middleware.CriticalRateLimit(), controller.AdminSaveTeamPolicy)
}

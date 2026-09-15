package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	// Public share URL for cached NewAPI videos. The random task ID is the
	// capability; no API token is required for this route.
	publicVideoRouter := router.Group("")
	publicVideoRouter.Use(middleware.RouteTag("relay"))
	{
		publicVideoRouter.GET("/video-cache/:file_name", controller.PublicVideoProxy)
		publicVideoRouter.HEAD("/video-cache/:file_name", controller.PublicVideoProxy)
		publicVideoRouter.GET("/image-cache/:file_name", controller.PublicImageProxy)
		publicVideoRouter.HEAD("/image-cache/:file_name", controller.PublicImageProxy)
		publicVideoRouter.GET("/video-input-cache/:file_name", controller.PublicVideoInput)
		publicVideoRouter.HEAD("/video-input-cache/:file_name", controller.PublicVideoInput)
	}

	videoSharedRouter := router.Group("/v1")
	videoSharedRouter.Use(middleware.RouteTag("relay"))
	videoSharedRouter.Use(middleware.TokenAuth())
	videoSharedRouter.Use(middleware.SystemPerformanceCheck())
	videoSharedRouter.POST(
		"/video/generations",
		middleware.PinTaskPluginEndpoint(),
		middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()),
		middleware.PrepareTaskPluginEndpoint(),
		middleware.Distribute(),
		func(c *gin.Context) {
			controller.RelayTaskPluginEndpoint(c, controller.RelayTask)
		},
	)

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
	// Keep the deployed generations alias; canonical /v1/videos is registered
	// once by SetTaskPluginProtocolRouter.
	videoV1Router.POST("/videos/generations", controller.RelayTask)
	videoV1Router.GET("/videos/generations/:task_id", controller.RelayTaskFetch)
}

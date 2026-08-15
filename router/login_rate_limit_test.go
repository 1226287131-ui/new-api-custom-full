package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordLoginIsNotBlockedByCriticalRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousGlobalEnabled := common.GlobalApiRateLimitEnable
	previousCriticalEnabled := common.CriticalRateLimitEnable
	previousCriticalNum := common.CriticalRateLimitNum
	previousCriticalDuration := common.CriticalRateLimitDuration
	previousTurnstileEnabled := common.TurnstileCheckEnabled
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable = previousGlobalEnabled
		common.CriticalRateLimitEnable = previousCriticalEnabled
		common.CriticalRateLimitNum = previousCriticalNum
		common.CriticalRateLimitDuration = previousCriticalDuration
		common.TurnstileCheckEnabled = previousTurnstileEnabled
	})

	common.GlobalApiRateLimitEnable = false
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = 1
	common.CriticalRateLimitDuration = 60
	common.TurnstileCheckEnabled = false

	engine := gin.New()
	SetApiRouter(engine)

	for range 3 {
		request := httptest.NewRequest(http.MethodPost, "/api/user/login", strings.NewReader("{}"))
		request.RemoteAddr = "192.0.2.10:12345"
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
}

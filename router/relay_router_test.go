package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesRoutesSupportVersionedAndHostOnlyBaseURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	require.NotEmpty(t, routes)

	for _, path := range []string{
		"/v1/responses",
		"/v1/responses/compact",
		"/responses",
		"/responses/compact",
	} {
		_, ok := routes[http.MethodPost+" "+path]
		assert.True(t, ok, "missing POST %s", path)
	}
}

func TestCanonicalizeResponsesCompatibilityPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		path string
		want string
	}{
		{path: "/responses", want: "/v1/responses"},
		{path: "/responses/compact", want: "/v1/responses/compact"},
		{path: "/unrelated", want: "/unrelated"},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, testCase.path, nil)

			canonicalizeResponsesCompatibilityPath(context)

			assert.Equal(t, testCase.want, context.Request.URL.Path)
		})
	}
}

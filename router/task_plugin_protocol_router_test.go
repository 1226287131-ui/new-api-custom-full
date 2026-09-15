package router

import (
	"fmt"
	"sort"
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostProtocolRegistryDrivesProtocolRoutesOnce(t *testing.T) {
	engine := gin.New()
	SetTaskPluginProtocolRouter(engine)

	expected := []string{
		"POST /v1/responses",
		"GET /v1/responses/:response_id",
		"POST /v1/videos",
		"GET /v1/videos/:task_id",
		"GET /v1/videos/:task_id/content",
		"HEAD /v1/videos/:task_id/content",
	}
	actual := make([]string, 0, len(engine.Routes()))
	for _, route := range engine.Routes() {
		actual = append(actual, fmt.Sprintf("%s %s", route.Method, route.Path))
	}
	sort.Strings(expected)
	sort.Strings(actual)
	assert.Equal(t, expected, actual)
}

func TestMergedRouterRegistersVideoPathsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousRegistry := jsplugin.DefaultRegistry
	registry := jsplugin.NewRegistry()
	for _, meta := range previousRegistry.Snapshot().Factory {
		source, err := plugins.Source(meta.Key)
		require.NoError(t, err)
		_, err = registry.RegisterFactory(source, jsplugin.Options{Key: meta.Key})
		require.NoError(t, err)
	}
	jsplugin.DefaultRegistry = registry
	t.Cleanup(func() { jsplugin.DefaultRegistry = previousRegistry })
	t.Setenv("FRONTEND_BASE_URL", "")
	engine := gin.New()
	require.NotPanics(t, func() {
		SetRouter(engine, WebAssets{IndexPage: []byte("test dashboard")})
	})
	assert.Empty(t, registry.RoutingErrors(), "built-in native routes must survive static route registration")
	assert.Empty(t, registry.LastRebuildError())
	routes := make(map[string]int)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path]++
	}
	for _, route := range []string{
		"POST /v1/videos",
		"GET /v1/videos/:task_id",
		"GET /v1/videos/:task_id/content",
		"HEAD /v1/videos/:task_id/content",
		"POST /v1/video/generations",
		"GET /v1/video/generations/:task_id",
		"POST /v1/videos/generations",
		"GET /v1/videos/generations/:task_id",
		"POST /v1/videos/:video_id/remix",
		"GET /video-cache/:file_name",
		"GET /video-input-cache/:file_name",
	} {
		assert.Equal(t, 1, routes[route], route)
	}
}

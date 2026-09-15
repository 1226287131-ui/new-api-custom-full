package controller

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelPricingExtensionsDatabaseMatrix(t *testing.T) {
	const pluginKey = "merge-pricing"
	_, err := jsplugin.DefaultRegistry.Register(`
export const meta = {
  apiVersion: 1, key: "merge-pricing", name: "Merge Pricing", version: "1.0.0",
  author: {name: "Test"}, models: ["merge-video"], fetchMode: "per_task",
  usageSchema: {seconds: {type: "number", unit: "second", description: "Video generation unit price"}}
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(pluginKey) })

	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "AUDIT_MYSQL_DSN"}, {"postgres", "AUDIT_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skipf("%s is not configured", dialect.env)
			}
			db := modelManagementDB(t, dialect.kind, dsn)
			// These are the pre-merge persisted option shapes; no conversion is
			// required for the new atomic API to load their complete prices.
			require.NoError(t, db.Create(&[]model.Option{
				{Key: "ImageResolutionPrice", Value: `{"merge-image":{"1K":0.1,"2K":0.2,"4K":0.4}}`},
				{Key: "billing_setting.task_billing_pricing", Value: `{"merge-video":{"mode":"per-second","resolution_prices":{"1080p":0.2}}}`},
				{Key: "billing_setting.scheduled_discount", Value: `{"merge-video":{"enabled":true,"start":"22:00","end":"02:00","discount":0.8}}`},
			}).Error)
			before, err := model.GetModelPricingSnapshot([]string{"merge-image", "merge-video"})
			require.NoError(t, err)
			require.Len(t, before.Entries, 2)
			assert.Contains(t, before.Entries[0].Configured, "ImageResolutionPrice")
			assert.Contains(t, before.Entries[1].Configured, "billing_setting.task_billing_pricing")
			assert.Contains(t, before.Entries[1].Configured, "billing_setting.scheduled_discount")

			imageDraft := model.PricingValues{
				"ImageResolutionPrice":               map[string]any{"1K": 0.0, "2K": 0.2, "4K": 0.4},
				"billing_setting.billing_mode":       "image_resolution",
				"billing_setting.scheduled_discount": map[string]any{"enabled": true, "start": "22:00", "end": "02:00", "discount": 0.75},
			}
			videoDraft := model.PricingValues{
				"billing_setting.billing_mode":          "per-second",
				"billing_setting.task_billing_pricing":  map[string]any{"mode": "per-second", "resolution_prices": map[string]any{"1080p": 0.2}},
				"billing_setting.scheduled_discount":    map[string]any{"enabled": true, "start": "22:00", "end": "02:00", "discount": 0.5},
				billing_setting.PluginBillingExprOption: map[string]any{pluginKey: `tier("video", u("seconds") * 0.1)`},
			}
			for _, tc := range []struct {
				name   string
				draft  model.PricingValues
				reason string
			}{
				{"merge-image", imageDraft, "Image resolution pricing must be converted manually while preserving every resolution tier and image count."},
				{"merge-video", videoDraft, "Task resolution pricing must be converted manually using the task usage schema."},
				{"merge-seconds", model.PricingValues{"ModelPrice": 0.2, "billing_setting.billing_mode": "per-second"}, "Task pricing must be converted manually using the task usage schema."},
			} {
				var conversion struct {
					Success bool                         `json:"success"`
					Data    model.ModelPricingConversion `json:"data"`
				}
				modelManagementRequest(t, PreviewModelPricingConversion, http.MethodPost, "/api/option/model_pricing/convert", map[string]any{"model_name": tc.name, "pricing": tc.draft}, &conversion)
				require.True(t, conversion.Success)
				assert.Empty(t, conversion.Data.Expression)
				assert.Equal(t, tc.reason, conversion.Data.UnsupportedReason)
			}
			changes := []model.ModelPricingChange{
				{ModelName: "merge-image", ExpectedVersion: before.Entries[0].Version, Pricing: imageDraft},
				{ModelName: "merge-video", ExpectedVersion: before.Entries[1].Version, Pricing: videoDraft},
				{ModelName: "merge-token", ExpectedVersion: before.EmptyVersion, Pricing: model.PricingValues{
					"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("tokens", p * 2 + c * 8)`,
					"billing_setting.scheduled_discount": map[string]any{"enabled": true, "start": "22:00", "end": "02:00", "discount": 0.8},
				}},
			}
			response := modelManagementRequest(t, UpdateModelPricingConfig, http.MethodPatch, "/api/option/model_pricing", map[string]any{"changes": changes}, nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var apiResponse struct {
				Success bool                       `json:"success"`
				Data    model.ModelPricingSnapshot `json:"data"`
			}
			modelManagementRequest(t, GetModelPricingConfig, http.MethodGet, "/api/option/model_pricing?model=merge-image&model=merge-video", nil, &apiResponse)
			require.True(t, apiResponse.Success)
			loaded := apiResponse.Data
			require.Len(t, loaded.Entries, 2)
			assert.Equal(t, imageDraft, loaded.Entries[0].Configured)
			assert.Equal(t, imageDraft["ImageResolutionPrice"], loaded.Entries[0].Effective["ImageResolutionPrice"])
			assert.Equal(t, videoDraft, loaded.Entries[1].Configured)
			assert.Contains(t, loaded.Options, "ImageResolutionPrice")
			assert.Contains(t, loaded.Options, "billing_setting.task_billing_pricing")
			prices, ok := ratio_setting.GetImageResolutionPrice("merge-image")
			require.True(t, ok)
			assert.Equal(t, 0.0, prices.OneK)
			assert.Equal(t, 0.4, prices.FourK)
			assert.Equal(t, 0.75, billing_setting.ScheduledDiscountMultiplier("merge-image", time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC)))
			selection, configured, err := billing_setting.ResolveTaskBillingPrice("merge-video", "1920x1080")
			require.NoError(t, err)
			assert.True(t, configured)
			assert.Equal(t, billing_setting.BillingModePerSecond, selection.Mode)
			assert.Equal(t, 0.2, selection.Price)
			_, _, err = billing_setting.ResolveTaskBillingPrice("merge-video", "720p")
			assert.Error(t, err, "a missing configured tier must not fall back to another price")
			expression, configured := billing_setting.ResolveTaskBillingExpr(pluginKey, "merge-video", "")
			assert.True(t, configured)
			assert.Equal(t, `tier("video", u("seconds") * 0.1)`, expression)
			assert.Equal(t, 0.5, billing_setting.ScheduledDiscountMultiplier("merge-video", time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC)))

			response = modelManagementRequest(t, UpdateModelPricingConfig, http.MethodPatch, "/api/option/model_pricing", map[string]any{"changes": changes[:1]}, nil)
			assert.Equal(t, http.StatusConflict, response.Code)
			invalid := []model.ModelPricingChange{
				{ModelName: "merge-video", ExpectedVersion: loaded.Entries[1].Version, Pricing: model.PricingValues{"ModelPrice": 9.0}},
				{ModelName: "merge-image", ExpectedVersion: loaded.Entries[0].Version, Pricing: model.PricingValues{"ImageResolutionPrice": map[string]any{"1K": 0.0}}},
			}
			assert.Error(t, model.UpdateModelPricing(invalid))
			after, err := model.GetModelPricingSnapshot([]string{"merge-image", "merge-video"})
			require.NoError(t, err)
			assert.Equal(t, loaded.Entries[0].Version, after.Entries[0].Version)
			assert.Equal(t, loaded.Entries[1].Version, after.Entries[1].Version, "a rejected batch must preserve the task configuration")
			require.NoError(t, model.UpdateModelPricing([]model.ModelPricingChange{{ModelName: "merge-video", ExpectedVersion: after.Entries[1].Version, Reset: true}}))
			after, err = model.GetModelPricingSnapshot([]string{"merge-video"})
			require.NoError(t, err)
			assert.Empty(t, after.Entries[0].Configured)
			_, configured = billing_setting.GetPluginBillingExpr(pluginKey, "merge-video")
			assert.False(t, configured)

			// A synchronized fixed price must use the imported unit, including
			// when the local model was previously charged per second.
			fixedVersion := before.EmptyVersion
			for _, mode := range []string{billing_setting.BillingModePerSecond, billing_setting.BillingModePerRequest} {
				draft := model.PricingValues{"ModelPrice": 0.2, "billing_setting.billing_mode": mode}
				change := model.ModelPricingChange{ModelName: "merge-sync", ExpectedVersion: fixedVersion, Pricing: draft}
				response := modelManagementRequest(t, UpdateModelPricingConfig, http.MethodPatch, "/api/option/model_pricing", map[string]any{"changes": []model.ModelPricingChange{change}}, nil)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var fixedResponse struct {
					Success bool                       `json:"success"`
					Data    model.ModelPricingSnapshot `json:"data"`
				}
				modelManagementRequest(t, GetModelPricingConfig, http.MethodGet, "/api/option/model_pricing?model=merge-sync", nil, &fixedResponse)
				require.True(t, fixedResponse.Success)
				require.Len(t, fixedResponse.Data.Entries, 1)
				fixed := fixedResponse.Data.Entries[0]
				assert.Equal(t, draft, fixed.Configured)
				assert.Equal(t, draft, fixed.Effective)
				price, configured := ratio_setting.GetModelPrice("merge-sync", false)
				require.True(t, configured)
				assert.Equal(t, 0.2, price)
				assert.Equal(t, mode, billing_setting.GetTaskBillingMode("merge-sync", false))
				assert.Equal(t, mode, billing_setting.GetTaskBillingMode("merge-sync", true))
				fixedVersion = fixed.Version
			}
		})
	}
}

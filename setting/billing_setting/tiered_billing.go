package billing_setting

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio        = "ratio"
	BillingModeTieredExpr   = "tiered_expr"
	BillingModePerRequest   = "per-request"
	BillingModePerSecond    = "per-second"
	BillingModeField        = "billing_mode"
	BillingExprField        = "billing_expr"
	TaskBillingPricingField = "task_billing_pricing"
)

// TaskBillingPriceConfig describes the price of an asynchronous task.
// Prices use the same USD-per-million-quota-unit convention as ModelPrice,
// but are selected by output resolution before the task is pre-charged.
type TaskBillingPriceConfig struct {
	Mode             string             `json:"mode"`
	DefaultPrice     *float64           `json:"default_price,omitempty"`
	ResolutionPrices map[string]float64 `json:"resolution_prices,omitempty"`
}

// TaskBillingPriceSelection is the immutable price selected for one request.
// It is kept on RelayInfo and copied into the task billing snapshot.
type TaskBillingPriceSelection struct {
	Mode       string
	Price      float64
	Resolution string
}

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr,
// billing_setting.task_billing_pricing
type BillingSetting struct {
	BillingMode        map[string]string                 `json:"billing_mode"`
	BillingExpr        map[string]string                 `json:"billing_expr"`
	TaskBillingPricing map[string]TaskBillingPriceConfig `json:"task_billing_pricing"`
}

var billingSetting = BillingSetting{
	BillingMode:        make(map[string]string),
	BillingExpr:        make(map[string]string),
	TaskBillingPricing: make(map[string]TaskBillingPriceConfig),
}

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

// GetTaskBillingMode resolves the billing unit for asynchronous task models.
//
// Historically, task models listed in TASK_PRICE_PATCH were charged once per
// request while all other task models applied adaptor-provided ratios such as
// duration. Keep that behavior for models without an explicit setting, while
// allowing pricing settings to select the unit per model.
func GetTaskBillingMode(model string, legacyPerRequest bool) string {
	return resolveTaskBillingMode(GetBillingMode(model), legacyPerRequest)
}

func resolveTaskBillingMode(configuredMode string, legacyPerRequest bool) string {
	switch configuredMode {
	case BillingModePerRequest:
		return BillingModePerRequest
	case BillingModePerSecond:
		return BillingModePerSecond
	default:
		if legacyPerRequest {
			return BillingModePerRequest
		}
		return BillingModePerSecond
	}
}

func GetBillingExpr(model string) (string, bool) {
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

func GetBillingModeCopy() map[string]string {
	return lo.Assign(billingSetting.BillingMode)
}

func GetBillingExprCopy() map[string]string {
	return lo.Assign(billingSetting.BillingExpr)
}

func GetTaskBillingPricingCopy() map[string]TaskBillingPriceConfig {
	result := make(map[string]TaskBillingPriceConfig, len(billingSetting.TaskBillingPricing))
	for modelName, config := range billingSetting.TaskBillingPricing {
		result[modelName] = cloneTaskBillingPriceConfig(config)
	}
	return result
}

func GetTaskBillingPriceConfig(model string) (TaskBillingPriceConfig, bool) {
	config, ok := billingSetting.TaskBillingPricing[model]
	if !ok {
		return TaskBillingPriceConfig{}, false
	}
	return cloneTaskBillingPriceConfig(config), true
}

// NormalizeTaskBillingResolution keeps request aliases and size strings
// stable so a price table can be shared by all video task adaptors.
func NormalizeTaskBillingResolution(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "4k", "2160p", "3840x2160", "2160x3840", "2160x2160":
		return "4k"
	case "1440p", "3360x1440", "2560x1440", "1920x1440", "1440x1440", "1440x1920", "1440x2560":
		return "1440p"
	case "1080p", "1920x1080", "1080x1920", "1792x1024", "1024x1792", "1080x1080":
		return "1080p"
	case "720p", "1280x720", "720x1280", "1024x1024":
		return "720p"
	case "480p", "854x480", "480x854":
		return "480p"
	default:
		return value
	}
}

// ResolveTaskBillingPrice returns the configured per-second or per-request
// price. When the resolution price table is non-empty, it is authoritative:
// missing resolution entries must fail instead of falling back to a default
// price. Configurations without a resolution table retain the legacy default
// price behavior.
func ResolveTaskBillingPrice(model, resolution string) (TaskBillingPriceSelection, bool, error) {
	config, configured := GetTaskBillingPriceConfig(model)
	if !configured {
		return TaskBillingPriceSelection{}, false, nil
	}

	if config.Mode != BillingModePerRequest && config.Mode != BillingModePerSecond {
		return TaskBillingPriceSelection{}, true, fmt.Errorf("model %s has invalid task billing mode %q", model, config.Mode)
	}

	resolution = NormalizeTaskBillingResolution(resolution)
	resolutionPricingEnabled := len(config.ResolutionPrices) > 0
	if resolution != "" {
		if price, ok := config.ResolutionPrices[resolution]; ok {
			if err := validateTaskBillingPrice(price); err != nil {
				return TaskBillingPriceSelection{}, true, fmt.Errorf("model %s resolution %s: %w", model, resolution, err)
			}
			return TaskBillingPriceSelection{Mode: config.Mode, Price: price, Resolution: resolution}, true, nil
		}
		if resolutionPricingEnabled {
			return TaskBillingPriceSelection{}, true, fmt.Errorf("model %s has no task price configured for resolution %s", model, resolution)
		}
	}

	if resolutionPricingEnabled {
		return TaskBillingPriceSelection{}, true, fmt.Errorf("model %s requires a configured resolution for task pricing", model)
	}

	if config.DefaultPrice != nil {
		if err := validateTaskBillingPrice(*config.DefaultPrice); err != nil {
			return TaskBillingPriceSelection{}, true, fmt.Errorf("model %s default task price: %w", model, err)
		}
		return TaskBillingPriceSelection{Mode: config.Mode, Price: *config.DefaultPrice, Resolution: resolution}, true, nil
	}

	if resolution == "" {
		return TaskBillingPriceSelection{}, true, fmt.Errorf("model %s has no default task price and the request has no resolution", model)
	}
	return TaskBillingPriceSelection{}, true, fmt.Errorf("model %s has no task price configured for resolution %s and no default price", model, resolution)
}

func cloneTaskBillingPriceConfig(config TaskBillingPriceConfig) TaskBillingPriceConfig {
	if config.ResolutionPrices == nil {
		return config
	}
	config.ResolutionPrices = lo.Assign(config.ResolutionPrices)
	return config
}

func validateTaskBillingPrice(price float64) error {
	if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
		return fmt.Errorf("task price must be a finite non-negative number")
	}
	return nil
}

func GetPricingSyncData(base map[string]any) map[string]any {
	extra := make(map[string]any, 3)
	if modes := GetBillingModeCopy(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := GetBillingExprCopy(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	if prices := GetTaskBillingPricingCopy(); len(prices) > 0 {
		extra[TaskBillingPricingField] = prices
	}
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}

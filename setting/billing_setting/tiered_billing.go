package billing_setting

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio          = "ratio"
	BillingModeTieredExpr     = "tiered_expr"
	BillingModePerRequest     = "per-request"
	BillingModePerSecond      = "per-second"
	BillingModeField          = "billing_mode"
	BillingExprField          = "billing_expr"
	TaskBillingPricingField   = "task_billing_pricing"
	ScheduledDiscountField    = "scheduled_discount"
	ScheduledDiscountRatioKey = "scheduled_discount"
)

const (
	scheduledDiscountTimeLayout = "15:04"
	scheduledDiscountTimezone   = "Asia/Shanghai"
)

// ScheduledDiscountConfig applies one recurring daily discount period to a
// model. Discount is a price multiplier: 0.8 charges 80% of the configured
// model price. All periods use Beijing time so every deployment is consistent.
type ScheduledDiscountConfig struct {
	Enabled  bool    `json:"enabled"`
	Start    string  `json:"start"`
	End      string  `json:"end"`
	Discount float64 `json:"discount"`
}

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
// billing_setting.task_billing_pricing, billing_setting.scheduled_discount
type BillingSetting struct {
	BillingMode        map[string]string                  `json:"billing_mode"`
	BillingExpr        map[string]string                  `json:"billing_expr"`
	TaskBillingPricing map[string]TaskBillingPriceConfig  `json:"task_billing_pricing"`
	ScheduledDiscount  map[string]ScheduledDiscountConfig `json:"scheduled_discount"`
}

var billingSetting = BillingSetting{
	BillingMode:        make(map[string]string),
	BillingExpr:        make(map[string]string),
	TaskBillingPricing: make(map[string]TaskBillingPriceConfig),
	ScheduledDiscount:  make(map[string]ScheduledDiscountConfig),
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

func GetScheduledDiscountCopy() map[string]ScheduledDiscountConfig {
	return lo.Assign(billingSetting.ScheduledDiscount)
}

func GetScheduledDiscountConfig(model string) (ScheduledDiscountConfig, bool) {
	config, ok := billingSetting.ScheduledDiscount[model]
	return config, ok
}

// ValidateScheduledDiscountJSONString validates the option payload before it
// is persisted. Disabled entries may omit their time range and multiplier.
func ValidateScheduledDiscountJSONString(value string) error {
	configs := make(map[string]ScheduledDiscountConfig)
	if err := common.UnmarshalJsonStr(value, &configs); err != nil {
		return fmt.Errorf("scheduled discount must be a JSON object: %w", err)
	}
	for model, config := range configs {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("scheduled discount model name cannot be empty")
		}
		if err := validateScheduledDiscountConfig(config); err != nil {
			return fmt.Errorf("scheduled discount for model %s: %w", model, err)
		}
	}
	return nil
}

func validateScheduledDiscountConfig(config ScheduledDiscountConfig) error {
	if !config.Enabled {
		return nil
	}
	start, err := parseScheduledDiscountTime(config.Start)
	if err != nil {
		return fmt.Errorf("invalid start time: %w", err)
	}
	end, err := parseScheduledDiscountTime(config.End)
	if err != nil {
		return fmt.Errorf("invalid end time: %w", err)
	}
	if start == end {
		return fmt.Errorf("start time and end time cannot be the same")
	}
	if math.IsNaN(config.Discount) || math.IsInf(config.Discount, 0) || config.Discount <= 0 || config.Discount > 1 {
		return fmt.Errorf("discount must be a finite number greater than 0 and at most 1")
	}
	return nil
}

func parseScheduledDiscountTime(value string) (int, error) {
	if len(value) != len(scheduledDiscountTimeLayout) {
		return 0, fmt.Errorf("must use HH:MM")
	}
	parsed, err := time.Parse(scheduledDiscountTimeLayout, value)
	if err != nil || parsed.Format(scheduledDiscountTimeLayout) != value {
		return 0, fmt.Errorf("must use HH:MM")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

// ScheduledDiscountMultiplier returns the multiplier active at at. Invalid
// legacy values are ignored so a bad historical option never changes billing.
func ScheduledDiscountMultiplier(model string, at time.Time) float64 {
	config, ok := billingSetting.ScheduledDiscount[model]
	if !ok || !config.Enabled || validateScheduledDiscountConfig(config) != nil {
		return 1
	}
	start, _ := parseScheduledDiscountTime(config.Start)
	end, _ := parseScheduledDiscountTime(config.End)
	location, err := time.LoadLocation(scheduledDiscountTimezone)
	if err != nil {
		common.SysError("load scheduled discount timezone: " + err.Error())
		return 1
	}
	local := at.In(location)
	minute := local.Hour()*60 + local.Minute()
	active := false
	if start < end {
		active = minute >= start && minute < end
	} else {
		// A start after end deliberately spans midnight, such as 22:00-02:00.
		active = minute >= start || minute < end
	}
	if !active {
		return 1
	}
	return config.Discount
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
	case "768p", "768", "1366x768", "768x1366", "1376x768", "768x1376", "1280x768", "768x1280":
		return "768p"
	case "720p", "1280x720", "720x1280", "1024x1024":
		return "720p"
	case "480p", "854x480", "480x854":
		return "480p"
	default:
		return value
	}
}

const (
	miniMaxH3MinMegapixels      = 0.2
	miniMaxH3LowMaxMegapixels   = 0.7
	miniMaxH3SizeLowMegapixels  = 0.8
	miniMaxH3HighMaxMegapixels  = 2.0
	miniMaxH3SizeHighMegapixels = 2.2
)

// NormalizeTaskBillingResolutionForModel handles provider-specific quality
// bands before the generic aliases are applied. MiniMax H3 exposes many
// arbitrary pixel sizes instead of a small fixed resolution enum.
func NormalizeTaskBillingResolutionForModel(model, value string) string {
	if isMiniMaxH3Model(model) {
		if normalized, recognized := normalizeMiniMaxH3Resolution(value); recognized {
			return normalized
		}
	}
	return NormalizeTaskBillingResolution(value)
}

func isMiniMaxH3Model(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	normalized = strings.NewReplacer("-", "", "_", "", " ", "").Replace(normalized)
	return strings.Contains(normalized, "minimaxh3")
}

func normalizeMiniMaxH3Resolution(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "×", "x")))
	normalized = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(normalized)
	switch normalized {
	case "480p", "480", "720p", "720", "768p", "768", "low", "medium", "standard":
		return "768p", true
	case "1080p", "1080", "2k", "1440p", "1440", "4k", "high":
		return "1080p", true
	}

	if strings.HasSuffix(normalized, "mp") {
		normalized = strings.TrimSuffix(normalized, "mp")
	}
	if megapixels, err := strconv.ParseFloat(normalized, 64); err == nil {
		if megapixels >= miniMaxH3MinMegapixels && megapixels <= miniMaxH3LowMaxMegapixels {
			return "768p", true
		}
		if megapixels > miniMaxH3LowMaxMegapixels && megapixels <= miniMaxH3HighMaxMegapixels {
			return "1080p", true
		}
		return "", true
	}

	parts := strings.Split(normalized, "x")
	if len(parts) != 2 {
		return "", false
	}
	width, widthErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	height, heightErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return "", true
	}
	megapixels := width * height / 1_000_000
	if megapixels < miniMaxH3MinMegapixels {
		return "", true
	}
	if megapixels <= miniMaxH3SizeLowMegapixels {
		return "768p", true
	}
	if megapixels <= miniMaxH3SizeHighMegapixels {
		return "1080p", true
	}
	return "", true
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

	resolution = NormalizeTaskBillingResolutionForModel(model, resolution)
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
	extra := make(map[string]any, 4)
	if modes := GetBillingModeCopy(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := GetBillingExprCopy(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	if prices := GetTaskBillingPricingCopy(); len(prices) > 0 {
		extra[TaskBillingPricingField] = prices
	}
	if discounts := GetScheduledDiscountCopy(); len(discounts) > 0 {
		extra[ScheduledDiscountField] = discounts
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

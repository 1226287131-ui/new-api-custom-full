package billing_setting

import (
	"fmt"
	"maps"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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
	PluginBillingExprOption   = "billing_setting.plugin_billing_expr"
	maxTaskExprSmokeTests     = 64
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
type TaskBillingPriceSelection = relaycommon.TaskBillingPriceSelection

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr,
// billing_setting.task_billing_pricing, billing_setting.scheduled_discount,
// billing_setting.plugin_billing_expr
type BillingSetting struct {
	BillingMode        map[string]string                  `json:"billing_mode"`
	BillingExpr        map[string]string                  `json:"billing_expr"`
	TaskBillingPricing map[string]TaskBillingPriceConfig  `json:"task_billing_pricing"`
	ScheduledDiscount  map[string]ScheduledDiscountConfig `json:"scheduled_discount"`
	PluginBillingExpr  map[string]string                  `json:"plugin_billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode:        make(map[string]string),
	BillingExpr:        make(map[string]string),
	TaskBillingPricing: make(map[string]TaskBillingPriceConfig),
	ScheduledDiscount:  make(map[string]ScheduledDiscountConfig),
	PluginBillingExpr:  make(map[string]string),
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
	if _, ok := builtinBillingExpr[model]; ok {
		// Existing administrator-configured legacy prices take precedence over
		// a newly introduced built-in expression unless a mode was explicit.
		if ratio_setting.HasConfiguredModelRatio(model) {
			return BillingModeRatio
		}
		if _, configured := ratio_setting.GetModelPrice(model, false); configured {
			return BillingModeRatio
		}
		if _, configured := ratio_setting.GetImageResolutionPrice(model); configured {
			return BillingModeRatio
		}
		if _, configured := billingSetting.TaskBillingPricing[model]; configured {
			return BillingModeRatio
		}
		return BillingModeTieredExpr
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
	if expr, ok := billingSetting.BillingExpr[model]; ok {
		return expr, true
	}
	if GetBillingMode(model) == BillingModeTieredExpr {
		expr, ok := builtinBillingExpr[model]
		return expr, ok
	}
	return "", false
}

func GetBuiltinBillingExpr(model string) (string, bool) {
	expression, ok := builtinBillingExpr[model]
	return expression, ok
}

func PluginBillingExprKey(pluginKey, model string) string {
	return pluginKey + "::" + model
}

func SplitPluginBillingExprKey(key string) (plugin, model string, ok bool) {
	plugin, model, ok = strings.Cut(key, "::")
	if !ok || !jsplugin.ValidPluginKey(plugin) || strings.TrimSpace(model) == "" {
		return "", "", false
	}
	return plugin, model, true
}

func GetPluginBillingExprCopy() map[string]string {
	return maps.Clone(billingSetting.PluginBillingExpr)
}

func GetPluginBillingExpr(pluginKey, model string) (string, bool) {
	expression, ok := billingSetting.PluginBillingExpr[PluginBillingExprKey(pluginKey, model)]
	return expression, ok
}

// ResolveTaskBillingExpr selects the executing plugin's override before the
// model expression, retaining the model alias fallback and explicit modes.
func ResolveTaskBillingExpr(pluginKey, model, mappedModel string) (string, bool) {
	if pluginKey != "" {
		if expr, ok := GetPluginBillingExpr(pluginKey, model); ok {
			return expr, true
		}
		if mappedModel != "" && mappedModel != model {
			if expr, ok := GetPluginBillingExpr(pluginKey, mappedModel); ok {
				return expr, true
			}
		}
	}
	if GetBillingMode(model) == BillingModeTieredExpr {
		return GetBillingExpr(model)
	}
	if mappedModel != "" && mappedModel != model && GetBillingMode(mappedModel) == BillingModeTieredExpr {
		expression, ok := GetBillingExpr(mappedModel)
		return expression, ok && strings.TrimSpace(expression) != ""
	}
	return "", false
}

// TaskExprCompatible checks the schema contract even for usage references in
// branches that the current request would not evaluate.
func TaskExprCompatible(expression string, schema map[string]jsplugin.UsageFieldSchema) bool {
	if strings.TrimSpace(expression) == "" {
		return false
	}
	if _, err := billingexpr.CompileFromCache(expression); err != nil {
		return false
	}
	for key := range billingexpr.UsedUsageKeys(expression) {
		if _, exists := schema[key]; !exists {
			return false
		}
	}
	return !billingexpr.UsesFixedPricing(expression)
}

func GetBuiltinBillingExprCopy() map[string]string {
	return lo.Assign(builtinBillingExpr)
}

func GetBillingModeCopy() map[string]string {
	modes := lo.Assign(billingSetting.BillingMode)
	for model := range builtinBillingExpr {
		if _, configured := modes[model]; !configured && GetBillingMode(model) == BillingModeTieredExpr {
			modes[model] = BillingModeTieredExpr
		}
	}
	return modes
}

func GetBillingExprCopy() map[string]string {
	expressions := lo.Assign(billingSetting.BillingExpr)
	for model := range builtinBillingExpr {
		if _, configured := expressions[model]; configured {
			continue
		}
		if expression, ok := GetBillingExpr(model); ok {
			expressions[model] = expression
		}
	}
	return expressions
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

// ValidateTaskBillingPricingJSONString validates complete task pricing tables
// before an atomic pricing update can publish them to the relay.
func ValidateTaskBillingPricingJSONString(value string) error {
	var configs map[string]*TaskBillingPriceConfig
	if err := common.UnmarshalJsonStr(value, &configs); err != nil {
		return err
	}
	if configs == nil {
		return fmt.Errorf("task pricing must be a JSON object")
	}
	for model, config := range configs {
		if strings.TrimSpace(model) == "" || config == nil {
			return fmt.Errorf("task pricing requires a model and a pricing object")
		}
		if config.Mode != BillingModePerRequest && config.Mode != BillingModePerSecond {
			return fmt.Errorf("model %s has invalid task billing mode %q", model, config.Mode)
		}
		if config.DefaultPrice == nil && len(config.ResolutionPrices) == 0 {
			return fmt.Errorf("model %s requires a default price or resolution prices", model)
		}
		if config.DefaultPrice != nil {
			if err := validateTaskBillingPrice(*config.DefaultPrice); err != nil {
				return fmt.Errorf("model %s: %w", model, err)
			}
		}
		for resolution, price := range config.ResolutionPrices {
			if strings.TrimSpace(resolution) == "" {
				return fmt.Errorf("model %s has an empty resolution", model)
			}
			if err := validateTaskBillingPrice(price); err != nil {
				return fmt.Errorf("model %s resolution %s: %w", model, resolution, err)
			}
		}
	}
	return nil
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
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "×", "x")))
	value = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(value)
	switch value {
	case "4k", "2160p", "2160", "4096p", "4096", "2160x2160":
		return "4k"
	case "2k", "1440p", "1440", "2048p", "2048", "1920x1440", "1440x1920", "1440x1440":
		return "1440p"
	case "1080p", "1920x1080", "1080x1920", "1792x1024", "1024x1792", "1080x1080":
		return "1080p"
	case "768p", "768", "1366x768", "768x1366", "1376x768", "768x1376", "1280x768", "768x1280":
		return "768p"
	case "720p", "1280x720", "720x1280", "1024x1024":
		return "720p"
	case "480p", "854x480", "480x854":
		return "480p"
	}

	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return value
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return value
	}
	longer, shorter := width, height
	if shorter > longer {
		longer, shorter = shorter, longer
	}

	// 4K covers UHD, DCI, scope, and common ultrawide 4K variants. Keep the
	// range bounded so 5K/8K requests cannot be charged as 4K by mistake.
	if longer >= 3840 && longer <= 4096 && shorter >= 1600 && shorter <= 2304 {
		return "4k"
	}
	// 2K covers DCI 2K plus QHD/WQHD/QHD+ families. The internal pricing key
	// remains 1440p for backward-compatible saved pricing configurations.
	if longer >= 1998 && longer <= 3440 && shorter >= 858 && shorter <= 1800 {
		return "1440p"
	}
	return value
}

const (
	miniMaxH3MinMegapixels     = 0.2
	miniMaxH3LowMaxMegapixels  = 0.7
	miniMaxH3HighMaxMegapixels = 2.0
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
	if tier, ok := constant.MiniMaxH3ResolutionTierForSize(normalized); ok {
		return tier, true
	}
	switch tier := NormalizeTaskBillingResolution(normalized); tier {
	case "480p", "720p", "768p", "1080p", "1440p", "4k":
		return tier, true
	}
	switch normalized {
	case "2k":
		return "1440p", true
	case "480":
		return "480p", true
	case "720":
		return "720p", true
	case "1080":
		return "1080p", true
	case "1440":
		return "1440p", true
	case "2160":
		return "4k", true
	case "low", "medium", "standard":
		return constant.MiniMaxH3Resolution768P, true
	case "high":
		return constant.MiniMaxH3Resolution1080P, true
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
	if _, err := billingexpr.CompileFromCache(exprStr); err != nil {
		return err
	}
	usageKeys := billingexpr.UsedUsageKeys(exprStr)
	if len(usageKeys) > 0 {
		sortedKeys := make([]string, 0, len(usageKeys))
		for key := range usageKeys {
			sortedKeys = append(sortedKeys, key)
		}
		sort.Strings(sortedKeys)
		return fmt.Errorf("expression references usage keys %v but the model has no task plugin usage schema", sortedKeys)
	}

	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
		{P: 300, C: 100, Len: 1000, CR: 100, Img: 400, ImgCR: 200},
		{P: 800, C: 50, Len: 1000, AI: 200, AO: 50},
		{Len: math.MaxInt32, ImgCR: math.MaxInt32},
	}

	for _, v := range vectors {
		for _, request := range billingExprSmokeRequests() {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if math.IsNaN(result) || math.IsInf(result, 0) || result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result must be finite and non-negative, got %f", v.P, v.C, result)
			}
		}
	}
	return nil
}

// SmokeTestTaskExpr validates a task usage expression against the usage facts
// declared by its plugin. Literal u() keys must be declared; dynamic calls are
// still exercised by the generated runtime vectors when possible.
func SmokeTestTaskExpr(exprStr string, schema map[string]jsplugin.UsageFieldSchema) error {
	if _, err := billingexpr.CompileFromCache(exprStr); err != nil {
		return err
	}
	if billingexpr.UsesFixedPricing(exprStr) {
		return fmt.Errorf("fixed pricing is not supported for task usage expressions")
	}
	for key := range billingexpr.UsedUsageKeys(exprStr) {
		if _, declared := schema[key]; !declared {
			return fmt.Errorf("usage key %q is not declared by the task plugin", key)
		}
	}

	for _, usage := range taskUsageSmokeVectors(schema) {
		for _, request := range billingExprSmokeRequests() {
			request.Usage = usage
			result, _, err := billingexpr.RunExprWithRequest(exprStr, billingexpr.TokenParams{}, request)
			if err != nil {
				return fmt.Errorf("usage vector %v: run failed: %w", usage, err)
			}
			if math.IsNaN(result) || math.IsInf(result, 0) || result < 0 {
				return fmt.Errorf("usage vector %v: result must be finite and non-negative, got %f", usage, result)
			}
		}
	}
	return nil
}

type usageSmokeDimension struct {
	name   string
	values []any
}

func taskUsageSmokeVectors(schema map[string]jsplugin.UsageFieldSchema) []map[string]any {
	names := make([]string, 0, len(schema))
	for name := range schema {
		names = append(names, name)
	}
	sort.Strings(names)

	dimensions := make([]usageSmokeDimension, 0, len(names))
	for _, name := range names {
		field := schema[name]
		if len(field.Enum) > 0 {
			values := make([]any, len(field.Enum))
			for index, value := range field.Enum {
				values[index] = value
			}
			dimensions = append(dimensions, usageSmokeDimension{name: name, values: values})
			continue
		}
		if field.Type == "boolean" {
			dimensions = append(dimensions, usageSmokeDimension{name: name, values: []any{false, true}})
			continue
		}
		limit := relaycommon.MaxTaskDurationSeconds
		if field.Unit == "count" {
			limit = dto.MaxImageN
		}
		if field.Unit == "token" || field.Unit == "credit" {
			limit = common.MaxQuota
		}
		dimensions = append(dimensions, usageSmokeDimension{
			name:   name,
			values: []any{float64(0), float64(1), float64(limit)},
		})
	}

	if usageSmokeCombinationCount(dimensions, maxTaskExprSmokeTests) > maxTaskExprSmokeTests {
		for index := range dimensions {
			field := schema[dimensions[index].name]
			if len(field.Enum) <= 2 {
				continue
			}
			dimensions[index].values = []any{field.Enum[0], field.Enum[len(field.Enum)-1]}
		}
	}

	vectors := make([]map[string]any, 0, maxTaskExprSmokeTests)
	var appendVectors func(int, map[string]any)
	appendVectors = func(index int, current map[string]any) {
		if len(vectors) >= maxTaskExprSmokeTests {
			return
		}
		if index == len(dimensions) {
			vector := make(map[string]any, len(current))
			maps.Copy(vector, current)
			vectors = append(vectors, vector)
			return
		}
		for _, value := range dimensions[index].values {
			current[dimensions[index].name] = value
			appendVectors(index+1, current)
		}
		delete(current, dimensions[index].name)
	}
	appendVectors(0, make(map[string]any, len(dimensions)))

	combinationCount := usageSmokeCombinationCount(dimensions, maxTaskExprSmokeTests)
	if combinationCount > maxTaskExprSmokeTests && len(vectors) > 0 {
		last := make(map[string]any, len(dimensions))
		for _, dimension := range dimensions {
			last[dimension.name] = dimension.values[len(dimension.values)-1]
		}
		vectors[len(vectors)-1] = last
	}
	return vectors
}

func usageSmokeCombinationCount(dimensions []usageSmokeDimension, stopAfter int) int {
	count := 1
	for _, dimension := range dimensions {
		if len(dimension.values) == 0 {
			return 0
		}
		if count > stopAfter/len(dimension.values) {
			return stopAfter + 1
		}
		count *= len(dimension.values)
	}
	return count
}

func billingExprSmokeRequests() []billingexpr.RequestInput {
	return []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}
}

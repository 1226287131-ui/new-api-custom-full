package helper

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/tidwall/gjson"
)

const (
	maxImageResolutionDimension = 16384
	imageResolution2KMinPixels  = 2_000_000
	imageResolution4KMinPixels  = 8_000_000
	imageResolution4KMaxPixels  = 20 * 1024 * 1024
)

var (
	imageDimensionsPattern  = regexp.MustCompile(`^\s*(\d+)\s*[xX*]\s*(\d+)\s*$`)
	imageAspectRatioPattern = regexp.MustCompile(`^\s*\d+\s*:\s*\d+\s*$`)
	imageResolutionPaths    = []string{
		"size",
		"imageSize",
		"image_size",
		"resolution",
		"outputResolution",
		"output_resolution",
		"parameters.size",
		"generationConfig.imageConfig.imageSize",
		"generationConfig.imageConfig.image_size",
		"generationConfig.imageConfig.resolution",
		"generation_config.image_config.imageSize",
		"generation_config.image_config.image_size",
		"generation_config.image_config.resolution",
		"parameters.imageSize",
		"parameters.image_size",
		"parameters.resolution",
		"parameters.outputResolution",
		"parameters.output_resolution",
		"imageConfig.imageSize",
		"imageConfig.image_size",
		"imageConfig.resolution",
		"image_config.imageSize",
		"image_config.image_size",
		"image_config.resolution",
		"input.size",
		"input.imageSize",
		"input.image_size",
		"input.resolution",
		"input.outputResolution",
		"input.output_resolution",
		"extra_body.size",
		"extra_body.imageSize",
		"extra_body.image_size",
		"extra_body.resolution",
		"extra_body.outputResolution",
		"extra_body.output_resolution",
		"extra_body.parameters.size",
		"extra_body.parameters.imageSize",
		"extra_body.parameters.image_size",
		"extra_body.parameters.resolution",
		"extra_body.parameters.outputResolution",
		"extra_body.parameters.output_resolution",
		"extra_body.google.image_config.image_size",
		"extra_body.google.image_config.imageSize",
		"extra_body.google.image_config.resolution",
		"extra_body.google.imageConfig.image_size",
		"extra_body.google.imageConfig.imageSize",
		"extra_body.google.imageConfig.resolution",
		"extra_body.generationConfig.imageConfig.imageSize",
		"extra_body.generationConfig.imageConfig.image_size",
		"extra_body.generationConfig.imageConfig.resolution",
		"extra_body.generation_config.image_config.imageSize",
		"extra_body.generation_config.image_config.image_size",
		"extra_body.generation_config.image_config.resolution",
	}
	imageDimensionPaths = [][2]string{
		{"width", "height"},
		{"parameters.width", "parameters.height"},
		{"imageConfig.width", "imageConfig.height"},
		{"image_config.width", "image_config.height"},
		{"generationConfig.imageConfig.width", "generationConfig.imageConfig.height"},
		{"generationConfig.image_config.width", "generationConfig.image_config.height"},
		{"generation_config.imageConfig.width", "generation_config.imageConfig.height"},
		{"generation_config.image_config.width", "generation_config.image_config.height"},
		{"input.width", "input.height"},
		{"extra_body.width", "extra_body.height"},
		{"extra_body.parameters.width", "extra_body.parameters.height"},
		{"extra_body.imageConfig.width", "extra_body.imageConfig.height"},
		{"extra_body.image_config.width", "extra_body.image_config.height"},
		{"extra_body.google.imageConfig.width", "extra_body.google.imageConfig.height"},
		{"extra_body.google.image_config.width", "extra_body.google.image_config.height"},
	}
	imageCountPaths = []string{
		"n",
		"batchSize",
		"batch_size",
		"sampleCount",
		"sample_count",
		"candidateCount",
		"candidate_count",
		"numOutputs",
		"num_outputs",
		"numImages",
		"num_images",
		"numberOfImages",
		"number_of_images",
		"imageCount",
		"image_count",
		"parameters.n",
		"parameters.batchSize",
		"parameters.batch_size",
		"parameters.sampleCount",
		"parameters.sample_count",
		"parameters.candidateCount",
		"parameters.candidate_count",
		"parameters.numOutputs",
		"parameters.num_outputs",
		"parameters.numImages",
		"parameters.num_images",
		"parameters.numberOfImages",
		"parameters.number_of_images",
		"parameters.imageCount",
		"parameters.image_count",
		"generationConfig.candidateCount",
		"generationConfig.sampleCount",
		"generation_config.candidate_count",
		"generation_config.sample_count",
		"input.n",
		"input.batchSize",
		"input.batch_size",
		"input.sampleCount",
		"input.sample_count",
		"input.candidateCount",
		"input.candidate_count",
		"input.numOutputs",
		"input.num_outputs",
		"input.numImages",
		"input.num_images",
		"input.numberOfImages",
		"input.number_of_images",
		"input.imageCount",
		"input.image_count",
		"extra_body.n",
		"extra_body.batchSize",
		"extra_body.batch_size",
		"extra_body.sampleCount",
		"extra_body.sample_count",
		"extra_body.candidateCount",
		"extra_body.candidate_count",
		"extra_body.numOutputs",
		"extra_body.num_outputs",
		"extra_body.numImages",
		"extra_body.num_images",
		"extra_body.numberOfImages",
		"extra_body.number_of_images",
		"extra_body.imageCount",
		"extra_body.image_count",
		"extra_body.parameters.n",
		"extra_body.parameters.batchSize",
		"extra_body.parameters.batch_size",
		"extra_body.parameters.sampleCount",
		"extra_body.parameters.sample_count",
		"extra_body.parameters.candidateCount",
		"extra_body.parameters.candidate_count",
		"extra_body.parameters.numOutputs",
		"extra_body.parameters.num_outputs",
		"extra_body.parameters.numImages",
		"extra_body.parameters.num_images",
		"extra_body.parameters.numberOfImages",
		"extra_body.parameters.number_of_images",
		"extra_body.parameters.imageCount",
		"extra_body.parameters.image_count",
	}
)

type imageResolutionBilling struct {
	Tier               string
	Count              float64
	ExplicitResolution bool
	ExplicitCount      bool
}

func ClassifyImageResolution(raw string) (string, error) {
	tier, explicit, err := classifyImageResolutionSignal(raw)
	if err != nil {
		return "", err
	}
	if !explicit {
		return ratio_setting.ImageResolutionTier1K, nil
	}
	return tier, nil
}

func classifyImageResolutionSignal(raw string) (string, bool, error) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	switch normalized {
	case "", "AUTO":
		return "", false, nil
	case ratio_setting.ImageResolutionTier1K:
		return ratio_setting.ImageResolutionTier1K, true, nil
	case ratio_setting.ImageResolutionTier2K:
		return ratio_setting.ImageResolutionTier2K, true, nil
	case ratio_setting.ImageResolutionTier4K:
		return ratio_setting.ImageResolutionTier4K, true, nil
	}

	if imageAspectRatioPattern.MatchString(normalized) {
		parts := strings.Split(normalized, ":")
		width, _ := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		height, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		if width == 0 || height == 0 {
			return "", false, fmt.Errorf("invalid image aspect ratio %q", raw)
		}
		return "", false, nil
	}

	matches := imageDimensionsPattern.FindStringSubmatch(normalized)
	if len(matches) != 3 {
		return "", false, fmt.Errorf("unsupported image resolution %q; use 1K, 2K, 4K or WxH", raw)
	}
	width, err := strconv.ParseUint(matches[1], 10, 32)
	if err != nil || width == 0 || width > maxImageResolutionDimension {
		return "", false, fmt.Errorf("invalid image width in resolution %q", raw)
	}
	height, err := strconv.ParseUint(matches[2], 10, 32)
	if err != nil || height == 0 || height > maxImageResolutionDimension {
		return "", false, fmt.Errorf("invalid image height in resolution %q", raw)
	}

	pixels := width * height
	switch {
	// Decimal megapixel boundaries keep standard 1920x1080 and 3840x2160
	// dimensions in their advertised 2K and 4K billing tiers.
	case pixels < imageResolution2KMinPixels:
		return ratio_setting.ImageResolutionTier1K, true, nil
	case pixels < imageResolution4KMinPixels:
		return ratio_setting.ImageResolutionTier2K, true, nil
	case pixels <= imageResolution4KMaxPixels:
		return ratio_setting.ImageResolutionTier4K, true, nil
	default:
		return "", false, fmt.Errorf("image resolution %q exceeds the supported 4K tier", raw)
	}
}

func parseImageBillingCount(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return 1, nil
	}
	count, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(count) || math.IsInf(count, 0) || count != math.Trunc(count) {
		return 0, fmt.Errorf("image count must be an integer between 1 and %d", dto.MaxImageN)
	}
	if count < 1 || count > dto.MaxImageN {
		return 0, fmt.Errorf("image count must be an integer between 1 and %d", dto.MaxImageN)
	}
	return count, nil
}

func addImageResolutionSignal(billing *imageResolutionBilling, selectedSource *string, source string, raw string) error {
	tier, explicit, err := classifyImageResolutionSignal(raw)
	if err != nil {
		return fmt.Errorf("image resolution at %s is invalid: %w", source, err)
	}
	if !explicit {
		return nil
	}
	if billing.ExplicitResolution && billing.Tier != tier {
		return fmt.Errorf(
			"conflicting image resolution parameters: %s selects %s but %s selects %s",
			*selectedSource, billing.Tier, source, tier,
		)
	}
	billing.Tier = tier
	billing.ExplicitResolution = true
	if *selectedSource == "" {
		*selectedSource = source
	}
	return nil
}

func addImageCountSignal(billing *imageResolutionBilling, selectedSource *string, source string, raw string) error {
	count, err := parseImageBillingCount(raw)
	if err != nil {
		return fmt.Errorf("image count at %s is invalid: %w", source, err)
	}
	if billing.ExplicitCount && billing.Count != count {
		return fmt.Errorf(
			"conflicting image count parameters: %s selects %.0f but %s selects %.0f",
			*selectedSource, billing.Count, source, count,
		)
	}
	billing.Count = count
	billing.ExplicitCount = true
	if *selectedSource == "" {
		*selectedSource = source
	}
	return nil
}

func resolveImageResolutionBilling(input billingexpr.RequestInput, meta *types.TokenCountMeta) (imageResolutionBilling, error) {
	billing := imageResolutionBilling{
		Tier:  ratio_setting.ImageResolutionTier1K,
		Count: 1,
	}
	resolutionSource := ""
	countSource := ""

	for _, path := range imageResolutionPaths {
		value := gjson.GetBytes(input.Body, path)
		if !value.Exists() {
			continue
		}
		if value.Type != gjson.String {
			return imageResolutionBilling{}, fmt.Errorf("image resolution at %s must be a string", path)
		}
		if err := addImageResolutionSignal(&billing, &resolutionSource, path, value.String()); err != nil {
			return imageResolutionBilling{}, err
		}
	}

	for _, paths := range imageDimensionPaths {
		width := gjson.GetBytes(input.Body, paths[0])
		height := gjson.GetBytes(input.Body, paths[1])
		if !width.Exists() && !height.Exists() {
			continue
		}
		if !width.Exists() || !height.Exists() {
			return imageResolutionBilling{}, fmt.Errorf("image width and height at %s/%s must be provided together", paths[0], paths[1])
		}
		if (width.Type != gjson.Number && width.Type != gjson.String) ||
			(height.Type != gjson.Number && height.Type != gjson.String) {
			return imageResolutionBilling{}, fmt.Errorf("image width and height at %s/%s must be numbers or numeric strings", paths[0], paths[1])
		}
		source := paths[0] + "/" + paths[1]
		if err := addImageResolutionSignal(&billing, &resolutionSource, source, width.String()+"x"+height.String()); err != nil {
			return imageResolutionBilling{}, err
		}
	}

	if meta != nil && strings.TrimSpace(meta.ImageResolution) != "" {
		if err := addImageResolutionSignal(&billing, &resolutionSource, "request metadata", meta.ImageResolution); err != nil {
			return imageResolutionBilling{}, err
		}
	}

	for _, path := range imageCountPaths {
		value := gjson.GetBytes(input.Body, path)
		if !value.Exists() {
			continue
		}
		if value.Type != gjson.Number && value.Type != gjson.String {
			return imageResolutionBilling{}, fmt.Errorf("image count at %s must be a number or numeric string", path)
		}
		rawCount := strings.TrimSpace(value.String())
		if rawCount == "" || rawCount == "0" {
			continue
		}
		if err := addImageCountSignal(&billing, &countSource, path, rawCount); err != nil {
			return imageResolutionBilling{}, err
		}
	}

	if !billing.ExplicitCount && meta != nil {
		if requestedCount, ok := meta.BillingRatios["n"]; ok {
			rawCount := strconv.FormatFloat(requestedCount, 'f', -1, 64)
			if err := addImageCountSignal(&billing, &countSource, "request metadata", rawCount); err != nil {
				return imageResolutionBilling{}, err
			}
		}
	}

	return billing, nil
}

func ValidateOutboundImageBilling(body []byte, priceData types.PriceData) error {
	if priceData.ImageResolutionTier == "" {
		return nil
	}

	billing, err := resolveImageResolutionBilling(billingexpr.RequestInput{Body: body}, nil)
	if err != nil {
		return err
	}
	if billing.ExplicitResolution && billing.Tier != priceData.ImageResolutionTier {
		return fmt.Errorf(
			"outbound image resolution %s does not match the pre-consumed %s tier",
			billing.Tier, priceData.ImageResolutionTier,
		)
	}

	expectedCount := priceData.ImageGenerationCount
	if expectedCount <= 0 {
		expectedCount = 1
		if count, ok := priceData.OtherRatios()["n"]; ok {
			expectedCount = count
		}
	}
	if billing.ExplicitCount && billing.Count != expectedCount {
		return fmt.Errorf(
			"outbound image count %.0f does not match the pre-consumed count %.0f",
			billing.Count, expectedCount,
		)
	}
	return nil
}

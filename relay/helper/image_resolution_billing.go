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
	imageDimensionsPattern  = regexp.MustCompile(`^\s*(\d+)\s*[xX]\s*(\d+)\s*$`)
	imageAspectRatioPattern = regexp.MustCompile(`^\s*\d+\s*:\s*\d+\s*$`)
	imageResolutionPaths    = []string{
		"generationConfig.imageConfig.imageSize",
		"generation_config.image_config.image_size",
		"parameters.imageSize",
		"parameters.image_size",
		"extra_body.google.image_config.image_size",
		"extra_body.google.image_config.imageSize",
		"imageConfig.imageSize",
		"image_config.image_size",
		"imageSize",
		"image_size",
		"size",
	}
	imageCountPaths = []string{
		"n",
		"parameters.sampleCount",
		"parameters.sample_count",
		"generationConfig.candidateCount",
		"generation_config.candidate_count",
		"sampleCount",
		"sample_count",
		"batch_size",
	}
)

type imageResolutionBilling struct {
	Tier  string
	Count float64
}

func ClassifyImageResolution(raw string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	switch normalized {
	case "", "AUTO", ratio_setting.ImageResolutionTier1K:
		return ratio_setting.ImageResolutionTier1K, nil
	case ratio_setting.ImageResolutionTier2K:
		return ratio_setting.ImageResolutionTier2K, nil
	case ratio_setting.ImageResolutionTier4K:
		return ratio_setting.ImageResolutionTier4K, nil
	}

	if imageAspectRatioPattern.MatchString(normalized) {
		parts := strings.Split(normalized, ":")
		width, _ := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		height, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		if width == 0 || height == 0 {
			return "", fmt.Errorf("invalid image aspect ratio %q", raw)
		}
		return ratio_setting.ImageResolutionTier1K, nil
	}

	matches := imageDimensionsPattern.FindStringSubmatch(normalized)
	if len(matches) != 3 {
		return "", fmt.Errorf("unsupported image resolution %q; use 1K, 2K, 4K or WxH", raw)
	}
	width, err := strconv.ParseUint(matches[1], 10, 32)
	if err != nil || width == 0 || width > maxImageResolutionDimension {
		return "", fmt.Errorf("invalid image width in resolution %q", raw)
	}
	height, err := strconv.ParseUint(matches[2], 10, 32)
	if err != nil || height == 0 || height > maxImageResolutionDimension {
		return "", fmt.Errorf("invalid image height in resolution %q", raw)
	}

	pixels := width * height
	switch {
	// Decimal megapixel boundaries keep standard 1920x1080 and 3840x2160
	// dimensions in their advertised 2K and 4K billing tiers.
	case pixels < imageResolution2KMinPixels:
		return ratio_setting.ImageResolutionTier1K, nil
	case pixels < imageResolution4KMinPixels:
		return ratio_setting.ImageResolutionTier2K, nil
	case pixels <= imageResolution4KMaxPixels:
		return ratio_setting.ImageResolutionTier4K, nil
	default:
		return "", fmt.Errorf("image resolution %q exceeds the supported 4K tier", raw)
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

func resolveImageResolutionBilling(input billingexpr.RequestInput, meta *types.TokenCountMeta) (imageResolutionBilling, error) {
	rawResolution := ""
	if meta != nil {
		rawResolution = meta.ImageResolution
	}
	if strings.TrimSpace(rawResolution) == "" {
		for _, path := range imageResolutionPaths {
			value := gjson.GetBytes(input.Body, path)
			if value.Exists() {
				if value.Type != gjson.String {
					return imageResolutionBilling{}, fmt.Errorf("image resolution at %s must be a string", path)
				}
				rawResolution = value.String()
				break
			}
		}
	}

	if strings.TrimSpace(rawResolution) == "" {
		for _, paths := range [][2]string{
			{"width", "height"},
			{"parameters.width", "parameters.height"},
			{"imageConfig.width", "imageConfig.height"},
			{"image_config.width", "image_config.height"},
		} {
			width := gjson.GetBytes(input.Body, paths[0])
			height := gjson.GetBytes(input.Body, paths[1])
			if width.Exists() || height.Exists() {
				if !width.Exists() || !height.Exists() {
					return imageResolutionBilling{}, fmt.Errorf("image width and height must be provided together")
				}
				rawResolution = width.String() + "x" + height.String()
				break
			}
		}
	}

	tier, err := ClassifyImageResolution(rawResolution)
	if err != nil {
		return imageResolutionBilling{}, err
	}

	count := float64(1)
	if meta != nil {
		if requestedCount, ok := meta.BillingRatios["n"]; ok {
			count, err = parseImageBillingCount(strconv.FormatFloat(requestedCount, 'f', -1, 64))
			if err != nil {
				return imageResolutionBilling{}, err
			}
			return imageResolutionBilling{Tier: tier, Count: count}, nil
		}
	}
	for _, path := range imageCountPaths {
		value := gjson.GetBytes(input.Body, path)
		if value.Exists() {
			count, err = parseImageBillingCount(value.String())
			if err != nil {
				return imageResolutionBilling{}, err
			}
			break
		}
	}
	return imageResolutionBilling{Tier: tier, Count: count}, nil
}

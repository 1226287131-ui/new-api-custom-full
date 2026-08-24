/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package minimaxvideo

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

const (
	h3BillingResolution480  = constant.MiniMaxH3Resolution480P
	h3BillingResolution768  = constant.MiniMaxH3Resolution768P
	h3BillingResolution1080 = constant.MiniMaxH3Resolution1080P

	// MiniMax documents direct megapixel selections by their nominal bands.
	// Pixel sizes are classified separately from the official size table.
	h3MinMegapixels     = 0.2
	h3LowMaxMegapixels  = 0.7
	h3HighMaxMegapixels = 2.0
)

// resolveH3BillingResolution converts MiniMax H3 quality and size values into
// local billing tiers. It only returns the internal billing tier; the original
// request fields remain untouched for the upstream payload.
func resolveH3BillingResolution(
	resolution, clarity, size string,
	megapixels float64, megapixelsProvided bool,
) (string, error) {
	if value := strings.TrimSpace(resolution); value != "" {
		if tier, recognized, err := classifyH3QualityValue(value); err != nil {
			return "", fmt.Errorf("resolution %q: %w", value, err)
		} else if recognized {
			return tier, nil
		}
	}

	if megapixelsProvided {
		return classifyH3Megapixels(megapixels)
	}

	if value := strings.TrimSpace(clarity); value != "" {
		if tier, recognized, err := classifyH3QualityValue(value); err != nil {
			return "", fmt.Errorf("clarity %q: %w", value, err)
		} else if recognized {
			return tier, nil
		}
	}

	if value := strings.TrimSpace(size); value != "" {
		if tier, recognized, err := classifyH3Size(value); err != nil {
			return "", fmt.Errorf("size %q: %w", value, err)
		} else if recognized {
			return tier, nil
		}
	}

	return "", nil
}

func classifyH3QualityValue(value string) (string, bool, error) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "×", "x")))
	normalized = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(normalized)
	switch normalized {
	case "low", "medium", "standard":
		return h3BillingResolution768, true, nil
	case "high":
		return h3BillingResolution1080, true, nil
	}
	if tier, ok := constant.MiniMaxH3ResolutionTierForSize(normalized); ok {
		return tier, true, nil
	}
	if tier := normalizeStandardH3BillingResolution(normalized); tier != "" {
		return tier, true, nil
	}

	if strings.HasSuffix(normalized, "mp") {
		normalized = strings.TrimSuffix(normalized, "mp")
	}
	if numeric, err := strconv.ParseFloat(normalized, 64); err == nil {
		tier, err := classifyH3Megapixels(numeric)
		return tier, true, err
	}

	if strings.Contains(normalized, "x") {
		tier, recognized, err := classifyH3Size(normalized)
		return tier, recognized, err
	}

	return "", false, nil
}

func classifyH3Megapixels(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < h3MinMegapixels {
		return "", fmt.Errorf("megapixels must be at least %.2f", h3MinMegapixels)
	}
	if value <= h3LowMaxMegapixels {
		return h3BillingResolution768, nil
	}
	if value <= h3HighMaxMegapixels {
		return h3BillingResolution1080, nil
	}
	return "", fmt.Errorf("megapixels %.4g exceeds the supported H3 high-quality band", value)
}

func normalizeStandardH3BillingResolution(value string) string {
	switch value {
	case "480p", "480", "854x480", "480x854":
		return h3BillingResolution480
	case "720p", "720", "1280x720", "720x1280":
		return "720p"
	case "768p", "768", "1366x768", "768x1366", "1280x768", "768x1280":
		return h3BillingResolution768
	case "1080p", "1080", "1920x1080", "1080x1920", "1792x1024", "1024x1792", "1080x1080":
		return h3BillingResolution1080
	case "2k", "1440p", "1440", "3360x1440", "2560x1440", "1920x1440", "1440x1440", "1440x1920", "1440x2560":
		return "1440p"
	case "4k", "2160p", "3840x2160", "2160x3840", "2160x2160":
		return "4k"
	default:
		return ""
	}
}

func classifyH3Size(value string) (string, bool, error) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "×", "x")))
	if isSupportedAspectRatio(normalized) {
		return "", false, nil
	}
	if tier, ok := constant.MiniMaxH3ResolutionTierForSize(normalized); ok {
		return tier, true, nil
	}
	parts := strings.Split(normalized, "x")
	if len(parts) != 2 {
		return "", false, nil
	}
	width, widthErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	height, heightErr := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return "", true, fmt.Errorf("size must use positive width and height")
	}
	return "", true, fmt.Errorf("size must be one of the documented MiniMax-H3 dimensions")
}

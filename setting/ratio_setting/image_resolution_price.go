package ratio_setting

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

const (
	ImageResolutionTier1K = "1K"
	ImageResolutionTier2K = "2K"
	ImageResolutionTier4K = "4K"
)

type ImageResolutionPrice struct {
	OneK  float64 `json:"1K"`
	TwoK  float64 `json:"2K"`
	FourK float64 `json:"4K"`
}

func (p ImageResolutionPrice) PriceForTier(tier string) (float64, bool) {
	switch tier {
	case ImageResolutionTier1K:
		return p.OneK, true
	case ImageResolutionTier2K:
		return p.TwoK, true
	case ImageResolutionTier4K:
		return p.FourK, true
	default:
		return 0, false
	}
}

var imageResolutionPriceMap = types.NewRWMap[string, ImageResolutionPrice]()

func parseImageResolutionPriceJSON(jsonStr string) (map[string]ImageResolutionPrice, error) {
	var raw map[string]map[string]float64
	if err := common.UnmarshalJsonStr(jsonStr, &raw); err != nil {
		return nil, err
	}

	parsed := make(map[string]ImageResolutionPrice, len(raw))
	for modelName, tiers := range raw {
		if strings.TrimSpace(modelName) == "" {
			return nil, fmt.Errorf("model name cannot be empty")
		}
		if len(tiers) != 3 {
			return nil, fmt.Errorf("model %s must configure exactly 1K, 2K and 4K prices", modelName)
		}

		prices := ImageResolutionPrice{}
		for _, tier := range []string{ImageResolutionTier1K, ImageResolutionTier2K, ImageResolutionTier4K} {
			price, ok := tiers[tier]
			if !ok {
				return nil, fmt.Errorf("model %s is missing %s price", modelName, tier)
			}
			if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
				return nil, fmt.Errorf("model %s %s price must be a finite non-negative number", modelName, tier)
			}
			switch tier {
			case ImageResolutionTier1K:
				prices.OneK = price
			case ImageResolutionTier2K:
				prices.TwoK = price
			case ImageResolutionTier4K:
				prices.FourK = price
			}
		}
		parsed[modelName] = prices
	}
	return parsed, nil
}

func ValidateImageResolutionPriceJSONString(jsonStr string) error {
	_, err := parseImageResolutionPriceJSON(jsonStr)
	return err
}

func UpdateImageResolutionPriceByJSONString(jsonStr string) error {
	parsed, err := parseImageResolutionPriceJSON(jsonStr)
	if err != nil {
		return err
	}
	imageResolutionPriceMap.ReplaceAll(parsed)
	InvalidateExposedDataCache()
	return nil
}

func ImageResolutionPrice2JSONString() string {
	return imageResolutionPriceMap.MarshalJSONString()
}

func GetImageResolutionPrice(name string) (ImageResolutionPrice, bool) {
	name = FormatMatchingModelName(name)
	return imageResolutionPriceMap.Get(name)
}

func GetImageResolutionPriceCopy() map[string]ImageResolutionPrice {
	return imageResolutionPriceMap.ReadAll()
}

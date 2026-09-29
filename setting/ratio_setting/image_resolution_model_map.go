package ratio_setting

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// ImageResolutionModelMap stores an optional upstream model override for an
// image model and resolution tier.  It is intentionally separate from image
// prices: the public model remains the billing model while only the upstream
// request model is changed.
var imageResolutionModelMap = types.NewRWMap[string, string]()

func parseImageResolutionModelMapJSON(jsonStr string) (map[string]string, error) {
	var raw map[string]string
	if err := common.UnmarshalJsonStr(jsonStr, &raw); err != nil {
		return nil, err
	}
	parsed := make(map[string]string, len(raw))
	for modelName, mappedModel := range raw {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return nil, fmt.Errorf("model name cannot be empty")
		}
		mappedModel = strings.TrimSpace(mappedModel)
		if mappedModel == "" {
			return nil, fmt.Errorf("mapped model for %s cannot be empty", modelName)
		}
		parsed[modelName] = mappedModel
	}
	return parsed, nil
}

func ValidateImageResolutionModelMapJSONString(jsonStr string) error {
	_, err := parseImageResolutionModelMapJSON(jsonStr)
	return err
}

func UpdateImageResolutionModelMapByJSONString(jsonStr string) error {
	parsed, err := parseImageResolutionModelMapJSON(jsonStr)
	if err != nil {
		return err
	}
	imageResolutionModelMap.ReplaceAll(parsed)
	InvalidateExposedDataCache()
	return nil
}

func ImageResolutionModelMap2JSONString() string {
	return imageResolutionModelMap.MarshalJSONString()
}

func GetImageResolutionModelMap(modelName string) (string, bool) {
	modelName = FormatMatchingModelName(modelName)
	return imageResolutionModelMap.Get(modelName)
}

func GetImageResolutionModelMapCopy() map[string]string {
	return imageResolutionModelMap.ReadAll()
}

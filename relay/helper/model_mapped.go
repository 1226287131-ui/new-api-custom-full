package helper

import (
	"errors"
	"fmt"
	"strings"

	rootcommon "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

// ResolveModelMapping follows a channel model mapping to its final upstream
// model. Adaptors can use it during validation when provider capabilities
// depend on the model selected after channel mapping.
func ResolveModelMapping(modelMapping, modelName string) (string, bool, error) {
	modelMapping = strings.TrimSpace(modelMapping)
	if modelMapping == "" || modelMapping == "{}" {
		return modelName, false, nil
	}

	modelMap := make(map[string]string)
	if err := rootcommon.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
		return "", false, fmt.Errorf("unmarshal_model_mapping_failed")
	}

	currentModel := modelName
	visitedModels := map[string]bool{
		currentModel: true,
	}
	for {
		mappedModel, exists := modelMap[currentModel]
		baseModel := hostreasoning.BaseModelName(currentModel)
		if (!exists || mappedModel == "") && baseModel != currentModel {
			mappedModel, exists = modelMap[baseModel]
		}
		if !exists || mappedModel == "" {
			return currentModel, currentModel != modelName, nil
		}
		if visitedModels[mappedModel] {
			if mappedModel == currentModel {
				return currentModel, currentModel != modelName, nil
			}
			return "", false, errors.New("model_mapping_contains_cycle")
		}
		visitedModels[mappedModel] = true
		currentModel = mappedModel
	}
}

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	mappedModelName, isMapped, err := ResolveModelMapping(
		c.GetString("model_mapping"),
		info.OriginModelName,
	)
	if err != nil {
		return err
	}
	info.IsModelMapped = isMapped
	if isMapped {
		info.UpstreamModelName = mappedModelName
	}

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}

package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// ImageResolutionRoutingModel resolves the channel identity before distribution.
// Authorization and billing still use the original public model.
func ImageResolutionRoutingModel(c *gin.Context, publicModel string) (string, error) {
	target, configured := ratio_setting.GetImageResolutionModelMap(publicModel)
	if !configured || billing_setting.GetBillingMode(publicModel) == billing_setting.BillingModeTieredExpr {
		return publicModel, nil
	}
	if _, priced := ratio_setting.GetImageResolutionPrice(publicModel); !priced {
		return publicModel, nil
	}
	mode := relayconstant.RelayModeImagesGenerations
	if strings.HasPrefix(c.Request.URL.Path, "/v1/images/edits") {
		mode = relayconstant.RelayModeImagesEdits
	}
	request, err := GetAndValidOpenAIImageRequest(c, mode)
	if err != nil {
		return "", err
	}
	input, err := ResolveIncomingBillingExprRequestInput(c, nil)
	if err != nil {
		return "", err
	}
	if strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		input, err = ResolveImageBillingRequestInput(c, &relaycommon.RelayInfo{Request: request}, input)
		if err != nil {
			return "", err
		}
	} else {
		count, countErr := request.ImageCount(false)
		if countErr != nil {
			return "", countErr
		}
		input.ImageCount = &count
	}
	billing, err := resolveImageResolutionBilling(billingexpr.RequestInput{Body: input.Body, ImageCount: input.ImageCount}, request.GetTokenCountMeta())
	if err != nil {
		return "", err
	}
	if billing.Tier == ratio_setting.ImageResolutionTier1K {
		return target, nil
	}
	return publicModel, nil
}

// ApplyImageResolutionModelMapping changes only the upstream model for an
// explicitly or implicitly 1K image request. OriginModelName remains intact
// so billing, usage logs and the public response continue to use the model
// requested by the customer.
func ApplyImageResolutionModelMapping(info *relaycommon.RelayInfo, request *dto.ImageRequest, channelModelMapping string) error {
	if info != nil {
		info.IsImageResolutionModelMapped = false
	}
	if info == nil || request == nil || strings.TrimSpace(info.OriginModelName) == "" {
		return nil
	}

	// The reservation already classified every supported size signal, including
	// nested provider fields. Routing must use the same frozen tier as billing.
	if info.PriceData.ImageResolutionTier != ratio_setting.ImageResolutionTier1K {
		return nil
	}

	mappedModel, ok := ratio_setting.GetImageResolutionModelMap(info.OriginModelName)
	if !ok || strings.TrimSpace(mappedModel) == "" {
		return nil
	}
	mappedModel, _, err := ResolveModelMapping(channelModelMapping, strings.TrimSpace(mappedModel))
	if err != nil {
		return err
	}
	info.UpstreamModelName = mappedModel
	info.IsModelMapped = true
	info.IsImageResolutionModelMapped = true
	request.SetModelName(info.UpstreamModelName)
	return nil
}

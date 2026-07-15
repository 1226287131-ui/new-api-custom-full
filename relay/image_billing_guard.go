package relay

import (
	"fmt"
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/types"
)

func validateOutboundImageBilling(jsonData []byte, info *relaycommon.RelayInfo) *types.NewAPIError {
	if info == nil {
		return nil
	}
	if err := helper.ValidateOutboundImageBilling(jsonData, info.PriceData); err != nil {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("image billing parameters changed after channel conversion: %w", err),
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	return nil
}

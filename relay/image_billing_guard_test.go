package relay

import (
	"net/http"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestValidateOutboundImageBillingRejectsChannelOverrideMismatch(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			ImageResolutionTier:  ratio_setting.ImageResolutionTier2K,
			ImageGenerationCount: 1,
		},
	}

	apiErr := validateOutboundImageBilling([]byte(`{"image_size":"4K","batch_size":1}`), info)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeModelPriceError, apiErr.GetErrorCode())
}

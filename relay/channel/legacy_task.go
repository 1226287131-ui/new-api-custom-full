package channel

import (
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
)

// LegacyTaskAdaptor preserves the deployed video providers while the new
// plugin pipeline owns persistence and client response publication.
type LegacyTaskAdaptor interface {
	Init(*relaycommon.RelayInfo)
	ValidateRequestAndSetAction(*gin.Context, *relaycommon.RelayInfo) *taskdto.TaskError
	EstimateBilling(*gin.Context, *relaycommon.RelayInfo) map[string]float64
	AdjustBillingOnSubmit(*relaycommon.RelayInfo, []byte) map[string]float64
	AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int
	BuildRequestURL(*relaycommon.RelayInfo) (string, error)
	BuildRequestHeader(*gin.Context, *http.Request, *relaycommon.RelayInfo) error
	BuildRequestBody(*gin.Context, *relaycommon.RelayInfo) (io.Reader, error)
	DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (*http.Response, error)
	DoResponse(*gin.Context, *http.Response, *relaycommon.RelayInfo) (string, []byte, *taskdto.TaskError)
	GetModelList() []string
	GetChannelName() string
	FetchTask(string, string, map[string]any, string) (*http.Response, error)
	ParseTaskResult([]byte) (*relaycommon.TaskInfo, error)
}

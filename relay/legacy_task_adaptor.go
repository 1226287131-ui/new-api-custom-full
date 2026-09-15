package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// legacyTaskAdaptor translates the deployed provider contract into the plugin
// host contract. In particular, legacy DoResponse must not publish success
// before the host has persisted the task and settled its reservation.
type legacyTaskAdaptor struct {
	channel.LegacyTaskAdaptor
}

func (a *legacyTaskAdaptor) ParseResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*channel.TaskSubmitResponse, *taskdto.TaskError) {
	originalWriter := c.Writer
	captured := &taskSubmitResponseBuffer{ResponseWriter: originalWriter, headers: make(http.Header), status: http.StatusOK}
	c.Writer = captured
	defer func() { c.Writer = originalWriter }()
	taskID, data, taskErr := a.DoResponse(c, resp, info)
	if taskErr != nil {
		// A 2xx response can already represent billable accepted work. A parse
		// failure must not submit the same video to another upstream.
		taskErr.NoRetry = true
		return nil, taskErr
	}
	var clientResponse any
	if captured.body.Len() > 0 {
		if !json.Valid(captured.body.Bytes()) {
			return nil, &taskdto.TaskError{Code: "invalid_response", Message: "task adaptor returned invalid JSON", StatusCode: http.StatusBadGateway, NoRetry: true}
		}
		clientResponse = json.RawMessage(bytes.Clone(captured.body.Bytes()))
	}
	return &channel.TaskSubmitResponse{UpstreamTaskID: taskID, TaskData: data, ClientResponse: clientResponse}, nil
}

func (a *legacyTaskAdaptor) FetchTask(baseURL, key string, task *model.Task, proxy string) (*http.Response, error) {
	if task == nil {
		return nil, fmt.Errorf("task is required")
	}
	return a.LegacyTaskAdaptor.FetchTask(baseURL, key, map[string]any{"task_id": task.GetUpstreamTaskID(), "action": task.Action}, proxy)
}

func (a *legacyTaskAdaptor) ParseTaskResult(task *model.Task, resp *http.Response, body []byte) (*relaycommon.TaskInfo, error) {
	return a.LegacyTaskAdaptor.ParseTaskResult(body)
}

func (a *legacyTaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	converter, ok := a.LegacyTaskAdaptor.(channel.OpenAIVideoConverter)
	if !ok {
		return nil, fmt.Errorf("task provider does not support OpenAI video conversion")
	}
	return converter.ConvertToOpenAIVideo(task)
}

type taskSubmitResponseBuffer struct {
	gin.ResponseWriter
	body    bytes.Buffer
	headers http.Header
	status  int
	written bool
}

func (w *taskSubmitResponseBuffer) Header() http.Header    { return w.headers }
func (w *taskSubmitResponseBuffer) WriteHeader(status int) { w.status = status }
func (w *taskSubmitResponseBuffer) WriteHeaderNow()        { w.written = true }
func (w *taskSubmitResponseBuffer) Write(body []byte) (int, error) {
	w.written = true
	return w.body.Write(body)
}
func (w *taskSubmitResponseBuffer) WriteString(body string) (int, error) {
	w.written = true
	return w.body.WriteString(body)
}
func (w *taskSubmitResponseBuffer) Status() int { return w.status }
func (w *taskSubmitResponseBuffer) Size() int {
	if !w.written {
		return -1
	}
	return w.body.Len()
}
func (w *taskSubmitResponseBuffer) Written() bool { return w.written }
func (w *taskSubmitResponseBuffer) Flush()        { w.written = true }

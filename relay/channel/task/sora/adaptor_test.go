package sora

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTaskResultRejectsNoFinalVideoURLSentinel(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"id": "task_upstream",
		"status": "processing",
		"progress": 100,
		"url": "https://upstream.example/Video%20generation%20returned%20no%20final%20video%20URL",
		"error": null,
		"data": {
			"data": [{"url": "https://upstream.example/Video%20generation%20returned%20no%20final%20video%20URL"}]
		}
	}`))

	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailure, result.Status)
	assert.Equal(t, "100%", result.Progress)
	assert.Equal(t, "upstream video generation returned no final video URL", result.Reason)
}

func TestParseTaskResultKeepsOrdinaryProcessingAtOneHundredPercent(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"id": "task_upstream",
		"status": "processing",
		"progress": 100,
		"error": null
	}`))

	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusInProgress, result.Status)
	assert.Empty(t, result.Progress)
	assert.Empty(t, result.Reason)
}

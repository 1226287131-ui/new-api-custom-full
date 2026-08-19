package relay

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskSubmitResponseSucceeded(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusAccepted,
		http.StatusNoContent,
	} {
		assert.True(t, taskSubmitResponseSucceeded(statusCode), "status %d should be accepted", statusCode)
	}

	for _, statusCode := range []int{
		http.StatusMultipleChoices,
		http.StatusBadRequest,
		http.StatusInternalServerError,
	} {
		assert.False(t, taskSubmitResponseSucceeded(statusCode), "status %d should be rejected", statusCode)
	}
}

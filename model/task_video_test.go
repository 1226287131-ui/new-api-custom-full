package model

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
)

func TestTaskStatusToVideoStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   TaskStatus
		expected string
	}{
		{name: "not started", status: TaskStatusNotStart, expected: dto.VideoStatusQueued},
		{name: "submitted", status: TaskStatusSubmitted, expected: dto.VideoStatusQueued},
		{name: "queued", status: TaskStatusQueued, expected: dto.VideoStatusQueued},
		{name: "in progress", status: TaskStatusInProgress, expected: dto.VideoStatusInProgress},
		{name: "success", status: TaskStatusSuccess, expected: dto.VideoStatusCompleted},
		{name: "failure", status: TaskStatusFailure, expected: dto.VideoStatusFailed},
		{name: "unknown", status: TaskStatusUnknown, expected: dto.VideoStatusUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, test.status.ToVideoStatus())
		})
	}
}

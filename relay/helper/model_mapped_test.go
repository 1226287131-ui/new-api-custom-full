package helper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveModelMapping(t *testing.T) {
	tests := []struct {
		name       string
		mapping    string
		model      string
		wantModel  string
		wantMapped bool
		wantErr    string
	}{
		{
			name:      "no mapping",
			model:     "seedance-2.0",
			wantModel: "seedance-2.0",
		},
		{
			name:       "follows chained mapping",
			mapping:    `{"customer-video":"seedance-2.0","seedance-2.0":"cvk-2-fast-720"}`,
			model:      "customer-video",
			wantModel:  "cvk-2-fast-720",
			wantMapped: true,
		},
		{
			name:      "self mapping is not a remap",
			mapping:   `{"seedance-2.0":"seedance-2.0"}`,
			model:     "seedance-2.0",
			wantModel: "seedance-2.0",
		},
		{
			name:    "rejects cycle",
			mapping: `{"a":"b","b":"a"}`,
			model:   "a",
			wantErr: "model_mapping_contains_cycle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mappedModel, mapped, err := ResolveModelMapping(tt.mapping, tt.model)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantModel, mappedModel)
			assert.Equal(t, tt.wantMapped, mapped)
		})
	}
}

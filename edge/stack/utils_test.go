package stack

import (
	"path/filepath"
	"testing"

	"github.com/portainer/agent"
	"github.com/portainer/portainer/api/edge"

	"github.com/stretchr/testify/assert"
)

func Test_getStackFileFolder(t *testing.T) {
	tests := []struct {
		name     string
		stack    *edgeStack
		expected string
	}{
		{
			name: "Default",
			stack: &edgeStack{
				StackPayload: edge.StackPayload{
					ID: 42,
				},
			},
			expected: filepath.Join(agent.EdgeStackFilesPath, "42"),
		},
		{
			name: "RelativePath",
			stack: &edgeStack{
				StackPayload: edge.StackPayload{
					ID:                  7,
					FilesystemPath:      "/tmp/edge",
					SupportRelativePath: true,
				},
			},
			expected: filepath.Join("/tmp/edge", agent.ComposePathPrefix, "7"),
		},
		{
			name: "EdgeUpdateID",
			stack: &edgeStack{
				StackPayload: edge.StackPayload{
					ID:           99,
					EdgeUpdateID: 123,
				},
			},
			expected: filepath.Join(agent.UpdateEdgeStackFilesPath, "99"),
		},
		{
			name: "RelativePath+EdgeUpdateID",
			stack: &edgeStack{
				StackPayload: edge.StackPayload{
					ID:                  55,
					FilesystemPath:      "/data/stack",
					SupportRelativePath: true,
					EdgeUpdateID:        1,
				},
			},
			expected: filepath.Join(agent.UpdateEdgeStackFilesPath, "55"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getStackFileFolder(tt.stack)
			assert.Equal(t, tt.expected, got)
		})
	}
}

package cleanup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUsedPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		total     uint64
		available uint64
		expected  float64
	}{
		{name: "zero total", total: 0, available: 0, expected: 0},
		{name: "half used", total: 100, available: 50, expected: 50},
		{name: "three quarters used", total: 200, available: 50, expected: 75},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.expected, usedPercent(tt.total, tt.available), 0.0001)
		})
	}
}

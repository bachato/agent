package edge

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPollAsync_NoEdgeID(t *testing.T) {
	t.Parallel()
	s := &PollService{edgeID: ""}

	err := s.pollAsync(true, true)
	require.Error(t, err)
}

package os

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionParser(t *testing.T) {
	t.Parallel()
	p := NewEnvOptionParser()
	require.NotNil(t, p)

	a := os.Args
	defer func() { os.Args = a }()

	os.Args = []string{"agent", "--fips-mode"}

	opts, err := p.Options()
	require.NoError(t, err)

	require.False(t, opts.EdgeMode)
	require.True(t, opts.FIPSMode)
}

package net

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupIPAddresses_Localhost(t *testing.T) {
	t.Parallel()

	addrs, err := LookupIPAddresses("localhost")
	require.NoError(t, err)
	require.NotEmpty(t, addrs)

	for _, addr := range addrs {
		require.NotEmpty(t, addr)
	}
}

func TestLookupIPAddresses_InvalidHost(t *testing.T) {
	t.Parallel()

	addrs, err := LookupIPAddresses("invalid host name")
	require.Error(t, err)
	require.Empty(t, addrs)
}

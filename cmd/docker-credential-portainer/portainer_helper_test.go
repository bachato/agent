package main

import (
	"testing"

	credentials "github.com/docker/docker-credential-helpers/credentials"
	"github.com/stretchr/testify/require"
)

func TestPortainerHelper(t *testing.T) {
	portainerHelper := portainerHelper{}

	require.NoError(t, portainerHelper.Add(&credentials.Credentials{}))
	require.NoError(t, portainerHelper.Delete("http://example.com"))

	l, err := portainerHelper.List()
	require.NoError(t, err)
	require.Empty(t, l)
}

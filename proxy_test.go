package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProxyEnvVars(t *testing.T) {
	// No proxy variables set, nothing should be returned
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("no_proxy", "")

	require.Empty(t, ProxyEnvVars())

	// Only the variables that are set are returned, in their KEY=value form
	t.Setenv("HTTP_PROXY", "http://proxy:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy:3128")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	envVars := ProxyEnvVars()
	require.ElementsMatch(t, []string{
		"HTTP_PROXY=http://proxy:3128",
		"HTTPS_PROXY=http://proxy:3128",
		"NO_PROXY=localhost,127.0.0.1",
	}, envVars)

	// Lower case variants are honoured as well
	t.Setenv("https_proxy", "http://lowerproxy:3128")

	envVars = ProxyEnvVars()
	require.Contains(t, envVars, "https_proxy=http://lowerproxy:3128")
}

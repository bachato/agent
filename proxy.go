package agent

import "os"

// proxyEnvVarNames are the standard proxy related environment variables that the
// agent propagates to the compose unpacker container so it can reach git
// repositories and image registries through the same proxy as the agent itself.
// Both the upper and lower case variants are honoured because different tooling
// inside the unpacker reads one or the other
var proxyEnvVarNames = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"NO_PROXY",
	"http_proxy",
	"https_proxy",
	"no_proxy",
}

// ProxyEnvVars returns the proxy related environment variables set on the agent,
// formatted as KEY=value strings suitable for a container's environment. Only
// the variables that are actually set are returned
func ProxyEnvVars() []string {
	var envVars []string

	for _, name := range proxyEnvVarNames {
		value := os.Getenv(name)
		if value == "" {
			continue
		}

		envVars = append(envVars, name+"="+value)
	}

	return envVars
}

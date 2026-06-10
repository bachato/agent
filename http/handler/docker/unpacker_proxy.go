package docker

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/portainer/agent"
	"github.com/portainer/portainer/api/logs"

	"github.com/rs/zerolog/log"
)

const unpackerContainerNamePrefix = "portainer-unpacker-"

// injectUnpackerProxyEnv detects a compose unpacker container creation request
// proxied from the Portainer server and injects the agent's own proxy
// environment variables into the container so it can reach external git
// repositories and image registries through the same proxy as the agent.
// Any failure leaves the original request untouched
func injectUnpackerProxyEnv(request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/containers/create" {
		return
	}

	if !strings.HasPrefix(request.URL.Query().Get("name"), unpackerContainerNamePrefix) {
		return
	}

	if request.Body == nil {
		return
	}

	proxyEnv := agent.ProxyEnvVars()
	if len(proxyEnv) == 0 {
		return
	}

	body, err := io.ReadAll(request.Body)
	logs.CloseAndLogErr(request.Body)
	if err != nil {
		log.Warn().Err(err).Msg("unable to read unpacker container create request, skipping proxy injection")

		return
	}

	newBody, err := mergeProxyEnv(body, proxyEnv)
	if err != nil {
		log.Warn().Err(err).Msg("unable to inject proxy environment variables into unpacker container, using original request")

		newBody = body
	}

	request.Body = io.NopCloser(bytes.NewReader(newBody))
	request.ContentLength = int64(len(newBody))
	request.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
}

// mergeProxyEnv decodes a Docker container create request body, appends the
// given proxy environment variables to its Env list without overriding any
// variable that is already present, and re-encodes the body
func mergeProxyEnv(body []byte, proxyEnv []string) ([]byte, error) {
	var config map[string]json.RawMessage
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, err
	}

	var env []string
	if raw, ok := config["Env"]; ok {
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, err
		}
	}

	for _, proxyVar := range proxyEnv {
		name, _, _ := strings.Cut(proxyVar, "=")
		if containsEnvVar(env, name) {
			continue
		}

		env = append(env, proxyVar)
	}

	encoded, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}

	config["Env"] = encoded

	return json.Marshal(config)
}

func containsEnvVar(env []string, name string) bool {
	prefix := name + "="

	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}

	return false
}

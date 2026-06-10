package docker

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func decodeEnv(t *testing.T, body io.Reader) []string {
	t.Helper()

	var config map[string]json.RawMessage
	err := json.NewDecoder(body).Decode(&config)
	require.NoError(t, err)

	var env []string
	if raw, ok := config["Env"]; ok {
		err = json.Unmarshal(raw, &env)
		require.NoError(t, err)
	}

	return env
}

func TestInjectUnpackerProxyEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy:3128")
	t.Setenv("NO_PROXY", "localhost")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("no_proxy", "")

	// An unpacker container create request gets the agent proxy variables injected
	// while preserving any environment variables already present
	body := `{"Image":"portainer/compose-unpacker:latest","Env":["FOO=bar"]}`
	request := httptest.NewRequest(http.MethodPost, "/containers/create?name=portainer-unpacker-1-stack-42", strings.NewReader(body))

	injectUnpackerProxyEnv(request)

	rewritten, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Equal(t, int64(len(rewritten)), request.ContentLength) // ContentLength must reflect the rewritten body

	env := decodeEnv(t, strings.NewReader(string(rewritten)))
	require.Contains(t, env, "FOO=bar")
	require.Contains(t, env, "HTTP_PROXY=http://proxy:3128")
	require.Contains(t, env, "HTTPS_PROXY=http://proxy:3128")
	require.Contains(t, env, "NO_PROXY=localhost")

	// A proxy variable explicitly set on the stack is not overridden by the agent value
	body = `{"Image":"portainer/compose-unpacker:latest","Env":["HTTP_PROXY=http://stackproxy:8080"]}`
	request = httptest.NewRequest(http.MethodPost, "/containers/create?name=portainer-unpacker-2-stack-7", strings.NewReader(body))

	injectUnpackerProxyEnv(request)

	env = decodeEnv(t, request.Body)
	require.Contains(t, env, "HTTP_PROXY=http://stackproxy:8080")
	require.NotContains(t, env, "HTTP_PROXY=http://proxy:3128")
	require.Contains(t, env, "HTTPS_PROXY=http://proxy:3128")

	// A non unpacker container create request is left untouched
	body = `{"Image":"nginx","Env":["FOO=bar"]}`
	request = httptest.NewRequest(http.MethodPost, "/containers/create?name=my-app", strings.NewReader(body))

	injectUnpackerProxyEnv(request)

	env = decodeEnv(t, request.Body)
	require.Equal(t, []string{"FOO=bar"}, env)

	// A non create request is left untouched
	request = httptest.NewRequest(http.MethodGet, "/containers/json", nil)

	injectUnpackerProxyEnv(request)
}

func TestInjectUnpackerProxyEnvForwardsToDocker(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy:3128")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("no_proxy", "")

	// Stand-in for the Docker daemon that records exactly what it receives
	var receivedBody []byte
	var receivedContentLength int64
	var receivedPath string
	var readBodyErr error

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedContentLength = r.ContentLength

		body, err := io.ReadAll(r.Body)
		readBodyErr = err
		receivedBody = body

		rw.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	body := `{"Image":"portainer/compose-unpacker:latest","Env":["FOO=bar"]}`
	request := httptest.NewRequest(http.MethodPost, "/containers/create?name=portainer-unpacker-1-stack-1", strings.NewReader(body))

	injectUnpackerProxyEnv(request)

	// Mimic LocalProxy.ServeHTTP: point the request at the daemon and round trip it
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	request.URL.Scheme = "http"
	request.URL.Host = target.Host
	request.RequestURI = ""

	response, err := http.DefaultTransport.RoundTrip(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, response.StatusCode)

	err = response.Body.Close()
	require.NoError(t, err)
	require.NoError(t, readBodyErr)

	// The daemon received the rewritten body, with a matching Content-Length and the proxy env injected
	require.Equal(t, "/containers/create", receivedPath)
	require.Equal(t, int64(len(receivedBody)), receivedContentLength)

	env := decodeEnv(t, bytes.NewReader(receivedBody))
	require.Contains(t, env, "FOO=bar")
	require.Contains(t, env, "HTTP_PROXY=http://proxy:3128")
}

func TestInjectUnpackerProxyEnvNoProxyConfigured(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("no_proxy", "")

	// With no proxy configured on the agent the request body stays exactly the same
	body := `{"Image":"portainer/compose-unpacker:latest","Env":["FOO=bar"]}`
	request := httptest.NewRequest(http.MethodPost, "/containers/create?name=portainer-unpacker-1-stack-1", strings.NewReader(body))

	injectUnpackerProxyEnv(request)

	env := decodeEnv(t, request.Body)
	require.Equal(t, []string{"FOO=bar"}, env)
}

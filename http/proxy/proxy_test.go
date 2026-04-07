package proxy

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"testing"

	"github.com/portainer/portainer/pkg/fips"

	"github.com/stretchr/testify/require"
)

func TestNewAgentReverseProxy(t *testing.T) {
	t.Parallel()
	fips.InitFIPS(false)

	u, err := url.Parse("http://localhost:9001")
	require.NoError(t, err)

	proxy := newAgentReverseProxy(u, "targetNode")
	require.NotNil(t, proxy)
	require.True(t, proxy.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify) //nolint:forbidigo
}

func TestCreateRewriteFn(t *testing.T) {
	t.Parallel()
	target := createURL(t, "https://portainer.io/api/docker?a=5&b=6")
	targetNode := "test-node"
	req := createRequest(
		t,
		"GET",
		"https://agent-portainer.io/test?c=7",
		map[string]string{"Accept-Encoding": "gzip", "Accept": "application/json", "User-Agent": "something"},
	)
	expectedReq := createRequest(
		t,
		"GET",
		"https://portainer.io/api/docker?a=5&b=6&c=7",
		map[string]string{
			"Accept-Encoding":         "gzip",
			"Accept":                  "application/json",
			"User-Agent":              "something",
			"X-Portaineragent-Target": "test-node",
		},
	)

	rewriteFn := createRewriteFn(target, targetNode)
	proxyRequest := httputil.ProxyRequest{
		In:  req.Clone(context.Background()),
		Out: req.Clone(context.Background()),
	}
	rewriteFn(&proxyRequest)

	require.Equal(t, req, proxyRequest.In, "rewriteFn should not modify In request")
	require.Equal(t, expectedReq, proxyRequest.Out)
}

func createURL(t *testing.T, urlString string) *url.URL {
	parsedURL, err := url.Parse(urlString)
	if err != nil {
		t.Fatalf("Failed to create url: %s", err)
	}

	return parsedURL
}

func createRequest(t *testing.T, method, url string, headers map[string]string) *http.Request {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("Failed to create http request: %s", err)
	} else {
		for k, v := range headers {
			req.Header.Add(k, v)
		}
	}

	return req
}

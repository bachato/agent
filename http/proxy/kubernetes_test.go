package proxy

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewKubernetesProxy(t *testing.T) {
	proxy := NewKubernetesProxy()
	require.NotNil(t, proxy)
	require.True(t, proxy.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify) //nolint:forbidigo
}

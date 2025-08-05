package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/portainer/portainer/api/crypto"
)

const kubernetesAPIURL = "https://kubernetes.default.svc"

func NewKubernetesProxy() *httputil.ReverseProxy {
	remoteURL, _ := url.Parse(kubernetesAPIURL)
	proxy := httputil.NewSingleHostReverseProxy(remoteURL)

	proxy.Transport = &http.Transport{
		TLSClientConfig: crypto.CreateTLSConfiguration(true),
	}

	return proxy
}

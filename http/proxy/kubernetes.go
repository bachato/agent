package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/portainer/portainer/api/crypto"
	"github.com/portainer/portainer/pkg/fips"
)

const kubernetesAPIURL = "https://kubernetes.default.svc"

func NewKubernetesProxy() http.Handler {
	remoteURL, _ := url.Parse(kubernetesAPIURL)
	proxy := httputil.NewSingleHostReverseProxy(remoteURL)

	tlsConfig := crypto.CreateTLSConfiguration()
	tlsConfig.InsecureSkipVerify = fips.CanTLSSkipVerify()

	proxy.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	return proxy
}

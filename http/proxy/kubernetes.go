package proxy

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/portainer/portainer/api/crypto"
	"github.com/portainer/portainer/pkg/fips"
	"github.com/rs/zerolog/log"
	"k8s.io/client-go/rest"
)

const kubernetesAPIURL = "https://kubernetes.default.svc"

func NewKubernetesProxy() *httputil.ReverseProxy {
	remoteURL, _ := url.Parse(kubernetesAPIURL)
	proxy := httputil.NewSingleHostReverseProxy(remoteURL)

	config, err := rest.InClusterConfig()
	if err != nil {
		if errors.Is(err, rest.ErrNotInCluster) {
			log.Info().Msg("kubernetes InClusterConfig reports not running in cluster")
		} else {
			log.Error().Err(err).Msg("error getting in-cluster config for Kubernetes proxy")
		}
	}

	var transport http.RoundTripper
	if config != nil {
		transport, err = rest.TransportFor(config)
		if err != nil {
			log.Error().Err(err).Msg("error getting transport for Kubernetes proxy with in-cluster config")
		}
	}

	if transport != nil {
		log.Info().
			Bool("insecure", config.TLSClientConfig.Insecure).
			Bool("can_tls_skip_verify", fips.CanTLSSkipVerify()).
			Msg("using in-cluster transport config for Kubernetes proxy")

		proxy.Transport = transport
	} else {
		log.Info().Msg("using default transport for Kubernetes proxy")
		proxy.Transport = &http.Transport{
			TLSClientConfig: crypto.CreateTLSConfiguration(true),
		}
	}

	return proxy
}

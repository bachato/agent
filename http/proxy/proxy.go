package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/portainer/agent"
	"github.com/portainer/portainer/api/crypto"

	"github.com/gorilla/websocket"
	"github.com/koding/websocketproxy"
)

// AgentHTTPRequest redirects a HTTP request to another agent.
func AgentHTTPRequest(rw http.ResponseWriter, request *http.Request, target *agent.ClusterMember, useTLS bool) {
	urlCopy := request.URL
	urlCopy.Host = target.IPAddress + ":" + target.Port

	urlCopy.Scheme = "http"
	if useTLS {
		urlCopy.Scheme = "https"
	}

	proxyHTTPRequest(rw, request, urlCopy, target.NodeName)
}

// WebsocketRequest redirects a websocket request to another agent.
func WebsocketRequest(rw http.ResponseWriter, request *http.Request, target *agent.ClusterMember) {
	urlCopy := request.URL
	urlCopy.Host = target.IPAddress + ":" + target.Port

	urlCopy.Scheme = "ws"
	if request.TLS != nil {
		urlCopy.Scheme = "wss"
	}

	proxyWebsocketRequest(rw, request, urlCopy, target.NodeName)
}

func proxyHTTPRequest(rw http.ResponseWriter, request *http.Request, target *url.URL, targetNode string) {
	proxy := newAgentReverseProxy(target, targetNode)
	proxy.ServeHTTP(rw, request)
}

func proxyWebsocketRequest(rw http.ResponseWriter, request *http.Request, target *url.URL, targetNode string) {
	proxy := websocketproxy.NewProxy(target)
	proxy.Director = func(incoming *http.Request, out http.Header) {
		out.Set(agent.HTTPSignatureHeaderName, request.Header.Get(agent.HTTPSignatureHeaderName))
		out.Set(agent.HTTPPublicKeyHeaderName, request.Header.Get(agent.HTTPPublicKeyHeaderName))
		out.Set(agent.HTTPTargetHeaderName, targetNode)
	}

	proxy.Dialer = &websocket.Dialer{
		TLSClientConfig: crypto.CreateTLSConfiguration(true),
	}

	proxy.ServeHTTP(rw, request)
}

func createRewriteFn(target *url.URL, targetNode string) func(*httputil.ProxyRequest) {
	targetQuery := target.RawQuery
	return func(req *httputil.ProxyRequest) {
		req.Out.URL.Scheme = target.Scheme
		req.Out.URL.Host = target.Host
		req.Out.URL.Path = target.Path
		req.Out.Host = req.Out.URL.Host

		if targetQuery == "" || req.Out.URL.RawQuery == "" {
			req.Out.URL.RawQuery = targetQuery + req.In.URL.RawQuery
		} else {
			req.Out.URL.RawQuery = targetQuery + "&" + req.In.URL.RawQuery
		}

		req.Out.Header.Set(agent.HTTPTargetHeaderName, targetNode)
	}
}

func newAgentReverseProxy(target *url.URL, targetNode string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: createRewriteFn(target, targetNode),
		Transport: &http.Transport{
			TLSClientConfig: crypto.CreateTLSConfiguration(true),
		},
	}
}

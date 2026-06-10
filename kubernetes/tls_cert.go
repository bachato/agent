package kubernetes

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/portainer/portainer/api/crypto"
	"github.com/portainer/portainer/api/logs"
)

const (
	apiServerCertSource = "apiserver"
	tlsDialTimeout      = 10 * time.Second
)

// TLSCertInfo is a simplified API server certificate view for metrics export.
type TLSCertInfo struct {
	Source   string
	CN       string
	NotAfter time.Time
}

// CollectAPIServerCert collects the API server leaf TLS certificate details.
//
// InsecureSkipVerify is intentional: we are inspecting the certificate itself
// (reading NotAfter), not authenticating the server. The cert must be readable
// even when it is expired, which is exactly the condition we need to report.
func CollectAPIServerCert(ctx context.Context, kc *KubeClient) (*TLSCertInfo, error) {
	if kc == nil {
		return nil, errors.New("kubernetes client is nil")
	}

	if kc.config == nil {
		return nil, errors.New("kubernetes client config is nil")
	}

	apiServerURL, err := resolveAPIServerURL(kc.config.Host)
	if err != nil {
		return nil, err
	}

	tlsTarget, err := buildAPIServerTLSTarget(apiServerURL)
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, tlsDialTimeout)
	defer cancel()

	netConn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", tlsTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to dial kubernetes api server tls endpoint. Error: %w", err)
	}
	defer logs.CloseAndLogErr(netConn)

	conn := tls.Client(netConn, crypto.CreateTLSConfiguration(true))
	if err = conn.HandshakeContext(dialCtx); err != nil {
		return nil, fmt.Errorf("tls handshake with kubernetes api server failed. Error: %w", err)
	}

	peerCerts := conn.ConnectionState().PeerCertificates
	if len(peerCerts) == 0 {
		return nil, errors.New("kubernetes api server connection returned no peer certificates")
	}

	leaf := peerCerts[0]
	commonName := strings.TrimSpace(leaf.Subject.CommonName)
	if commonName == "" {
		commonName = "unknown"
	}

	return &TLSCertInfo{
		Source:   apiServerCertSource,
		CN:       commonName,
		NotAfter: leaf.NotAfter,
	}, nil
}

func buildAPIServerTLSTarget(apiServerURL *url.URL) (string, error) {
	if apiServerURL == nil {
		return "", errors.New("kubernetes api server url is nil")
	}

	host := strings.TrimSpace(apiServerURL.Hostname())
	if host == "" {
		return "", fmt.Errorf("kubernetes api server host is invalid. host=%q", apiServerURL.String())
	}

	port := strings.TrimSpace(apiServerURL.Port())
	if port == "" {
		port = "443"
	}

	return net.JoinHostPort(host, port), nil
}

func resolveAPIServerURL(apiServerHost string) (*url.URL, error) {
	trimmed := strings.TrimSpace(apiServerHost)
	if trimmed == "" {
		return nil, errors.New("kubernetes api server host is empty")
	}

	parsedURL, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubernetes api server host. host=%q. Error: %w", trimmed, err)
	}

	if strings.TrimSpace(parsedURL.Host) == "" {
		return nil, fmt.Errorf("kubernetes api server host is invalid. host=%q", trimmed)
	}

	return parsedURL, nil
}

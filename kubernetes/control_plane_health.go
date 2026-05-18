package kubernetes

import (
	"context"
	"crypto/tls" //nolint:forbidigo
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComponentHealthStatus holds the aggregated health state for a control plane component.
type ComponentHealthStatus struct {
	Component string
	Healthy   bool // true if any pod returned 200
	Valid     bool // true if any pod returned 200, 500, or 503 (definitive probe result)
}

var controlPlaneComponents = []struct {
	label string
	port  string
}{
	{"kube-scheduler", "10259"},
	{"kube-controller-manager", "10257"},
}

type listControlPlanePodsFn func(ctx context.Context, component string) ([]corev1.Pod, error)

// controlPlaneHTTPClient is a package-level variable so tests can substitute a
// TLS-configured client that trusts the httptest server certificate.
// crypto.CreateTLSConfiguration is not used here because it calls fips.FIPSMode() which
// requires InitFIPS to have been called, making it incompatible with package-level init.
var controlPlaneHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:forbidigo,gosec // control-plane /healthz endpoints use self-signed certs
	},
}

var probeControlPlaneHealthFn = probeComponentHealth

// CollectControlPlaneHealth probes kube-scheduler (:10259) and kube-controller-manager (:10257)
// by listing their pods in kube-system and probing each pod's host IP directly.
// Returns an error only when the Kubernetes API pod list itself fails.
// Components whose pods are not found (managed clusters) are omitted from results.
func CollectControlPlaneHealth(ctx context.Context, kc *KubeClient) ([]ComponentHealthStatus, error) {
	return collectControlPlaneHealth(ctx, func(ctx context.Context, component string) ([]corev1.Pod, error) {
		pods, err := kc.cli.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
			LabelSelector: "component=" + component,
		})
		if err != nil {
			return nil, err
		}

		return pods.Items, nil
	})
}

func collectControlPlaneHealth(ctx context.Context, listPods listControlPlanePodsFn) ([]ComponentHealthStatus, error) {
	var results []ComponentHealthStatus

	for _, comp := range controlPlaneComponents {
		pods, err := listPods(ctx, comp.label)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s pods: %w", comp.label, err)
		}

		if len(pods) == 0 {
			continue
		}

		var anyHealthy, anyValid bool
		anyProbed := false

		for i := range pods {
			hostIP := pods[i].Status.HostIP
			if hostIP == "" {
				continue
			}

			anyProbed = true
			healthy, valid := probeControlPlaneHealthFn(ctx, hostIP, comp.port)
			if healthy {
				anyHealthy = true
			}

			if valid {
				anyValid = true
			}

			if anyHealthy && anyValid {
				break
			}
		}

		// All pods exist but none are scheduled yet (no HostIP). The component
		// is definitively not running — treat as unhealthy+valid so alerts can fire.
		if !anyProbed {
			anyValid = true
		}

		results = append(results, ComponentHealthStatus{
			Component: comp.label,
			Healthy:   anyHealthy,
			Valid:     anyValid,
		})
	}

	return results, nil
}

// probeComponentHealth probes https://{hostIP}:{port}/healthz and returns whether
// the component is healthy and whether the probe produced a definitive result.
//
// valid=true means we got a definitive answer — either the component responded over
// HTTP (200/500/503) or it actively refused the TCP connection (ECONNREFUSED), which
// means the process is not listening on this port. valid=false means we cannot tell:
// a timeout or host-unreachable error indicates a network problem, not a component
// problem, so alerts should not fire.
func probeComponentHealth(ctx context.Context, hostIP, port string) (healthy, valid bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort(hostIP, port)+"/healthz", nil)
	if err != nil {
		return false, false
	}

	resp, err := controlPlaneHTTPClient.Do(req)
	if err != nil {
		// ECONNREFUSED means the process is definitively not listening on this port.
		// Treat as unhealthy+valid rather than indeterminate, so alerts can fire.
		if errors.Is(err, syscall.ECONNREFUSED) {
			return false, true
		}
		return false, false
	}

	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, true
	case http.StatusInternalServerError, http.StatusServiceUnavailable:
		return false, true
	default:
		return false, false
	}
}

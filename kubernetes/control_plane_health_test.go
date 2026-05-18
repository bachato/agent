package kubernetes

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestProbeComponentHealth(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		wantHealthy   bool
		wantValid     bool
	}{
		{
			name:        "200 is healthy and valid",
			statusCode:  http.StatusOK,
			wantHealthy: true,
			wantValid:   true,
		},
		{
			name:        "503 is unhealthy but valid",
			statusCode:  http.StatusServiceUnavailable,
			wantHealthy: false,
			wantValid:   true,
		},
		{
			name:        "403 is unhealthy and invalid",
			statusCode:  http.StatusForbidden,
			wantHealthy: false,
			wantValid:   false,
		},
		{
			name:        "401 is unhealthy and invalid",
			statusCode:  http.StatusUnauthorized,
			wantHealthy: false,
			wantValid:   false,
		},
		{
			name:        "500 is unhealthy but valid",
			statusCode:  http.StatusInternalServerError,
			wantHealthy: false,
			wantValid:   true,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			t.Cleanup(server.Close)

			oldClient := controlPlaneHTTPClient
			controlPlaneHTTPClient = server.Client()
			t.Cleanup(func() { controlPlaneHTTPClient = oldClient })

			host, port := splitHostPort(t, server.URL)
			healthy, valid := probeComponentHealth(context.Background(), host, port)

			assert.Equal(t, tc.wantHealthy, healthy)
			assert.Equal(t, tc.wantValid, valid)
		})
	}
}

func TestProbeComponentHealthConnectionRefused(t *testing.T) {
	// Port 1 on localhost has no listener — ECONNREFUSED is a definitive "not running" result.
	healthy, valid := probeComponentHealth(context.Background(), "127.0.0.1", "1")
	assert.False(t, healthy)
	assert.True(t, valid)
}

func TestCollectControlPlaneHealthAggregatesAcrossPods(t *testing.T) {
	oldProbeFn := probeControlPlaneHealthFn
	probeControlPlaneHealthFn = func(_ context.Context, hostIP, _ string) (healthy, valid bool) {
		switch hostIP {
		case "sched-a":
			return true, true
		case "sched-b":
			return false, true
		case "ctrl-a":
			return false, true
		default:
			return false, false
		}
	}
	t.Cleanup(func() { probeControlPlaneHealthFn = oldProbeFn })

	listPods := func(_ context.Context, component string) ([]corev1.Pod, error) {
		switch component {
		case "kube-scheduler":
			return []corev1.Pod{
				{Status: corev1.PodStatus{HostIP: "sched-a"}},
				{Status: corev1.PodStatus{HostIP: "sched-b"}},
			}, nil
		case "kube-controller-manager":
			return []corev1.Pod{{Status: corev1.PodStatus{HostIP: "ctrl-a"}}}, nil
		default:
			return nil, nil
		}
	}

	statuses, err := collectControlPlaneHealth(context.Background(), listPods)
	require.NoError(t, err)
	assert.Equal(t, []ComponentHealthStatus{
		{Component: "kube-scheduler", Healthy: true, Valid: true},
		{Component: "kube-controller-manager", Healthy: false, Valid: true},
	}, statuses)
}

func TestCollectControlPlaneHealthSkipsComponentsWithNoPods(t *testing.T) {
	listPods := func(_ context.Context, _ string) ([]corev1.Pod, error) {
		return []corev1.Pod{}, nil
	}

	statuses, err := collectControlPlaneHealth(context.Background(), listPods)
	require.NoError(t, err)
	assert.Empty(t, statuses)
}

func TestCollectControlPlaneHealthAllPodsUnscheduled(t *testing.T) {
	// Pods exist but none have a HostIP yet (Pending, unscheduled). This
	// can happen in non-kubeadm distros under resource pressure or node failures.
	// Component is definitively not running → valid=true, healthy=false.
	listPods := func(_ context.Context, component string) ([]corev1.Pod, error) {
		switch component {
		case "kube-scheduler":
			return []corev1.Pod{
				{Status: corev1.PodStatus{HostIP: ""}},
				{Status: corev1.PodStatus{HostIP: ""}},
			}, nil
		case "kube-controller-manager":
			return []corev1.Pod{
				{Status: corev1.PodStatus{HostIP: ""}},
			}, nil
		default:
			return nil, nil
		}
	}

	statuses, err := collectControlPlaneHealth(context.Background(), listPods)
	require.NoError(t, err)
	assert.Equal(t, []ComponentHealthStatus{
		{Component: "kube-scheduler", Healthy: false, Valid: true},
		{Component: "kube-controller-manager", Healthy: false, Valid: true},
	}, statuses)
}

func TestCollectControlPlaneHealthReturnsErrorWhenPodListFails(t *testing.T) {
	listPods := func(_ context.Context, component string) ([]corev1.Pod, error) {
		if component == "kube-scheduler" {
			return nil, errors.New("list failed")
		}

		return []corev1.Pod{}, nil
	}

	statuses, err := collectControlPlaneHealth(context.Background(), listPods)
	require.Nil(t, statuses)
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to list kube-scheduler pods")
}

func splitHostPort(t *testing.T, rawURL string) (host, port string) {
	t.Helper()

	u, err := url.Parse(rawURL)
	require.NoError(t, err)

	host, port, err = net.SplitHostPort(u.Host)
	require.NoError(t, err)

	return host, port
}

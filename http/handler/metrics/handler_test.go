package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/portainer/agent/kubernetes"
	pkgmetrics "github.com/portainer/portainer/pkg/metrics"
	"github.com/stretchr/testify/require"
)

func TestUpdateMetricsReplacesPublishedSnapshot(t *testing.T) {
	h := NewHandler()

	h.UpdateMetrics(&kubernetes.ClusterRawMetrics{
		HasCPU:               true,
		CPUUsageNanoCores:    2_000_000_000,
		CPUCapacityNanoCores: 4_000_000_000,
	})

	body := serveMetrics(t, h)
	require.Contains(t, body, pkgmetrics.ClusterCPUUsageCoresMetric)
	require.NotContains(t, body, pkgmetrics.ClusterMemoryWorkingSetBytesMetric)

	h.UpdateMetrics(&kubernetes.ClusterRawMetrics{
		HasMemory:             true,
		MemoryWorkingSetBytes: 1024,
		MemoryCapacityBytes:   2048,
	})

	body = serveMetrics(t, h)
	require.NotContains(t, body, pkgmetrics.ClusterCPUUsageCoresMetric)
	require.Contains(t, body, pkgmetrics.ClusterMemoryWorkingSetBytesMetric)
}

func TestClearMetricsRemovesPublishedSnapshot(t *testing.T) {
	h := NewHandler()
	h.UpdateMetrics(&kubernetes.ClusterRawMetrics{
		HasCPU:               true,
		CPUUsageNanoCores:    1_000_000_000,
		CPUCapacityNanoCores: 2_000_000_000,
	})

	h.ClearMetrics()

	body := serveMetrics(t, h)
	require.NotContains(t, body, pkgmetrics.ClusterCPUUsageCoresMetric)
	require.Empty(t, body)
}

func serveMetrics(t *testing.T, h *Handler) string {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}

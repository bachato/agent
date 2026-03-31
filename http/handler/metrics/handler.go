package metrics

import (
	"net/http"
	"sync"

	"github.com/portainer/agent/kubernetes"
	pkgmetrics "github.com/portainer/portainer/pkg/metrics"
	prometheusreg "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var metricNames = []string{
	pkgmetrics.ClusterCPUUsageCoresMetric,
	pkgmetrics.ClusterCPUCapacityCoresMetric,
	pkgmetrics.ClusterMemoryWorkingSetBytesMetric,
	pkgmetrics.ClusterMemoryCapacityBytesMetric,
	pkgmetrics.ClusterFilesystemUsedBytesMetric,
	pkgmetrics.ClusterFilesystemCapacityBytesMetric,
	pkgmetrics.ClusterNetworkReceiveBytesMetric,
	pkgmetrics.ClusterNetworkTransmitBytesMetric,
}

// Handler serves Prometheus exposition-format metrics at /api/metrics.
// It maintains the latest successful cluster metrics snapshot for exposition.
type Handler struct {
	handler http.Handler

	mu          sync.RWMutex
	descriptors map[string]*prometheusreg.Desc
	values      map[string]float64
}

// NewHandler creates a new metrics handler with a dedicated Prometheus registry.
func NewHandler() *Handler {
	reg := prometheusreg.NewRegistry()

	descriptors := make(map[string]*prometheusreg.Desc, len(metricNames))
	for _, name := range metricNames {
		descriptors[name] = prometheusreg.NewDesc(name, "Edge agent cluster metric: "+name, nil, nil)
	}

	h := &Handler{
		descriptors: descriptors,
		values:      make(map[string]float64),
	}
	reg.MustRegister(h)

	h.handler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	return h
}

// UpdateMetrics sets gauge values from raw Kubernetes cluster metrics.
func (h *Handler) UpdateMetrics(raw *kubernetes.ClusterRawMetrics) {
	snapshot := make(map[string]float64, len(metricNames))
	if raw != nil {
		if raw.HasCPU {
			snapshot[pkgmetrics.ClusterCPUUsageCoresMetric] = float64(raw.CPUUsageNanoCores) / 1e9
			snapshot[pkgmetrics.ClusterCPUCapacityCoresMetric] = float64(raw.CPUCapacityNanoCores) / 1e9
		}

		if raw.HasMemory {
			snapshot[pkgmetrics.ClusterMemoryWorkingSetBytesMetric] = float64(raw.MemoryWorkingSetBytes)
			snapshot[pkgmetrics.ClusterMemoryCapacityBytesMetric] = float64(raw.MemoryCapacityBytes)
		}

		if raw.HasDisk {
			snapshot[pkgmetrics.ClusterFilesystemUsedBytesMetric] = float64(raw.DiskUsedBytes)
			snapshot[pkgmetrics.ClusterFilesystemCapacityBytesMetric] = float64(raw.DiskCapacityBytes)
		}

		if raw.HasNetwork {
			snapshot[pkgmetrics.ClusterNetworkReceiveBytesMetric] = float64(raw.NetworkRxBytes)
			snapshot[pkgmetrics.ClusterNetworkTransmitBytesMetric] = float64(raw.NetworkTxBytes)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.values = snapshot
}

// ClearMetrics removes the currently published metric snapshot.
func (h *Handler) ClearMetrics() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.values = make(map[string]float64)
}

func (h *Handler) Describe(ch chan<- *prometheusreg.Desc) {
	for _, name := range metricNames {
		ch <- h.descriptors[name]
	}
}

func (h *Handler) Collect(ch chan<- prometheusreg.Metric) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, name := range metricNames {
		value, ok := h.values[name]
		if !ok {
			continue
		}

		vt := prometheusreg.GaugeValue
		if name == pkgmetrics.ClusterNetworkReceiveBytesMetric || name == pkgmetrics.ClusterNetworkTransmitBytesMetric {
			// Network bytes are cumulative counters from the kubelet stats/summary
			// endpoint; they must be exported with CounterValue to match the _total
			// suffix in their metric names.
			vt = prometheusreg.CounterValue
		}

		ch <- prometheusreg.MustNewConstMetric(h.descriptors[name], vt, value)
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

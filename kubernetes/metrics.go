package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	portainer "github.com/portainer/portainer/api"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// minimal subset of kubelet's stats/summary response - avoids k8s.io/kubelet dependency
type nodeSummary struct {
	Node struct {
		CPU     *struct{ UsageNanoCores *uint64 }  `json:"cpu"`
		Memory  *struct{ WorkingSetBytes *uint64 } `json:"memory"`
		Fs      *struct {
			UsedBytes     *uint64 `json:"usedBytes"`
			CapacityBytes *uint64 `json:"capacityBytes"`
		} `json:"fs"`
		Network *struct {
			TxBytes *uint64 `json:"txBytes"`
			RxBytes *uint64 `json:"rxBytes"`
		} `json:"network"`
	} `json:"node"`
}

type nodePerformanceSample struct {
	CPUUsageNanoCores     uint64
	CPUCapacityNanoCores  uint64
	HasCPU                bool
	MemoryWorkingSetBytes uint64
	MemoryCapacityBytes   uint64
	HasMemory             bool
	DiskUsedBytes         uint64
	DiskCapacityBytes     uint64
	HasDisk               bool
	NetworkTotalBytes     uint64
	HasNetwork            bool
}

var collectNodeMetricsFn = collectNodeMetrics

// CollectPerformanceMetrics collects CPU, memory, disk, and network usage by querying
// each node's kubelet stats/summary endpoint directly via the Kubernetes API proxy
// (GET /api/v1/nodes/{name}/proxy/stats/summary). The kubelet proxy is used instead of
// metrics.k8s.io because the Metrics API does not expose filesystem data, and the kubelet
// endpoint works without metrics-server installed.
func CollectPerformanceMetrics(ctx context.Context, kc *KubeClient) (*portainer.PerformanceMetrics, error) {
	nodeList, err := kc.cli.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return aggregateClusterPerformanceMetrics(nodeList.Items, func(node corev1.Node) (*nodePerformanceSample, error) {
		return collectNodeMetricsFn(ctx, kc, node)
	})
}

// aggregateClusterPerformanceMetrics queries all nodes concurrently, then sums the
// results into a single PerformanceMetrics value.
// CPU and memory are expressed as percentages of cluster capacity.
// Network is the cumulative lifetime byte total across all nodes (caller is responsible for delta conversion).
// Returns nil metrics (without error) if all nodes scraped successfully but none reported usable data.
func aggregateClusterPerformanceMetrics(nodes []corev1.Node, collectFn func(node corev1.Node) (*nodePerformanceSample, error)) (*portainer.PerformanceMetrics, error) {
	type result struct {
		node   string
		sample *nodePerformanceSample
		err    error
	}

	results := make([]result, len(nodes))

	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Go(func() {
			sample, err := collectFn(node)
			results[i] = result{node: node.Name, sample: sample, err: err}
		})
	}
	wg.Wait()

	totals := &portainer.PerformanceMetrics{}

	var successfulScrapes int
	var hasAnyMetric bool
	var totalCPUUsageNanoCores, totalCPUCapacityNanoCores uint64
	var totalMemoryWorkingSetBytes, totalMemoryCapacityBytes uint64
	var totalDiskUsedBytes, totalDiskCapacityBytes uint64
	var totalNetworkBytes uint64

	for _, r := range results {
		if r.err != nil {
			log.Warn().Err(r.err).Str("node", r.node).Msg("failed to collect node performance metrics")
			continue
		}

		successfulScrapes++

		if r.sample == nil {
			continue
		}

		if r.sample.HasCPU {
			hasAnyMetric = true
			totalCPUUsageNanoCores += r.sample.CPUUsageNanoCores
			totalCPUCapacityNanoCores += r.sample.CPUCapacityNanoCores
		}

		if r.sample.HasMemory {
			hasAnyMetric = true
			totalMemoryWorkingSetBytes += r.sample.MemoryWorkingSetBytes
			totalMemoryCapacityBytes += r.sample.MemoryCapacityBytes
		}

		if r.sample.HasDisk {
			hasAnyMetric = true
			totalDiskUsedBytes += r.sample.DiskUsedBytes
			totalDiskCapacityBytes += r.sample.DiskCapacityBytes
		}

		if r.sample.HasNetwork {
			hasAnyMetric = true
			totalNetworkBytes += r.sample.NetworkTotalBytes
		}
	}

	if successfulScrapes == 0 {
		return nil, errors.New("failed to collect performance metrics from all nodes")
	}

	if !hasAnyMetric {
		return nil, nil
	}

	if totalCPUCapacityNanoCores > 0 {
		totals.CPUUsage = math.Round(float64(totalCPUUsageNanoCores) / float64(totalCPUCapacityNanoCores) * 100)
	}

	if totalMemoryCapacityBytes > 0 {
		totals.MemoryUsage = math.Round(float64(totalMemoryWorkingSetBytes) / float64(totalMemoryCapacityBytes) * 100)
	}

	if totalDiskCapacityBytes > 0 {
		totals.DiskUsage = math.Round(float64(totalDiskUsedBytes) / float64(totalDiskCapacityBytes) * 100)
	}

	if totalNetworkBytes > 0 {
		totals.NetworkUsage = float64(totalNetworkBytes)
	}

	return totals, nil
}

// collectNodeMetrics fetches CPU, memory, and network stats for a single node via its
// kubelet stats/summary proxy endpoint. Returns nil (without error) if the node reports
// no usable metrics.
func collectNodeMetrics(ctx context.Context, kc *KubeClient, node corev1.Node) (*nodePerformanceSample, error) {
	raw, err := kc.cli.RESTClient().Get().
		AbsPath(fmt.Sprintf("/api/v1/nodes/%s/proxy/stats/summary", node.Name)).
		DoRaw(ctx)
	if err != nil {
		return nil, err
	}

	var stats nodeSummary
	if err := json.Unmarshal(raw, &stats); err != nil {
		return nil, err
	}

	sample := &nodePerformanceSample{}

	if stats.Node.CPU != nil && stats.Node.CPU.UsageNanoCores != nil {
		cpuCapacity := node.Status.Capacity.Cpu().Value() * 1_000_000_000
		if cpuCapacity > 0 {
			sample.CPUUsageNanoCores = *stats.Node.CPU.UsageNanoCores
			sample.CPUCapacityNanoCores = uint64(cpuCapacity)
			sample.HasCPU = true
		}
	}

	if stats.Node.Memory != nil && stats.Node.Memory.WorkingSetBytes != nil {
		memoryCapacity := node.Status.Capacity.Memory().Value()
		if memoryCapacity > 0 {
			sample.MemoryWorkingSetBytes = *stats.Node.Memory.WorkingSetBytes
			sample.MemoryCapacityBytes = uint64(memoryCapacity)
			sample.HasMemory = true
		}
	}

	if stats.Node.Fs != nil && stats.Node.Fs.UsedBytes != nil && stats.Node.Fs.CapacityBytes != nil {
		if *stats.Node.Fs.CapacityBytes > 0 {
			sample.DiskUsedBytes = *stats.Node.Fs.UsedBytes
			sample.DiskCapacityBytes = *stats.Node.Fs.CapacityBytes
			sample.HasDisk = true
		}
	}

	if stats.Node.Network != nil && stats.Node.Network.RxBytes != nil && stats.Node.Network.TxBytes != nil {
		sample.NetworkTotalBytes = *stats.Node.Network.RxBytes + *stats.Node.Network.TxBytes
		sample.HasNetwork = true
	}

	if !sample.HasCPU && !sample.HasMemory && !sample.HasDisk && !sample.HasNetwork {
		return nil, nil
	}

	return sample, nil
}

package kubernetes

import (
	"errors"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeNode(name string, cpuMillis int64, memBytes int64) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMillis, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
			},
		},
	}
}

func TestAggregateClusterPerformanceMetrics_DiskUsage(t *testing.T) {
	nodes := []corev1.Node{makeNode("node1", 4000, 8*1024*1024*1024)}

	t.Run("disk populated when fs data present", func(t *testing.T) {
		collectFn := func(_ corev1.Node) (*nodePerformanceSample, error) {
			return &nodePerformanceSample{
				DiskUsedBytes:     50 * 1024 * 1024 * 1024,
				DiskCapacityBytes: 100 * 1024 * 1024 * 1024,
				HasDisk:           true,
			}, nil
		}

		metrics, err := aggregateClusterPerformanceMetrics(nodes, collectFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if metrics == nil {
			t.Fatal("expected non-nil metrics")
		}
		if metrics.DiskUsage != 50 {
			t.Errorf("DiskUsage = %v, want 50", metrics.DiskUsage)
		}
	})

	t.Run("disk zero when fs data absent", func(t *testing.T) {
		collectFn := func(_ corev1.Node) (*nodePerformanceSample, error) {
			return &nodePerformanceSample{
				CPUUsageNanoCores:    1_000_000_000,
				CPUCapacityNanoCores: 4_000_000_000,
				HasCPU:               true,
			}, nil
		}

		metrics, err := aggregateClusterPerformanceMetrics(nodes, collectFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if metrics == nil {
			t.Fatal("expected non-nil metrics")
		}
		if metrics.DiskUsage != 0 {
			t.Errorf("DiskUsage = %v, want 0", metrics.DiskUsage)
		}
	})

	t.Run("disk aggregated across multiple nodes", func(t *testing.T) {
		twoNodes := []corev1.Node{
			makeNode("node1", 4000, 8*1024*1024*1024),
			makeNode("node2", 4000, 8*1024*1024*1024),
		}

		var counter atomic.Int64
		collectFn := func(_ corev1.Node) (*nodePerformanceSample, error) {
			i := counter.Add(1)
			used := uint64(i) * 25 * 1024 * 1024 * 1024
			return &nodePerformanceSample{
				DiskUsedBytes:     used,
				DiskCapacityBytes: 100 * 1024 * 1024 * 1024,
				HasDisk:           true,
			}, nil
		}

		// node1: 25/100 GB, node2: 50/100 GB → total 75/200 = 37.5 → rounds to 38
		metrics, err := aggregateClusterPerformanceMetrics(twoNodes, collectFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if metrics.DiskUsage != 38 {
			t.Errorf("DiskUsage = %v, want 38", metrics.DiskUsage)
		}
	})
}

func TestAggregateClusterPerformanceMetrics_AllNodesFail(t *testing.T) {
	nodes := []corev1.Node{makeNode("node1", 4000, 8*1024*1024*1024)}

	collectFn := func(_ corev1.Node) (*nodePerformanceSample, error) {
		return nil, errors.New("connection refused")
	}

	_, err := aggregateClusterPerformanceMetrics(nodes, collectFn)
	if err == nil {
		t.Fatal("expected error when all nodes fail")
	}
}


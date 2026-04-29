package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeReadyStatus holds the readiness state of a single node.
type NodeReadyStatus struct {
	Name          string
	Ready         bool
	Unschedulable bool
}

// CollectNodeConditions lists all nodes and returns their Ready condition status.
func CollectNodeConditions(ctx context.Context, kc *KubeClient) ([]NodeReadyStatus, error) {
	nodeList, err := kc.cli.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return collectNodeReadyStatuses(nodeList.Items), nil
}

func collectNodeReadyStatuses(nodes []corev1.Node) []NodeReadyStatus {
	statuses := make([]NodeReadyStatus, 0, len(nodes))
	for _, node := range nodes {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				ready = condition.Status == corev1.ConditionTrue
				break
			}
		}

		statuses = append(statuses, NodeReadyStatus{Name: node.Name, Ready: ready, Unschedulable: node.Spec.Unschedulable})
	}

	return statuses
}

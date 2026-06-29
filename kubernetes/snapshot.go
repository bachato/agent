package kubernetes

import (
	"context"
	"fmt"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/snapshot"
)

// CreateSnapshot creates a snapshot of a specific Kubernetes environment(endpoint)
func CreateSnapshot(edgeKey string) (*portainer.KubernetesSnapshot, error) {
	cli, err := BuildLocalClient()
	if err != nil {
		return nil, fmt.Errorf("unable to create Kubernetes client. Error: %w", err)
	}

	res := cli.RESTClient().Get().AbsPath("/healthz").Do(context.TODO())
	if res.Error() != nil {
		return nil, fmt.Errorf("failed to ping /healthz endpoint. Error: %w", res.Error())
	}

	kubernetesSnapshot, err := snapshot.CreateKubernetesSnapshot(cli)
	if err != nil {
		return nil, fmt.Errorf("unable to create Kubernetes snapshot. Error: %w", err)
	}

	diagnosticsData, err := snapshot.KubernetesSnapshotDiagnostics(cli, edgeKey)
	if err != nil {
		return nil, fmt.Errorf("unable to create Kubernetes snapshot diagnostics. Error: %w", err)
	}
	kubernetesSnapshot.DiagnosticsData = diagnosticsData

	return kubernetesSnapshot, nil
}

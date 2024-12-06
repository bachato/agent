package docker

import (
	"context"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/snapshot"
)

func CreateSnapshot(edgeKey string) (*portainer.DockerSnapshot, error) {
	cli, err := NewClient()
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	_, err = cli.Ping(context.Background())
	if err != nil {
		return nil, err
	}

	dockerSnapshot, err := snapshot.CreateDockerSnapshot(cli)
	if err != nil {
		return nil, err
	}

	diagnosticsData, err := snapshot.DockerSnapshotDiagnostics(cli, edgeKey)
	if err != nil {
		return nil, err
	}
	dockerSnapshot.DiagnosticsData = diagnosticsData

	return dockerSnapshot, nil
}

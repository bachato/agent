package docker

import (
	"context"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/snapshot"

	"golang.org/x/sync/singleflight"
)

var snapshotSingleflight singleflight.Group

func CreateSnapshot(edgeKey string) (*portainer.DockerSnapshot, error) {
	snapshot, err, _ := snapshotSingleflight.Do(edgeKey, func() (interface{}, error) {
		return createSnapshot(edgeKey)
	})

	return snapshot.(*portainer.DockerSnapshot), err
}

func createSnapshot(edgeKey string) (*portainer.DockerSnapshot, error) {
	cli, err := NewClient()
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	if _, err := cli.Ping(context.Background()); err != nil {
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

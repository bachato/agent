package docker

import (
	"sort"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/snapshot"
)

type Snapshotter struct{}

func (Snapshotter) CreateSnapshot(edgeKey string) (*portainer.DockerSnapshot, error) {
	cli, err := NewClient()
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	dockerSnapshot, err := snapshot.CreateDockerSnapshot(cli)
	if err != nil {
		return nil, err
	}

	diagnosticsData, err := snapshot.DockerSnapshotDiagnostics(cli, edgeKey)
	if err != nil {
		return nil, err
	}
	dockerSnapshot.DiagnosticsData = diagnosticsData

	optimizeDockerSnapshot(dockerSnapshot)

	return dockerSnapshot, nil
}

func optimizeDockerSnapshot(s *portainer.DockerSnapshot) {
	sort.Slice(s.SnapshotRaw.Images, func(i, j int) bool {
		return s.SnapshotRaw.Images[i].ID < s.SnapshotRaw.Images[j].ID
	})

	sort.Slice(s.SnapshotRaw.Networks, func(i, j int) bool {
		return s.SnapshotRaw.Networks[i].Name < s.SnapshotRaw.Networks[j].Name
	})

	sort.Slice(s.SnapshotRaw.Volumes.Volumes, func(i, j int) bool {
		return s.SnapshotRaw.Volumes.Volumes[i].Name < s.SnapshotRaw.Volumes.Volumes[j].Name
	})

	for k := range s.SnapshotRaw.Containers {
		sort.Slice(s.SnapshotRaw.Containers[k].Mounts, func(i, j int) bool {
			return s.SnapshotRaw.Containers[k].Mounts[i].Destination < s.SnapshotRaw.Containers[k].Mounts[j].Destination
		})

		sort.Slice(s.SnapshotRaw.Containers[k].Ports, func(i, j int) bool {
			return s.SnapshotRaw.Containers[k].Ports[i].PrivatePort < s.SnapshotRaw.Containers[k].Ports[j].PrivatePort
		})
	}
}

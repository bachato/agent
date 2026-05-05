package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

func TestContainerWait(t *testing.T) {
	t.Setenv("DOCKER_HOST", "invalid-host")

	statusCh, errCh := ContainerWait("container-name", container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return
		}
	case <-statusCh:
	}

	t.Fail()
}

func TestGetVolumeMountpoint_DockerUnavailable_ReturnsError(t *testing.T) {
	t.Setenv("DOCKER_HOST", "invalid-host")

	mountpoint, err := GetVolumeMountpoint("any-volume")
	require.Error(t, err)
	require.Empty(t, mountpoint)
}

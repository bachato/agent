package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
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

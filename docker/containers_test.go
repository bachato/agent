package docker

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/portainer/portainer/api/filesystem"
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

func TestCopyToContainer_TarsSourceDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	expectedFiles := map[string]string{
		"compose.yml": "test-compose-file",
	}
	for name, content := range expectedFiles {
		require.NoError(t, os.WriteFile(filesystem.JoinPaths(tmpDir, name), []byte(content), 0o600))
	}

	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(server.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+server.Listener.Addr().String())

	err := CopyToContainer("my-container", "/dst", tmpDir)
	require.NoError(t, err)

	tr := tar.NewReader(bytes.NewReader(receivedBody))
	gotFiles := map[string]string{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)

		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		gotFiles[header.Name] = string(data)
	}

	require.Equal(t, expectedFiles, gotFiles)
}

func TestCopyToContainer_NonExistentSource_ReturnsError(t *testing.T) {
	err := CopyToContainer("my-container", "/dst", "/path/does/not/exist")
	require.Error(t, err)
}

func TestGetVolumeMountpoint_DockerUnavailable_ReturnsError(t *testing.T) {
	t.Setenv("DOCKER_HOST", "invalid-host")

	mountpoint, err := GetVolumeMountpoint("any-volume")
	require.Error(t, err)
	require.Empty(t, mountpoint)
}

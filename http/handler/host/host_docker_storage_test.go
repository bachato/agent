//go:build !windows

package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/go-units"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentdocker "github.com/portainer/agent/docker"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
)

// fakeCleanupClient is a test double for agentdocker.CleanupClient.
type fakeCleanupClient struct {
	diskUsage    dockertypes.DiskUsage
	diskUsageErr error
}

func (c *fakeCleanupClient) DiskUsage(_ context.Context, _ dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error) {
	return c.diskUsage, c.diskUsageErr
}

func (c *fakeCleanupClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return nil, nil
}

func (c *fakeCleanupClient) ImageList(_ context.Context, _ image.ListOptions) ([]image.Summary, error) {
	return nil, nil
}

func (c *fakeCleanupClient) ImageRemove(_ context.Context, _ string, _ image.RemoveOptions) ([]image.DeleteResponse, error) {
	return nil, nil
}

func (c *fakeCleanupClient) BuildCachePrune(_ context.Context, _ build.CachePruneOptions) (*build.CachePruneReport, error) {
	return nil, nil
}

func (c *fakeCleanupClient) Close() error { return nil }

func fakeCleanupFactory(client *fakeCleanupClient, factoryErr error) agentdocker.CleanupClientFactory {
	return func() (agentdocker.CleanupClient, error) {
		if factoryErr != nil {
			return nil, factoryErr
		}
		return client, nil
	}
}

// handlerWithFactory creates a bare Handler with the given factory and a real disk path,
// bypassing the normal NewHandler constructor which requires proxy and notary services.
func handlerWithFactory(factory agentdocker.CleanupClientFactory) *Handler {
	return &Handler{
		Router:               mux.NewRouter(),
		cleanupClientFactory: factory,
		diskPath:             os.TempDir(),
	}
}

// storageResponse mirrors the JSON shape returned by the docker-storage endpoint.
type storageResponse struct {
	RootDir         string `json:"rootDir"`
	TotalBytes      uint64 `json:"totalBytes"`
	DockerBytes     uint64 `json:"dockerBytes"`
	ImageBytes      uint64 `json:"imageBytes"`
	ContainerBytes  uint64 `json:"containerBytes"`
	VolumeBytes     uint64 `json:"volumeBytes"`
	BuildCacheBytes uint64 `json:"buildCacheBytes"`
	AvailableBytes  uint64 `json:"availableBytes"`
}

func TestDockerStorageHandler_Success(t *testing.T) {
	t.Parallel()

	// Agent is running on a host with Docker data on the root partition.
	// The endpoint should return 200 with a fully populated JSON payload.
	client := &fakeCleanupClient{
		diskUsage: dockertypes.DiskUsage{
			LayersSize: 500 * units.MiB,
			Containers: []*container.Summary{
				{SizeRw: 10 * units.MiB},
			},
			Volumes: []*volume.Volume{
				{UsageData: &volume.UsageData{Size: 50 * units.MiB}},
			},
			BuildCache: []*build.CacheRecord{
				{Size: 20 * units.MiB},
			},
		},
	}

	h := handlerWithFactory(fakeCleanupFactory(client, nil))
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/host/docker-storage", nil)

	httperror.LoggerHandler(h.dockerStorage).ServeHTTP(rw, req)

	require.Equal(t, http.StatusOK, rw.Code)

	var resp storageResponse
	require.NoError(t, json.NewDecoder(rw.Body).Decode(&resp))

	assert.Positive(t, resp.TotalBytes)
	assert.Positive(t, resp.AvailableBytes)
	assert.Equal(t, uint64(580*units.MiB), resp.DockerBytes)
	assert.Equal(t, uint64(500*units.MiB), resp.ImageBytes)
	assert.Equal(t, uint64(10*units.MiB), resp.ContainerBytes)
	assert.Equal(t, uint64(50*units.MiB), resp.VolumeBytes)
	assert.Equal(t, uint64(20*units.MiB), resp.BuildCacheBytes)

	assert.Equal(t, os.TempDir(), resp.RootDir)
}

func TestDockerStorageHandler_StorageUnavailable(t *testing.T) {
	t.Parallel()

	// The agent cannot reach the Docker socket (e.g. sidecar deployment without socket mount).
	// The response should be 204 No Content so the caller can distinguish
	// "agent reachable but storage unavailable" from "agent unreachable".
	h := handlerWithFactory(fakeCleanupFactory(nil, errors.New("connect: no such file or directory")))
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/host/docker-storage", nil)

	httperror.LoggerHandler(h.dockerStorage).ServeHTTP(rw, req)

	assert.Equal(t, http.StatusNoContent, rw.Code)
}

func TestDockerStorageHandler_DockerDaemonError(t *testing.T) {
	t.Parallel()

	// The Docker daemon is reachable but returns an error mid-operation
	// (e.g. daemon restarting during a request). Same 204 response applies.
	client := &fakeCleanupClient{diskUsageErr: errors.New("daemon not responding")}
	h := handlerWithFactory(fakeCleanupFactory(client, nil))
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/host/docker-storage", nil)

	httperror.LoggerHandler(h.dockerStorage).ServeHTTP(rw, req)

	assert.Equal(t, http.StatusNoContent, rw.Code)
}

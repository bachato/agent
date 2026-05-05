//go:build !windows

package cleanup

import (
	"context"
	"errors"
	"os"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentdocker "github.com/portainer/agent/docker"
)

// fakeDockerClient is a minimal CleanupClient for testing GetDockerStorageUsage.
type fakeDockerClient struct {
	diskUsage    dockertypes.DiskUsage
	diskUsageErr error
}

func (c *fakeDockerClient) DiskUsage(_ context.Context, _ dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error) {
	return c.diskUsage, c.diskUsageErr
}

func (c *fakeDockerClient) Close() error { return nil }

func fakeFactory(client *fakeDockerClient, factoryErr error) agentdocker.CleanupClientFactory {
	return func() (agentdocker.CleanupClient, error) {
		if factoryErr != nil {
			return nil, factoryErr
		}
		return client, nil
	}
}

func TestGetDockerStorageUsage_Success(t *testing.T) {
	t.Parallel()

	layersSize := int64(500 * 1024 * 1024) // 500 MB
	containerSizeRw := int64(10 * 1024 * 1024)
	volumeSize := int64(50 * 1024 * 1024)
	cacheSize := int64(20 * 1024 * 1024)

	client := &fakeDockerClient{
		diskUsage: dockertypes.DiskUsage{
			LayersSize: layersSize,
			Containers: []*container.Summary{
				{SizeRw: containerSizeRw},
			},
			Volumes: []*volume.Volume{
				{UsageData: &volume.UsageData{Size: volumeSize}},
			},
			BuildCache: []*build.CacheRecord{
				{Size: cacheSize},
			},
		},
	}

	usage, err := getDockerStorageUsage(context.Background(), fakeFactory(client, nil), os.TempDir())
	require.NoError(t, err)

	expectedDockerBytes := uint64(layersSize + containerSizeRw + volumeSize + cacheSize)
	assert.Equal(t, os.TempDir(), usage.RootDir)
	assert.Equal(t, expectedDockerBytes, usage.DockerBytes)
	assert.Equal(t, uint64(layersSize), usage.ImageBytes)
	assert.Equal(t, uint64(containerSizeRw), usage.ContainerBytes)
	assert.Equal(t, uint64(volumeSize), usage.VolumeBytes)
	assert.Equal(t, uint64(cacheSize), usage.BuildCacheBytes)
	assert.Positive(t, usage.TotalBytes)
}

func TestGetDockerStorageUsage_ClientFactoryError(t *testing.T) {
	t.Parallel()

	_, err := getDockerStorageUsage(context.Background(), fakeFactory(nil, errors.New("connection refused")), os.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create Docker cleanup client")
}

func TestGetDockerStorageUsage_DiskUsageError(t *testing.T) {
	t.Parallel()

	client := &fakeDockerClient{diskUsageErr: errors.New("daemon not responding")}

	_, err := getDockerStorageUsage(context.Background(), fakeFactory(client, nil), os.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query Docker disk usage")
}

func TestGetDockerStorageUsage_InaccessibleDiskPath(t *testing.T) {
	t.Parallel()

	client := &fakeDockerClient{}

	_, err := getDockerStorageUsage(context.Background(), fakeFactory(client, nil), "/nonexistent/path/that/does/not/exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stat disk at")
}

func TestSumDockerBytes_SkipsNegativeVolumeSizes(t *testing.T) {
	t.Parallel()

	du := dockertypes.DiskUsage{
		LayersSize: 100,
		Volumes: []*volume.Volume{
			{UsageData: &volume.UsageData{Size: -1}}, // unknown, non-local driver
			{UsageData: &volume.UsageData{Size: 50}},
		},
	}
	b := sumDockerBytes(du)
	assert.Equal(t, uint64(100), b.imageBytes)
	assert.Equal(t, uint64(50), b.volumeBytes)
	assert.Equal(t, uint64(150), b.total())
}

func TestSumDockerBytes_SkipsContainerSizeRootFs(t *testing.T) {
	t.Parallel()

	// SizeRootFs includes image layers already counted in LayersSize; only SizeRw should be added.
	du := dockertypes.DiskUsage{
		LayersSize: 1000,
		Containers: []*container.Summary{
			{SizeRw: 50, SizeRootFs: 1050}, // SizeRootFs would double-count 1000 of image data
		},
	}
	b := sumDockerBytes(du)
	assert.Equal(t, uint64(1000), b.imageBytes)
	assert.Equal(t, uint64(50), b.containerBytes)
	assert.Equal(t, uint64(1050), b.total())
}

func TestSumDockerBytes_EmptyInput(t *testing.T) {
	t.Parallel()

	// Fresh install: no images, containers, volumes, or build cache.
	b := sumDockerBytes(dockertypes.DiskUsage{})
	assert.Equal(t, uint64(0), b.imageBytes)
	assert.Equal(t, uint64(0), b.containerBytes)
	assert.Equal(t, uint64(0), b.volumeBytes)
	assert.Equal(t, uint64(0), b.buildCacheBytes)
	assert.Equal(t, uint64(0), b.total())
}

func TestSumDockerBytes_SkipsNegativeLayersSize(t *testing.T) {
	t.Parallel()

	// Docker returns -1 for LayersSize when the daemon cannot determine image disk usage.
	b := sumDockerBytes(dockertypes.DiskUsage{LayersSize: -1})
	assert.Equal(t, uint64(0), b.imageBytes)
	assert.Equal(t, uint64(0), b.total())
}

func TestSumDockerBytes_SkipsVolumeWithNilUsageData(t *testing.T) {
	t.Parallel()

	// Non-local volume drivers often return nil UsageData; they must not be counted or panic.
	du := dockertypes.DiskUsage{
		Volumes: []*volume.Volume{
			{UsageData: nil},
			{UsageData: &volume.UsageData{Size: 100}},
		},
	}
	b := sumDockerBytes(du)
	assert.Equal(t, uint64(100), b.volumeBytes)
}

func TestSumDockerBytes_MultipleContainersSum(t *testing.T) {
	t.Parallel()

	// Three containers running concurrently; only writable-layer deltas (SizeRw) are summed.
	// SizeRootFs includes shared image layers already counted in LayersSize and must be excluded.
	du := dockertypes.DiskUsage{
		Containers: []*container.Summary{
			{SizeRw: 10, SizeRootFs: 110},
			{SizeRw: 20, SizeRootFs: 120},
			{SizeRw: 0, SizeRootFs: 100}, // stopped container, no writable-layer delta
		},
	}
	b := sumDockerBytes(du)
	assert.Equal(t, uint64(30), b.containerBytes)
	assert.Equal(t, uint64(30), b.total())
}

func TestDockerBreakdown_Total(t *testing.T) {
	t.Parallel()

	b := dockerBreakdown{
		imageBytes:      500,
		containerBytes:  10,
		volumeBytes:     50,
		buildCacheBytes: 20,
	}
	assert.Equal(t, uint64(580), b.total())
}

func TestGetDockerStorageUsage_ZeroDockerBytes(t *testing.T) {
	t.Parallel()

	// Fresh Docker install: daemon reports no disk usage.
	// Disk fields must still be populated; DockerBytes and DockerPercent must be zero.
	client := &fakeDockerClient{diskUsage: dockertypes.DiskUsage{}}

	usage, err := getDockerStorageUsage(context.Background(), fakeFactory(client, nil), os.TempDir())
	require.NoError(t, err)

	assert.Equal(t, uint64(0), usage.DockerBytes)
	assert.Positive(t, usage.TotalBytes)
	assert.Positive(t, usage.AvailableBytes)
	assert.Equal(t, os.TempDir(), usage.RootDir)
}

func TestGetDockerStorageUsage_AvailableBytesLessThanTotal(t *testing.T) {
	t.Parallel()

	// Any real filesystem has some bytes consumed by the OS or other processes.
	client := &fakeDockerClient{
		diskUsage: dockertypes.DiskUsage{LayersSize: int64(1024)},
	}

	usage, err := getDockerStorageUsage(context.Background(), fakeFactory(client, nil), os.TempDir())
	require.NoError(t, err)

	assert.Greater(t, usage.TotalBytes, usage.AvailableBytes,
		"filesystem should have some bytes consumed by non-Docker processes")
}

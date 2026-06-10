package cleanup

import (
	"context"
	"fmt"

	dockertypes "github.com/docker/docker/api/types"

	"github.com/portainer/agent/constants"
	agentdocker "github.com/portainer/agent/docker"
	"github.com/portainer/portainer/api/logs"
)

// StorageUsage is a point-in-time view of Docker storage relative to the disk
// that backs the Docker data directory.
type StorageUsage struct {
	// RootDir is the path used to determine disk capacity (always dockerDataPath).
	RootDir string
	// DockerBytes is the total bytes consumed by Docker artifacts:
	// images, container writable layers, volumes, and build cache.
	// This is NOT total disk used — other processes on the same disk are excluded.
	// See sumDockerBytes for the exact accounting.
	DockerBytes uint64
	// ImageBytes is the deduplicated image layer data (LayersSize from docker system df).
	ImageBytes uint64
	// ContainerBytes is the sum of writable-layer sizes across all containers (SizeRw).
	// Image layers are excluded to avoid double-counting with ImageBytes.
	ContainerBytes uint64
	// VolumeBytes is the sum of known volume sizes. Volumes with unknown sizes
	// (e.g. non-local drivers) are excluded.
	VolumeBytes uint64
	// BuildCacheBytes is the sum of all build cache entry sizes.
	BuildCacheBytes uint64
	// TotalBytes is the total capacity of the partition hosting the Docker data directory.
	TotalBytes uint64
	// AvailableBytes is the bytes available to unprivileged processes on that partition.
	// Total non-Docker disk used = TotalBytes - AvailableBytes - DockerBytes.
	AvailableBytes uint64
}

// dockerBreakdown holds per-artifact Docker byte counts computed by sumDockerBytes.
type dockerBreakdown struct {
	imageBytes      uint64
	containerBytes  uint64
	volumeBytes     uint64
	buildCacheBytes uint64
}

// total returns the sum of all artifact byte counts.
func (b dockerBreakdown) total() uint64 {
	return b.imageBytes + b.containerBytes + b.volumeBytes + b.buildCacheBytes
}

// GetDockerStorageUsage returns a StorageUsage snapshot combining:
//   - disk capacity from statfs on dockerDataPath (always bind-mounted in standard deployments)
//   - Docker's own byte accounting via "docker system df" (through the Docker socket)
func GetDockerStorageUsage(ctx context.Context, newClient agentdocker.CleanupClientFactory) (StorageUsage, error) {
	return getDockerStorageUsage(ctx, newClient, constants.SystemVolumePath)
}

// GetDockerStorageUsageForPath is like GetDockerStorageUsage but scopes disk metrics
// to the filesystem containing diskPath instead of the default SystemVolumePath.
// It is used by the HTTP handler so the disk path can be overridden in tests.
func GetDockerStorageUsageForPath(ctx context.Context, newClient agentdocker.CleanupClientFactory, diskPath string) (StorageUsage, error) {
	return getDockerStorageUsage(ctx, newClient, diskPath)
}

// getDockerStorageUsage is the testable inner form that accepts a custom diskPath.
func getDockerStorageUsage(ctx context.Context, newClient agentdocker.CleanupClientFactory, diskPath string) (StorageUsage, error) {
	diskStats, err := storageUsageForPath(diskPath)
	if err != nil {
		return StorageUsage{}, fmt.Errorf("failed to stat disk at %s. Error: %w", diskPath, err)
	}

	cli, err := newClient()
	if err != nil {
		return StorageUsage{}, fmt.Errorf("failed to create Docker cleanup client. Error: %w", err)
	}
	defer logs.CloseAndLogErr(cli)

	dockerDiskUsage, err := cli.DiskUsage(ctx, dockertypes.DiskUsageOptions{})
	if err != nil {
		return StorageUsage{}, fmt.Errorf("failed to query Docker disk usage. Error: %w", err)
	}

	breakdown := sumDockerBytes(dockerDiskUsage)

	return StorageUsage{
		RootDir:         diskPath,
		TotalBytes:      diskStats.TotalBytes,
		AvailableBytes:  diskStats.AvailableBytes,
		DockerBytes:     breakdown.total(),
		ImageBytes:      breakdown.imageBytes,
		ContainerBytes:  breakdown.containerBytes,
		VolumeBytes:     breakdown.volumeBytes,
		BuildCacheBytes: breakdown.buildCacheBytes,
	}, nil
}

// sumDockerBytes calculates per-artifact Docker byte counts.
//
// Included:
//   - LayersSize: deduplicated image layer data (the bulk of Docker's disk use)
//   - SizeRw per container: writable layer only — image layers are already in LayersSize
//   - UsageData.Size per volume: skipped when -1 (unknown, e.g. non-local drivers)
//   - Size per build cache entry
//
// Excluded:
//   - SizeRootFs per container: this includes image layers, which are already in
//     LayersSize; adding it would double-count shared image data.
func sumDockerBytes(dockerDiskUsage dockertypes.DiskUsage) dockerBreakdown {
	var b dockerBreakdown

	if dockerDiskUsage.LayersSize > 0 {
		b.imageBytes = uint64(dockerDiskUsage.LayersSize)
	}

	for _, c := range dockerDiskUsage.Containers {
		if c.SizeRw > 0 {
			b.containerBytes += uint64(c.SizeRw)
		}
	}

	for _, v := range dockerDiskUsage.Volumes {
		if v.UsageData != nil && v.UsageData.Size > 0 {
			b.volumeBytes += uint64(v.UsageData.Size)
		}
	}

	for _, bc := range dockerDiskUsage.BuildCache {
		if bc.Size > 0 {
			b.buildCacheBytes += uint64(bc.Size)
		}
	}

	return b
}

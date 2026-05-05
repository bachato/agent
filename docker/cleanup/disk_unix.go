//go:build !windows

package cleanup

import (
	"fmt"
	"syscall"
)

// diskUsagePercent returns the percentage of the filesystem containing
// dockerRootDir that is currently in use.
//
// This metric is scoped specifically to the filesystem that backs Docker's
// image storage — it is NOT a generic "host disk usage" figure. On hosts
// where the Docker data root lives on a separate mount (e.g. /var/lib/docker
// on its own LVM volume), this correctly reflects that mount's occupancy.
//
// Portainer agents are typically deployed with the host root bind-mounted,
// which gives the agent access to the real host filesystem paths. If the
// Docker data root path is not accessible, Statfs will return an error and
// the cleanup cycle will abort for that run.
func diskUsagePercent(dockerRootDir string) (float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dockerRootDir, &stat); err != nil {
		return 0, fmt.Errorf("failed to stat filesystem at %s. Error: %w", dockerRootDir, err)
	}
	if stat.Blocks == 0 {
		return 0, nil
	}
	// Use Bavail (blocks available to unprivileged user) rather than Bfree
	// (which includes root-reserved blocks) to avoid falsely reporting more
	// free space than unprivileged processes can actually use.
	return usedPercent(stat.Blocks, stat.Bavail), nil
}

// filesystemFreeBytes returns the number of bytes available to an unprivileged
// user on the filesystem containing dockerRootDir.
func filesystemFreeBytes(dockerRootDir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dockerRootDir, &stat); err != nil {
		return 0, fmt.Errorf("failed to stat filesystem at %s. Error: %w", dockerRootDir, err)
	}
	return stat.Bavail * uint64(stat.Bsize), nil //nolint:gosec // Bsize is positive on real filesystems
}

// storageUsageForPath returns a StorageUsage snapshot for the filesystem
// containing path, computed in a single Statfs call.
func storageUsageForPath(path string) (StorageUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return StorageUsage{}, fmt.Errorf("failed to stat filesystem at %s. Error: %w", path, err)
	}
	if stat.Blocks == 0 {
		return StorageUsage{}, nil
	}
	totalBytes := stat.Blocks * uint64(stat.Bsize) //nolint:gosec // Bsize is positive on real filesystems
	availBytes := stat.Bavail * uint64(stat.Bsize) //nolint:gosec
	return StorageUsage{
		TotalBytes:     totalBytes,
		AvailableBytes: availBytes,
	}, nil
}

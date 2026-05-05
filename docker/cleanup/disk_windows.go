//go:build windows

package cleanup

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/windows"
)

// diskUsagePercent returns the percentage of the filesystem containing
// dockerRootDir that is currently in use.
//
// This metric is scoped specifically to the filesystem that backs Docker's
// image storage — it is NOT a generic "host disk usage" figure.
func diskUsagePercent(dockerRootDir string) (float64, error) {
	pathPtr, err := syscall.UTF16PtrFromString(dockerRootDir)
	if err != nil {
		return 0, fmt.Errorf("invalid path %q. Error: %w", dockerRootDir, err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("failed to get disk free space for %s. Error: %w", dockerRootDir, err)
	}
	if total == 0 {
		return 0, nil
	}
	return usedPercent(total, free), nil
}

// filesystemFreeBytes returns the number of bytes available to the calling
// process on the filesystem containing dockerRootDir.
func filesystemFreeBytes(dockerRootDir string) (uint64, error) {
	pathPtr, err := syscall.UTF16PtrFromString(dockerRootDir)
	if err != nil {
		return 0, fmt.Errorf("invalid path %q. Error: %w", dockerRootDir, err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("failed to get disk free space for %s. Error: %w", dockerRootDir, err)
	}
	return free, nil
}

// storageUsageForPath returns a StorageUsage snapshot for the filesystem
// containing path, computed in a single GetDiskFreeSpaceEx call.
func storageUsageForPath(path string) (StorageUsage, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return StorageUsage{}, fmt.Errorf("invalid path %q. Error: %w", path, err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree); err != nil {
		return StorageUsage{}, fmt.Errorf("failed to get disk free space for %s. Error: %w", path, err)
	}
	if total == 0 {
		return StorageUsage{}, nil
	}
	return StorageUsage{
		TotalBytes:     total,
		AvailableBytes: free,
	}, nil
}

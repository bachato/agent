//go:build !windows

package cleanup

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiskUsagePercent(t *testing.T) {
	t.Parallel()

	pct, err := diskUsagePercent(os.TempDir())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, pct, 0.0)
	assert.LessOrEqual(t, pct, 100.0)
}

func TestFilesystemFreeBytes(t *testing.T) {
	t.Parallel()

	free, err := filesystemFreeBytes(os.TempDir())
	require.NoError(t, err)
	assert.Positive(t, free)
}

func TestStorageUsageForPath_ValidPath(t *testing.T) {
	t.Parallel()

	// When Docker's data directory is on a separate mount, storageUsageForPath must
	// correctly reflect that partition's total and available bytes.
	usage, err := storageUsageForPath(os.TempDir())
	require.NoError(t, err)
	assert.Positive(t, usage.TotalBytes)
	assert.Positive(t, usage.AvailableBytes)
	assert.Greater(t, usage.TotalBytes, usage.AvailableBytes)
}

func TestStorageUsageForPath_InvalidPath(t *testing.T) {
	t.Parallel()

	// If the Docker data directory path is not accessible (e.g. host not bind-mounted),
	// the function must return a descriptive error rather than silent zeroes.
	_, err := storageUsageForPath("/nonexistent/portainer/test/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stat filesystem at")
}

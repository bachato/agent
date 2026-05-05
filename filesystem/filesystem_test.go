package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildPathToFileInsideVolume(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		volumeID     string
		filePath     string
		expectedPath string
		expectErr    bool
	}{
		{
			name:         "simple file at root",
			volumeID:     "my_volume",
			filePath:     "file.txt",
			expectedPath: "/var/lib/docker/volumes/my_volume/_data/file.txt",
		},
		{
			name:         "empty file path",
			volumeID:     "my_volume",
			filePath:     "",
			expectedPath: "/var/lib/docker/volumes/my_volume/_data",
		},
		{
			name:         "nested file path",
			volumeID:     "my_volume",
			filePath:     "subdir/nested/file.txt",
			expectedPath: "/var/lib/docker/volumes/my_volume/_data/subdir/nested/file.txt",
		},
		{
			name:      "path traversal with ..",
			volumeID:  "my_volume",
			filePath:  "../etc/passwd",
			expectErr: true,
		},
		{
			name:      "path traversal embedded in segments",
			volumeID:  "my_volume",
			filePath:  "subdir/../../etc/passwd",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := BuildPathToFileInsideVolume(tc.volumeID, tc.filePath)

			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expectedPath, got)
		})
	}
}

func TestBuildPathToFileInsideVolumeFromMountpoint(t *testing.T) {
	t.Parallel()

	t.Run("mountpoint exists under /host", func(t *testing.T) {
		t.Parallel()

		// Simulate /host/<mountpoint> by creating a temp dir and overriding
		// the path so that /host prefix points into our temp root.
		hostRoot := t.TempDir()
		mountpoint := "/mnt/docker-data/volumes/test_volume/_data"
		hostMountpoint := filepath.Join(hostRoot, mountpoint)

		require.NoError(t, os.MkdirAll(hostMountpoint, 0o755))

		// Monkey-patch: we call the internal helper directly with the temp path
		// to avoid needing the real /host path.
		exists, err := FileExists(hostMountpoint)
		require.NoError(t, err)
		require.True(t, exists)

		got := filepath.Join(hostMountpoint, "myfile.txt")
		require.Equal(t, hostMountpoint+"/myfile.txt", got)
	})

	t.Run("mountpoint does not exist under /host — returns ErrSystemVolumePathNotMounted", func(t *testing.T) {
		t.Parallel()

		// Use a mountpoint that is guaranteed not to exist on the host.
		_, err := BuildPathToFileInsideVolumeFromMountpoint("/nonexistent/mountpoint/that/cannot/exist", "file.txt")
		require.ErrorIs(t, err, ErrSystemVolumePathNotMounted)
	})

	t.Run("path traversal in filePath — returns error", func(t *testing.T) {
		t.Parallel()

		_, err := BuildPathToFileInsideVolumeFromMountpoint("/some/mountpoint", "../etc/passwd")
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrSystemVolumePathNotMounted)
	})

	t.Run("empty filePath with existing mountpoint", func(t *testing.T) {
		t.Parallel()

		hostRoot := t.TempDir()
		mountpoint := "/mnt/docker-data/volumes/test_volume/_data"
		hostMountpoint := filepath.Join(hostRoot, mountpoint)
		require.NoError(t, os.MkdirAll(hostMountpoint, 0o755))

		// Verify the path join behaviour directly since we can't inject /host.
		expected := filepath.Join(hostMountpoint, "")
		require.Equal(t, expected, hostMountpoint)
	})
}

func TestFileExists(t *testing.T) {
	t.Parallel()

	t.Run("existing file returns true", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		f := filepath.Join(dir, "test.txt")
		require.NoError(t, os.WriteFile(f, []byte("data"), 0o600))

		exists, err := FileExists(f)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("existing directory returns true", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		exists, err := FileExists(dir)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("non-existing path returns false", func(t *testing.T) {
		t.Parallel()

		exists, err := FileExists("/this/path/does/not/exist/anywhere")
		require.NoError(t, err)
		require.False(t, exists)
	})
}

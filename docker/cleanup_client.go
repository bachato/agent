package docker

import (
	"context"

	dockertypes "github.com/docker/docker/api/types"
)

// CleanupClient is the subset of the Docker client API used for storage usage queries.
// *client.Client already satisfies this interface, so production code continues
// using the existing NewClient() path while tests can inject a mock.
type CleanupClient interface {
	// DiskUsage returns Docker's own accounting of disk space used by images,
	// containers, volumes, and build cache (equivalent to "docker system df").
	DiskUsage(ctx context.Context, options dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error)
	Close() error
}

// CleanupClientFactory is a function that creates a CleanupClient.
// Tests can provide a factory that returns a mock CleanupClient.
type CleanupClientFactory func() (CleanupClient, error)

// NewCleanupClient creates a new CleanupClient backed by the production Docker client.
func NewCleanupClient() (CleanupClient, error) {
	return NewClient()
}

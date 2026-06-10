package docker

import (
	"context"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
)

// CleanupClient is the subset of the Docker client API used for image garbage
// collection and storage usage queries.  *client.Client already satisfies this
// interface, so production code continues using the existing NewClient() path
// while tests can inject a mock.
type CleanupClient interface {
	// ContainerList lists containers (used to determine which images are in use).
	ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error)
	// ImageList lists images available on the daemon.
	ImageList(ctx context.Context, opts image.ListOptions) ([]image.Summary, error)
	// ImageRemove removes an image by ID.
	ImageRemove(ctx context.Context, imageID string, opts image.RemoveOptions) ([]image.DeleteResponse, error)
	// DiskUsage returns Docker's own accounting of disk space used by images,
	// containers, volumes, and build cache (equivalent to "docker system df").
	DiskUsage(ctx context.Context, options dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error)
	// BuildCachePrune removes all Docker build cache entries.
	BuildCachePrune(ctx context.Context, opts build.CachePruneOptions) (*build.CachePruneReport, error)
	Close() error
}

// CleanupClientFactory is a function that creates a CleanupClient.
// Tests can provide a factory that returns a mock CleanupClient.
type CleanupClientFactory func() (CleanupClient, error)

// NewCleanupClient creates a new CleanupClient backed by the production Docker client.
func NewCleanupClient() (CleanupClient, error) {
	return NewClient()
}

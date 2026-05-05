package host

import (
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/portainer/agent/docker/cleanup"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
	"github.com/portainer/portainer/pkg/libhttp/response"
)

// dockerStorageUsageResponse is the JSON payload returned by the docker-storage endpoint.
// All byte fields refer to the partition that hosts Docker's data directory.
type dockerStorageUsageResponse struct {
	// RootDir is the filesystem path used to measure disk capacity.
	RootDir string `json:"rootDir"`
	// TotalBytes is the total capacity of the partition.
	TotalBytes uint64 `json:"totalBytes"`
	// DockerBytes is the bytes consumed by Docker artifacts (images, container
	// layers, volumes, build cache). Non-Docker disk usage is excluded.
	DockerBytes uint64 `json:"dockerBytes"`
	// ImageBytes is the deduplicated image layer data (LayersSize from docker system df).
	ImageBytes uint64 `json:"imageBytes"`
	// ContainerBytes is the sum of writable-layer sizes across all containers.
	// Image layers are excluded to avoid double-counting with ImageBytes.
	ContainerBytes uint64 `json:"containerBytes"`
	// VolumeBytes is the sum of known volume sizes. Volumes with unknown sizes are excluded.
	VolumeBytes uint64 `json:"volumeBytes"`
	// BuildCacheBytes is the sum of all build cache entry sizes.
	BuildCacheBytes uint64 `json:"buildCacheBytes"`
	// AvailableBytes is the bytes available to unprivileged processes.
	// Non-Docker used = TotalBytes - AvailableBytes - DockerBytes.
	AvailableBytes uint64 `json:"availableBytes"`
}

func (handler *Handler) dockerStorage(rw http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	usage, err := cleanup.GetDockerStorageUsageForPath(r.Context(), handler.cleanupClientFactory, handler.diskPath)
	if err != nil {
		log.Debug().
			Err(err).
			Str("context", "HostDockerStorageHandler").
			Msg("Unable to determine Docker storage usage")
		// Return 204 rather than an error status so the caller can distinguish
		// "agent reachable but storage unavailable" from "agent unreachable".
		rw.WriteHeader(http.StatusNoContent)
		return nil
	}

	return response.JSON(rw, dockerStorageUsageResponse{
		RootDir:    usage.RootDir,
		TotalBytes: usage.TotalBytes,
		DockerBytes:     usage.DockerBytes,
		ImageBytes:      usage.ImageBytes,
		ContainerBytes:  usage.ContainerBytes,
		VolumeBytes:     usage.VolumeBytes,
		BuildCacheBytes: usage.BuildCacheBytes,
		AvailableBytes:  usage.AvailableBytes,
	})
}

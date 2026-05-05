package browse

import (
	"errors"
	"net/http"

	"github.com/portainer/agent/docker"
	"github.com/portainer/agent/filesystem"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
	"github.com/portainer/portainer/pkg/libhttp/request"
	"github.com/portainer/portainer/pkg/libhttp/response"
)

// GET request on /browse/ls?volumeID=:id&path=:path
func (handler *Handler) browseList(rw http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	volumeID, _ := request.RetrieveQueryParameter(r, "volumeID", true)
	filePath, err := request.RetrieveQueryParameter(r, "path", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: path", err)
	}

	var browsePath string
	if volumeID != "" {
		browsePath, err = resolveVolumePathFunc(volumeID, filePath)
		if err != nil {
			if errors.Is(err, filesystem.ErrSystemVolumePathNotMounted) {
				return httperror.InternalServerError("Volume path not mounted", err)
			}
			return httperror.BadRequest("Invalid volume", err)
		}
	} else {
		browsePath = filePath
	}

	files, err := filesystem.ListFilesInsideDirectory(browsePath)
	if err != nil {
		return httperror.InternalServerError("Unable to list files inside specified directory", err)
	}

	return response.JSON(rw, files)
}

// GET request on /v1/browse/:id/ls?path=:path
func (handler *Handler) browseListV1(rw http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	volumeID, err := request.RetrieveRouteVariableValue(r, "id")
	if err != nil {
		return httperror.BadRequest("Invalid volume identifier route variable", err)
	}

	filePath, err := request.RetrieveQueryParameter(r, "path", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: path", err)
	}

	browsePath, err := resolveVolumePathFunc(volumeID, filePath)
	if err != nil {
		if errors.Is(err, filesystem.ErrSystemVolumePathNotMounted) {
			return httperror.InternalServerError("Volume path not mounted", err)
		}
		return httperror.BadRequest("Invalid path", err)
	}

	files, err := filesystem.ListFilesInsideDirectory(browsePath)
	if err != nil {
		return httperror.InternalServerError("Unable to list files inside specified directory", err)
	}

	return response.JSON(rw, files)
}

// resolveVolumePath returns the host-accessible path to a file inside a volume.
// It queries Docker for the volume's Mountpoint, then resolves it via /host.
// Falls back to the legacy SystemVolumePath-based path if the Docker inspect fails.
func resolveVolumePath(volumeID, filePath string) (string, error) {
	mountpoint, err := docker.GetVolumeMountpoint(volumeID)
	if err == nil && mountpoint != "" {
		return filesystem.BuildPathToFileInsideVolumeFromMountpoint(mountpoint, filePath)
	}

	return filesystem.BuildPathToFileInsideVolume(volumeID, filePath)
}

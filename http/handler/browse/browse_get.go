package browse

import (
	"errors"
	"net/http"

	"github.com/portainer/agent/filesystem"
	"github.com/portainer/portainer/api/logs"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
	"github.com/portainer/portainer/pkg/libhttp/request"
)

// GET request on /browse/get?volumeID=:id&path=:path
func (handler *Handler) browseGet(rw http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	volumeID, _ := request.RetrieveQueryParameter(r, "volumeID", true)
	path, err := request.RetrieveQueryParameter(r, "path", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: path", err)
	}

	if volumeID != "" {
		path, err = resolveVolumePathFunc(volumeID, path)
		if err != nil {
			if errors.Is(err, filesystem.ErrSystemVolumePathNotMounted) {
				return httperror.InternalServerError("Volume path not mounted", err)
			}
			return httperror.BadRequest("Invalid volume", err)
		}
	}

	fileDetails, err := filesystem.OpenFile(path)
	if err != nil {
		return httperror.InternalServerError("Unable to open file", err)
	}
	defer logs.CloseAndLogErr(fileDetails.File)

	rw.Header().Set("Content-Disposition", "attachment; filename=\""+fileDetails.BasePath+"\"")
	http.ServeContent(rw, r, fileDetails.BasePath, fileDetails.ModTime, fileDetails.File)

	return nil
}

// GET request on /v1/browse/:id/get?path=:path
func (handler *Handler) browseGetV1(rw http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	volumeID, err := request.RetrieveRouteVariableValue(r, "id")
	if err != nil {
		return httperror.BadRequest("Invalid volume identifier route variable", err)
	}

	path, err := request.RetrieveQueryParameter(r, "path", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: path", err)
	}

	path, err = resolveVolumePathFunc(volumeID, path)
	if err != nil {
		if errors.Is(err, filesystem.ErrSystemVolumePathNotMounted) {
			return httperror.InternalServerError("Volume path not mounted", err)
		}
		return httperror.BadRequest("Invalid path", err)
	}

	fileDetails, err := filesystem.OpenFile(path)
	if err != nil {
		return httperror.InternalServerError("Unable to open file", err)
	}
	defer logs.CloseAndLogErr(fileDetails.File)

	rw.Header().Set("Content-Disposition", "attachment; filename=\""+fileDetails.BasePath+"\"")
	http.ServeContent(rw, r, fileDetails.BasePath, fileDetails.ModTime, fileDetails.File)

	return nil
}

package diagnostics

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/portainer/agent"
	"github.com/portainer/agent/edge"
	"github.com/portainer/agent/http/security"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
)

type Handler struct {
	*mux.Router
	containerPlatform agent.ContainerPlatform
	notaryService     *security.NotaryService
	edgeManager       *edge.Manager
}

// NewHandler returns a new instance of Handler
func NewHandler(containerPlatform agent.ContainerPlatform, edgeManager *edge.Manager, notaryService *security.NotaryService) *Handler {
	h := &Handler{
		Router:            mux.NewRouter(),
		containerPlatform: containerPlatform,
		edgeManager:       edgeManager,
		notaryService:     notaryService,
	}

	h.Handle("/diagnostics", notaryService.DigitalSignatureVerification(httperror.LoggerHandler(h.diagnostics))).Methods(http.MethodGet)

	return h
}

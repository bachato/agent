package host

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/portainer/agent"
	"github.com/portainer/agent/constants"
	agentdocker "github.com/portainer/agent/docker"
	"github.com/portainer/agent/http/proxy"
	"github.com/portainer/agent/http/security"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
)

// Handler represents an HTTP API Handler for host specific actions
type Handler struct {
	*mux.Router
	systemService        agent.SystemService
	cleanupClientFactory agentdocker.CleanupClientFactory
	diskPath             string
}

// NewHandler returns a new instance of Handler
func NewHandler(systemService agent.SystemService, agentProxy *proxy.AgentProxy, notaryService *security.NotaryService) *Handler {
	h := &Handler{
		Router:               mux.NewRouter(),
		systemService:        systemService,
		cleanupClientFactory: agentdocker.NewCleanupClient,
		diskPath:             constants.SystemVolumePath,
	}

	h.Handle("/host/info",
		agentProxy.Redirect(notaryService.DigitalSignatureVerification(httperror.LoggerHandler(h.hostInfo)))).Methods(http.MethodGet)

	h.Handle("/host/docker-storage",
		agentProxy.Redirect(notaryService.DigitalSignatureVerification(httperror.LoggerHandler(h.dockerStorage)))).Methods(http.MethodGet)

	return h
}

package http

import (
	"context"
	"net/http"
	"time"

	"github.com/portainer/agent/edge"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
)

// EdgeServer expose an UI to associate an Edge key with the agent.
type EdgeServer struct {
	httpServer  *http.Server
	edgeManager *edge.Manager
}

// NewEdgeServer returns a pointer to a new instance of EdgeServer.
func NewEdgeServer(edgeManager *edge.Manager) *EdgeServer {
	return &EdgeServer{edgeManager: edgeManager}
}

// Start starts a new web server by listening on the specified addr and port.
func (server *EdgeServer) Start(addr, port string) error {
	router := mux.NewRouter()
	router.HandleFunc("/init", server.handleKeySetup()).Methods(http.MethodPost)
	router.PathPrefix("/").Handler(http.FileServer(http.Dir("./static")))

	listenAddr := addr + ":" + port
	server.httpServer = &http.Server{Addr: listenAddr, Handler: router}

	if err := server.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}

	return nil
}

func (server *EdgeServer) handleKeySetup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Unable to parse form", http.StatusInternalServerError)
			return
		}

		key := r.Form.Get("key")
		if key == "" {
			http.Error(w, "Missing key parameter", http.StatusBadRequest)
			return
		}

		if err := server.edgeManager.SetKey(key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		if err := server.edgeManager.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		go server.propagateKeyInCluster()

		if _, err := w.Write([]byte("Agent setup OK. You can close this page.")); err != nil {
			log.Warn().Err(err).Msg("failed to write response")
		}

		if err := server.Shutdown(); err != nil {
			log.Warn().Err(err).Msg("failed to shutdown server")
		}
	}
}

func (server *EdgeServer) propagateKeyInCluster() {
	if err := server.edgeManager.PropagateKeyInCluster(); err != nil {
		log.Error().Err(err).Msg("unable to propagate key to cluster")
	}
}

// Shutdown is used to shutdown the server.
func (server *EdgeServer) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	server.httpServer.SetKeepAlivesEnabled(false)

	return server.httpServer.Shutdown(ctx)
}

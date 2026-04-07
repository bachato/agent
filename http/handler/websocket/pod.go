package websocket

import (
	"io"
	"net/http"
	"strings"

	"github.com/portainer/agent"
	"github.com/portainer/agent/kubernetes"
	kubecli "github.com/portainer/portainer/api/kubernetes/cli"
	"github.com/portainer/portainer/api/logs"
	"github.com/portainer/portainer/api/ws"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
	"github.com/portainer/portainer/pkg/libhttp/request"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

func (handler *Handler) websocketPodExec(w http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	namespace, err := request.RetrieveQueryParameter(r, "namespace", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: namespace", err)
	}

	podName, err := request.RetrieveQueryParameter(r, "podName", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: podName", err)
	}

	containerName, err := request.RetrieveQueryParameter(r, "containerName", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: containerName", err)
	}

	command, err := request.RetrieveQueryParameter(r, "command", false)
	if err != nil {
		return httperror.BadRequest("Invalid query parameter: command", err)
	}

	token := r.Header.Get(agent.HTTPKubernetesSATokenHeaderName)

	commandArray := strings.Split(command, " ")

	websocketConn, err := handler.connectionUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return httperror.InternalServerError("Unable to upgrade the connection", err)
	}
	defer logs.CloseAndLogErr(websocketConn)

	stdinReader, stdinWriter := io.Pipe()
	defer logs.CloseAndLogErr(stdinWriter)
	stdoutReader, stdoutWriter := io.Pipe()
	defer logs.CloseAndLogErr(stdoutWriter)

	errorChan := make(chan error, 2)

	sizeQueue := kubecli.NewTerminalSizeQueue()
	defer sizeQueue.Close()
	go ws.StreamFromWebsocketToWriter(websocketConn, stdinWriter, errorChan, ws.ResizeHandler(sizeQueue))
	go ws.StreamFromReaderToWebsocket(websocketConn, stdoutReader, errorChan)

	err = handler.kubeClient.StartExecProcess(kubernetes.ExecProcessParams{
		Token:         token,
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: containerName,
		Command:       commandArray,
		Stdin:         stdinReader,
		Stdout:        stdoutWriter,
		ResizeQueue:   sizeQueue,
	})
	if err != nil {
		return httperror.InternalServerError("Unable to start exec process inside container", err)
	}

	err = <-errorChan
	if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
		log.Error().Err(err).Msg("websocket error")
	}

	return nil
}


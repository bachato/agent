package stack

import (
	"fmt"
	"time"

	"github.com/portainer/portainer/api/edge"
)

type (
	edgeStackID     int
	edgeStackStatus int
	edgeStackAction int

	edgeStack struct {
		edge.StackPayload

		FileFolder string
		FileName   string
		Status     edgeStackStatus
		Action     edgeStackAction

		PullCount    int
		PullFinished bool
		DeployCount  int

		FirstAction      time.Time
		LastAction       time.Time
		EdgeUpdateFailed bool
	}
)

const (
	_ edgeStackStatus = iota
	StatusPending
	StatusDeployed
	StatusError
	StatusDeploying
	StatusRetry
	StatusRemoving
	StatusAwaitingDeployedStatus
	StatusAwaitingRemovedStatus
	StatusCompleted
	StatusAwaitingCleanup
)

var edgeStackStatusStr = map[edgeStackStatus]string{
	StatusPending:                "Pending",
	StatusDeployed:               "Deployed",
	StatusError:                  "Error",
	StatusDeploying:              "Deploying",
	StatusRetry:                  "Retry",
	StatusRemoving:               "Removing",
	StatusAwaitingDeployedStatus: "AwaitingDeployedStatus",
	StatusAwaitingRemovedStatus:  "AwaitingRemovedStatus",
	StatusCompleted:              "Completed",
	StatusAwaitingCleanup:        "AwaitingCleanup",
}

func (s edgeStackStatus) String() string {
	if str, ok := edgeStackStatusStr[s]; ok {
		return fmt.Sprintf("%d (%s)", s, str)
	}
	return fmt.Sprintf("%d (UNKNOWN)", s)
}

const (
	_ edgeStackAction = iota
	actionDeploy
	actionUpdate
	actionDelete
	actionIdle
)

var edgeStackActionStr = map[edgeStackAction]string{
	actionDeploy: "Deploy",
	actionUpdate: "Update",
	actionDelete: "Delete",
	actionIdle:   "Idle",
}

func (a edgeStackAction) String() string {
	if str, ok := edgeStackActionStr[a]; ok {
		return fmt.Sprintf("%d (%s)", a, str)
	}
	return fmt.Sprintf("%d (UNKNOWN)", a)
}

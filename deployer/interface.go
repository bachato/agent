package deployer

import (
	"context"

	"github.com/portainer/agent"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/pkg/libstack"
)

type (
	Deployer interface {
		Deploy(ctx context.Context, name string, filePaths []string, options DeployOptions) error
		Remove(ctx context.Context, name string, filePaths []string, options RemoveOptions) error
		Pull(ctx context.Context, name string, filePaths []string, options PullOptions) error
		Validate(ctx context.Context, name string, filePaths []string, options ValidateOptions) error
		// WaitForStatus waits until status is reached or an error occurred
		// if the received value is an empty string it means the status was
		WaitForStatus(ctx context.Context, name string, status libstack.Status, options CheckStatusOptions) <-chan libstack.WaitResult
		GetEdgeStacks(ctx context.Context) ([]agent.EdgeStack, error)
	}

	DeployerBaseOptions struct {
		// Namespace to use for kubernetes stack. Keep empty to use the manifest namespace.
		Namespace  string
		WorkingDir string
		Env        []string
		Registries []edge.RegistryCredentials
	}

	DeployOptions struct {
		DeployerBaseOptions
		Prune       bool
		EdgeStackID portainer.EdgeStackID
	}

	RemoveOptions struct {
		DeployerBaseOptions
	}

	ValidateOptions struct {
		DeployerBaseOptions
	}

	PullOptions struct {
		DeployerBaseOptions
	}

	CheckStatusOptions struct {
		DeployerBaseOptions
		StackFileLocation string
	}
)

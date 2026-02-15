package stack

import (
	"strings"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"
	"github.com/rs/zerolog/log"
)

// addHelmConfigToStack adds Helm-specific configuration as environment variables to the stack.
// This includes the Helm chart path, values files, atomic flag, and timeout settings.
func addHelmConfigToStack(stack *edgeStack, stackPayload *edge.StackPayload) {
	if !IsHelmDeploymentStack(stack) {
		return
	}

	log.Debug().
		Str("helm_chart_path", stackPayload.HelmConfig.ChartPath).
		Int("helm_values_files_count", len(stackPayload.HelmConfig.ValuesFiles)).
		Bool("helm_atomic", stackPayload.HelmConfig.Atomic).
		Str("helm_timeout", stackPayload.HelmConfig.Timeout).
		Msg("processing Helm chart stack")

	// Add Helm configuration as environment variables for the deployer
	helmEnvVars := []portainer.Pair{
		{Name: "HELM_CHART_PATH", Value: stackPayload.HelmConfig.ChartPath},
	}

	// Add values files as a comma-separated list if present
	if len(stackPayload.HelmConfig.ValuesFiles) > 0 {
		valuesFiles := strings.Join(stackPayload.HelmConfig.ValuesFiles, ",")
		helmEnvVars = append(helmEnvVars, portainer.Pair{
			Name:  "HELM_VALUES_FILES",
			Value: valuesFiles,
		})
	}

	// Add atomic flag to enable automatic rollback on failure
	if stackPayload.HelmConfig.Atomic {
		helmEnvVars = append(helmEnvVars, portainer.Pair{
			Name:  "HELM_ATOMIC",
			Value: "true",
		})
	}

	// Add timeout for Helm operations if specified
	if stackPayload.HelmConfig.Timeout != "" {
		helmEnvVars = append(helmEnvVars, portainer.Pair{
			Name:  "HELM_TIMEOUT",
			Value: stackPayload.HelmConfig.Timeout,
		})
	}

	stack.EnvVars = append(stack.EnvVars, helmEnvVars...)
}

package stack

import (
	"strings"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"
	"github.com/rs/zerolog/log"
)

// addHelmConfigToStack adds Helm-specific configuration as environment variables to the stack.
// This includes the Helm chart path or repo URL, values, atomic flag, and timeout settings.
func addHelmConfigToStack(stack *edgeStack, stackPayload *edge.StackPayload) {
	if !IsHelmStack(stack) {
		return
	}

	log.Debug().
		Str("helm_chart_path", stackPayload.HelmConfig.ChartPath).
		Str("helm_chart_url", stackPayload.HelmConfig.ChartURL).
		Str("helm_chart_name", stackPayload.HelmConfig.ChartName).
		Str("helm_chart_version", stackPayload.HelmConfig.ChartVersion).
		Int("helm_values_files_count", len(stackPayload.HelmConfig.ValuesFiles)).
		Bool("helm_atomic", stackPayload.HelmConfig.Atomic).
		Str("helm_timeout", stackPayload.HelmConfig.Timeout).
		Msg("processing Helm chart stack")

	var helmEnvVars []portainer.Pair

	if IsGitRepoHelmStack(stack) {
		// Git-based deployment: chart is at a local path after git clone
		helmEnvVars = append(helmEnvVars, portainer.Pair{
			Name:  "HELM_CHART_PATH",
			Value: stackPayload.HelmConfig.ChartPath,
		})

		// Add values files as a pipe-separated list if present
		if len(stackPayload.HelmConfig.ValuesFiles) > 0 {
			valuesFiles := strings.Join(stackPayload.HelmConfig.ValuesFiles, "|")
			helmEnvVars = append(helmEnvVars, portainer.Pair{
				Name:  "HELM_VALUES_FILES",
				Value: valuesFiles,
			})
		}
	} else if IsHelmRepoStack(stack) {
		// Repository-based deployment: fetch chart from a Helm repository
		helmEnvVars = append(helmEnvVars,
			portainer.Pair{Name: "HELM_REPO_URL", Value: stackPayload.HelmConfig.ChartURL},
			portainer.Pair{Name: "HELM_CHART_NAME", Value: stackPayload.HelmConfig.ChartName},
		)

		if stackPayload.HelmConfig.ChartVersion != "" {
			helmEnvVars = append(helmEnvVars, portainer.Pair{
				Name:  "HELM_CHART_VERSION",
				Value: stackPayload.HelmConfig.ChartVersion,
			})
		}

		if stackPayload.HelmConfig.ValuesInline != "" {
			helmEnvVars = append(helmEnvVars, portainer.Pair{
				Name:  "HELM_VALUES_INLINE",
				Value: stackPayload.HelmConfig.ValuesInline,
			})
		}
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

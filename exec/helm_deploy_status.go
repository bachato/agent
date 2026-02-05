package exec

import (
	"context"
	"fmt"

	"github.com/portainer/agent/deployer"
	"github.com/portainer/portainer/pkg/libhelm/options"
	"github.com/portainer/portainer/pkg/libhelm/release"
	libstack "github.com/portainer/portainer/pkg/libstack"
	"github.com/rs/zerolog/log"
)

// WaitForStatus checks the status of a Helm release
func (d *HelmDeployer) WaitForStatus(ctx context.Context, name string, requiredStatus libstack.Status, checkOpts deployer.CheckStatusOptions) libstack.WaitResult {
	log.Debug().
		Str("context", "HelmDeployer").
		Str("release_name", name).
		Str("required_status", string(requiredStatus)).
		Str("namespace", checkOpts.Namespace).
		Msg("checking Helm release status")

	releaseName := convertStackNameToReleaseName(name)

	// Get release status from Helm
	getOpts := options.GetOptions{
		Name:                    releaseName,
		Namespace:               checkOpts.Namespace,
		KubernetesClusterAccess: d.getKubeAccess(),
	}

	release, err := d.helmManager.Get(getOpts)
	if err != nil {
		// Release not found means it's been removed
		if requiredStatus == libstack.StatusRemoved {
			log.Debug().
				Str("context", "HelmDeployer").
				Str("release_name", name).
				Msg("Helm release not found, confirming removal")
			return libstack.WaitResult{Status: libstack.StatusRemoved}
		}

		log.Error().
			Str("context", "HelmDeployer").
			Str("release_name", name).
			Err(err).
			Msg("failed to get Helm release status")

		return libstack.WaitResult{
			Status:   libstack.StatusError,
			ErrorMsg: fmt.Sprintf("failed to get release status: %v", err),
		}
	}

	log.Debug().
		Str("context", "HelmDeployer").
		Str("release_name", release.Name).
		Int("version", release.Version).
		Str("helm_status", string(release.Info.Status)).
		Msg("Helm release found")

	// Map Helm release status to libstack status
	status := d.mapHelmStatusToLibstack(release.Info.Status)

	log.Debug().
		Str("context", "HelmDeployer").
		Str("release_name", name).
		Str("helm_status", string(release.Info.Status)).
		Str("mapped_status", string(status)).
		Msg("mapped Helm status to libstack status")

	// If the Helm status indicates an error, include the description
	if status == libstack.StatusError && release.Info.Description != "" {
		return libstack.WaitResult{
			Status:   status,
			ErrorMsg: "Helm release failed: " + release.Info.Description,
		}
	}

	return libstack.WaitResult{Status: status}
}

// mapHelmStatusToLibstack maps Helm release status to libstack status
// Helm status values from helm.sh/helm/v3/pkg/release:
// - deployed: successfully deployed
// - failed: deployment/upgrade failed
// - pending-install: installation in progress
// - pending-upgrade: upgrade in progress
// - pending-rollback: rollback in progress
// - uninstalling: uninstall in progress
// - uninstalled: successfully uninstalled
// - superseded: replaced by a newer release
func (d *HelmDeployer) mapHelmStatusToLibstack(helmStatus release.Status) libstack.Status {
	// Helm status constants from helm.sh/helm/v3/pkg/release
	// Convert to string for comparison
	statusStr := string(helmStatus)

	switch statusStr {
	case "deployed":
		// Successfully deployed
		return libstack.StatusRunning
	case "failed":
		// Deployment/upgrade failed
		return libstack.StatusError
	case "pending-install", "pending-upgrade", "pending-rollback":
		// Deployment/upgrade/rollback in progress
		return libstack.StatusStarting
	case "uninstalling":
		// Uninstall in progress
		return libstack.StatusRemoving
	case "uninstalled":
		// Successfully uninstalled
		return libstack.StatusRemoved
	case "superseded":
		// Replaced by newer release - treat as stopped
		return libstack.StatusStopped
	default:
		// Unknown status
		log.Warn().
			Str("context", "HelmDeployer").
			Str("helm_status", statusStr).
			Msg("unknown Helm status, treating as unknown")
		return libstack.StatusUnknown
	}
}

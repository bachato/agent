package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/portainer/agent/deployer"
	"github.com/portainer/agent/kubernetes"
	"github.com/portainer/portainer/pkg/libhelm/options"
	"github.com/portainer/portainer/pkg/libhelm/sdk"
	"github.com/rs/zerolog/log"
	"k8s.io/client-go/rest"
)

// HelmDeployer implements the Deployer interface for Helm chart deployments
type HelmDeployer struct {
	helmManager *sdk.HelmSDKPackageManager
	kubeClient  *kubernetes.KubeClient
}

// NewHelmDeployer creates a new HelmDeployer instance
func NewHelmDeployer(kubeClient *kubernetes.KubeClient) *HelmDeployer {
	return &HelmDeployer{
		helmManager: sdk.NewHelmSDKPackageManager(),
		kubeClient:  kubeClient,
	}
}

// Deploy deploys a Helm chart using the Helm SDK
func (d *HelmDeployer) Deploy(ctx context.Context, name string, filePaths []string, deployOpts deployer.DeployOptions) error {
	log.Debug().
		Str("context", "HelmDeployer").
		Str("release_name", name).
		Str("namespace", deployOpts.Namespace).
		Str("working_dir", deployOpts.WorkingDir).
		Msg("deploying Helm chart")

	// Parse Helm configuration from deployOpts
	helmConfig, err := d.parseHelmConfig(deployOpts)
	if err != nil {
		return fmt.Errorf("failed to parse Helm configuration: %w", err)
	}

	// Validate required fields
	if helmConfig.ChartPath == "" {
		return errors.New("helm chart path is required")
	}

	releaseName := convertStackNameToReleaseName(name)

	// Resolve the absolute chart path
	absoluteChartPath, err := d.resolveChartPath(helmConfig.ChartPath, deployOpts.WorkingDir)
	if err != nil {
		return fmt.Errorf("failed to resolve chart path: %w", err)
	}

	log.Debug().
		Str("context", "HelmDeployer").
		Str("relative_chart_path", helmConfig.ChartPath).
		Str("absolute_chart_path", absoluteChartPath).
		Str("working_dir", deployOpts.WorkingDir).
		Msg("resolved chart path")

	// Resolve absolute paths for values files
	absoluteValuesFiles := make([]string, 0, len(helmConfig.ValuesFiles))
	for _, valuesFile := range helmConfig.ValuesFiles {
		absoluteValuesPath, err := d.resolveChartPath(valuesFile, deployOpts.WorkingDir)
		if err != nil {
			return fmt.Errorf("failed to resolve values file path %s: %w", valuesFile, err)
		}
		absoluteValuesFiles = append(absoluteValuesFiles, absoluteValuesPath)
	}

	// Merge values from all values files
	var mergedValues map[string]any
	if len(absoluteValuesFiles) > 0 {
		for _, valuesFile := range absoluteValuesFiles {
			values, err := sdk.GetHelmValuesFromFile(valuesFile)
			if err != nil {
				return fmt.Errorf("failed to read values file %s: %w", valuesFile, err)
			}
			mergedValues = sdk.MergeValues(mergedValues, values)
		}
	}

	// Parse timeout duration from config or use default
	timeout := 300 * time.Second // Default timeout: 5 minutes
	if helmConfig.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(helmConfig.Timeout)
		if err != nil {
			log.Warn().
				Str("context", "HelmDeployer").
				Str("timeout_value", helmConfig.Timeout).
				Err(err).
				Msg("failed to parse timeout, using default")
		} else {
			timeout = parsedTimeout
		}
	}

	// Prepare install options for Helm SDK
	installOpts := options.InstallOptions{
		Name:                    releaseName,
		Namespace:               deployOpts.Namespace,
		Chart:                   absoluteChartPath,
		Values:                  mergedValues,
		Wait:                    true,
		Timeout:                 timeout,
		CreateNamespace:         true,
		Atomic:                  helmConfig.Atomic,
		KubernetesClusterAccess: d.getKubeAccess(),
		HelmAppLabels:           deployOpts.HelmAppLabels,
	}

	// Use helm upgrade --install pattern (idempotent)
	release, err := d.helmManager.Upgrade(installOpts)
	if err != nil {
		return fmt.Errorf("failed to deploy Helm chart: %w", err)
	}

	log.Info().
		Str("context", "HelmDeployer").
		Str("release_name", release.Name).
		Str("release_namespace", release.Namespace).
		Str("requested_namespace", deployOpts.Namespace).
		Str("chart", fmt.Sprintf("%s-%s", release.Chart.Metadata.Name, release.Chart.Metadata.Version)).
		Msg("Helm chart deployed successfully")

	// Log a warning if the release namespace differs from requested namespace
	if release.Namespace != deployOpts.Namespace {
		log.Warn().
			Str("context", "HelmDeployer").
			Str("requested_namespace", deployOpts.Namespace).
			Str("actual_release_namespace", release.Namespace).
			Msg("Helm release namespace differs from requested namespace - check chart templates")
	}

	return nil
}

// Remove removes a Helm release
func (d *HelmDeployer) Remove(ctx context.Context, name string, filePaths []string, removeOpts deployer.RemoveOptions) error {
	log.Debug().
		Str("context", "HelmDeployer").
		Str("release_name", name).
		Str("namespace", removeOpts.Namespace).
		Msg("uninstalling Helm release")

	releaseName := convertStackNameToReleaseName(name)

	uninstallOpts := options.UninstallOptions{
		Name:                    releaseName,
		Namespace:               removeOpts.Namespace,
		KubernetesClusterAccess: d.getKubeAccess(),
	}

	err := d.helmManager.Uninstall(uninstallOpts)
	if err != nil {
		return fmt.Errorf("failed to uninstall Helm release: %w", err)
	}

	log.Info().
		Str("context", "HelmDeployer").
		Str("release_name", name).
		Str("namespace", removeOpts.Namespace).
		Msg("Helm release uninstalled successfully")

	return nil
}

// Validate validates a Helm chart (currently a no-op, validation happens during deploy)
func (d *HelmDeployer) Validate(ctx context.Context, name string, filePaths []string, validateOpts deployer.ValidateOptions) error {
	log.Debug().
		Str("context", "HelmDeployer").
		Str("release_name", name).
		Msg("validating Helm chart")

	// Helm chart validation happens during the upgrade/install with dry-run
	// For now, we'll just ensure the configuration is parseable
	helmConfig, err := d.parseHelmConfigFromBase(validateOpts.DeployerBaseOptions)
	if err != nil {
		return fmt.Errorf("failed to parse Helm configuration: %w", err)
	}

	if helmConfig.ChartPath == "" {
		return errors.New("helm chart path is required")
	}

	log.Debug().
		Str("context", "HelmDeployer").
		Str("chart_path", helmConfig.ChartPath).
		Int("values_files_count", len(helmConfig.ValuesFiles)).
		Msg("Helm chart configuration validated")

	return nil
}

// Pull is a no-op for Helm as charts are pulled automatically during install/upgrade
func (d *HelmDeployer) Pull(ctx context.Context, name string, filePaths []string, pullOpts deployer.PullOptions) error {
	log.Debug().
		Str("context", "HelmDeployer").
		Str("release_name", name).
		Msg("Helm charts are pulled automatically during deployment")

	return nil
}

// getKubeAccess returns the Kubernetes cluster access configuration
func (d *HelmDeployer) getKubeAccess() *options.KubernetesClusterAccess {
	// Build explicit KubernetesClusterAccess to prevent Helm from using cli.New()
	// which would pick up the service account's namespace instead of respecting
	// the namespace parameter passed to Helm operations.

	// Check if running with DEV_KUBECONFIG_PATH (local development)
	devKubeConfigPath := os.Getenv("DEV_KUBECONFIG_PATH")
	if devKubeConfigPath != "" {
		// For local development, return empty struct to use kubeconfig from environment
		return &options.KubernetesClusterAccess{}
	}

	// Get in-cluster config to extract cluster details
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Warn().
			Str("context", "HelmDeployer").
			Err(err).
			Msg("failed to get in-cluster config, falling back to nil (may cause namespace issues)")
		return nil
	}

	// Build KubernetesClusterAccess with in-cluster configuration
	// This ensures Helm respects the namespace parameter instead of using the service account's namespace
	return &options.KubernetesClusterAccess{
		ClusterName:      "in-cluster",
		ContextName:      "in-cluster-context",
		UserName:         "in-cluster-user",
		ClusterServerURL: config.Host,
		AuthToken:        config.BearerToken,
	}
}

// helmConfig holds parsed Helm configuration
type helmConfig struct {
	ChartPath   string
	ValuesFiles []string
	Atomic      bool
	Timeout     string
}

// parseHelmConfig extracts Helm configuration from DeployOptions
func (d *HelmDeployer) parseHelmConfig(options deployer.DeployOptions) (*helmConfig, error) {
	return d.parseHelmConfigFromBase(options.DeployerBaseOptions)
}

// parseHelmConfigFromBase extracts Helm configuration from environment variables
func (d *HelmDeployer) parseHelmConfigFromBase(baseOpts deployer.DeployerBaseOptions) (*helmConfig, error) {
	config := &helmConfig{
		ValuesFiles: []string{},
	}

	// Parse from environment variables (set by stack manager)
	for _, envVar := range baseOpts.Env {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		switch key {
		case "HELM_CHART_PATH":
			config.ChartPath = value
		case "HELM_VALUES_FILES":
			// Values files are passed as comma-separated list
			if value != "" {
				config.ValuesFiles = strings.Split(value, ",")
			}
		case "HELM_ATOMIC":
			// Parse atomic flag (enables automatic rollback on failure)
			config.Atomic = value == "true" || value == "1"
		case "HELM_TIMEOUT":
			// Parse timeout for Helm operations (e.g., "5m0s", "10m", "1h30m")
			config.Timeout = value
		}
	}

	log.Debug().
		Str("context", "HelmDeployer").
		Str("chart_path", config.ChartPath).
		Int("values_files_count", len(config.ValuesFiles)).
		Bool("atomic", config.Atomic).
		Str("timeout", config.Timeout).
		Msg("parsed Helm configuration")

	return config, nil
}

// resolveChartPath resolves the absolute path for a chart or values file
// relativePath is the path from the git repository (e.g., "charts/hello-world")
// workingDir is the stack's file folder where files were written (e.g., "/opt/portainer/edge_stacks/1")
func (d *HelmDeployer) resolveChartPath(relativePath, workingDir string) (string, error) {
	// If the path is already absolute, return it as-is
	if strings.HasPrefix(relativePath, "/") {
		return relativePath, nil
	}

	// Construct absolute path by joining workingDir with relativePath
	absolutePath := relativePath
	if workingDir != "" {
		absolutePath = fmt.Sprintf("%s/%s", strings.TrimSuffix(workingDir, "/"), relativePath)
	}

	// Verify the path exists
	if _, err := os.Stat(absolutePath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("chart path does not exist: %s", absolutePath)
		}
		return "", fmt.Errorf("failed to check chart path: %w", err)
	}

	return absolutePath, nil
}

func convertStackNameToReleaseName(stackName string) string {
	// Use stack name as release name (edge stacks use edge_<stackname> pattern,
	// but Helm naming rule doesn't allow underscores, so we convert to edge-<stackname>)
	if strings.HasPrefix(stackName, "edge_") {
		_, name, ok := strings.Cut(stackName, "edge_")
		if ok {
			return "edge-" + name
		}
	}
	return stackName
}

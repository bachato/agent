package stack

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/portainer/portainer/api/filesystem"
	"github.com/portainer/portainer/api/kubernetes"
	"github.com/rs/zerolog/log"
)

// applyK8sLabelsToManifest applies Kubernetes labels to the manifest file in dirEntries.
// It modifies the dirEntries in-place by updating the manifest file content with labels.
// This function should only be called for Kubernetes edge stacks.
func applyK8sLabelsToManifest(
	dirEntries []filesystem.DirEntry,
	entryFileName string,
	appLabels kubernetes.KubeAppLabels,
) error {
	// Find the entry file in dirEntries
	for i, dirEntry := range dirEntries {
		if !dirEntry.IsFile || dirEntry.Name != entryFileName {
			continue
		}

		// Only process YAML/YML files
		ext := strings.ToLower(filepath.Ext(dirEntry.Name))
		if ext != ".yaml" && ext != ".yml" {
			log.Debug().
				Str("context", "K8sEdgeStack").
				Str("file_name", dirEntry.Name).
				Str("ext", ext).
				Msg("Skipping non-YAML file for label application")
			continue
		}

		log.Debug().
			Str("context", "K8sEdgeStack").
			Int("stack_id", appLabels.StackID).
			Str("stack_name", appLabels.StackName).
			Str("file_name", dirEntry.Name).
			Msg("Applying Kubernetes labels to manifest")

		// Add labels to the manifest
		labeledContent, err := kubernetes.AddAppLabels([]byte(dirEntry.Content), appLabels.ToMap())
		if err != nil {
			return fmt.Errorf("failed to add Kubernetes labels: %w", err)
		}

		// Update the dirEntry content in-place
		dirEntries[i].Content = string(labeledContent)

		log.Debug().
			Str("context", "K8sEdgeStack").
			Int("stack_id", appLabels.StackID).
			Str("file_name", dirEntry.Name).
			Str("owner", appLabels.Owner).
			Str("owner_id", appLabels.OwnerId).
			Msg("Successfully added Kubernetes labels to manifest")
	}

	return nil
}

// buildK8sAppLabels returns the Portainer app label map for the given edge stack.
func buildK8sAppLabels(stack *edgeStack) kubernetes.KubeAppLabels {
	return kubernetes.KubeAppLabels{
		StackID:   stack.ID,
		StackName: stack.Name,
		Owner:     stack.CreatedBy,
		OwnerId:   stack.CreatedByUserId,
		StackKind: "edge",
	}
}

// shouldApplyK8sLabels returns true if K8s labels should be applied to this stack
func (manager *StackManager) shouldApplyK8sLabels(stack *edgeStack) bool {
	return manager.engineType == EngineTypeKubernetes && !IsHelmStack(stack)
}

// applyK8sLabelsIfNeeded applies Kubernetes labels to the manifest if this is a Kubernetes stack. It modifies the dirEntries in-place by updating the manifest file content with labels. This doesn't apply to Helm stack.
// It checks the deployment type and only applies labels for Kubernetes edge stacks.
func (manager *StackManager) applyK8sLabelsIfNeeded(stack *edgeStack, dirEntries []filesystem.DirEntry) error {
	// Only apply labels for Kubernetes stacks
	if !manager.shouldApplyK8sLabels(stack) {
		return nil
	}

	log.Debug().Str("stack_file_name", stack.FileName).
		Str("stack_folder_name", stack.FileFolder).Msg("applying k8s labels to edge stack manifest")

	// Create the label map
	labels := buildK8sAppLabels(stack)

	// Deployment type check is done via engineType in StackManager
	// which is set based on portainer.EdgeStackDeploymentKubernetes
	return applyK8sLabelsToManifest(
		dirEntries,
		stack.FileName,
		labels,
	)
}

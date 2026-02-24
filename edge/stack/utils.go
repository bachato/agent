package stack

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/portainer/agent"
	"github.com/portainer/portainer/api/filesystem"
)

// successFolderSuffix is suffix for the path where the last successfully deployed edge stack files are saved
const successFolderSuffix = ".success"

// IsRelativePathStack checks if the edge stack enables relative path or not
func IsRelativePathStack(stack *edgeStack) bool {
	return stack.SupportRelativePath && stack.FilesystemPath != ""
}

func SuccessStackFileFolder(fileFolder string) string {
	return fmt.Sprintf("%s%s", fileFolder, successFolderSuffix)
}

func backupSuccessStack(stack *edgeStack) error {
	src := stack.FileFolder
	dst := SuccessStackFileFolder(src)

	return filesystem.CopyDir(src, dst, false)
}

func getStackFileFolder(stack *edgeStack) string {
	stackIDStr := strconv.Itoa(stack.ID)

	if IsHelmDeploymentStack(stack) {
		return filepath.Join(agent.EdgeStackFilesPath, stackIDStr)
	}

	folder := filepath.Join(agent.EdgeStackFilesPath, stackIDStr)
	if stack.EdgeUpdateID != 0 {
		folder = filepath.Join(agent.UpdateEdgeStackFilesPath, stackIDStr)
	} else if IsRelativePathStack(stack) {
		folder = filepath.Join(stack.FilesystemPath, agent.ComposePathPrefix, stackIDStr)
	}

	return folder
}

func IsHelmDeploymentStack(stack *edgeStack) bool {
	return stack.HelmConfig.ChartPath != ""
}

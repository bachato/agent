package health

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/pkg/errors"
)

const (
	portainerHealthyFile = ".portainer-healthy"
)

var (
	portainerHealthyFilePath = filepath.Join(os.TempDir(), portainerHealthyFile)
	mu                       sync.Mutex
)

func Healthy() bool {
	mu.Lock()
	defer mu.Unlock()

	return healthy()
}

func healthy() bool {
	inf, err := os.Stat(portainerHealthyFilePath)
	if err != nil {
		return false
	}

	return inf.Mode().IsRegular()
}

func SetHealthy() error {
	mu.Lock()
	defer mu.Unlock()

	if healthy() {
		return nil
	}

	file, err := os.Create(portainerHealthyFilePath)
	if err != nil {
		return errors.Wrap(err, "failed to create healthy file")
	}
	_ = file.Close()

	return nil
}

func SetUnHealthy() error {
	mu.Lock()
	defer mu.Unlock()

	if !healthy() {
		return nil
	}

	if err := os.Remove(portainerHealthyFilePath); err != nil {
		return errors.Wrap(err, "failed to remove healthy file")
	}

	return nil
}

package evaluator

import (
	"os"
	"path/filepath"
	"testing"

	portainer "github.com/portainer/portainer/api"
	alertmanagermodels "github.com/prometheus/alertmanager/api/v2/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noopAlertPoster struct{}

func (noopAlertPoster) PostAlerts(portainer.EndpointID, alertmanagermodels.PostableAlerts) error {
	return nil
}

func TestNewPreservesExistingTSDBDataDir(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "tsdb")
	require.NoError(t, os.MkdirAll(dataDir, 0o750))
	markerFile := filepath.Join(dataDir, "marker-file")
	require.NoError(t, os.WriteFile(markerFile, []byte("keep"), 0o600))

	svc, err := New(Config{
		DataDir:    dataDir,
		EndpointID: portainer.EndpointID(1),
		Poster:     noopAlertPoster{},
	})
	require.NoError(t, err)
	t.Cleanup(svc.Stop)

	// The marker file should still exist — ensureTSDBDataDir does not wipe.
	_, err = os.Stat(markerFile)
	require.NoError(t, err)

	entries, err := os.ReadDir(dataDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)
}


package cleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/portainer/agent/constants"
	agentdocker "github.com/portainer/agent/docker"
	"github.com/portainer/agent/docker/cleanup/mocks"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// mockFactory wraps a MockCleanupClient into a CleanupClientFactory.
func mockFactory(m *mocks.MockCleanupClient) agentdocker.CleanupClientFactory {
	return func() (agentdocker.CleanupClient, error) { return m, nil }
}

// minimalCfg returns a Config with very short intervals suitable for unit tests.
func minimalCfg() Config {
	return Config{
		CleanupInterval:                  Duration(1 * time.Millisecond),
		CleanOldImages:                   true,
		ImageMaximumAgeForGC:             Duration(30 * 24 * time.Hour),
		CleanImagesAtStorageLimit:        true,
		CleanupStartHostThresholdPercent: 80.0,
		CleanupEndHostThresholdPercent:   70.0,
		MaximumImagesPerInterval:         10,
		MinAge:                           Duration(24 * time.Hour),
	}
}

// prepSvc creates a CleanupService wired with injectable mocks.
func prepSvc(cfg Config, client *mocks.MockCleanupClient, diskPct float64) *CleanupService {
	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(client)
	svc.statfs = func(string) (float64, error) { return diskPct, nil }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }
	return svc
}

// ── Old-image cleanup ───────────────────────────────────────────────────────

func TestRunCycle_OldImageCleanupRemovesOldImages(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	oldImg := image.Summary{ID: "sha256:old", Created: now.Add(-40 * 24 * time.Hour).Unix()}
	newImg := image.Summary{ID: "sha256:new", Created: now.Add(-1 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return([]image.Summary{oldImg, newImg}, nil)
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:old", image.RemoveOptions{Force: true, PruneChildren: true}).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	svc := prepSvc(minimalCfg(), m, 50.0 /* below 80% – storage-limit cleanup skipped */)
	svc.runCycle(context.Background())

	snap := svc.Snapshot()
	assert.Equal(t, 1, snap.OldImagesRemoved)
	assert.Equal(t, 0, snap.SpaceImagesRemoved)
}

func TestRunCycle_OldImageCleanupSkipsInUseImages(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	oldImg := image.Summary{ID: "sha256:old", Created: now.Add(-40 * 24 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).
		Return([]container.Summary{{ImageID: "sha256:old"}}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return([]image.Summary{oldImg}, nil)
	// No ImageRemove expected — image is in use by a container
	m.EXPECT().Close().AnyTimes()

	svc := prepSvc(minimalCfg(), m, 50.0)
	svc.runCycle(context.Background())

	snap := svc.Snapshot()
	assert.Equal(t, 0, snap.OldImagesRemoved)
}

func TestRunCycle_OldImageCleanupErrorContinues(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	oldA := image.Summary{ID: "sha256:a", Created: now.Add(-40 * 24 * time.Hour).Unix()}
	oldB := image.Summary{ID: "sha256:b", Created: now.Add(-41 * 24 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return([]image.Summary{oldA, oldB}, nil)
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:a", gomock.Any()).Return(nil, errors.New("remove failed"))
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:b", gomock.Any()).Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	svc := prepSvc(minimalCfg(), m, 50.0)
	svc.runCycle(context.Background())

	snap := svc.Snapshot()
	assert.Equal(t, 1, snap.OldImagesRemoved)
	assert.Len(t, snap.Errors, 1)
	assert.Contains(t, snap.Errors[0], "oldImage sha256:a")
}

// ── Storage-limit cleanup ────────────────────────────────────────────────────

func TestRunCycle_SpaceCleanupSkipsWhenBelowStartThreshold(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	img := image.Summary{ID: "sha256:p2", Created: now.Add(-48 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return([]image.Summary{img}, nil)
	// No ImageRemove — disk is at 60%, below 80% start threshold
	m.EXPECT().Close().AnyTimes()

	svc := prepSvc(minimalCfg(), m, 60.0)
	svc.runCycle(context.Background())

	assert.Equal(t, 0, svc.Snapshot().SpaceImagesRemoved)
}

func TestRunCycle_SpaceCleanupStopsAfterMaxImagesPerInterval(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	imgs := []image.Summary{
		{ID: "sha256:1", Created: now.Add(-96 * time.Hour).Unix()},
		{ID: "sha256:2", Created: now.Add(-72 * time.Hour).Unix()},
		{ID: "sha256:3", Created: now.Add(-48 * time.Hour).Unix()},
	}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return(imgs, nil)
	// Exactly 2 removals expected (MaximumImagesPerInterval = 2)
	m.EXPECT().ImageRemove(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]image.DeleteResponse{{}}, nil).Times(2)
	m.EXPECT().Close().AnyTimes()

	cfg := minimalCfg()
	cfg.MaximumImagesPerInterval = 2
	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 85.0, nil } // always above end threshold
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	assert.Equal(t, 2, svc.Snapshot().SpaceImagesRemoved)
}

func TestRunCycle_SpaceCleanupStopsAtEndThreshold(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	imgs := []image.Summary{
		{ID: "sha256:1", Created: now.Add(-96 * time.Hour).Unix()},
		{ID: "sha256:2", Created: now.Add(-72 * time.Hour).Unix()},
		{ID: "sha256:3", Created: now.Add(-48 * time.Hour).Unix()},
	}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return(imgs, nil)
	// Only 1 remove before disk drops below end threshold (70%)
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:1", gomock.Any()).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	cfg := minimalCfg()
	cfg.MaximumImagesPerInterval = 10 // high limit so threshold governs
	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(m)
	call := 0
	svc.statfs = func(string) (float64, error) {
		call++
		if call == 1 {
			return 85.0, nil // first call: above start threshold
		}
		return 65.0, nil // second call: below end threshold → stop
	}
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	assert.Equal(t, 1, svc.Snapshot().SpaceImagesRemoved)
	assert.InDelta(t, 65.0, svc.Snapshot().DiskUsedPercent, 0.0001)
}

func TestRunCycle_SpaceCleanupNeverRemovesExcludedImages(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	excluded := image.Summary{
		ID:       "sha256:excl",
		Created:  now.Add(-48 * time.Hour).Unix(),
		RepoTags: []string{"nginx:latest"},
	}
	included := image.Summary{
		ID:      "sha256:incl",
		Created: now.Add(-48 * time.Hour).Unix(),
	}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return([]image.Summary{excluded, included}, nil)
	// Only the non-excluded image may be removed
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:incl", gomock.Any()).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	cfg := minimalCfg()
	cfg.ExcludedImages = []string{"nginx:latest"}
	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 85.0, nil }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	assert.Equal(t, 1, svc.Snapshot().SpaceImagesRemoved)
}

// ── Start / Stop lifecycle ────────────────────────────────────────────────────

func TestStartStop_IdempotentStartStop(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.EXPECT().Close().AnyTimes()

	cfg := minimalCfg()
	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 50.0, nil }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	ctx := context.Background()
	svc.Start(ctx)
	svc.Start(ctx) // must be a no-op; only one goroutine spins up

	svc.Stop()
	svc.Stop() // must be a no-op; no panic
}

func TestStart_InvalidIntervalDoesNotPanic(t *testing.T) {
	svc := NewCleanupService(Config{})
	assert.NotPanics(t, func() {
		svc.Start(context.Background())
		svc.Stop()
	})
}

// ── Snapshot ─────────────────────────────────────────────────────────────────

func TestSnapshot_EmptyBeforeFirstCycle(t *testing.T) {
	svc := NewCleanupService(minimalCfg())
	snap := svc.Snapshot()
	assert.True(t, snap.LastRun.IsZero())
}

func TestSnapshot_PopulatedAfterCycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	oldImg := image.Summary{ID: "sha256:s1", Created: now.Add(-40 * 24 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return([]image.Summary{oldImg}, nil)
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:s1", gomock.Any()).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	svc := prepSvc(minimalCfg(), m, 50.0)
	svc.runCycle(context.Background())

	snap := svc.Snapshot()
	assert.False(t, snap.LastRun.IsZero())
	assert.Equal(t, 1, snap.OldImagesRemoved)
}

func TestSnapshot_TracksBytesFreed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	oldImg := image.Summary{ID: "sha256:freed", Created: now.Add(-40 * 24 * time.Hour).Unix(), Size: 60}

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return([]image.Summary{oldImg}, nil)
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:freed", gomock.Any()).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	call := 0
	svc := NewCleanupService(minimalCfg())
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 50.0, nil }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 {
		call++
		if call == 1 {
			return 160 // before: 160 bytes of image layer storage
		}
		return 100 // after: 100 bytes (60 freed)
	}

	svc.runCycle(context.Background())

	assert.Equal(t, int64(60), svc.Snapshot().BytesFreed)
}

func TestRunCycle_NewClientError(t *testing.T) {
	svc := NewCleanupService(minimalCfg())
	svc.newClient = func() (agentdocker.CleanupClient, error) {
		return nil, errors.New("client init failed")
	}

	svc.runCycle(context.Background())
	assert.True(t, svc.Snapshot().LastRun.IsZero())
}

func TestRunCycle_ContainerListError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(nil, errors.New("containers failed"))
	m.EXPECT().Close().AnyTimes()

	svc := prepSvc(minimalCfg(), m, 50.0)
	svc.runCycle(context.Background())

	assert.True(t, svc.Snapshot().LastRun.IsZero())
}

func TestRunCycle_ImageListError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(nil, errors.New("images failed"))
	m.EXPECT().Close().AnyTimes()

	svc := prepSvc(minimalCfg(), m, 50.0)
	svc.runCycle(context.Background())

	assert.True(t, svc.Snapshot().LastRun.IsZero())
}

func TestRunCycle_RecordsOldImageResultsWhenDiskCheckFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	oldImg := image.Summary{ID: "sha256:phase1", Created: now.Add(-40 * 24 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return([]image.Summary{oldImg}, nil)
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:phase1", gomock.Any()).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	svc := NewCleanupService(minimalCfg())
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 0, errors.New("statfs failed") }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	snap := svc.Snapshot()
	assert.Equal(t, 1, snap.OldImagesRemoved)
	assert.Equal(t, 0, snap.SpaceImagesRemoved)
	assert.InDelta(t, 0.0, snap.DiskUsedPercent, 0.0001)
}

func TestRunCycle_DiskChecksUseSystemVolumePath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	m.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return([]image.Summary{}, nil)
	m.EXPECT().Close().AnyTimes()

	var statfsPath string
	svc := NewCleanupService(minimalCfg())
	svc.newClient = mockFactory(m)
	svc.statfs = func(path string) (float64, error) {
		statfsPath = path
		return 50.0, nil
	}
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	assert.Equal(t, constants.SystemVolumePath, statfsPath)
}

func TestRunCycle_BareNameExcludesAllTagsOfRepo(t *testing.T) {
	// "nginx" in ExcludedImages must protect nginx:latest AND nginx:1.25.3,
	// but must not protect nginx-proxy:latest.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	nginxLatest := image.Summary{
		ID:       "sha256:nginx-latest",
		Created:  now.Add(-48 * time.Hour).Unix(),
		RepoTags: []string{"nginx:latest"},
	}
	nginxVersioned := image.Summary{
		ID:       "sha256:nginx-versioned",
		Created:  now.Add(-48 * time.Hour).Unix(),
		RepoTags: []string{"nginx:1.25.3"},
	}
	nginxProxy := image.Summary{
		ID:       "sha256:nginx-proxy",
		Created:  now.Add(-48 * time.Hour).Unix(),
		RepoTags: []string{"nginx-proxy:latest"},
	}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).
		Return([]image.Summary{nginxLatest, nginxVersioned, nginxProxy}, nil)
	// Only nginx-proxy may be removed; both nginx images are excluded by the bare name.
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:nginx-proxy", gomock.Any()).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	cfg := minimalCfg()
	cfg.ExcludedImages = []string{"nginx"}
	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 85.0, nil }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	assert.Equal(t, 1, svc.Snapshot().SpaceImagesRemoved)
}

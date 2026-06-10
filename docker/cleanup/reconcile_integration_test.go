package cleanup

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	agentdocker "github.com/portainer/agent/docker"
	"github.com/portainer/agent/docker/cleanup/mocks"
	"github.com/portainer/agent/policyreconcile"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/libpolicy"
)

// TestReconcilerIntegration_ApplyAndRemove verifies the full lifecycle of a
// cleanup-docker policy through the real Reconciler + Registration() factory:
//
//  1. Reconcile with a desired state → handler created, service started.
//  2. Status reports "applied" with a message.
//  3. Reconcile with empty desired → handler removed, service stopped.
func TestReconcilerIntegration_ApplyAndRemove(t *testing.T) {
	reg := Registration()

	reconciler := policyreconcile.NewReconciler()
	reconciler.RegisterFactory(reg.Type, reg.Factory)

	cfg := map[string]any{
		"cleanupInterval":      int64(24 * 60 * 60 * 1000), // 24h in ms
		"cleanOldImages":       true,
		"imageMaximumAgeForGC": int64(30 * 24 * 60 * 60 * 1000), // 30d in ms
	}
	cfgBytes, err := json.Marshal(cfg)
	require.NoError(t, err)

	desired := []policyreconcile.DesiredState{{
		PolicyID:    portainer.PolicyID(1),
		Type:        "cleanup-docker",
		Fingerprint: libpolicy.ConfigFingerprint(cfgBytes),
		Config:      cfgBytes,
	}}

	// Apply
	reconciler.Reconcile(context.Background(), desired)
	statuses := reconciler.Statuses()
	require.Len(t, statuses, 1)
	assert.Equal(t, policyreconcile.StatusApplied, statuses[0].Status)
	assert.Equal(t, desired[0].Fingerprint, statuses[0].Fingerprint)

	// PollHook should enrich the status with a message
	hookStates := reg.PollHooks[0].Tick(context.Background(), []portainer.PolicyID{1})
	require.Len(t, hookStates, 1)
	assert.Equal(t, policyreconcile.StatusApplied, hookStates[0].Status)
	assert.Contains(t, hookStates[0].Message, "waiting for first cycle")

	// Remove
	reconciler.Reconcile(context.Background(), nil)
	assert.Empty(t, reconciler.Statuses())

	// PollHook for removed policy returns empty
	hookStates = reg.PollHooks[0].Tick(context.Background(), []portainer.PolicyID{1})
	assert.Empty(t, hookStates)
}

// TestReconcilerIntegration_InvalidConfig_StatusFailed verifies that invalid config
// causes the reconciler to report StatusFailed with a meaningful message.
func TestReconcilerIntegration_InvalidConfig_StatusFailed(t *testing.T) {
	reg := Registration()

	reconciler := policyreconcile.NewReconciler()
	reconciler.RegisterFactory(reg.Type, reg.Factory)

	// cleanupInterval = 0 fails validation
	cfgBytes := json.RawMessage(`{"cleanupInterval": 0}`)

	desired := []policyreconcile.DesiredState{{
		PolicyID:    portainer.PolicyID(1),
		Type:        "cleanup-docker",
		Fingerprint: libpolicy.ConfigFingerprint(cfgBytes),
		Config:      cfgBytes,
	}}

	reconciler.Reconcile(context.Background(), desired)
	statuses := reconciler.Statuses()
	require.Len(t, statuses, 1)
	assert.Equal(t, policyreconcile.StatusFailed, statuses[0].Status)
	assert.Contains(t, statuses[0].Message, "cleanupInterval must be at least 5 minutes")
}

// TestReconcilerIntegration_FingerprintChange_ReappliesService verifies that a
// config fingerprint change triggers a service replacement.
func TestReconcilerIntegration_FingerprintChange_ReappliesService(t *testing.T) {
	reg := Registration()

	reconciler := policyreconcile.NewReconciler()
	reconciler.RegisterFactory(reg.Type, reg.Factory)

	makeCfg := func(intervalMs int64) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{
			"cleanupInterval":      intervalMs,
			"cleanOldImages":       true,
			"imageMaximumAgeForGC": int64(30 * 24 * 60 * 60 * 1000),
		})
		return raw
	}

	cfg1 := makeCfg(86400000)
	cfg2 := makeCfg(43200000) // different interval → different fingerprint

	reconciler.Reconcile(context.Background(), []policyreconcile.DesiredState{{
		PolicyID: 1, Type: "cleanup-docker", Fingerprint: libpolicy.ConfigFingerprint(cfg1), Config: cfg1,
	}})
	reconciler.Reconcile(context.Background(), []policyreconcile.DesiredState{{
		PolicyID: 1, Type: "cleanup-docker", Fingerprint: libpolicy.ConfigFingerprint(cfg2), Config: cfg2,
	}})

	statuses := reconciler.Statuses()
	require.Len(t, statuses, 1)
	assert.Equal(t, policyreconcile.StatusApplied, statuses[0].Status)
	assert.Equal(t, libpolicy.ConfigFingerprint(cfg2), statuses[0].Fingerprint)

	// Cleanup
	reconciler.Reconcile(context.Background(), nil)
}

// TestRunCycle_OldImageCleanupDisabled_OnlyStorageLimitRuns verifies that when
// CleanOldImages=false, only storage-limit cleanup runs (phase 2 only).
func TestRunCycle_OldImageCleanupDisabled_OnlyStorageLimitRuns(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	// This image is old enough for both phases, but phase 1 is disabled.
	img := image.Summary{ID: "sha256:old", Created: now.Add(-40 * 24 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return([]image.Summary{img}, nil)
	// Image is removed only by storage-limit cleanup (phase 2)
	m.EXPECT().ImageRemove(gomock.Any(), "sha256:old", image.RemoveOptions{Force: true, PruneChildren: true}).
		Return([]image.DeleteResponse{{}}, nil)
	m.EXPECT().Close().AnyTimes()

	cfg := Config{
		CleanupInterval:                  Duration(1 * time.Millisecond),
		CleanOldImages:                   false, // phase 1 disabled
		ImageMaximumAgeForGC:             Duration(30 * 24 * time.Hour),
		CleanImagesAtStorageLimit:        true,
		CleanupStartHostThresholdPercent: 80.0,
		CleanupEndHostThresholdPercent:   70.0,
		MinAge:                           Duration(24 * time.Hour),
		MaximumImagesPerInterval:         10,
	}

	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 85.0, nil }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	snap := svc.Snapshot()
	assert.Equal(t, 0, snap.OldImagesRemoved, "phase 1 disabled, no old-image removals")
	assert.Equal(t, 1, snap.SpaceImagesRemoved, "phase 2 should remove the eligible image")
}

// TestRunCycle_BothPhasesDisabled_NoRemovals verifies that when both cleanup phases
// are disabled, no images are removed but the cycle still completes cleanly.
func TestRunCycle_BothPhasesDisabled_NoRemovals(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	m := mocks.NewMockCleanupClient(ctrl)

	now := time.Now()
	img := image.Summary{ID: "sha256:old", Created: now.Add(-40 * 24 * time.Hour).Unix()}

	m.EXPECT().ContainerList(gomock.Any(), container.ListOptions{All: true}).Return([]container.Summary{}, nil)
	m.EXPECT().ImageList(gomock.Any(), image.ListOptions{All: false}).Return([]image.Summary{img}, nil)
	// No ImageRemove expected
	m.EXPECT().Close().AnyTimes()

	cfg := Config{
		CleanupInterval:           Duration(1 * time.Millisecond),
		CleanOldImages:            false,
		CleanImagesAtStorageLimit: false,
	}

	svc := NewCleanupService(cfg)
	svc.newClient = mockFactory(m)
	svc.statfs = func(string) (float64, error) { return 85.0, nil }
	svc.imageLayersSize = func(context.Context, agentdocker.CleanupClient) int64 { return 0 }

	svc.runCycle(context.Background())

	snap := svc.Snapshot()
	assert.Equal(t, 0, snap.OldImagesRemoved)
	assert.Equal(t, 0, snap.SpaceImagesRemoved)
	assert.False(t, snap.LastRun.IsZero(), "snapshot should still record the cycle")
}

// TestClearSpaceCandidates_MinAgeZero_AllAgesEligible verifies that when MinAge=0,
// images of any age (even created 1 second ago) are eligible for storage-limit cleanup.
func TestClearSpaceCandidates_MinAgeZero_AllAgesEligible(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	minAge := time.Duration(0) // zero = any age

	veryRecent := makeImg("sha256:recent", now.Add(-1*time.Second).Unix())
	old := makeImg("sha256:old", now.Add(-48*time.Hour).Unix())

	candidates := clearSpaceCandidates(now, []image.Summary{veryRecent, old}, nil, nil, minAge, nil)

	// Both images should be candidates since minAge = 0
	assert.Len(t, candidates, 2)
	// Still sorted oldest-first
	assert.Equal(t, "sha256:old", candidates[0].ID)
	assert.Equal(t, "sha256:recent", candidates[1].ID)
}

// TestDecodeConfig_PartiallyEnforcedFieldsPassValidation verifies that a config
// with only some fields populated (simulating a partially-enforced policy) still
// passes validation. This mirrors the server's ToAgentConfig() output when only
// some directives have Enforce=true.
func TestDecodeConfig_PartiallyEnforcedFieldsPassValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "only cleanupInterval and cleanOldImages enforced",
			raw:  `{"cleanupInterval": 86400000, "cleanOldImages": true, "imageMaximumAgeForGC": 2592000000}`,
		},
		{
			name: "only storage-limit fields enforced",
			raw: `{
				"cleanupInterval": 86400000,
				"cleanImagesAtStorageLimit": true,
				"cleanupStartHostThresholdPercent": 80,
				"cleanupEndHostThresholdPercent": 70
			}`,
		},
		{
			name: "excluded images only with interval",
			raw:  `{"cleanupInterval": 86400000, "excludedImages": ["nginx:latest", "redis"]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := DecodeConfig(json.RawMessage(tc.raw))
			require.NoError(t, err, "partially-enforced config must pass validation")
			assert.Positive(t, cfg.CleanupInterval.Std())
		})
	}
}

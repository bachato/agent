package cleanup

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/libpolicy"

	"github.com/portainer/agent/policyreconcile"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validHandlerConfig returns the minimal JSON for a config that passes
// DecodeConfig without enabling CleanImagesAtStorageLimit (which would trigger
// a real syscall in the pre-flight check).
func validHandlerConfig(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"cleanupInterval":      int64(24 * 60 * 60 * 1000), // 24h in ms
		"cleanOldImages":       true,
		"imageMaximumAgeForGC": int64(30 * 24 * 60 * 60 * 1000), // 30d in ms
	})
	require.NoError(t, err)
	return raw
}

// ── Factory / registration ───────────────────────────────────────────────────

func TestNewHandler_FactoryRegistersHandlerWithReporter(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	factory := NewHandler(reporter)

	handler := factory(portainer.PolicyID(1))

	reporter.mu.RLock()
	_, registered := reporter.handlers[portainer.PolicyID(1)]
	reporter.mu.RUnlock()

	assert.True(t, registered, "handler should be registered with the reporter")
	assert.IsType(t, &CleanupHandler{}, handler)
}

// ── Apply ────────────────────────────────────────────────────────────────────

func TestApply_ValidConfig_StartsServiceAndReturnsNil(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)

	err := h.Apply(context.Background(), validHandlerConfig(t))

	require.NoError(t, err)
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.NotNil(t, h.service, "service should be started after Apply")
	assert.NotEmpty(t, h.fingerprint, "fingerprint should be set after Apply")
	h.service.Stop()
}

func TestApply_InvalidJSON_ReturnsError(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)

	err := h.Apply(context.Background(), json.RawMessage(`{not valid json`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cleanup config")
}

func TestApply_InvalidConfig_ReturnsError(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)

	// cleanupInterval = 0 fails validation
	raw, _ := json.Marshal(map[string]any{"cleanupInterval": int64(0)})
	err := h.Apply(context.Background(), raw)

	require.Error(t, err)
}

func TestApply_StorageLimitEnabled_AlwaysReturnsNilRegardlessOfDiskStat(t *testing.T) {
	// Whether the pre-flight disk-stat check succeeds or fails, Apply must not
	// return an error — only the status message carries the warning.
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)

	raw, _ := json.Marshal(map[string]any{
		"cleanupInterval":                  int64(24 * 60 * 60 * 1000),
		"cleanImagesAtStorageLimit":        true,
		"cleanupStartHostThresholdPercent": 80.0,
		"cleanupEndHostThresholdPercent":   70.0,
	})

	err := h.Apply(context.Background(), raw)

	// Regardless of whether this machine can stat SystemVolumePath, Apply is
	// non-fatal — the service starts and a warning appears in the status message.
	require.NoError(t, err)

	h.mu.Lock()
	defer h.mu.Unlock()
	assert.NotNil(t, h.service)
	h.service.Stop()
}

func TestApply_ReplacesExistingServiceOnReapply(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)

	cfg := validHandlerConfig(t)

	require.NoError(t, h.Apply(context.Background(), cfg))
	h.mu.Lock()
	first := h.service
	h.mu.Unlock()

	require.NoError(t, h.Apply(context.Background(), cfg))
	h.mu.Lock()
	second := h.service
	h.mu.Unlock()

	assert.NotSame(t, first, second, "re-applying should replace the service")
	second.Stop()
}

// ── Remove ───────────────────────────────────────────────────────────────────

func TestRemove_StopsServiceAndDeregistersFromReporter(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)
	require.NoError(t, h.Apply(context.Background(), validHandlerConfig(t)))

	err := h.Remove(context.Background())

	require.NoError(t, err)

	h.mu.Lock()
	assert.Nil(t, h.service, "service should be stopped after Remove")
	h.mu.Unlock()

	reporter.mu.RLock()
	_, stillRegistered := reporter.handlers[portainer.PolicyID(1)]
	reporter.mu.RUnlock()
	assert.False(t, stillRegistered, "handler should be deregistered from reporter after Remove")
}

// ── Status / ActualState ─────────────────────────────────────────────────────

func TestStatus_BeforeFirstCycle_WaitingMessage(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)
	require.NoError(t, h.Apply(context.Background(), validHandlerConfig(t)))
	defer func() {
		h.mu.Lock()
		if h.service != nil {
			h.service.Stop()
		}
		h.mu.Unlock()
	}()

	state := h.Status()

	assert.Equal(t, policyreconcile.StatusApplied, state.Status)
	assert.Contains(t, state.Message, "waiting for first cycle")
}

func TestStatus_WithDiskStatWarning_IncludesWarningInMessage(t *testing.T) {
	// Directly set diskStatWarn to test status message construction without
	// depending on the host's filesystem.
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)
	require.NoError(t, h.Apply(context.Background(), validHandlerConfig(t)))
	defer func() {
		h.mu.Lock()
		if h.service != nil {
			h.service.Stop()
		}
		h.mu.Unlock()
	}()

	h.mu.Lock()
	h.diskStatWarn = "storage-limit cleanup may not function: cannot read disk usage (test error); age-based cleanup is still active"
	h.mu.Unlock()

	state := h.Status()

	assert.Equal(t, policyreconcile.StatusApplied, state.Status)
	assert.Contains(t, state.Message, "storage-limit cleanup may not function")
	assert.Contains(t, state.Message, "age-based cleanup is still active")
}

func TestStatus_AfterSnapshotRecorded_IncludesSnapshotData(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)
	require.NoError(t, h.Apply(context.Background(), validHandlerConfig(t)))
	defer func() {
		h.mu.Lock()
		if h.service != nil {
			h.service.Stop()
		}
		h.mu.Unlock()
	}()

	// Inject a snapshot directly to test message formatting without waiting for
	// a real cycle.
	now := time.Now().UTC()
	h.mu.Lock()
	h.service.statusMu.Lock()
	h.service.snapshot = CleanupStatusSnapshot{
		LastRun:            now,
		OldImagesRemoved:   3,
		SpaceImagesRemoved: 5,
		BytesFreed:         150 * 1024 * 1024, // 150 MB
		DiskUsedPercent:    65.0,
	}
	h.service.statusMu.Unlock()
	h.mu.Unlock()

	state := h.Status()

	assert.Equal(t, policyreconcile.StatusApplied, state.Status)
	assert.Contains(t, state.Message, "removed 3 age-based and 5 storage-limit images")
	assert.Contains(t, state.Message, "150.0 MB")
	assert.Contains(t, state.Message, "disk: 65.0%")
}

func TestStatus_AfterSnapshotWithErrors_IncludesErrorCount(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)
	require.NoError(t, h.Apply(context.Background(), validHandlerConfig(t)))
	defer func() {
		h.mu.Lock()
		if h.service != nil {
			h.service.Stop()
		}
		h.mu.Unlock()
	}()

	h.mu.Lock()
	h.service.statusMu.Lock()
	h.service.snapshot = CleanupStatusSnapshot{
		LastRun:          time.Now().UTC(),
		OldImagesRemoved: 1,
		BytesFreed:       1024,
		Errors:           []string{"oldImage sha256:abc: permission denied", "clearSpace sha256:def: image in use"},
	}
	h.service.statusMu.Unlock()
	h.mu.Unlock()

	state := h.Status()

	assert.Contains(t, state.Message, "2 removal error(s)")
}

// ── Tick / PollHook ──────────────────────────────────────────────────────────

func TestTick_ActivePolicy_ReturnsEnrichedState(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 42, reporter: reporter}
	reporter.register(42, h)
	require.NoError(t, h.Apply(context.Background(), validHandlerConfig(t)))
	defer func() {
		h.mu.Lock()
		if h.service != nil {
			h.service.Stop()
		}
		h.mu.Unlock()
	}()

	states := reporter.Tick(context.Background(), []portainer.PolicyID{42})

	require.Len(t, states, 1)
	assert.Equal(t, portainer.PolicyID(42), states[0].PolicyID)
	assert.Equal(t, policyreconcile.StatusApplied, states[0].Status)
	assert.Equal(t, cleanupDockerPolicyType, states[0].Type)
}

func TestTick_PolicyNotInDesiredSet_ReturnsEmpty(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 42, reporter: reporter}
	reporter.register(42, h)
	require.NoError(t, h.Apply(context.Background(), validHandlerConfig(t)))
	defer func() {
		h.mu.Lock()
		if h.service != nil {
			h.service.Stop()
		}
		h.mu.Unlock()
	}()

	// Pass a desired set that does NOT include policy 42.
	states := reporter.Tick(context.Background(), []portainer.PolicyID{99})

	assert.Empty(t, states)
}

func TestTick_MultipleHandlers_ReturnsOnlyActive(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	cfg := validHandlerConfig(t)

	h1 := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h1)
	require.NoError(t, h1.Apply(context.Background(), cfg))
	defer func() {
		h1.mu.Lock()
		if h1.service != nil {
			h1.service.Stop()
		}
		h1.mu.Unlock()
	}()

	h2 := &CleanupHandler{policyID: 2, reporter: reporter}
	reporter.register(2, h2)
	require.NoError(t, h2.Apply(context.Background(), cfg))
	defer func() {
		h2.mu.Lock()
		if h2.service != nil {
			h2.service.Stop()
		}
		h2.mu.Unlock()
	}()

	// Only policy 1 is in the desired set.
	states := reporter.Tick(context.Background(), []portainer.PolicyID{1})

	require.Len(t, states, 1)
	assert.Equal(t, portainer.PolicyID(1), states[0].PolicyID)
}

// ── Fingerprint ──────────────────────────────────────────────────────────────

func TestConfigFingerprint_SameInputProducesSameOutput(t *testing.T) {
	raw := json.RawMessage(`{"cleanupInterval":86400000}`)
	fp1 := libpolicy.ConfigFingerprint(raw)
	fp2 := libpolicy.ConfigFingerprint(raw)
	assert.Equal(t, fp1, fp2)
}

func TestConfigFingerprint_DifferentInputProducesDifferentOutput(t *testing.T) {
	fp1 := libpolicy.ConfigFingerprint(json.RawMessage(`{"cleanupInterval":86400000}`))
	fp2 := libpolicy.ConfigFingerprint(json.RawMessage(`{"cleanupInterval":43200000}`))
	assert.NotEqual(t, fp1, fp2)
}

func TestApply_FingerprintMatchesRawConfig(t *testing.T) {
	reporter := NewCleanupStatusReporter()
	h := &CleanupHandler{policyID: 1, reporter: reporter}
	reporter.register(1, h)

	raw := validHandlerConfig(t)
	require.NoError(t, h.Apply(context.Background(), raw))
	defer func() {
		h.mu.Lock()
		if h.service != nil {
			h.service.Stop()
		}
		h.mu.Unlock()
	}()

	h.mu.Lock()
	gotFP := h.fingerprint
	h.mu.Unlock()

	assert.Equal(t, libpolicy.ConfigFingerprint(raw), gotFP)
}

// ── formatBytes ──────────────────────────────────────────────────────────────

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{int64(1.5 * 1024 * 1024), "1.5 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, formatBytes(tc.input), "input: %d", tc.input)
	}
}

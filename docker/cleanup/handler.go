package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/libpolicy"

	"github.com/portainer/agent/constants"
	"github.com/portainer/agent/policyreconcile"
	"github.com/rs/zerolog/log"
)

const (
	cleanupDockerPolicyType = "cleanup-docker"
	cleanupHandlerContext   = "DockerCleanupHandler"
)

// Registration returns a policyreconcile.Registration for cleanup-docker policies.
// Call this from edge.go inside the Docker/Podman platform guard.
func Registration() policyreconcile.Registration {
	reporter := NewCleanupStatusReporter()

	log.Debug().
		Str("type", cleanupDockerPolicyType).
		Str("context", cleanupHandlerContext).
		Msg("Registering cleanup-docker policy reconciler")

	return policyreconcile.Registration{
		Type:      cleanupDockerPolicyType,
		Factory:   NewHandler(reporter),
		PollHooks: []policyreconcile.PollHook{reporter},
	}
}

// CleanupHandler implements policyreconcile.PolicyHandler for cleanup-docker policies.
// One instance is created per active policy ID by the factory returned from NewHandler.
type CleanupHandler struct {
	policyID portainer.PolicyID
	reporter *CleanupStatusReporter

	mu           sync.Mutex
	service      *CleanupService
	fingerprint  string
	diskStatWarn string // non-empty when the pre-flight disk-stat check failed
}

// NewHandler returns a HandlerFactory for cleanup-docker policies.
// The reporter is shared across all handlers produced by this factory and is
// included as a PollHook in Registration().
func NewHandler(reporter *CleanupStatusReporter) policyreconcile.HandlerFactory {
	return func(policyID portainer.PolicyID) policyreconcile.PolicyHandler {
		log.Debug().
			Int("policy_id", int(policyID)).
			Str("context", cleanupHandlerContext).
			Msg("Registering cleanup-docker handler")
		h := &CleanupHandler{
			policyID: policyID,
			reporter: reporter,
		}
		reporter.register(policyID, h)
		return h
	}
}

// Apply implements policyreconcile.PolicyHandler.
// It decodes and validates the config, performs a non-fatal disk-stat pre-flight
// check when storage-limit cleanup is requested, then starts (or replaces) the
// CleanupService. Returns nil on success.
//
// Disk-stat failures are intentionally non-fatal: age-based cleanup (CleanOldImages)
// works regardless of whether the host filesystem is accessible, so the service is
// started and the limitation is surfaced as a warning in Status().Message instead
// of causing Apply to fail and leaving the policy in a perpetual retry loop.
func (h *CleanupHandler) Apply(ctx context.Context, raw json.RawMessage) error {
	cfg, err := DecodeConfig(raw)
	if err != nil {
		return err
	}

	// Pre-flight: verify disk stats are readable when storage-limit cleanup is
	// enabled. Failure is non-fatal — we start the service anyway (age-based
	// cleanup still works) and surface the limitation in the status message.
	var diskStatWarn string
	if cfg.CleanImagesAtStorageLimit {
		if _, err := diskUsagePercent(constants.SystemVolumePath); err != nil {
			diskStatWarn = fmt.Sprintf(
				"storage-limit cleanup may not function: cannot read disk usage (%s); age-based cleanup is still active",
				err.Error(),
			)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	var prevSnapshot CleanupStatusSnapshot
	if h.service != nil {
		prevSnapshot = h.service.Snapshot()
		h.service.Stop()
	}

	svc := NewCleanupService(cfg)
	if !prevSnapshot.LastRun.IsZero() {
		svc.seedSnapshot(prevSnapshot)
	}
	svc.Start(ctx)

	h.service = svc
	h.fingerprint = libpolicy.ConfigFingerprint(raw)
	h.diskStatWarn = diskStatWarn

	log.Debug().
		Int("policy_id", int(h.policyID)).
		Str("fingerprint", h.fingerprint).
		Bool("clean_old_images", cfg.CleanOldImages).
		Bool("clean_images_at_storage_limit", cfg.CleanImagesAtStorageLimit).
		Bool("clear_build_cache", cfg.ClearBuildCache).
		Str("context", cleanupHandlerContext).
		Msg("Applied cleanup-docker policy")

	return nil
}

// Remove implements policyreconcile.PolicyHandler.
// It stops the background cleanup service and deregisters this handler from the
// reporter so that Tick() no longer emits status entries for this policy.
func (h *CleanupHandler) Remove(_ context.Context) error {
	log.Debug().
		Int("policy_id", int(h.policyID)).
		Str("context", cleanupHandlerContext).
		Msg("Removing cleanup-docker handler")

	h.reporter.deregister(h.policyID)

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.service != nil {
		h.service.Stop()
		h.service = nil
	}
	return nil
}

// Status implements policyreconcile.PolicyHandler.
// Returns the handler's current ActualState for direct observation (e.g. in tests).
// The reconciler itself does not call this — it manages its own r.actual map.
func (h *CleanupHandler) Status() policyreconcile.ActualState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buildActualState()
}

// buildActualState constructs a rich ActualState from the current service snapshot.
// Must be called with h.mu held.
func (h *CleanupHandler) buildActualState() policyreconcile.ActualState {
	state := policyreconcile.ActualState{
		PolicyID:    h.policyID,
		Type:        cleanupDockerPolicyType,
		Fingerprint: h.fingerprint,
		Status:      policyreconcile.StatusApplied,
	}

	if h.service == nil {
		state.Status = policyreconcile.StatusFailed
		state.Message = "Cleanup service is not running"
		return state
	}

	var parts []string

	if h.diskStatWarn != "" {
		parts = append(parts, h.diskStatWarn)
	}

	snap := h.service.Snapshot()
	if snap.LastRun.IsZero() {
		parts = append(parts, "Cleanup service running, waiting for first cycle")
	} else {
		summary := fmt.Sprintf(
			"Last run %s: removed %d age-based and %d storage-limit images, freed %s",
			snap.LastRun.UTC().Format(time.RFC3339),
			snap.OldImagesRemoved,
			snap.SpaceImagesRemoved,
			formatBytes(snap.BytesFreed),
		)
		if snap.BuildCacheBytesFreed > 0 {
			summary += fmt.Sprintf(" (build cache: %s reclaimed)", formatBytes(snap.BuildCacheBytesFreed))
		}
		if snap.DiskUsedPercent > 0 {
			summary += fmt.Sprintf(" (disk: %.1f%%)", snap.DiskUsedPercent)
		}
		parts = append(parts, summary)

		if len(snap.Errors) > 0 {
			parts = append(parts, fmt.Sprintf("%d removal error(s): %s",
				len(snap.Errors), strings.Join(snap.Errors, "; ")))
		}
	}

	state.Message = strings.Join(parts, "; ")
	return state
}

// CleanupStatusReporter tracks active CleanupHandlers and emits snapshot-enriched
// ActualState entries on each poll cycle, overriding the reconciler's empty "applied"
// message with live data from each cleanup service.
//
// It implements policyreconcile.PollHook and is automatically registered
// as a hook via Registration().
//
// The hook's statuses are appended to reconciler.Statuses() before being sent to
// the server. The server upserts by policy ID in order, so the hook's richer
// message (last in the slice) overwrites the reconciler's empty success message
// for the same policy.
type CleanupStatusReporter struct {
	mu       sync.RWMutex
	handlers map[portainer.PolicyID]*CleanupHandler
}

// NewCleanupStatusReporter returns a reporter ready for use as a PollHook.
func NewCleanupStatusReporter() *CleanupStatusReporter {
	return &CleanupStatusReporter{
		handlers: make(map[portainer.PolicyID]*CleanupHandler),
	}
}

func (r *CleanupStatusReporter) register(id portainer.PolicyID, h *CleanupHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[id] = h
}

func (r *CleanupStatusReporter) deregister(id portainer.PolicyID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handlers, id)
}

// Tick implements policyreconcile.PollHook.
// For each active cleanup handler it returns a snapshot-enriched ActualState.
func (r *CleanupStatusReporter) Tick(_ context.Context, desired []portainer.PolicyID) []policyreconcile.ActualState {
	desiredSet := make(map[portainer.PolicyID]struct{}, len(desired))
	for _, id := range desired {
		desiredSet[id] = struct{}{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []policyreconcile.ActualState
	for id, h := range r.handlers {
		if _, active := desiredSet[id]; !active {
			continue
		}
		h.mu.Lock()
		state := h.buildActualState()
		h.mu.Unlock()
		out = append(out, state)
	}
	return out
}

// formatBytes renders a byte count as a human-readable string - SI units (e.g. "1.5 GB").
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

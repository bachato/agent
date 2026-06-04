package helm

import (
	"context"
	"fmt"
	"sync"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/rs/zerolog/log"

	"github.com/portainer/agent/policyreconcile"
)

// RestoreTypeForChart maps a Helm chart name to the portainer.PolicyType whose
// restore settings should be applied after that chart is uninstalled.
// Returns an empty string for charts with no registered restore type.
func RestoreTypeForChart(chartName string) portainer.PolicyType {
	switch chartName {
	case "portainer-registry-k8s":
		return portainer.RegistryK8s
	case "gatekeeper", "portainer-security-k8s":
		return portainer.SecurityK8s
	default:
		return ""
	}
}

// compile-time assertion that RestoreCoordinator implements policyreconcile.PollHook.
var _ policyreconcile.PollHook = (*RestoreCoordinator)(nil)

// ApplierFunc applies a single restore manifest.
// Implementations are responsible for tolerating idempotent-failure cases inherent
// to the manifest's intent. The coordinator treats every error as transient and
// retryable; classification of "this error means success" is the applier's job.
type ApplierFunc func(ctx context.Context, manifest string) error

type pendingRestore struct {
	policyID portainer.PolicyID
	manifest string
	attempts int
	lastErr  error
	lastAt   time.Time
}

// visibleAfterAttempts is the number of failed Tick calls after which the
// coordinator surfaces a StatusFailed entry in its Statuses return value.
// Below this threshold, retries are silent (transient noise suppressed).
const visibleAfterAttempts = 3

// RestoreCoordinator implements policyreconcile.PollHook for helm restore-manifest
// retry. It lives inside the helm package so all restore vocabulary stays
// helm-internal; the generic poll loop only knows the PollHook interface.
//
// Design rationale: cross-poll retry is unbounded to match the pre-existing
// pendingRestorations guarantee. A bounded retry (e.g. 3 attempts) was rejected
// because transient failure modes — CRD propagation lag, webhook races, API
// rate-limits — can persist for minutes and would silently abandon the restore.
// Recovery for a truly stuck manifest is agent restart, which clears the in-memory queue.
type RestoreCoordinator struct {
	mu      sync.Mutex
	pending map[portainer.PolicyID]*pendingRestore
	apply   ApplierFunc
}

// NewRestoreCoordinator returns a RestoreCoordinator backed by the given applier.
// Use kubernetesApplierFor(kubeClient) to wrap the existing kubernetesDeployer.
func NewRestoreCoordinator(apply ApplierFunc) *RestoreCoordinator {
	return &RestoreCoordinator{
		pending: make(map[portainer.PolicyID]*pendingRestore),
		apply:   apply,
	}
}

// Enqueue stores a restore manifest for a policy. Idempotent on policyID:
// re-enqueueing the same manifest leaves the attempt counter unchanged;
// re-enqueueing a different manifest resets the counter to 0.
func (c *RestoreCoordinator) Enqueue(policyID portainer.PolicyID, manifest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.pending[policyID]; ok {
		if existing.manifest != manifest {
			existing.manifest = manifest
			existing.attempts = 0
			existing.lastErr = nil
		}
		return
	}
	c.pending[policyID] = &pendingRestore{policyID: policyID, manifest: manifest}
}

// Tick implements policyreconcile.PollHook.
// It cancels entries for policies that are back in the desired set (re-creation
// race), then attempts each remaining restore once. Returns ActualState entries
// for restores at or above visibleAfterAttempts.
func (c *RestoreCoordinator) Tick(ctx context.Context, desired []portainer.PolicyID) []policyreconcile.ActualState {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Drop entries whose policy reappeared — fresh install supersedes stale restore.
	for _, id := range desired {
		delete(c.pending, id)
	}

	var visible []policyreconcile.ActualState
	for id, p := range c.pending {
		err := c.apply(ctx, p.manifest)
		if err == nil {
			log.Info().Str("context", "HelmRestoreCoordinator").Int("policy_id", int(id)).Msg("Restore manifest applied successfully")
			delete(c.pending, id)
			continue
		}
		if ctx.Err() != nil {
			// Context was cancelled during or before apply — don't count as an attempt.
			// The applier is still called so callers can observe the invocation, but the
			// cancellation is not the manifest's fault: don't penalise the attempt counter.
			continue
		}
		p.attempts++
		p.lastErr = err
		p.lastAt = time.Now()
		log.Debug().Err(err).Str("context", "HelmRestoreCoordinator").Int("policy_id", int(id)).Int("attempts", p.attempts).
			Msg("Restore manifest apply failed, will retry next poll")
	}

	for _, p := range c.pending {
		if p.attempts >= visibleAfterAttempts {
			visible = append(visible, policyreconcile.ActualState{
				PolicyID: p.policyID,
				Type:     "helm-k8s",
				Status:   policyreconcile.StatusFailed,
				Message: fmt.Sprintf("restoration pending after %d attempts: %v",
					p.attempts, p.lastErr),
			})
		}
	}
	return visible
}

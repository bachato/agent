package helm

import (
	"sync"
	"time"

	portainer "github.com/portainer/portainer/api"
)

// ChartStatusReporter aggregates per-chart status across all live HelmHandlers.
// It exists solely to feed the legacy per-chart status endpoint during the transition
// period. Removed when the per-policy status endpoint is the sole path.
type ChartStatusReporter struct {
	mu        sync.Mutex
	perPolicy map[portainer.PolicyID][]portainer.PolicyChartStatus
}

func NewChartStatusReporter() *ChartStatusReporter {
	return &ChartStatusReporter{
		perPolicy: make(map[portainer.PolicyID][]portainer.PolicyChartStatus),
	}
}

// Set replaces the chart-status slice for a policy. Called by HelmHandler after
// each reconcileCharts pass.
func (r *ChartStatusReporter) Set(policyID portainer.PolicyID, statuses []portainer.PolicyChartStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.perPolicy[policyID] = statuses
}

// Clear drops a policy's entries. Called by HelmHandler.Remove.
func (r *ChartStatusReporter) Clear(policyID portainer.PolicyID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.perPolicy, policyID)
}

// Snapshot returns a flat slice of all per-chart statuses across all live handlers.
func (r *ChartStatusReporter) Snapshot() []portainer.PolicyChartStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []portainer.PolicyChartStatus
	for _, statuses := range r.perPolicy {
		out = append(out, statuses...)
	}
	return out
}

// ChartStatusSnapshot is a nil-safe free function so callers constructed without
// a reporter (Docker/Podman agents) can call it without a nil check.
func ChartStatusSnapshot(r *ChartStatusReporter) []portainer.PolicyChartStatus {
	if r == nil {
		return nil
	}
	return r.Snapshot()
}

// buildChartStatuses converts a handler's installedCharts into PolicyChartStatus
// records for the legacy status endpoint. Called from HelmHandler.reconcileCharts.
func buildChartStatuses(endpointID portainer.EndpointID, charts map[string]chartRecord) []portainer.PolicyChartStatus {
	statuses := make([]portainer.PolicyChartStatus, 0, len(charts))
	for _, rec := range charts {
		statuses = append(statuses, portainer.PolicyChartStatus{
			EnvironmentID:   endpointID,
			ChartName:       rec.ChartName,
			Fingerprint:     rec.Fingerprint,
			Status:          rec.Status,
			Message:         rec.Message,
			Namespace:       rec.Namespace,
			LastAttemptTime: time.Now().Unix(),
		})
	}
	return statuses
}

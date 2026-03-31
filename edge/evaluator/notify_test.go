package evaluator

import (
	"context"
	"sync"
	"testing"
	"time"

	portainer "github.com/portainer/portainer/api"
	alertmanagermodels "github.com/prometheus/alertmanager/api/v2/models"
	"github.com/go-openapi/strfmt"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingPoster records the most recent PostAlerts call.
// called is closed when PostAlerts is invoked, allowing tests to synchronise
// with the fire-and-forget goroutine started by notify().
type capturingPoster struct {
	mu      sync.Mutex
	captured alertmanagermodels.PostableAlerts
	called  chan struct{}
}

func newCapturingPoster() *capturingPoster {
	return &capturingPoster{called: make(chan struct{})}
}

func (p *capturingPoster) PostAlerts(_ portainer.EndpointID, alerts alertmanagermodels.PostableAlerts) error {
	p.mu.Lock()
	p.captured = alerts
	p.mu.Unlock()
	close(p.called)
	return nil
}

func (p *capturingPoster) waitCalled(t *testing.T) {
	t.Helper()
	select {
	case <-p.called:
	case <-time.After(time.Second):
		t.Fatal("PostAlerts was not called within timeout")
	}
}

func (p *capturingPoster) getCaptured() alertmanagermodels.PostableAlerts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.captured
}

func newTestService(poster AlertPoster) *Service {
	return &Service{
		endpointID:     portainer.EndpointID(1),
		poster:         poster,
		scrapeInterval: defaultRuleEvalInterval,
	}
}

func TestNotifyFiringAlertIsForwarded(t *testing.T) {
	poster := newCapturingPoster()
	svc := newTestService(poster)

	now := time.Now()
	alert := &rules.Alert{
		State: rules.StateFiring,
		Labels: labels.FromStrings(
			"alertname", "HighCPU",
			"alert_rule_id", "42",
			"severity", "critical",
		),
		Annotations: labels.FromStrings(
			"description", "CPU usage is above 90%",
		),
		ActiveAt: now.Add(-5 * time.Minute),
		FiredAt:  now.Add(-1 * time.Minute),
	}

	svc.notify(context.Background(), "", alert)
	poster.waitCalled(t)

	captured := poster.getCaptured()
	require.Len(t, captured, 1)

	pa := captured[0]
	assert.Equal(t, "HighCPU", pa.Alert.Labels["alertname"])
	assert.Equal(t, "42", pa.Alert.Labels["alert_rule_id"])
	assert.Equal(t, "critical", pa.Alert.Labels["severity"])
	assert.Equal(t, "CPU usage is above 90%", pa.Annotations["description"])
	assert.Equal(t, strfmt.DateTime(alert.ActiveAt), pa.StartsAt)
	// Firing alert should have a future EndsAt to keep the alert alive in Alertmanager.
	assert.True(t, time.Time(pa.EndsAt).After(time.Now()), "EndsAt should be in the future for firing alerts")
}

func TestNotifyResolvedAlertIsForwarded(t *testing.T) {
	poster := newCapturingPoster()
	svc := newTestService(poster)

	now := time.Now()
	resolvedAt := now.Add(-30 * time.Second)
	alert := &rules.Alert{
		State: rules.StateInactive,
		Labels: labels.FromStrings(
			"alertname", "HighCPU",
			"alert_rule_id", "42",
			"severity", "critical",
		),
		Annotations: labels.FromStrings(
			"description", "CPU usage is above 90%",
		),
		ActiveAt:   now.Add(-5 * time.Minute),
		ResolvedAt: resolvedAt,
	}

	svc.notify(context.Background(), "", alert)
	poster.waitCalled(t)

	captured := poster.getCaptured()
	require.Len(t, captured, 1)

	pa := captured[0]
	assert.Equal(t, "HighCPU", pa.Alert.Labels["alertname"])
	assert.Equal(t, strfmt.DateTime(alert.ActiveAt), pa.StartsAt)
	assert.Equal(t, strfmt.DateTime(resolvedAt), pa.EndsAt)
}

func TestNotifyPendingAlertIsSkipped(t *testing.T) {
	poster := newCapturingPoster()
	svc := newTestService(poster)

	alert := &rules.Alert{
		State: rules.StatePending,
		Labels: labels.FromStrings(
			"alertname", "HighCPU",
			"alert_rule_id", "42",
			"severity", "warning",
		),
		ActiveAt: time.Now().Add(-10 * time.Second),
	}

	svc.notify(context.Background(), "", alert)

	// PostAlerts must never be called for pending alerts — no goroutine is
	// started, so it is safe to assert immediately without waiting.
	assert.Nil(t, poster.getCaptured())
}

func TestNotifyInvalidRuleIDIsSkipped(t *testing.T) {
	poster := newCapturingPoster()
	svc := newTestService(poster)

	tests := []struct {
		name   string
		labels labels.Labels
	}{
		{
			name: "missing alert_rule_id",
			labels: labels.FromStrings(
				"alertname", "HighCPU",
				"severity", "critical",
			),
		},
		{
			name: "non-numeric alert_rule_id",
			labels: labels.FromStrings(
				"alertname", "HighCPU",
				"alert_rule_id", "abc",
				"severity", "critical",
			),
		},
		{
			name: "zero alert_rule_id",
			labels: labels.FromStrings(
				"alertname", "HighCPU",
				"alert_rule_id", "0",
				"severity", "critical",
			),
		},
		{
			name: "negative alert_rule_id",
			labels: labels.FromStrings(
				"alertname", "HighCPU",
				"alert_rule_id", "-1",
				"severity", "critical",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := &rules.Alert{
				State:    rules.StateFiring,
				Labels:   tt.labels,
				ActiveAt: time.Now(),
			}

			svc.notify(context.Background(), "", alert)

			// All alerts in this test have invalid rule IDs — they are filtered
			// before any goroutine is started, so asserting immediately is safe.
			assert.Nil(t, poster.getCaptured())
		})
	}
}

func TestNotifyMixedBatch(t *testing.T) {
	poster := newCapturingPoster()
	svc := newTestService(poster)

	now := time.Now()
	resolvedAt := now.Add(-10 * time.Second)

	firingAlert := &rules.Alert{
		State: rules.StateFiring,
		Labels: labels.FromStrings(
			"alertname", "HighCPU",
			"alert_rule_id", "1",
			"severity", "critical",
		),
		ActiveAt: now.Add(-5 * time.Minute),
	}

	resolvedAlert := &rules.Alert{
		State: rules.StateInactive,
		Labels: labels.FromStrings(
			"alertname", "HighMemory",
			"alert_rule_id", "2",
			"severity", "warning",
		),
		ActiveAt:   now.Add(-10 * time.Minute),
		ResolvedAt: resolvedAt,
	}

	pendingAlert := &rules.Alert{
		State: rules.StatePending,
		Labels: labels.FromStrings(
			"alertname", "HighDisk",
			"alert_rule_id", "3",
			"severity", "info",
		),
		ActiveAt: now.Add(-5 * time.Second),
	}

	invalidRuleAlert := &rules.Alert{
		State: rules.StateFiring,
		Labels: labels.FromStrings(
			"alertname", "NoRuleID",
			"severity", "critical",
		),
		ActiveAt: now,
	}

	svc.notify(context.Background(), "", firingAlert, resolvedAlert, pendingAlert, invalidRuleAlert)
	poster.waitCalled(t)

	captured := poster.getCaptured()
	require.Len(t, captured, 2)

	// First should be the firing alert.
	assert.Equal(t, "HighCPU", captured[0].Alert.Labels["alertname"])
	assert.Equal(t, "1", captured[0].Alert.Labels["alert_rule_id"])
	assert.True(t, time.Time(captured[0].EndsAt).After(time.Now()), "EndsAt should be in the future for firing alerts")

	// Second should be the resolved alert.
	assert.Equal(t, "HighMemory", captured[1].Alert.Labels["alertname"])
	assert.Equal(t, "2", captured[1].Alert.Labels["alert_rule_id"])
	assert.Equal(t, strfmt.DateTime(resolvedAt), captured[1].EndsAt)
}

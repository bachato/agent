package evaluator

import (
	"context"
	"strconv"
	"time"

	"github.com/go-openapi/strfmt"
	pkgmetrics "github.com/portainer/portainer/pkg/metrics"
	alertmanagermodels "github.com/prometheus/alertmanager/api/v2/models"
	promLabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/rules"
	"github.com/rs/zerolog/log"
)

// notify is called by the rules.Manager when alerts change state.
// Both firing and resolved (inactive) alerts are forwarded to the Portainer
// server as standard Alertmanager PostableAlerts.
func (s *Service) notify(_ context.Context, _ string, alerts ...*rules.Alert) {
	log.Debug().Int("total_alerts", len(alerts)).Msg("evaluator: notify callback triggered")

	var postableAlerts alertmanagermodels.PostableAlerts

	for _, a := range alerts {
		log.Debug().
			Str("alert", a.Labels.Get("alertname")).
			Str("state", a.State.String()).
			Str("alert_rule_id", a.Labels.Get(pkgmetrics.AlertRuleIDLabel)).
			Str("severity", a.Labels.Get("severity")).
			Msg("evaluator: processing alert from rules manager")

		// Only forward firing and resolved alerts — skip pending.
		if a.State != rules.StateFiring && a.State != rules.StateInactive {
			continue
		}

		ruleIDStr := a.Labels.Get(pkgmetrics.AlertRuleIDLabel)
		ruleID, err := strconv.Atoi(ruleIDStr)
		if err != nil || ruleID <= 0 {
			log.Warn().Str("alert_rule_id", ruleIDStr).Msg("evaluator: skipping alert with invalid rule ID")
			continue
		}

		labels := make(alertmanagermodels.LabelSet, a.Labels.Len())
		a.Labels.Range(func(l promLabels.Label) { labels[l.Name] = l.Value })

		annotations := make(alertmanagermodels.LabelSet, a.Annotations.Len())
		a.Annotations.Range(func(l promLabels.Label) { annotations[l.Name] = l.Value })

		pa := &alertmanagermodels.PostableAlert{
			Alert:       alertmanagermodels.Alert{Labels: labels},
			Annotations: annotations,
			StartsAt:    strfmt.DateTime(a.ActiveAt),
		}
		switch a.State {
		case rules.StateFiring:
			pa.EndsAt = strfmt.DateTime(time.Now().Add(4 * s.scrapeInterval))
		case rules.StateInactive:
			if !a.ResolvedAt.IsZero() {
				pa.EndsAt = strfmt.DateTime(a.ResolvedAt)
			}
		}

		postableAlerts = append(postableAlerts, pa)

		log.Debug().
			Int("rule_id", ruleID).
			Str("state", a.State.String()).
			Msg("evaluator: alert added to batch")
	}

	if len(postableAlerts) == 0 {
		log.Debug().Msg("evaluator: no alerts to forward in this notify call")
		return
	}

	log.Debug().
		Int("alert_count", len(postableAlerts)).
		Int("endpoint_id", int(s.endpointID)).
		Msg("evaluator: posting alerts to server")

	// Post asynchronously so a slow or unavailable server does not block the
	// rules.Manager evaluation callback and delay subsequent rule processing.
	// This still starts one goroutine per batch; during prolonged server
	// failures those in-flight sends can accumulate. If that becomes a problem,
	// replace this with a bounded worker or coalescing queue.
	endpointID := s.endpointID
	go func() {
		if err := s.poster.PostAlerts(endpointID, postableAlerts); err != nil {
			log.Warn().Err(err).Int("alert_count", len(postableAlerts)).Msg("evaluator: failed to post alerts")
		} else {
			log.Debug().Int("alert_count", len(postableAlerts)).Msg("evaluator: alerts posted successfully")
		}
	}()
}

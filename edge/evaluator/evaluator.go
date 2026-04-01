package evaluator

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	portainer "github.com/portainer/portainer/api"
	libprom "github.com/portainer/portainer/pkg/libprometheus"
	pkgmetrics "github.com/portainer/portainer/pkg/metrics"
	prometheusreg "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	"github.com/prometheus/prometheus/notifier"
	"github.com/prometheus/prometheus/rules"
	"github.com/prometheus/prometheus/scrape"
	"github.com/rs/zerolog/log"
)

const defaultRuleEvalInterval = 60 * time.Second

// Config holds the configuration for the alert evaluator.
type Config struct {
	DataDir             string
	EndpointID          portainer.EndpointID
	ScrapeTarget        string
	AlertmanagerTarget  string
	AlertmanagerHeaders map[string]string
	ScrapeInterval      time.Duration // default: 60s
	InsecureSkipVerify  bool
}

func (c *Config) scrapeInterval() time.Duration {
	if c.ScrapeInterval > 0 {
		return c.ScrapeInterval
	}
	return defaultRuleEvalInterval
}

// Service embeds a Prometheus TSDB + rules.Manager for local alert rule evaluation.
type Service struct {
	db             *libprom.InMemoryDB
	manager        *rules.Manager
	notifier       *notifier.Manager
	endpointID     portainer.EndpointID
	scrapeInterval time.Duration

	mu            sync.RWMutex
	scrapeManager *scrape.Manager
	scrapeTsets   chan map[string][]*targetgroup.Group
	notifyTsets   chan map[string][]*targetgroup.Group
}

// New opens a TSDB under dataDir and wires up a Prometheus rules.Manager.
func New(cfg Config) (*Service, error) {
	log.Info().
		Int("endpoint_id", int(cfg.EndpointID)).
		Str("data_dir", cfg.DataDir).
		Str("scrape_target", cfg.ScrapeTarget).
		Msg("evaluator.New: creating evaluator")

	if err := ensureDataDir(cfg.DataDir); err != nil {
		return nil, err
	}

	svc := &Service{
		endpointID:     cfg.EndpointID,
		scrapeInterval: cfg.scrapeInterval(),
	}

	// Use a dedicated registerer to avoid collisions with other Prometheus
	// instrumentation in the agent process.
	reg := prometheusreg.NewRegistry()

	db, err := libprom.NewInMemoryTSDB(reg)
	if err != nil {
		return nil, fmt.Errorf("open TSDB: %w", err)
	}
	svc.db = db
	cleanupDB := true
	defer func() {
		if cleanupDB {
			if err := db.Close(); err != nil {
				log.Warn().Err(err).Msg("evaluator.New: error closing TSDB after initialization failure")
			}
		}
	}()
	log.Debug().Msg("evaluator.New: TSDB opened successfully")

	engine := libprom.NewEngine()

	// Write config.yaml — this is both the on-disk artefact and the runtime
	// source of truth: it is loaded back immediately to configure the scrape manager.
	if cfg.ScrapeTarget != "" {
		managers, err := libprom.BootstrapManagerSet(cfg.DataDir, libprom.PrometheusConfigOptions{
			ScrapeInterval:      cfg.scrapeInterval().String(),
			JobName:             "edge-agent",
			ScrapeTarget:        cfg.ScrapeTarget,
			AlertmanagerTarget:  cfg.AlertmanagerTarget,
			AlertmanagerHeaders: cfg.AlertmanagerHeaders,
			InsecureSkipVerify:  cfg.InsecureSkipVerify,
		}, db, reg)
		if err != nil {
			return nil, fmt.Errorf("bootstrap prometheus managers: %w", err)
		}

		svc.scrapeManager = managers.ScrapeManager
		svc.scrapeTsets = managers.ScrapeTargetSets
		svc.notifier = managers.NotifierManager
		svc.notifyTsets = managers.NotifierTargetSets
	}

	notifyFunc := rules.NotifyFunc(func(context.Context, string, ...*rules.Alert) {})
	if svc.notifier != nil {
		notifyFunc = rules.SendAlerts(svc.notifier, "")
	}

	svc.manager = libprom.NewRuleManager(libprom.RuleManagerConfig{
		Engine:     engine,
		Queryable:  db,
		Appendable: db,
		NotifyFunc: notifyFunc,
		Context:    context.Background(),
		Registerer: reg,
	})

	log.Info().Msg("evaluator.New: evaluator created successfully")
	cleanupDB = false

	return svc, nil
}

// ensureDataDir creates the alerting data directory if it does not exist.
// The TSDB is in-memory (NewInMemoryTSDB uses a tmpdir); this directory
// holds config.yaml and alerts.yaml only. No metric data persists across
// process restarts.
func ensureDataDir(dataDir string) error {
	if dataDir == "" || dataDir == string(os.PathSeparator) {
		return fmt.Errorf("invalid data dir. path=%q", dataDir)
	}

	return os.MkdirAll(dataDir, 0o750)
}

// Start begins rule evaluation and the scrape manager in the background.
func (s *Service) Start() {
	go s.manager.Run()

	if s.scrapeManager != nil {
		go s.scrapeManager.Run(s.scrapeTsets) //nolint:errcheck // Run returns only on shutdown
	}

	if s.notifier != nil {
		go s.notifier.Run(s.notifyTsets)
	}
}

// Stop halts the scrape manager, rule evaluation, and closes the TSDB.
func (s *Service) Stop() {
	if s.notifier != nil {
		s.notifier.Stop()
	}
	if s.scrapeManager != nil {
		s.scrapeManager.Stop()
	}
	s.manager.Stop()
	if err := s.db.Close(); err != nil {
		log.Warn().Err(err).Msg("evaluator: error closing TSDB")
	}
}

// ReloadRules updates the active rule set from a YAML file on disk.
// Pass an empty string to clear all rules.
func (s *Service) ReloadRules(alertsFilePath string) error {
	log.Debug().
		Str("alerts_file", alertsFilePath).
		Msg("evaluator: reloading rules from file")

	return libprom.ReloadRules(s.manager, s.scrapeInterval, alertsFilePath)
}

// AlertState returns the current evaluation state for each alerting rule
// managed by this evaluator. This is intended for inclusion in the edge
// agent's poll request so the server can track rule health.
func (s *Service) AlertState() []pkgmetrics.EdgeAlertRuleState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.manager == nil {
		return nil
	}

	return libprom.ExtractAlertStates(s.manager)
}

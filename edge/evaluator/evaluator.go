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
	alertmanagermodels "github.com/prometheus/alertmanager/api/v2/models"
	prometheusreg "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/rules"
	"github.com/rs/zerolog/log"
)

const defaultRuleEvalInterval = 60 * time.Second

// AlertPoster can post alerts to the Portainer server.
type AlertPoster interface {
	PostAlerts(endpointID portainer.EndpointID, alerts alertmanagermodels.PostableAlerts) error
}

// Config holds the configuration for the alert evaluator.
type Config struct {
	DataDir        string
	EndpointID     portainer.EndpointID
	Poster         AlertPoster
	ScrapeTarget   string
	ScrapeInterval time.Duration // default: 60s
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
	endpointID     portainer.EndpointID
	poster         AlertPoster
	scrapeInterval time.Duration

	mu           sync.RWMutex
	scrapeTarget string
	scrapeCancel context.CancelFunc
}

// New opens a TSDB under dataDir and wires up a Prometheus rules.Manager.
func New(cfg Config) (*Service, error) {
	log.Info().
		Int("endpoint_id", int(cfg.EndpointID)).
		Str("data_dir", cfg.DataDir).
		Str("scrape_target", cfg.ScrapeTarget).
		Msg("evaluator.New: creating evaluator")

	if err := ensureTSDBDataDir(cfg.DataDir); err != nil {
		return nil, err
	}

	svc := &Service{
		endpointID:     cfg.EndpointID,
		poster:         cfg.Poster,
		scrapeTarget:   cfg.ScrapeTarget,
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
	log.Debug().Msg("evaluator.New: TSDB opened successfully")

	engine := libprom.NewEngine()

	svc.manager = libprom.NewRuleManager(libprom.RuleManagerConfig{
		Engine:     engine,
		Queryable:  db,
		Appendable: db,
		NotifyFunc: svc.notify,
		Context:    context.Background(),
		Registerer: reg,
	})

	// Write a config.yaml for inspectability.
	if err := libprom.WritePrometheusConfig(cfg.DataDir, cfg.scrapeInterval().String(), "edge-agent", cfg.ScrapeTarget); err != nil {
		log.Warn().Err(err).Msg("evaluator.New: failed to write prometheus config.yaml (non-fatal)")
	}

	log.Info().Msg("evaluator.New: evaluator created successfully")

	return svc, nil
}

// ensureTSDBDataDir creates the data directory if it does not exist.
// The TSDB itself is in-memory (NewInMemoryTSDB uses a tmpdir); this
// directory holds on-disk config artefacts only. No metric data persists
// across process restarts.
func ensureTSDBDataDir(dataDir string) error {
	if dataDir == "" || dataDir == string(os.PathSeparator) {
		return fmt.Errorf("invalid TSDB data dir. path=%q", dataDir)
	}

	return os.MkdirAll(dataDir, 0o750)
}

// Start begins rule evaluation and the scrape loop in the background.
func (s *Service) Start() {
	go s.manager.Run()

	if s.scrapeTarget != "" {
		ctx, cancel := context.WithCancel(context.Background())
		s.scrapeCancel = cancel
		go s.startScrapeLoop(ctx, s.scrapeInterval)
	}
}

// Stop halts evaluation, scrape loop, and closes the TSDB.
func (s *Service) Stop() {
	if s.scrapeCancel != nil {
		s.scrapeCancel()
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

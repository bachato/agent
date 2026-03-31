package evaluator

import (
	"context"
	"io"
	"net/http"
	"time"

	libprom "github.com/portainer/portainer/pkg/libprometheus"
	"github.com/rs/zerolog/log"
)

// scrapeHTTPClient is used for scraping the local metrics endpoint.
// It has an explicit timeout to prevent goroutine leaks from stuck targets.
var scrapeHTTPClient = &http.Client{Timeout: 30 * time.Second}

// startScrapeLoop periodically scrapes the local metrics endpoint and appends
// samples to the TSDB. It exits when ctx is cancelled.
//
// Note: this custom scrape path does not emit Prometheus stale markers when a
// series disappears from the published snapshot. If a metric vanishes (e.g. a
// resource type becomes unavailable), the previous sample remains queryable
// until TSDB lookback expiry (~5 min). During that window a firing alert may
// continue to fire, or a resolving alert may briefly re-fire against stale data.
func (s *Service) startScrapeLoop(ctx context.Context, interval time.Duration) {
	log.Info().
		Str("target", s.scrapeTarget).
		Dur("interval", interval).
		Msg("evaluator: starting scrape loop")

	// Fire immediately on first tick.
	s.scrape(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Debug().Msg("evaluator: scrape loop context cancelled")
			return
		case <-ticker.C:
			s.scrape(ctx)
		}
	}
}

func (s *Service) scrape(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.scrapeTarget, nil)
	if err != nil {
		log.Warn().Err(err).Msg("evaluator: failed to create scrape request")
		return
	}

	resp, err := scrapeHTTPClient.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("evaluator: scrape request failed")
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		if err := resp.Body.Close(); err != nil {
			log.Warn().Err(err).Msg("evaluator: failed to close scrape response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		log.Warn().Int("status", resp.StatusCode).Msg("evaluator: scrape returned non-200")
		return
	}

	if err := libprom.AppendExpositionSamples(ctx, s.db, resp.Body); err != nil {
		log.Warn().Err(err).Msg("evaluator: failed to append scraped samples")
	}
}

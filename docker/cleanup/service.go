package cleanup

import (
	"context"
	"sync"
	"time"

	"github.com/portainer/agent/constants"
	agentdocker "github.com/portainer/agent/docker"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/portainer/portainer/api/logs"
	"github.com/rs/zerolog/log"
)

const (
	cleanupServiceStartContext    = "DockerCleanupServiceStart"
	cleanupServiceLoopContext     = "DockerCleanupServiceLoop"
	cleanupServiceRunCycleContext = "DockerCleanupServiceRunCycle"
)

// CleanupStatusSnapshot is a point-in-time view of what the last cleanup
// cycle did.
type CleanupStatusSnapshot struct {
	LastRun              time.Time `json:"lastRun"`
	OldImagesRemoved     int       `json:"oldImagesRemoved"`
	SpaceImagesRemoved   int       `json:"spaceImagesRemoved"`
	BytesFreed           int64     `json:"bytesFreed"`
	BuildCacheBytesFreed int64     `json:"buildCacheBytesFreed,omitempty"`
	DiskUsedPercent      float64   `json:"diskUsedPercent"`
	Errors               []string  `json:"errors,omitempty"`
}

// CleanupService is a background service that periodically garbage-collects
// Docker images.  It runs two cleanup phases per interval:
//
//  1. Old-image cleanup: removes any image older than ImageMaximumAgeForGC,
//     regardless of disk usage.
//  2. Storage-limit cleanup: when disk usage exceeds
//     CleanupStartHostThresholdPercent it removes images aged >= MinAge,
//     stopping when usage drops below CleanupEndHostThresholdPercent or
//     MaximumImagesPerInterval is reached.
//
// Both phases respect the configured exclusion lists and never remove images
// that are in use by any container (running or stopped).
type CleanupService struct {
	cfg             Config
	newClient       agentdocker.CleanupClientFactory
	statfs          func(string) (float64, error)
	imageLayersSize func(ctx context.Context, cli agentdocker.CleanupClient) int64

	compiledImages []string // pre-normalised ExcludedImages patterns

	cycleMu     sync.Mutex // guards runCycle; TryLock drops concurrent ticks
	lifecycleMu sync.Mutex // guards Start/Stop idempotency
	statusMu    sync.RWMutex
	snapshot    CleanupStatusSnapshot

	cancel context.CancelFunc
}

// NewCleanupService constructs a CleanupService with the given config.
// Exclusion patterns are compiled once at construction time. Injectable
// dependencies (newClient, statfs, imageLayersSize) default to production
// implementations and can be overridden in tests.
func NewCleanupService(cfg Config) *CleanupService {
	return &CleanupService{
		cfg:             cfg,
		newClient:       agentdocker.NewCleanupClient,
		statfs:          diskUsagePercent,
		imageLayersSize: dockerImageLayersSize,
		compiledImages:  parseExclusionPatterns(cfg.ExcludedImages),
	}
}

// Start launches the background ticker loop. The first cycle fires after one
// full CleanupInterval — this is intentional to avoid a burst of cleanup work
// immediately after a rolling restart.  Calling Start on an already-running
// service is a no-op.
func (s *CleanupService) Start(ctx context.Context) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.cancel != nil {
		return
	}

	if s.cfg.CleanupInterval.Std() <= 0 {
		log.Error().
			Str("context", cleanupServiceStartContext).
			Dur("cleanup_interval", s.cfg.CleanupInterval.Std()).
			Msg("Docker cleanup service refused to start due to a non-positive cleanup interval")
		return
	}

	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go s.loop(loopCtx)
}

// Stop cancels the ticker loop. Any cleanup cycle that is currently in
// progress will run to completion before the goroutine exits.
func (s *CleanupService) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.cancel == nil {
		return
	}
	s.cancel()
	s.cancel = nil
}

// Snapshot returns the most recent cycle status. Safe to call concurrently.
func (s *CleanupService) Snapshot() CleanupStatusSnapshot {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.snapshot
}

// seedSnapshot pre-loads a snapshot from a previous service instance so that
// status reporting shows the last known state immediately after reconfiguration,
// rather than reverting to "waiting for first cycle". Must be called before Start.
func (s *CleanupService) seedSnapshot(snap CleanupStatusSnapshot) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.snapshot = snap
}

// loop is the ticker goroutine.
func (s *CleanupService) loop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.CleanupInterval.Std())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.cycleMu.TryLock() {
				// A previous cycle is still running (slow daemon or large number of
				// images). Drop this tick rather than queue work behind it.
				log.Debug().
					Str("context", cleanupServiceLoopContext).
					Msg("Docker cleanup previous cycle is still in progress, skipping tick")
				continue
			}
			s.runCycle(ctx)
			s.cycleMu.Unlock()
		}
	}
}

// runCycle executes one full cleanup pass (old-image cleanup + storage-limit cleanup).
// It is safe to call directly in tests. The caller must hold cycleMu.
func (s *CleanupService) runCycle(ctx context.Context) {
	log.Debug().
		Str("context", cleanupServiceRunCycleContext).
		Msg("Starting cleanup cycle")

	cli, err := s.newClient()
	if err != nil {
		log.Error().
			Err(err).
			Str("context", cleanupServiceRunCycleContext).
			Msg("Failed to create Docker cleanup client")
		return
	}
	defer logs.CloseAndLogErr(cli)

	cycleErrors := []string{}

	beforeLayersSize := s.imageLayersSize(ctx, cli)

	// Build in-use index from ALL containers (running + stopped).
	// image.Summary.Containers is -1 by default (not calculated), so we
	// must derive the in-use set from ContainerList.
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		log.Error().
			Err(err).
			Str("context", cleanupServiceRunCycleContext).
			Msg("Failed to list containers for Docker cleanup")
		return
	}
	inUse := make(map[string]bool, len(containers))
	for _, c := range containers {
		inUse[c.ImageID] = true
	}

	imgs, err := cli.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		log.Error().
			Err(err).
			Str("context", cleanupServiceRunCycleContext).
			Msg("Failed to list images for Docker cleanup")
		return
	}

	now := time.Now()

	// ── Old-image cleanup: forced age-based removal ─────────────────────────────
	alreadyRemoved := make(map[string]bool)
	oldImagesRemoved := 0
	if s.cfg.CleanOldImages {
		oldImages := oldImageCandidates(now, imgs, inUse, s.compiledImages, s.cfg.ImageMaximumAgeForGC.Std())
		alreadyRemoved = make(map[string]bool, len(oldImages))
		for _, img := range oldImages {
			if _, err := cli.ImageRemove(ctx, img.ID, image.RemoveOptions{Force: true, PruneChildren: true}); err != nil {
				log.Warn().
					Err(err).
					Str("context", cleanupServiceRunCycleContext).
					Str("image_id", img.ID).
					Msg("Old-image cleanup failed to remove image")
				cycleErrors = append(cycleErrors, "oldImage "+img.ID+": "+err.Error())
				continue
			}
			alreadyRemoved[img.ID] = true
			oldImagesRemoved++
		}
		log.Info().
			Int("removed", oldImagesRemoved).
			Str("context", cleanupServiceRunCycleContext).
			Msg("Old-image cleanup completed")
	}

	// ── Storage-limit cleanup: threshold-driven 'make space' ─────────────────────
	if !s.cfg.CleanImagesAtStorageLimit {
		s.recordSnapshot(now, oldImagesRemoved, 0, layerBytesFreed(beforeLayersSize, s.imageLayersSize(ctx, cli)), 0, 0, cycleErrors)
		return
	}

	usedPct, err := s.statfs(constants.SystemVolumePath)
	if err != nil {
		log.Error().
			Err(err).
			Str("context", cleanupServiceRunCycleContext).
			Str("disk_path", constants.SystemVolumePath).
			Msg("Failed to read disk usage for storage-limit cleanup, skipping")
		// Old-image cleanup already ran — record its results even if storage-limit cleanup is aborted.
		s.recordSnapshot(now, oldImagesRemoved, 0, layerBytesFreed(beforeLayersSize, s.imageLayersSize(ctx, cli)), 0, 0, cycleErrors)
		return
	}

	if usedPct < s.cfg.CleanupStartHostThresholdPercent {
		log.Debug().
			Float64("disk_used_pct", usedPct).
			Float64("threshold", s.cfg.CleanupStartHostThresholdPercent).
			Str("context", cleanupServiceRunCycleContext).
			Msg("Disk usage is below the storage-limit cleanup threshold, skipping")
		s.recordSnapshot(now, oldImagesRemoved, 0, layerBytesFreed(beforeLayersSize, s.imageLayersSize(ctx, cli)), 0, usedPct, cycleErrors)
		return
	}

	// ── Build-cache prune (optional step within storage-limit cleanup) ──────────
	// Runs before image removal so that reclaiming build cache space may
	// bring disk usage below the threshold, avoiding image removal entirely.
	var buildCacheBytesFreed int64
	if s.cfg.ClearBuildCache {
		if report, err := cli.BuildCachePrune(ctx, build.CachePruneOptions{All: true}); err != nil {
			log.Warn().
				Err(err).
				Str("context", cleanupServiceRunCycleContext).
				Msg("Failed to prune Docker build cache during storage-limit cleanup")
			cycleErrors = append(cycleErrors, "buildCache: "+err.Error())
		} else {
			buildCacheBytesFreed = int64(report.SpaceReclaimed) //nolint:gosec // SpaceReclaimed is always non-negative
			log.Info().
				Int64("space_reclaimed", buildCacheBytesFreed).
				Str("context", cleanupServiceRunCycleContext).
				Msg("Docker build cache pruned during storage-limit cleanup")
			// Re-read disk usage; if we're now below threshold, skip image removal.
			if newPct, statErr := s.statfs(constants.SystemVolumePath); statErr == nil {
				usedPct = newPct
			}
		}
	}

	if usedPct < s.cfg.CleanupStartHostThresholdPercent {
		log.Debug().
			Float64("disk_used_pct", usedPct).
			Float64("threshold", s.cfg.CleanupStartHostThresholdPercent).
			Str("context", cleanupServiceRunCycleContext).
			Msg("Disk usage dropped below threshold after build cache prune, skipping image removal")
		s.recordSnapshot(now, oldImagesRemoved, 0, layerBytesFreed(beforeLayersSize, s.imageLayersSize(ctx, cli)), buildCacheBytesFreed, usedPct, cycleErrors)
		return
	}

	spaceImages := clearSpaceCandidates(now, imgs, inUse, s.compiledImages, s.cfg.MinAge.Std(), alreadyRemoved)
	spaceImagesRemoved := 0
	finalUsedPct := usedPct
	for _, img := range spaceImages {
		if s.cfg.MaximumImagesPerInterval > 0 && spaceImagesRemoved >= s.cfg.MaximumImagesPerInterval {
			log.Debug().
				Int("limit", s.cfg.MaximumImagesPerInterval).
				Str("context", cleanupServiceRunCycleContext).
				Msg("Docker cleanup reached MaximumImagesPerInterval, stopping storage-limit cleanup early")
			break
		}

		if _, err := cli.ImageRemove(ctx, img.ID, image.RemoveOptions{Force: true, PruneChildren: true}); err != nil {
			log.Warn().
				Err(err).
				Str("context", cleanupServiceRunCycleContext).
				Str("image_id", img.ID).
				Msg("Storage-limit cleanup failed to remove image")
			cycleErrors = append(cycleErrors, "clearSpace "+img.ID+": "+err.Error())
			continue
		}
		spaceImagesRemoved++

		// Re-check disk usage after each removal so we stop as soon as we
		// drop below the end threshold. syscall.Statfs is < 1ms.
		currentPct, err := s.statfs(constants.SystemVolumePath)
		if err == nil {
			finalUsedPct = currentPct
		}
		if err == nil && currentPct < s.cfg.CleanupEndHostThresholdPercent {
			log.Debug().
				Float64("disk_used_pct", currentPct).
				Float64("end_threshold", s.cfg.CleanupEndHostThresholdPercent).
				Str("context", cleanupServiceRunCycleContext).
				Msg("Disk usage is below the end threshold, stopping storage-limit cleanup early")
			break
		}
	}

	log.Info().
		Int("removed", spaceImagesRemoved).
		Float64("disk_used_pct", finalUsedPct).
		Str("context", cleanupServiceRunCycleContext).
		Msg("Storage-limit cleanup completed")

	s.recordSnapshot(now, oldImagesRemoved, spaceImagesRemoved, layerBytesFreed(beforeLayersSize, s.imageLayersSize(ctx, cli)), buildCacheBytesFreed, finalUsedPct, cycleErrors)
}

func (s *CleanupService) recordSnapshot(now time.Time, oldRemoved, spaceRemoved int, bytesFreed, buildCacheFreed int64, usedPct float64, errs []string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.snapshot = CleanupStatusSnapshot{
		LastRun:              now,
		OldImagesRemoved:     oldRemoved,
		SpaceImagesRemoved:   spaceRemoved,
		BytesFreed:           bytesFreed,
		BuildCacheBytesFreed: buildCacheFreed,
		DiskUsedPercent:      usedPct,
		Errors:               errs,
	}
}

// dockerImageLayersSize returns the total bytes used by all image layers on
// the Docker daemon, using the daemon's own accounting (equivalent to the
// "Images" row in "docker system df"). Returns -1 if the query fails.
func dockerImageLayersSize(ctx context.Context, cli agentdocker.CleanupClient) int64 {
	usage, err := cli.DiskUsage(ctx, dockertypes.DiskUsageOptions{
		Types: []dockertypes.DiskUsageObject{dockertypes.ImageObject},
	})
	if err != nil {
		return -1
	}
	return usage.LayersSize
}

// layerBytesFreed returns how many bytes of image layer storage were freed
// between the before and after snapshots. Returns 0 if either snapshot is
// unavailable (sentinel -1) or if disk usage did not decrease (e.g. another
// process pulled an image during the cycle).
func layerBytesFreed(before, after int64) int64 {
	if before < 0 || after < 0 || after >= before {
		return 0
	}
	return before - after
}


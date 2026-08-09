package recorder

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/sentinel-nvr/sentinel/internal/config"
	"github.com/sentinel-nvr/sentinel/internal/events"
	"github.com/sentinel-nvr/sentinel/internal/storage"
)

// RetentionManager deletes old recording segments AND old events (with their
// snapshot/clip media) on a schedule.
type RetentionManager struct {
	store    *events.Store
	stor     *storage.LocalStorage
	cameras  map[string]config.CameraConfig
	interval time.Duration
}

// NewRetentionManager creates a RetentionManager. interval controls how often
// the cleanup pass runs; 0 defaults to 1 hour. stor may be nil (event-media
// files are then not deleted, but event rows still are).
func NewRetentionManager(store *events.Store, stor *storage.LocalStorage, cameras map[string]config.CameraConfig, interval time.Duration) *RetentionManager {
	if interval <= 0 {
		interval = time.Hour
	}
	return &RetentionManager{
		store:    store,
		stor:     stor,
		cameras:  cameras,
		interval: interval,
	}
}

// Run starts the retention loop. It blocks until ctx is cancelled.
func (rm *RetentionManager) Run(ctx context.Context) {
	ticker := time.NewTicker(rm.interval)
	defer ticker.Stop()

	// Run once immediately at startup.
	rm.runPass(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rm.runPass(ctx)
		}
	}
}

func (rm *RetentionManager) runPass(ctx context.Context) {
	for camera, cfg := range rm.cameras {
		if ctx.Err() != nil {
			return
		}
		days := 3.0
		if cfg.Record != nil && cfg.Record.Retain.Days > 0 {
			days = cfg.Record.Retain.Days
		}
		cutoff := time.Now().Add(-time.Duration(days * float64(24*time.Hour)))
		cutoffSecs := float64(cutoff.UnixMicro()) / 1e6

		// Fetch segments older than cutoff to get their paths before deletion.
		beforeVal := cutoffSecs
		segs, err := rm.store.ListRecordings(ctx, events.RecordingFilter{
			Camera: camera,
			Before: &beforeVal,
			Limit:  1000,
		})
		if err != nil {
			slog.Error("retention: list recordings", "camera", camera, "err", err)
			continue
		}

		// Delete files from disk.
		var deletedPaths int
		for _, seg := range segs {
			if err := os.Remove(seg.Path); err != nil && !os.IsNotExist(err) {
				slog.Warn("retention: remove file", "path", seg.Path, "err", err)
			} else {
				deletedPaths++
			}
		}

		// Delete DB rows.
		n, err := rm.store.DeleteRecordingsBefore(ctx, camera, cutoffSecs)
		if err != nil {
			slog.Error("retention: delete db rows", "camera", camera, "err", err)
			continue
		}

		if n > 0 {
			slog.Info("retention: deleted old recordings",
				"camera", camera,
				"db_rows", n,
				"files", deletedPaths,
				"cutoff_days", days,
			)
		}

		rm.pruneEvents(ctx, camera, cfg)
	}
}

// pruneEvents deletes ended events (and their snapshot/clip files) older than
// the per-camera event retention window. This is what actually keeps the events
// table and snapshot/clip dirs from growing forever — previously nothing did,
// so record.events.retain.days was dead config.
func (rm *RetentionManager) pruneEvents(ctx context.Context, camera string, cfg config.CameraConfig) {
	eventDays := 14.0
	if cfg.Record != nil && cfg.Record.Events.Retain.Days > 0 {
		eventDays = cfg.Record.Events.Retain.Days
	}
	cutoff := time.Now().Add(-time.Duration(eventDays * float64(24*time.Hour)))
	cutoffSecs := float64(cutoff.UnixMicro()) / 1e6

	// First, delete snapshot/clip files for expired media-bearing events and
	// remove exactly those rows (batched). retain_indefinitely events are excluded
	// by the query. Deleting each batch's rows by ID (not a bulk cutoff delete)
	// ensures every media file is handled before the rows disappear.
	var mediaFiles int
	if rm.stor != nil {
		for {
			if ctx.Err() != nil {
				return
			}
			ids, err := rm.store.ListExpiredEventMediaIDs(ctx, camera, cutoffSecs, 5000)
			if err != nil {
				slog.Error("retention: list expired event media", "camera", camera, "err", err)
				break
			}
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				_ = rm.stor.Delete(ctx, rm.stor.SnapshotPath(id))
				_ = rm.stor.Delete(ctx, rm.stor.ClipPath(id))
				mediaFiles++
			}
			if _, err := rm.store.DeleteEventsByIDs(ctx, ids); err != nil {
				slog.Error("retention: delete event media batch", "camera", camera, "err", err)
				break
			}
			if len(ids) < 5000 {
				break
			}
		}
	}

	// Bulk-delete any remaining expired (media-less) event rows.
	n, err := rm.store.DeleteEventsBefore(ctx, camera, cutoffSecs)
	if err != nil {
		slog.Error("retention: delete events", "camera", camera, "err", err)
		return
	}
	if n > 0 || mediaFiles > 0 {
		slog.Info("retention: deleted old events",
			"camera", camera,
			"db_rows", n,
			"media_files", mediaFiles,
			"cutoff_days", eventDays,
		)
	}
}

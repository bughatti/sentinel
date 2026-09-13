// Package recorder watches for new recording segment files, persists them to
// the database, and enforces retention policies.
package recorder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/bughatti/sentinel/internal/events"
	"github.com/google/uuid"
)

// MotionQuerier is satisfied by motion.Manager. Defined here to avoid an
// import cycle between recorder and motion packages.
type MotionQuerier interface {
	WasActiveInRange(camera string, start, end time.Time) bool
}

// Recorder watches segmentCh for new MP4 segment paths and persists them.
// Each segment is stat'd for size and inserted into the recordings table.
type Recorder struct {
	camera    string
	store     *events.Store
	segmentCh <-chan string
	motion    MotionQuerier // nil if motion tracking is disabled
}

// NewRecorder creates a Recorder for one camera.
func NewRecorder(camera string, store *events.Store, segmentCh <-chan string, motion MotionQuerier) *Recorder {
	return &Recorder{
		camera:    camera,
		store:     store,
		segmentCh: segmentCh,
		motion:    motion,
	}
}

// Run processes incoming segment paths until ctx is cancelled.
func (r *Recorder) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-r.segmentCh:
			if !ok {
				return
			}
			if err := r.handleSegment(ctx, path); err != nil {
				slog.Error("recorder: handle segment",
					"camera", r.camera,
					"path", path,
					"err", err,
				)
			}
		}
	}
}

func (r *Recorder) handleSegment(ctx context.Context, path string) error {
	path = filepath.Clean(path)
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	// Infer start_time from file modification time (ffmpeg sets mtime on close).
	// In production a more precise approach is to probe the file with ffprobe.
	endTime := fi.ModTime()
	startTime := endTime.Add(-10 * time.Second) // approximate from segment_time

	hasMotion := r.motion != nil && r.motion.WasActiveInRange(r.camera, startTime, endTime)

	rec := &events.Recording{
		ID:          uuid.New().String(),
		Camera:      r.camera,
		Path:        path,
		StartTime:   float64(startTime.UnixMicro()) / 1e6,
		EndTime:     float64(endTime.UnixMicro()) / 1e6,
		Duration:    10.0, // segment_time default; overridden by ffprobe if integrated
		SegmentSize: fi.Size(),
		Motion:      hasMotion,
		Objects:     []string{},
	}

	if err := r.store.InsertRecording(ctx, rec); err != nil {
		return fmt.Errorf("insert recording: %w", err)
	}

	slog.Debug("recording segment persisted",
		"camera", r.camera,
		"path", path,
		"size_kb", fi.Size()/1024,
	)
	return nil
}

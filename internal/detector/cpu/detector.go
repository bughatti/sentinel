// Package cpu provides a no-op detector fallback that allows Sentinel NVR to
// run (recording-only mode) when no real detection backend is configured.
// All frames pass through with zero detections. A warning is logged once on
// startup so the operator knows detection is not active.
package cpu

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bughatti/sentinel/internal/camera"
	"github.com/bughatti/sentinel/internal/detector"
)

// Detector is the no-op CPU fallback.
type Detector struct {
	warnOnce sync.Once
}

// New creates a CPU fallback detector.
func New() *Detector {
	return &Detector{}
}

// Detect returns a slice of nil detection slices (no detections) for every
// frame in the batch.
func (d *Detector) Detect(_ context.Context, frames []camera.Frame) ([][]detector.Detection, error) {
	d.warnOnce.Do(func() {
		slog.Warn("CPU fallback detector active — no real detection is running. " +
			"Set detector.type to 'onnx' or 'deepstream' for object detection.")
	})
	results := make([][]detector.Detection, len(frames))
	for i := range results {
		results[i] = nil
	}
	return results, nil
}

// Warmup is a no-op for the CPU fallback.
func (d *Detector) Warmup(_ context.Context) error {
	slog.Info("cpu fallback detector: warmup complete (no-op)")
	return nil
}

// Close is a no-op for the CPU fallback.
func (d *Detector) Close() error { return nil }

// Type returns "cpu".
func (d *Detector) Type() string { return "cpu" }

// Stats reports no processed frames (the fallback does no inference).
func (d *Detector) Stats() (int64, float64) { return 0, 0 }

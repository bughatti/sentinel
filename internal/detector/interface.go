// Package detector defines the Detector interface and Detection type used by
// the Sentinel NVR detection pipeline.
package detector

import (
	"context"
	"image"

	"github.com/bughatti/sentinel/internal/camera"
)

// Detection is the output of an object detector for one detected object.
type Detection struct {
	// Label is the class name, e.g. "person", "car".
	Label string

	// Score is the detection confidence in [0, 1].
	Score float32

	// Box is the bounding box in pixel coordinates at detect resolution.
	Box image.Rectangle

	// TrackID is assigned by the tracker (not the detector) and is 0 until
	// the pipeline assigns it. It remains stable across frames for the same
	// physical object.
	TrackID uint64
}

// Detector is the interface satisfied by all detection backends (ONNX, CPU
// fallback, DeepStream bridge, etc.).
type Detector interface {
	// Detect processes a batch of frames and returns a corresponding slice
	// of detection slices. The outer slice length equals len(frames).
	Detect(ctx context.Context, frames []camera.Frame) ([][]Detection, error)

	// Warmup pre-loads model weights and runs a single dummy inference so
	// the first real call doesn't pay a cold-start latency penalty.
	Warmup(ctx context.Context) error

	// Close releases all resources held by the detector.
	Close() error

	// Type returns a human-readable name for the backend, e.g. "onnx",
	// "cpu", "deepstream".
	Type() string

	// Stats returns the total number of frames the detector has processed and
	// the average inference time per frame in milliseconds (0 if none yet).
	Stats() (frames int64, avgInferenceMs float64)
}

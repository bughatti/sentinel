// Package face implements face detection + recognition: SCRFD (buffalo_l
// detection, 5 landmarks) locates faces, a 5-point similarity transform aligns
// each to 112x112, and ArcFace (buffalo_l w600k_r50) produces a 512-d embedding.
// Embeddings are matched against enrolled identities via pgvector cosine
// similarity (that matching lives in the events.Store; this package is the CV +
// ONNX layer only).
package face

import (
	"errors"
	"image"

	"github.com/bughatti/sentinel/internal/camera"
	"github.com/bughatti/sentinel/internal/config"
)

// ErrDisabled is returned when an operation needs an active recognizer.
var ErrDisabled = errors.New("face recognition is disabled")

// DetectedFace is one detected+embedded face in a frame.
type DetectedFace struct {
	Box       image.Rectangle // in frame pixel coordinates
	Score     float32         // detector confidence
	Embedding []float32       // L2-normalised 512-d ArcFace embedding
}

// Recognizer detects faces in frames and produces embeddings. Matching against
// enrolled identities is done by the caller (via the Store's pgvector query).
type Recognizer interface {
	// Enabled reports whether real recognition is active.
	Enabled() bool

	// DetectAndEmbed finds faces in a frame and returns each with a 512-d
	// embedding. Faces smaller than the configured minimum are skipped.
	DetectAndEmbed(frame *camera.Frame) ([]DetectedFace, error)

	// EmbedLargestFace detects faces in a standalone image (e.g. an event
	// snapshot for enrollment) and returns the embedding + box of the largest.
	EmbedLargestFace(img image.Image) ([]float32, image.Rectangle, error)

	// Close releases model resources.
	Close() error
}

// disabled is the no-op recognizer used when face recognition is off or the
// build has no ONNX support.
type disabled struct{}

func (disabled) Enabled() bool { return false }
func (disabled) DetectAndEmbed(*camera.Frame) ([]DetectedFace, error) {
	return nil, nil
}
func (disabled) EmbedLargestFace(image.Image) ([]float32, image.Rectangle, error) {
	return nil, image.Rectangle{}, ErrDisabled
}
func (disabled) Close() error { return nil }

// Disabled returns a no-op Recognizer.
func Disabled() Recognizer { return disabled{} }

// Config is the subset of settings the recognizer needs.
type Config = config.FaceRecognitionConfig

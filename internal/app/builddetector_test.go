package app

import (
	"path/filepath"
	"testing"

	"github.com/bughatti/sentinel/internal/config"
)

func onnxConfig(t *testing.T, modelPath string) *config.Config {
	t.Helper()
	return &config.Config{
		Detector: config.DetectorConfig{
			Type:       "onnx",
			NumThreads: 1,
			BatchSize:  1,
			ONNX: config.ONNXConfig{
				ModelPath:   modelPath,
				LabelPath:   filepath.Join(t.TempDir(), "labels-that-do-not-exist.txt"),
				Threshold:   0.5,
				InputWidth:  640,
				InputHeight: 640,
			},
		},
	}
}

// A missing model must never take the recorder down with it. Losing detection
// is bad; losing the footage as well is far worse. This pins the fallback so
// nobody restores the crash-loop by accident.
func TestBuildDetectorFallsBackWhenModelMissing(t *testing.T) {
	cfg := onnxConfig(t, filepath.Join(t.TempDir(), "does-not-exist.onnx"))

	d, err := buildDetector(cfg)
	if err != nil {
		t.Fatalf("a missing model must not be fatal, got: %v", err)
	}
	if d == nil {
		t.Fatal("expected a working detector, got nil")
	}
	defer d.Close()
	if got := d.Type(); got != "cpu" {
		t.Errorf("expected the recording-only fallback, got type %q", got)
	}
}

// An unknown backend name is a genuine configuration error, not something to
// paper over, so it should still be reported.
func TestBuildDetectorRejectsUnknownType(t *testing.T) {
	cfg := &config.Config{Detector: config.DetectorConfig{Type: "magic"}}
	if _, err := buildDetector(cfg); err == nil {
		t.Fatal("an unknown detector type should be an error")
	}
}

// The explicit cpu backend must keep working and must never error.
func TestBuildDetectorCPUBackend(t *testing.T) {
	cfg := &config.Config{Detector: config.DetectorConfig{Type: "cpu"}}
	d, err := buildDetector(cfg)
	if err != nil {
		t.Fatalf("cpu backend should not error: %v", err)
	}
	defer d.Close()
	if got := d.Type(); got != "cpu" {
		t.Errorf("type = %q, want cpu", got)
	}
}

//go:build !cgo

package app

import (
	"fmt"
	"log/slog"

	"github.com/bughatti/sentinel/internal/config"
	det "github.com/bughatti/sentinel/internal/detector"
	cpudet "github.com/bughatti/sentinel/internal/detector/cpu"
)

// buildDetector creates the configured detection backend (non-CGO build).
// ONNX is not available in this build; detector.type=onnx will return an error.
func buildDetector(cfg *config.Config) (det.Detector, error) {
	switch cfg.Detector.Type {
	case "onnx":
		slog.Warn("detector: onnx requested but this binary was built without CGO; falling back to cpu")
		return cpudet.New(), nil

	case "cpu", "":
		slog.Info("detector: using cpu fallback (no real detection)")
		return cpudet.New(), nil

	default:
		return nil, fmt.Errorf("unknown detector type %q (valid: onnx, cpu)", cfg.Detector.Type)
	}
}

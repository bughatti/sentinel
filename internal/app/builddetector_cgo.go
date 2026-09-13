//go:build cgo

package app

import (
	"fmt"
	"log/slog"

	"github.com/bughatti/sentinel/internal/config"
	det "github.com/bughatti/sentinel/internal/detector"
	cpudet "github.com/bughatti/sentinel/internal/detector/cpu"
	onnxdet "github.com/bughatti/sentinel/internal/detector/onnx"
)

// buildDetector creates the configured detection backend (CGO build).
func buildDetector(cfg *config.Config) (det.Detector, error) {
	switch cfg.Detector.Type {
	case "onnx":
		d, err := onnxdet.New(
			cfg.Detector.ONNX.ModelPath,
			cfg.Detector.ONNX.LabelPath,
			cfg.Detector.ONNX.Threshold,
			cfg.Detector.NumThreads,
			cfg.Detector.ONNX.InputWidth,
			cfg.Detector.ONNX.InputHeight,
			cfg.Detector.UseGPU,
			cfg.Detector.GPUDeviceID,
			cfg.Detector.UseTensorRT,
			cfg.Detector.BatchSize,
		)
		if err != nil {
			// A missing or unreadable model must not take the recorder down
			// with it. Losing detection is bad; losing the footage as well is
			// far worse, so fall back to recording-only and say so loudly.
			slog.Error("detector: onnx unavailable, continuing in recording-only mode — no object detection",
				"model", cfg.Detector.ONNX.ModelPath, "err", err)
			return cpudet.New(), nil
		}
		slog.Info("detector: onnx loaded", "model", cfg.Detector.ONNX.ModelPath)
		return d, nil

	case "cpu", "":
		slog.Info("detector: using cpu fallback (no real detection)")
		return cpudet.New(), nil

	default:
		return nil, fmt.Errorf("unknown detector type %q (valid: onnx, cpu)", cfg.Detector.Type)
	}
}

//go:build cgo

package app

import (
	"fmt"
	"log/slog"

	"github.com/sentinel-nvr/sentinel/internal/config"
	det "github.com/sentinel-nvr/sentinel/internal/detector"
	cpudet "github.com/sentinel-nvr/sentinel/internal/detector/cpu"
	onnxdet "github.com/sentinel-nvr/sentinel/internal/detector/onnx"
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
			return nil, fmt.Errorf("onnx detector: %w", err)
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

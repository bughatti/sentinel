//go:build !cgo

package face

import (
	"log/slog"

	"github.com/sentinel-nvr/sentinel/internal/config"
)

// New returns the no-op recognizer in non-CGO builds (no ONNX Runtime).
func New(cfg *config.Config) (Recognizer, error) {
	if cfg.FaceRecognition.Enabled {
		slog.Warn("face: recognition requested but this build has no ONNX (cgo) support — disabled")
	}
	return Disabled(), nil
}

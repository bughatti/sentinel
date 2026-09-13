package api

import (
	"net/http"
	"runtime"
	"time"
)

// statsPayload is the /api/stats response.
type statsPayload struct {
	Detection detectionStats         `json:"detection"`
	Cameras   map[string]cameraStats `json:"cameras"`
	Service   serviceStats           `json:"service"`
	Timestamp float64                `json:"timestamp"`
}

type detectionStats struct {
	Enabled        bool    `json:"detection_enabled"`
	TotalFrames    int64   `json:"total_frames"`
	TotalDetTime   float64 `json:"total_detection_time"`
	DetectionFPS   float32 `json:"detection_fps"`
	AvgInferenceMs float32 `json:"avg_inference_speed"`
}

type cameraStats struct {
	CameraFPS  float32 `json:"camera_fps"`
	DetectFPS  float32 `json:"detect_fps"`
	ProcessFPS float32 `json:"process_fps"`
	SkippedFPS float32 `json:"skipped_fps"`
}

type serviceStats struct {
	Uptime    float64 `json:"uptime"`
	Version   string  `json:"version"`
	GoVersion string  `json:"go_version"`
	NumCPU    int     `json:"num_cpu"`
	NumGo     int     `json:"num_goroutines"`
}

var startTime = time.Now()

// handleStats — GET /api/stats
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	cameras := s.cameras.Cameras()
	cams := make(map[string]cameraStats, len(cameras))
	var totalDetectFPS float32
	for _, name := range cameras {
		capFPS, detFPS, skipFPS, _, ok := s.cameras.CameraRuntime(name)
		if !ok {
			cams[name] = cameraStats{}
			continue
		}
		cams[name] = cameraStats{
			CameraFPS:  round1(capFPS),
			DetectFPS:  round1(detFPS),
			ProcessFPS: round1(detFPS),
			SkippedFPS: round1(skipFPS),
		}
		totalDetectFPS += detFPS
	}

	det := detectionStats{Enabled: true, DetectionFPS: round1(totalDetectFPS)}
	if s.detector != nil {
		frames, avgMs := s.detector.Stats()
		det.TotalFrames = frames
		det.AvgInferenceMs = round1(float32(avgMs))
		det.TotalDetTime = avgMs * float64(frames) / 1000.0
	}

	payload := statsPayload{
		Detection: det,
		Cameras:   cams,
		Service: serviceStats{
			Uptime:    time.Since(startTime).Seconds(),
			Version:   s.version,
			GoVersion: runtime.Version(),
			NumCPU:    runtime.NumCPU(),
			NumGo:     runtime.NumGoroutine(),
		},
		Timestamp: float64(time.Now().UnixMicro()) / 1e6,
	}
	writeJSON(w, http.StatusOK, payload)
}

func round1(f float32) float32 {
	return float32(int(f*10+0.5)) / 10
}

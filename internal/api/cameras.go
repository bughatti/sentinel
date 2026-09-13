package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/bughatti/sentinel/internal/events"
)

// cameraInfo is the response shape for camera endpoints.
type cameraInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Width   int    `json:"detect_width"`
	Height  int    `json:"detect_height"`
	FPS     int    `json:"detect_fps"`
	Online  bool   `json:"online"`
}

// handleListCameras — GET /api/cameras
func (s *Server) handleListCameras(w http.ResponseWriter, r *http.Request) {
	cameras := s.cameras.Cameras()
	infos := make([]cameraInfo, 0, len(cameras))
	for _, name := range cameras {
		infos = append(infos, s.buildCameraInfo(name))
	}
	writeJSON(w, http.StatusOK, infos)
}

// handleGetCamera — GET /api/cameras/{name}
func (s *Server) handleGetCamera(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	found := false
	for _, c := range s.cameras.Cameras() {
		if c == name {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "camera not found")
		return
	}
	writeJSON(w, http.StatusOK, s.buildCameraInfo(name))
}

// handleLatestFrame — GET /api/cameras/{name}/latest-frame
// Returns a live preview JPEG from the running capture stream, falling back to
// the most recent event snapshot if no live frame is available yet.
func (s *Server) handleLatestFrame(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Try live cached frame first (zero-cost — no new FFmpeg process).
	if jpg := s.cameras.LatestFrame(name); jpg != nil {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jpg)
		return
	}

	// Fall back to most-recent event snapshot.
	hasSnap := true
	evts, err := s.store.ListEvents(r.Context(), events.EventFilter{
		Camera:      name,
		HasSnapshot: &hasSnap,
		Limit:       1,
	})
	if err == nil && len(evts) > 0 {
		path := s.storage.SnapshotPath(evts[0].ID)
		serveFile(w, r, path, "image/jpeg")
		return
	}

	writeError(w, http.StatusServiceUnavailable, "frame unavailable")
}

func (s *Server) buildCameraInfo(name string) cameraInfo {
	cfg, ok := s.cameras.CameraConfig(name)
	if !ok {
		return cameraInfo{Name: name}
	}
	_, _, _, online, _ := s.cameras.CameraRuntime(name)
	return cameraInfo{
		Name:    name,
		Enabled: cfg.Detect.Enabled,
		Width:   cfg.Detect.Width,
		Height:  cfg.Detect.Height,
		FPS:     cfg.Detect.FPS,
		Online:  online,
	}
}

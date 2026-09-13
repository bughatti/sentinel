package api

import (
	"context"
	"embed"
	"fmt"
	"image"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/bughatti/sentinel/internal/config"
	"github.com/bughatti/sentinel/internal/db"
	"github.com/bughatti/sentinel/internal/events"
	"github.com/bughatti/sentinel/internal/storage"
)

//go:embed webdist
var webFS embed.FS

// Server is the Sentinel HTTP API server.
type Server struct {
	cfg      config.APIConfig
	fullCfg  *config.Config
	router   chi.Router
	httpSrv  *http.Server
	db       *db.DB
	store    *events.Store
	bus      *events.EventBus
	storage  *storage.LocalStorage
	cameras  cameraStater
	detector detectorStater
	faces    faceEnroller
	version  string
}

// cameraStater is satisfied by camera.Manager (interface to break import cycle).
type cameraStater interface {
	Cameras() []string
	CameraConfig(name string) (config.CameraConfig, bool)
	DetectURL(name string) string
	LatestFrame(name string) []byte
	CameraRuntime(name string) (capFPS, detFPS, skipFPS float32, online, ok bool)
}

// detectorStater exposes detector metrics for /api/stats.
type detectorStater interface {
	Stats() (frames int64, avgInferenceMs float64)
	Type() string
}

// faceEnroller is the face-recognition capability the API needs (enrollment).
type faceEnroller interface {
	Enabled() bool
	EmbedLargestFace(img image.Image) ([]float32, image.Rectangle, error)
}

// New creates a Server and registers all routes. Call Start() to listen.
func New(
	cfg config.APIConfig,
	fullCfg *config.Config,
	database *db.DB,
	store *events.Store,
	bus *events.EventBus,
	stor *storage.LocalStorage,
	cameras cameraStater,
	detector detectorStater,
	faces faceEnroller,
	version string,
) *Server {
	s := &Server{
		cfg:      cfg,
		fullCfg:  fullCfg,
		db:       database,
		store:    store,
		bus:      bus,
		storage:  stor,
		cameras:  cameras,
		detector: detector,
		faces:    faces,
		version:  version,
	}
	s.router = s.buildRouter()
	s.httpSrv = &http.Server{
		Addr:         cfg.Listen,
		Handler:      s.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 = no timeout (needed for streaming)
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Start begins listening. It blocks until the server exits.
func (s *Server) Start() error {
	slog.Info("api server listening", "addr", s.cfg.Listen)
	if s.cfg.TLS.Enabled {
		return s.httpSrv.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
	}
	return s.httpSrv.ListenAndServe()
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	slog.Info("api server shutting down")
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()

	// Global middleware stack.
	r.Use(requestIDMiddleware)
	r.Use(loggingMiddleware)
	r.Use(corsMiddleware(s.cfg.CORSOrigins))
	r.Use(middleware.Recoverer)
	if s.cfg.AuthEnabled {
		r.Use(authMiddleware(s.cfg.APIKey))
	}

	// Health check — unauthenticated.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	// WebSocket hub.
	hub := newWebSocketHub(s.bus)
	go hub.run(context.Background())
	r.Get("/ws", hub.handleWS)

	// ── REST API ──────────────────────────────────────────────────────────────
	r.Route("/api", func(r chi.Router) {
		r.Get("/stats", s.handleStats)
		r.Get("/version", s.handleVersion)
		r.Get("/config", s.handleConfig)

		// Events.
		r.Get("/events", s.handleListEvents)
		r.Get("/events/summary", s.handleEventsSummary)
		r.Get("/events/{id}", s.handleGetEvent)
		r.Delete("/events/{id}", s.handleDeleteEvent)
		r.Post("/events/{id}/false_positive", s.handleFalsePositive)
		r.Post("/events/{id}/retain", s.handleRetainEvent)
		r.Get("/events/{id}/snapshot.jpg", s.handleEventSnapshot)
		r.Get("/events/{id}/clip.mp4", s.handleEventClip)

		// Recordings.
		r.Get("/recordings", s.handleListRecordings)
		r.Get("/recordings/summary", s.handleRecordingsSummary)
		r.Get("/cameras/{name}/recordings", s.handleCameraRecordings)

		// Cameras.
		r.Get("/cameras", s.handleListCameras)
		r.Get("/cameras/{name}", s.handleGetCamera)
		r.Get("/cameras/{name}/latest-frame", s.handleLatestFrame)

		// Face recognition.
		r.Get("/faces", s.handleListFaces)
		r.Post("/faces/enroll", s.handleEnrollFace)
		r.Delete("/faces/{id}", s.handleDeleteFace)

		// Notifications proxy.
		r.Post("/notifications/proxy", s.handleNotificationProxy)
	})

	// ── VOD / HLS ─────────────────────────────────────────────────────────────
	r.Get("/vod/{year}-{month}-{day}/{hour}/{camera}/index.m3u8", s.handleHLSPlaylist)
	r.Get("/vod/{year}-{month}-{day}/{hour}/{camera}/{segment}", s.handleHLSSegment)

	// ── Clips ─────────────────────────────────────────────────────────────────
	r.Get("/clips/{file}", s.handleServeClip)

	// ── Embedded Web UI ───────────────────────────────────────────────────────
	webSub, err := fs.Sub(webFS, "webdist")
	if err == nil {
		fileServer := http.FileServer(http.FS(webSub))
		r.Handle("/*", fileServer)
	} else {
		slog.Warn("web/dist embed not found — UI disabled", "err", err)
	}

	return r
}

// handleVersion returns the server version.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

// handleConfig returns a secret-free view of the running config for the
// Settings page (passwords / RTSP credentials are never included).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.fullCfg == nil {
		writeJSON(w, http.StatusOK, map[string]string{"note": "config unavailable"})
		return
	}
	c := s.fullCfg

	cams := make(map[string]any, len(c.Cameras))
	for name, cam := range c.Cameras {
		roles := [][]string{}
		for _, in := range cam.FFmpeg.Inputs {
			roles = append(roles, in.Roles) // roles only — never the URL (has creds)
		}
		motionEnabled := c.Motion.Enabled
		if cam.Motion != nil {
			motionEnabled = cam.Motion.Enabled
		}
		cams[name] = map[string]any{
			"enabled":        cam.Detect.Enabled,
			"detect_width":   cam.Detect.Width,
			"detect_height":  cam.Detect.Height,
			"detect_fps":     cam.Detect.FPS,
			"motion_enabled": motionEnabled,
			"input_roles":    roles,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"detector": map[string]any{
			"type":         c.Detector.Type,
			"use_gpu":      c.Detector.UseGPU,
			"num_threads":  c.Detector.NumThreads,
			"model_path":   c.Detector.ONNX.ModelPath,
			"threshold":    c.Detector.ONNX.Threshold,
			"input_width":  c.Detector.ONNX.InputWidth,
			"input_height": c.Detector.ONNX.InputHeight,
		},
		"record": map[string]any{
			"enabled":          c.Record.Enabled,
			"segment_duration": c.Record.SegmentDuration,
			"retain_days":      c.Record.Retain.Days,
			"event_retain_days": c.Record.Events.Retain.Days,
			"pre_capture":      c.Record.Events.PreCapture,
			"post_capture":     c.Record.Events.PostCapture,
		},
		"snapshots": map[string]any{
			"enabled": c.Snapshots.Enabled,
			"bboxes":  c.Snapshots.BBoxes,
			"quality": c.Snapshots.Quality,
		},
		"motion": map[string]any{
			"enabled":               c.Motion.Enabled,
			"contour_area":          c.Motion.ContourArea,
			"lightning_thresh":      c.Motion.LightningThresh,
			"detect_without_motion": c.Motion.DetectWithoutMotion,
		},
		"objects": map[string]any{
			"track": c.Objects.Track,
		},
		"face_recognition": map[string]any{
			"enabled":   c.FaceRecognition.Enabled,
			"threshold": c.FaceRecognition.Threshold,
		},
		"storage": map[string]any{
			"recordings_dir": c.Storage.RecordingsDir,
			"snapshots_dir":  c.Storage.SnapshotsDir,
			"clips_dir":      c.Storage.ClipsDir,
		},
		"mqtt": map[string]any{
			"enabled":      c.MQTT.Enabled,
			"host":         c.MQTT.Host, // host only — never user/password
			"topic_prefix": c.MQTT.TopicPrefix,
		},
		"api": map[string]any{
			"listen":       c.API.Listen,
			"auth_enabled": c.API.AuthEnabled,
		},
		"cameras": cams,
	})
}

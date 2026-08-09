// Package app wires together all Sentinel NVR subsystems and manages the
// application lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/sentinel-nvr/sentinel/internal/api"
	"github.com/sentinel-nvr/sentinel/internal/camera"
	"github.com/sentinel-nvr/sentinel/internal/config"
	"github.com/sentinel-nvr/sentinel/internal/db"
	det "github.com/sentinel-nvr/sentinel/internal/detector"
	"github.com/sentinel-nvr/sentinel/internal/events"
	"github.com/sentinel-nvr/sentinel/internal/face"
	"github.com/sentinel-nvr/sentinel/internal/motion"
	"github.com/sentinel-nvr/sentinel/internal/mqtt"
	"github.com/sentinel-nvr/sentinel/internal/pipeline"
	"github.com/sentinel-nvr/sentinel/internal/recorder"
	"github.com/sentinel-nvr/sentinel/internal/snapshot"
	"github.com/sentinel-nvr/sentinel/internal/storage"
)

// Version is injected at build time via -ldflags.
var Version = "dev"

// App is the root application object. All subsystems are owned here and wired
// together via dependency injection — there is no global state.
type App struct {
	cfg      *config.Config
	db       *db.DB
	stor     *storage.LocalStorage
	cameras  *camera.Manager
	detector det.Detector
	motion   *motion.Manager
	pipeline *pipeline.Manager
	recorder *recorder.Manager
	snapshot *snapshot.Saver
	face     face.Recognizer
	eventBus *events.EventBus
	store    *events.Store
	mqtt     *mqtt.Client
	mqttPub  *mqtt.Publisher
	api      *api.Server
}

// New constructs the App from the loaded config. No goroutines are started here.
func New(cfg *config.Config) (*App, error) {
	a := &App{cfg: cfg}

	// ── Storage ───────────────────────────────────────────────────────────────
	a.stor = storage.NewLocalStorage(
		cfg.Storage.RecordingsDir,
		cfg.Storage.SnapshotsDir,
		cfg.Storage.ClipsDir,
		cfg.Storage.ExportsDir,
		cfg.Storage.TmpDir,
	)
	if err := a.stor.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("app: storage dirs: %w", err)
	}

	// ── Database ──────────────────────────────────────────────────────────────
	ctx := context.Background()
	database, err := db.New(ctx, cfg.Database.URL, cfg.Database.MaxConns)
	if err != nil {
		return nil, fmt.Errorf("app: database: %w", err)
	}
	a.db = database
	a.store = events.NewStore(database)

	// ── Event bus ─────────────────────────────────────────────────────────────
	a.eventBus = events.NewEventBus()

	// ── Detector ──────────────────────────────────────────────────────────────
	detector, err := buildDetector(cfg)
	if err != nil {
		return nil, fmt.Errorf("app: detector: %w", err)
	}
	a.detector = detector

	// ── Camera manager ────────────────────────────────────────────────────────
	a.cameras = camera.NewManager(cfg.Storage.RecordingsDir)
	for name, camCfg := range cfg.Cameras {
		a.cameras.AddCamera(ctx, name, camCfg)
	}

	// ── Snapshot saver ────────────────────────────────────────────────────────
	// Built before the pipelines so it can be injected into them (the pipeline
	// saves the best-frame snapshot when it confirms an event).
	snapQuality := cfg.Snapshots.Quality
	if snapQuality <= 0 {
		snapQuality = 70
	}
	a.snapshot = snapshot.NewSaver(a.stor, a.store, snapQuality, cfg.Snapshots.BBoxes)

	// ── Face recognizer ───────────────────────────────────────────────────────
	// Built before the pipelines (they run it on person events). Returns a no-op
	// recognizer when disabled or unavailable.
	rec, err := face.New(cfg)
	if err != nil {
		slog.Warn("app: face recognizer init failed — continuing without", "err", err)
		rec = face.Disabled()
	}
	a.face = rec

	// ── Pipeline manager ──────────────────────────────────────────────────────
	a.pipeline = pipeline.NewPipelineManager()
	for name, camCfg := range cfg.Cameras {
		p, err := pipeline.NewPipeline(name, camCfg, a.eventBus, a.store, a.snapshot, a.stor, a.face, cfg.FaceRecognition.Threshold)
		if err != nil {
			slog.Warn("app: pipeline init error", "camera", name, "err", err)
			continue
		}
		a.pipeline.Add(name, p)
	}

	// ── Motion manager ────────────────────────────────────────────────────────
	a.motion = motion.NewManager(a.store, cfg.Cameras)

	// ── Recorder manager ──────────────────────────────────────────────────────
	a.recorder = recorder.NewManager(a.store, a.motion)
	for name := range cfg.Cameras {
		a.recorder.AddCamera(ctx, name)
	}

	// ── MQTT ──────────────────────────────────────────────────────────────────
	if cfg.MQTT.Enabled && cfg.MQTT.Host != "" {
		mqttClient, err := mqtt.NewClient(cfg.MQTT)
		if err != nil {
			slog.Warn("app: mqtt connect failed (will continue without MQTT)", "err", err)
		} else {
			a.mqtt = mqttClient
			a.mqttPub = mqtt.NewPublisher(mqttClient)
		}
	}

	// ── API server ────────────────────────────────────────────────────────────
	a.api = api.New(
		cfg.API,
		cfg,
		database,
		a.store,
		a.eventBus,
		a.stor,
		a.cameras,
		a.detector,
		a.face,
		Version,
	)

	return a, nil
}

// Start launches all background goroutines. It returns after starting all
// subsystems; the caller should block on a signal channel and then call Stop.
func (a *App) Start(ctx context.Context) error {
	slog.Info("sentinel NVR starting", "version", Version)

	// Warmup detector.
	if err := a.detector.Warmup(ctx); err != nil {
		slog.Warn("app: detector warmup failed", "err", err)
	}

	// Start camera manager (capture goroutines).
	a.cameras.Start(ctx)

	// Start per-camera motion goroutines.
	for name, w := range a.cameras.Workers() {
		go a.motion.Run(ctx, name, w.MotionCh)
	}

	// Start retention manager (prunes old recordings AND old events + their media).
	ret := recorder.NewRetentionManager(a.store, a.stor, a.cfg.Cameras, time.Hour)
	go ret.Run(ctx)

	// Detection + pipeline loop.
	go a.detectionLoop(ctx)

	// Recording dispatch loop.
	go a.recordingDispatchLoop(ctx)

	// Event bus → MQTT fanout.
	if a.mqttPub != nil {
		go a.mqttFanout(ctx)
	}

	// API server (blocking in its own goroutine).
	go func() {
		if err := a.api.Start(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server error", "err", err)
			os.Exit(1)
		}
	}()

	slog.Info("sentinel NVR started")
	return nil
}

// Stop performs a graceful shutdown of all subsystems.
func (a *App) Stop(ctx context.Context) error {
	slog.Info("sentinel NVR shutting down")

	// Stop HTTP API.
	if err := a.api.Stop(ctx); err != nil {
		slog.Warn("app: api stop error", "err", err)
	}

	// Flush all in-progress events.
	a.pipeline.FlushAll(ctx)

	// Stop camera workers.
	a.cameras.Stop()

	// Wait for recorders to drain.
	a.recorder.Wait()

	// Disconnect MQTT (publishes "offline" LWT).
	if a.mqtt != nil {
		a.mqtt.Disconnect()
	}

	// Close detector.
	if err := a.detector.Close(); err != nil {
		slog.Warn("app: detector close error", "err", err)
	}

	// Close face recognizer.
	if a.face != nil {
		_ = a.face.Close()
	}

	// Close database pool.
	a.db.Close()

	slog.Info("sentinel NVR stopped")
	return nil
}

// detectionLoop reads batches from the camera manager, runs detection, and
// feeds results to the pipeline.
func (a *App) detectionLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-a.cameras.BatchCh:
			if !ok {
				return
			}
			a.processBatch(ctx, batch)
		}
	}
}

func (a *App) processBatch(ctx context.Context, batch []camera.BatchItem) {
	frames := make([]camera.Frame, len(batch))
	for i, item := range batch {
		frames[i] = item.Frame
	}

	detections, err := a.detector.Detect(ctx, frames)
	if err != nil {
		slog.Error("app: detect error", "err", err)
		for _, item := range batch {
			close(item.ResultCh)
		}
		return
	}

	for i, item := range batch {
		var dets []det.Detection
		if i < len(detections) {
			dets = detections[i]
		}

		// Feed to pipeline.
		frame := frames[i]
		a.pipeline.Process(ctx, &frame, dets)

		// Signal the batch item as done.
		close(item.ResultCh)
	}
}

// recordingDispatchLoop routes segment paths from the camera manager to the
// per-camera recorder.
func (a *App) recordingDispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-a.cameras.RecordNotifyCh:
			if !ok {
				return
			}
			cam := cameraFromPath(path)
			if cam == "" {
				slog.Warn("app: cannot determine camera for segment", "path", path)
				continue
			}
			a.recorder.Dispatch(cam, path)
		}
	}
}

// mqttFanout subscribes to the event bus and publishes all events via MQTT.
func (a *App) mqttFanout(ctx context.Context) {
	ch := make(chan events.Event, 256)
	a.eventBus.Subscribe(ch)
	defer a.eventBus.Unsubscribe(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			a.mqttPub.PublishEvent(e)
		}
	}
}

// cameraFromPath extracts a camera name from a recording segment path.
// Expected convention: .../recordings/<camera>/<date>/<hour>/<file>.mp4
func cameraFromPath(path string) string {
	parts := splitPath(path)
	for i, p := range parts {
		if p == "recordings" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func splitPath(path string) []string {
	var parts []string
	start := 0
	for i, c := range path {
		if c == '/' || c == 92 {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}

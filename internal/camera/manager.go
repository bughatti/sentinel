package camera

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bughatti/sentinel/internal/config"
)

const (
	// batchFlushInterval is the maximum time to wait before flushing an
	// incomplete batch to the detector.
	batchFlushInterval = 100 * time.Millisecond
)

// Manager owns all CameraWorkers and the shared fanin channel. It assembles
// per-camera frames into batches for the detector pipeline.
type Manager struct {
	mu      sync.RWMutex
	workers map[string]*CameraWorker

	// fanin receives BatchItems from all workers.
	fanin chan BatchItem

	// BatchCh is the output: slices of BatchItems ready for the detector.
	// The batch assembler goroutine flushes every batchFlushInterval or when
	// the batch length reaches the number of enabled cameras.
	BatchCh chan []BatchItem

	// RecordNotifyCh aggregates segment paths from all camera workers.
	RecordNotifyCh chan string

	recordingsDir string

	startedAt time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager creates a Manager. recordingsDir is the base directory for
// recording segments (e.g. "/recordings"). Start() must be called before
// it processes frames.
func NewManager(recordingsDir string) *Manager {
	return &Manager{
		workers:        make(map[string]*CameraWorker),
		fanin:          make(chan BatchItem, 256),
		BatchCh:        make(chan []BatchItem, 32),
		RecordNotifyCh: make(chan string, 128),
		recordingsDir:  recordingsDir,
	}
}

// Start launches the batch-assembly goroutine and all currently-configured workers.
func (m *Manager) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.startedAt = time.Now()

	m.wg.Add(1)
	go m.batchAssembler(ctx)

	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, w := range m.workers {
		w.Start(ctx)
		m.wg.Add(1)
		go m.forwardRecordNotify(ctx, w)
	}
}

// Stop cancels the context and waits for all goroutines to exit.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.RLock()
	for _, w := range m.workers {
		w.Stop()
	}
	m.mu.RUnlock()
	m.wg.Wait()
}

// UpdateConfig performs a diff between the current workers and the new camera
// config, stopping removed cameras and starting new ones. Cameras whose
// config has not changed are left untouched.
func (m *Manager) UpdateConfig(ctx context.Context, cameras map[string]config.CameraConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop workers for cameras that have been removed.
	for name, w := range m.workers {
		if _, ok := cameras[name]; !ok {
			slog.Info("camera removed — stopping worker", "camera", name)
			w.Stop()
			delete(m.workers, name)
		}
	}

	// Start workers for new cameras.
	for name, cfg := range cameras {
		if _, exists := m.workers[name]; !exists {
			slog.Info("camera added — starting worker", "camera", name)
			w := NewCameraWorker(name, cfg, m.fanin, m.recordingsDir)
			w.Start(ctx)
			m.workers[name] = w
			m.wg.Add(1)
			go m.forwardRecordNotify(ctx, w)
		}
	}
}

// AddCamera adds a single camera worker. Idempotent for existing cameras.
func (m *Manager) AddCamera(ctx context.Context, name string, cfg config.CameraConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.workers[name]; exists {
		return
	}
	w := NewCameraWorker(name, cfg, m.fanin, m.recordingsDir)
	w.Start(ctx)
	m.workers[name] = w
	m.wg.Add(1)
	go m.forwardRecordNotify(ctx, w)
}

// Workers returns a snapshot copy of the current worker map.
func (m *Manager) Workers() map[string]*CameraWorker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*CameraWorker, len(m.workers))
	for name, w := range m.workers {
		out[name] = w
	}
	return out
}

// Cameras returns a snapshot of the current worker names.
func (m *Manager) Cameras() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.workers))
	for name := range m.workers {
		names = append(names, name)
	}
	return names
}

// CameraConfig returns the config for a named camera, or false if not found.
func (m *Manager) CameraConfig(name string) (config.CameraConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workers[name]
	if !ok {
		return config.CameraConfig{}, false
	}
	return w.cfg, true
}

// DetectURL returns the detect-role RTSP URL for a camera.
func (m *Manager) DetectURL(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workers[name]
	if !ok {
		return ""
	}
	return w.detectURL()
}

// CameraRuntime returns average FPS since start (capture / detect / skipped) and
// whether the camera is currently online (produced a frame in the last 15s).
func (m *Manager) CameraRuntime(name string) (capFPS, detFPS, skipFPS float32, online bool, ok bool) {
	m.mu.RLock()
	w, exists := m.workers[name]
	started := m.startedAt
	m.mu.RUnlock()
	if !exists {
		return 0, 0, 0, false, false
	}
	captured, detect, skipped, lastNano := w.Counts()
	uptime := time.Since(started).Seconds()
	if uptime < 1 {
		uptime = 1
	}
	capFPS = float32(float64(captured) / uptime)
	detFPS = float32(float64(detect) / uptime)
	skipFPS = float32(float64(skipped) / uptime)
	online = lastNano > 0 && time.Since(time.Unix(0, lastNano)) < 15*time.Second
	return capFPS, detFPS, skipFPS, online, true
}

// LatestFrame returns the most recent cached JPEG preview for a camera, or nil.
func (m *Manager) LatestFrame(name string) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workers[name]
	if !ok {
		return nil
	}
	return w.LatestFrame()
}

// batchAssembler collects BatchItems from fanin and flushes them to BatchCh
// every batchFlushInterval or when the batch is full.
func (m *Manager) batchAssembler(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(batchFlushInterval)
	defer ticker.Stop()

	var batch []BatchItem

	flush := func() {
		if len(batch) == 0 {
			return
		}
		b := batch
		batch = nil
		select {
		case m.BatchCh <- b:
		case <-ctx.Done():
		default:
			slog.Warn("detector batch channel full — dropping batch", "size", len(b))
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return

		case item, ok := <-m.fanin:
			if !ok {
				flush()
				return
			}
			batch = append(batch, item)

			// Flush when we have one frame per camera.
			m.mu.RLock()
			numCameras := len(m.workers)
			m.mu.RUnlock()
			if numCameras > 0 && len(batch) >= numCameras {
				flush()
			}

		case <-ticker.C:
			flush()
		}
	}
}

// forwardRecordNotify copies segment paths from one worker's RecordNotifyCh
// to the Manager's aggregate RecordNotifyCh.
func (m *Manager) forwardRecordNotify(ctx context.Context, w *CameraWorker) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-w.RecordNotifyCh:
			if !ok {
				return
			}
			select {
			case m.RecordNotifyCh <- path:
			case <-ctx.Done():
				return
			}
		}
	}
}

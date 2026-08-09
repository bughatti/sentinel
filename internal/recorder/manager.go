package recorder

import (
	"context"
	"log/slog"
	"sync"

	"github.com/sentinel-nvr/sentinel/internal/events"
)

// Manager owns one Recorder goroutine per camera plus a shared segment
// dispatch goroutine that routes paths from the camera.Manager's aggregate
// RecordNotifyCh to per-camera recorders.
type Manager struct {
	store      *events.Store
	motion     MotionQuerier
	recorders  map[string]*Recorder
	channels   map[string]chan string
	mu         sync.RWMutex
	wg         sync.WaitGroup
}

// NewManager creates a recording Manager. motion may be nil to disable motion
// annotation on recording segments.
func NewManager(store *events.Store, motion MotionQuerier) *Manager {
	return &Manager{
		store:     store,
		motion:    motion,
		recorders: make(map[string]*Recorder),
		channels:  make(map[string]chan string),
	}
}

// AddCamera registers a Recorder for the given camera. The Recorder is started
// immediately. Calling AddCamera for an already-registered camera is a no-op.
func (m *Manager) AddCamera(ctx context.Context, camera string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.recorders[camera]; exists {
		return
	}
	ch := make(chan string, 64)
	rec := NewRecorder(camera, m.store, ch, m.motion)
	m.recorders[camera] = rec
	m.channels[camera] = ch

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		rec.Run(ctx)
	}()
	slog.Info("recorder: camera registered", "camera", camera)
}

// Dispatch sends a segment path to the correct per-camera recorder channel.
// If the camera is not registered the segment is dropped with a warning.
func (m *Manager) Dispatch(camera, path string) {
	m.mu.RLock()
	ch, ok := m.channels[camera]
	m.mu.RUnlock()
	if !ok {
		slog.Warn("recorder: segment for unregistered camera dropped",
			"camera", camera, "path", path)
		return
	}
	select {
	case ch <- path:
	default:
		slog.Warn("recorder: channel full, dropping segment",
			"camera", camera, "path", path)
	}
}

// Wait blocks until all recorder goroutines have exited (i.e. their ctx was
// cancelled).
func (m *Manager) Wait() {
	m.wg.Wait()
}

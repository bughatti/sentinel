// Package motion tracks per-camera motion state, creates motion events in the
// database, and answers range queries so the recorder can annotate segments.
package motion

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/bughatti/sentinel/internal/camera"
	"github.com/bughatti/sentinel/internal/config"
	"github.com/bughatti/sentinel/internal/events"
)

// interval is a half-open time window [start, end) during which motion was seen.
type interval struct {
	start, end time.Time
}

// cameraState holds the live motion state for one camera.
// Fields without mu comments are only written by the Run goroutine.
type cameraState struct {
	// state machine fields — Run goroutine only
	isActive   bool
	eventID    string
	lastMotion time.Time

	// intervals ring — written by Run, read by WasActiveInRange
	mu        sync.RWMutex
	intervals []interval
}

// Manager tracks motion for all cameras and writes motion events to the DB.
type Manager struct {
	store  *events.Store
	camCfg map[string]config.CameraConfig

	mu     sync.RWMutex
	states map[string]*cameraState
}

// NewManager creates a Manager. camCfg is used to read per-camera post_capture.
func NewManager(store *events.Store, camCfg map[string]config.CameraConfig) *Manager {
	return &Manager{
		store:  store,
		camCfg: camCfg,
		states: make(map[string]*cameraState),
	}
}

// WasActiveInRange returns true if motion was detected at any point during
// [start, end). Called by the recorder to annotate segment rows.
func (m *Manager) WasActiveInRange(camName string, start, end time.Time) bool {
	m.mu.RLock()
	st, ok := m.states[camName]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	for _, iv := range st.intervals {
		if iv.start.Before(end) && iv.end.After(start) {
			return true
		}
	}
	return false
}

// Run processes motion signals for one camera. It should be started in its own
// goroutine and runs until ctx is cancelled or ch is closed.
func (m *Manager) Run(ctx context.Context, camName string, ch <-chan camera.MotionSignal) {
	st := &cameraState{intervals: make([]interval, 0, 128)}
	m.mu.Lock()
	m.states[camName] = st
	m.mu.Unlock()

	postCapture := 30 * time.Second
	if cfg, ok := m.camCfg[camName]; ok && cfg.Record != nil && cfg.Record.Events.PostCapture > 0 {
		postCapture = time.Duration(cfg.Record.Events.PostCapture) * time.Second
	}

	// coolTimer fires postCapture after the last motion frame, closing the event.
	coolTimer := time.NewTimer(0)
	if !coolTimer.Stop() {
		select {
		case <-coolTimer.C:
		default:
		}
	}
	timerArmed := false

	for {
		select {
		case <-ctx.Done():
			coolTimer.Stop()
			if st.isActive {
				m.closeEvent(context.Background(), camName, st)
			}
			return

		case sig, ok := <-ch:
			if !ok {
				return
			}
			if !sig.HasMotion {
				continue
			}

			// Record the motion timestamp into the intervals ring.
			m.recordInterval(st, sig.At)

			// Reset the post-capture cooldown.
			if timerArmed {
				if !coolTimer.Stop() {
					select {
					case <-coolTimer.C:
					default:
					}
				}
			}
			coolTimer.Reset(postCapture)
			timerArmed = true

			st.lastMotion = sig.At

			if !st.isActive {
				st.isActive = true
				m.openEvent(ctx, camName, st, sig)
			}

		case <-coolTimer.C:
			timerArmed = false
			if st.isActive {
				m.closeEvent(ctx, camName, st)
			}
		}
	}
}

func (m *Manager) recordInterval(st *cameraState, t time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()

	const mergeGap = 2 * time.Second
	if len(st.intervals) > 0 && t.Sub(st.intervals[len(st.intervals)-1].end) < mergeGap {
		st.intervals[len(st.intervals)-1].end = t.Add(time.Second)
	} else {
		st.intervals = append(st.intervals, interval{start: t, end: t.Add(time.Second)})
	}

	// Trim intervals older than 5 minutes — the recorder only looks back one segment.
	cutoff := t.Add(-5 * time.Minute)
	i := 0
	for i < len(st.intervals) && st.intervals[i].end.Before(cutoff) {
		i++
	}
	if i > 0 {
		st.intervals = st.intervals[i:]
	}
}

func (m *Manager) openEvent(ctx context.Context, camName string, st *cameraState, sig camera.MotionSignal) {
	id := fmt.Sprintf("%.6f.%s", float64(sig.At.UnixMicro())/1e6, uuid.New().String()[:8])
	e := &events.Event{
		ID:           id,
		Camera:       camName,
		Label:        "motion",
		StartTime:    float64(sig.At.UnixMicro()) / 1e6,
		Score:        float32(sig.Score),
		TopScore:     float32(sig.Score),
		Data:         map[string]any{"motion_score": sig.Score},
		EnteredZones: []string{},
		CurrentZones: []string{},
	}
	if err := m.store.InsertEvent(ctx, e); err != nil {
		slog.Error("motion: insert event", "camera", camName, "err", err)
		// Still mark active so we try to close properly later.
	}
	st.eventID = id
	slog.Info("motion event started", "camera", camName, "id", id, "score", sig.Score)
}

func (m *Manager) closeEvent(ctx context.Context, camName string, st *cameraState) {
	st.isActive = false
	id := st.eventID
	endT := st.lastMotion
	st.eventID = ""

	if id == "" {
		return
	}

	// Fetch the current row so UpdateEvent keeps all its other fields intact.
	existing, err := m.store.GetEvent(ctx, id)
	if err != nil {
		slog.Error("motion: get event for close", "camera", camName, "id", id, "err", err)
		return
	}

	endUnix := float64(endT.UnixMicro()) / 1e6
	existing.EndTime = &endUnix

	if err := m.store.UpdateEvent(ctx, existing); err != nil {
		slog.Error("motion: close event", "camera", camName, "id", id, "err", err)
		return
	}
	slog.Info("motion event closed", "camera", camName, "id", id,
		"duration_s", endUnix-existing.StartTime)
}

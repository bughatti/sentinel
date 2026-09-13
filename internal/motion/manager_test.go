package motion

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bughatti/sentinel/internal/camera"
	"github.com/bughatti/sentinel/internal/config"
	"github.com/bughatti/sentinel/internal/events"
)

// fakeStore is an in-memory eventWriter. The real store needs Postgres, and
// the state machine under test has nothing to do with storage.
type fakeStore struct {
	mu               sync.Mutex
	rows             map[string]*events.Event
	inserts, updates int
}

func (f *fakeStore) InsertEvent(_ context.Context, e *events.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = make(map[string]*events.Event)
	}
	cp := *e
	f.rows[e.ID] = &cp
	f.inserts++
	return nil
}

func (f *fakeStore) GetEvent(_ context.Context, id string) (*events.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.rows[id]
	if !ok {
		return nil, fmt.Errorf("no such event %q", id)
	}
	cp := *e
	return &cp, nil
}

// counts returns the insert and update tallies under the lock. closeEvent
// notifies before it writes, so a test that reads these straight after seeing
// a transition would otherwise race the write.
func (f *fakeStore) counts() (inserts, updates int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inserts, f.updates
}

func (f *fakeStore) UpdateEvent(_ context.Context, e *events.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *e
	f.rows[e.ID] = &cp
	f.updates++
	return nil
}

// camCfg builds a single-camera config with the given post-capture cooldown
// in seconds, which is what decides when motion is declared over.
func camCfg(postCaptureSecs int) map[string]config.CameraConfig {
	return map[string]config.CameraConfig{
		"drive": {
			Name: "drive",
			Record: &config.RecordConfig{
				Events: config.EventRecordConfig{PostCapture: postCaptureSecs},
			},
		},
	}
}

// recorder collects callback invocations from the motion goroutine.
type recorder struct {
	mu   sync.Mutex
	seen []bool
}

func (r *recorder) add(active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, active)
}

func (r *recorder) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bool, len(r.seen))
	copy(out, r.seen)
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole point of an edge-triggered callback: a camera that sees motion in
// many consecutive frames must produce exactly one "motion started" message,
// not one per frame. Getting this wrong would flood the broker.
func TestCallbackFiresOncePerTransitionNotPerFrame(t *testing.T) {
	store := &fakeStore{}
	m := NewManager(store, camCfg(30)) // long cooldown: motion stays active
	rec := &recorder{}
	m.SetChangeCallback(func(cam string, active bool) {
		if cam != "drive" {
			t.Errorf("callback got camera %q, want drive", cam)
		}
		rec.add(active)
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan camera.MotionSignal)
	done := make(chan struct{})
	go func() {
		m.Run(ctx, "drive", ch)
		close(done)
	}()

	for i := 0; i < 25; i++ {
		ch <- camera.MotionSignal{HasMotion: true, Score: 0.7, At: time.Now()}
	}

	waitFor(t, "the rising edge", func() bool { return len(rec.snapshot()) >= 1 })

	if got := rec.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("25 motion frames produced %v, want exactly one true", got)
	}
	if inserts, _ := store.counts(); inserts != 1 {
		t.Errorf("inserted %d event rows for one continuous motion period, want 1", inserts)
	}

	// Shutting down closes the open event, which is also a state change.
	cancel()
	<-done

	waitFor(t, "the shutdown transition", func() bool { return len(rec.snapshot()) >= 2 })
	got := rec.snapshot()
	if len(got) != 2 || got[0] != true || got[1] != false {
		t.Errorf("transitions = %v, want [true false]", got)
	}
}

// When motion stops, the cooldown expires and subscribers must be told. A
// retained ON that never turns OFF would leave automations stuck on.
func TestCallbackFiresOnCooldownExpiry(t *testing.T) {
	store := &fakeStore{}
	m := NewManager(store, camCfg(1)) // 1s cooldown so the test stays quick
	rec := &recorder{}
	m.SetChangeCallback(func(_ string, active bool) { rec.add(active) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan camera.MotionSignal)
	go m.Run(ctx, "drive", ch)

	ch <- camera.MotionSignal{HasMotion: true, Score: 0.9, At: time.Now()}

	waitFor(t, "motion to start then stop on its own", func() bool {
		return len(rec.snapshot()) >= 2
	})

	got := rec.snapshot()
	if len(got) != 2 || got[0] != true || got[1] != false {
		t.Fatalf("transitions = %v, want [true false]", got)
	}
	// The close is written after the notification, so wait for it rather than
	// reading the instant the transition arrives.
	waitFor(t, "the event row to be closed", func() bool {
		_, updates := store.counts()
		return updates >= 1
	})
	if _, updates := store.counts(); updates != 1 {
		t.Errorf("closed the event %d times, want 1", updates)
	}
}

// Signals with no motion must not be treated as a transition.
func TestNoMotionSignalsProduceNoCallback(t *testing.T) {
	m := NewManager(&fakeStore{}, camCfg(30))
	rec := &recorder{}
	m.SetChangeCallback(func(_ string, active bool) { rec.add(active) })

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan camera.MotionSignal)
	done := make(chan struct{})
	go func() {
		m.Run(ctx, "drive", ch)
		close(done)
	}()

	for i := 0; i < 5; i++ {
		ch <- camera.MotionSignal{HasMotion: false, At: time.Now()}
	}
	cancel()
	<-done

	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("quiet camera produced transitions %v, want none", got)
	}
}

// Motion tracking must work with no callback registered, which is the case
// whenever MQTT is off or "motion" is not in the publish list.
func TestNoCallbackRegisteredIsSafe(t *testing.T) {
	m := NewManager(&fakeStore{}, camCfg(30))

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan camera.MotionSignal)
	done := make(chan struct{})
	go func() {
		m.Run(ctx, "drive", ch)
		close(done)
	}()

	ch <- camera.MotionSignal{HasMotion: true, Score: 0.5, At: time.Now()}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return; a missing callback must not wedge the loop")
	}
}

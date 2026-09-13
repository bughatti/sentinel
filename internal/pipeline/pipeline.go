package pipeline

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bughatti/sentinel/internal/camera"
	"github.com/bughatti/sentinel/internal/config"
	"github.com/bughatti/sentinel/internal/detector"
	"github.com/bughatti/sentinel/internal/events"
	"github.com/bughatti/sentinel/internal/face"
	"github.com/bughatti/sentinel/internal/snapshot"
	"github.com/bughatti/sentinel/internal/storage"
	"github.com/google/uuid"
)

// eventState tracks a single in-progress or recently-ended event.
type eventState struct {
	event    *events.Event
	lastSeen time.Time
	topScore float32
	topBox   image.Rectangle
	zonesHit map[string]int // zone name → consecutive frames inside

	initialBox       image.Rectangle // bounding box when event first started
	stationaryFrames int             // consecutive frames with high IoU vs initialBox
	stationary       bool            // declared stationary — events suppressed

	// confirmed reports whether this object has been persisted/alerted. Persons
	// are confirmed the moment they appear; all other labels (car/dog/cat) stay
	// unconfirmed until they TRANSLATE from where they first appeared. An object
	// that is never confirmed (a parked car that never moves) is silently
	// discarded on end — this is the parked-car flood fix.
	confirmed bool
	// birthCX/birthCY is the normalised [0,1] box center when the object first
	// appeared. Confirmation uses displacement from here (robust against the
	// bounding-box jitter that plagues parked cars — jitter wobbles the box in
	// place but does not translate the center).
	birthCX, birthCY float32
}

// movementThreshold is how far (fraction of the frame) an object's center must
// travel from its birth position before a non-person event is confirmed.
const movementThreshold = 0.08

// Pipeline processes detector output for one camera and manages event
// lifecycle (start, update, end) publishing to an EventBus.
type Pipeline struct {
	camera   string
	cfg      config.CameraConfig
	bus      *events.EventBus
	store    *events.Store
	snap     *snapshot.Saver
	stor     *storage.LocalStorage
	face     face.Recognizer
	faceThld float64
	tracker  *Tracker
	zones    []*Zone

	mu     sync.Mutex
	active map[uint64]*eventState // trackID → active event
}

// NewPipeline creates a Pipeline for one camera. snap, stor, and rec may be nil
// (snapshots / clips / face recognition are then skipped, but tracking works).
func NewPipeline(cameraName string, cfg config.CameraConfig, bus *events.EventBus, store *events.Store, snap *snapshot.Saver, stor *storage.LocalStorage, rec face.Recognizer, faceThreshold float64) (*Pipeline, error) {
	maxDisapp := 5
	if cfg.Detect.MaxDisapp > 0 {
		maxDisapp = cfg.Detect.MaxDisapp
	}
	if rec == nil {
		rec = face.Disabled()
	}
	if faceThreshold <= 0 {
		faceThreshold = 0.5
	}

	p := &Pipeline{
		camera:   cameraName,
		cfg:      cfg,
		bus:      bus,
		store:    store,
		snap:     snap,
		stor:     stor,
		face:     rec,
		faceThld: faceThreshold,
		tracker:  NewTracker(maxDisapp),
		active:   make(map[uint64]*eventState),
	}

	for zoneName, zoneCfg := range cfg.Zones {
		z, err := NewZone(zoneName, zoneCfg.Coordinates, zoneCfg.Objects, zoneCfg.Inertia)
		if err != nil {
			slog.Warn("pipeline: invalid zone config", "camera", cameraName, "zone", zoneName, "err", err)
			continue
		}
		p.zones = append(p.zones, z)
	}

	return p, nil
}

// Process is called for each frame that reached the detector. detections is
// the raw detector output for this frame.
func (p *Pipeline) Process(ctx context.Context, frame *camera.Frame, detections []detector.Detection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Filter detections by label whitelist and minimum score.
	var filtered []inputDetection
	for _, d := range detections {
		if !p.isTracked(d.Label) {
			continue
		}
		if !p.passesFilter(d, frame.Width, frame.Height) {
			continue
		}
		filtered = append(filtered, inputDetection{
			Label: d.Label,
			Score: d.Score,
			Box:   d.Box,
		})
	}

	tracked := p.tracker.Update(filtered)
	now := frame.Timestamp
	seenIDs := make(map[uint64]bool)

	for _, td := range tracked {
		seenIDs[td.TrackID] = true

		// Normalise box to [0,1].
		nx1, ny1, nx2, ny2 := normBox(td.Box, frame.Width, frame.Height)

		// Check zones.
		var currentZones []string
		for _, z := range p.zones {
			if !z.TracksObject(td.Label) {
				continue
			}
			if z.ContainsBox(nx1, ny1, nx2, ny2) {
				currentZones = append(currentZones, z.Name)
			}
		}

		st, exists := p.active[td.TrackID]
		if !exists {
			// New object. Persons are alerted immediately; everything else
			// (car/dog/cat) is held until it actually moves, so a parked car
			// that never moves never becomes an event.
			e := p.createEvent(td, frame, nx1, ny1, nx2, ny2, currentZones)
			st = &eventState{
				event:      e,
				lastSeen:   now,
				topScore:   td.Score,
				topBox:     td.Box,
				zonesHit:   make(map[string]int),
				initialBox: td.Box,
				birthCX:    (nx1 + nx2) / 2,
				birthCY:    (ny1 + ny2) / 2,
			}
			p.active[td.TrackID] = st

			if holdUntilMoves(td.Label) {
				// Unconfirmed: no DB row, no publish, no snapshot yet. The update
				// path confirms it the first frame it moves; endEvent discards it
				// if it never does.
				slog.Debug("object pending movement confirmation",
					"camera", p.camera, "label", td.Label, "score", td.Score)
				continue
			}

			st.confirmed = true
			if err := p.store.InsertEvent(ctx, e); err != nil {
				slog.Error("pipeline: insert event", "err", err, "camera", p.camera)
			}
			p.saveSnapshot(ctx, e, frame)
			p.maybeRecognize(e, frame)
			p.bus.Publish(*e)
			slog.Info("event started",
				"id", e.ID,
				"camera", p.camera,
				"label", td.Label,
				"score", td.Score,
			)
			continue
		}

		// Update existing event.
		st.lastSeen = now

		if td.Score > st.topScore {
			st.topScore = td.Score
			st.topBox = td.Box
		}

		// Stationary suppression: count consecutive frames where the object
		// hasn't moved significantly from its initial position.
		stationaryThreshold := 50 // ~10s at 5 fps
		if p.cfg.Detect.Stationary.Threshold > 0 {
			stationaryThreshold = p.cfg.Detect.Stationary.Threshold
		}
		if boxIoU(td.Box, st.initialBox) > 0.85 {
			st.stationaryFrames++
			if st.stationaryFrames >= stationaryThreshold && !st.stationary {
				st.stationary = true
				slog.Info("object marked stationary — suppressing",
					"id", st.event.ID,
					"camera", p.camera,
					"label", td.Label,
					"frames", st.stationaryFrames,
				)
			}
		} else {
			// Object moved — reset.
			if st.stationary {
				slog.Info("stationary object moved — resuming",
					"id", st.event.ID,
					"camera", p.camera,
					"label", td.Label,
				)
			}
			st.stationaryFrames = 0
			st.stationary = false
			st.initialBox = td.Box
		}

		// Confirm a held (non-person) object once its center has genuinely
		// TRANSLATED from where it first appeared. Using displacement rather than
		// frame-to-frame IoU rejects the box jitter of a parked car (which
		// wobbles in place but never travels) while still catching any car that
		// actually drives through the scene.
		if !st.confirmed {
			cx := (nx1 + nx2) / 2
			cy := (ny1 + ny2) / 2
			dx := float64(cx - st.birthCX)
			dy := float64(cy - st.birthCY)
			if math.Hypot(dx, dy) >= movementThreshold {
				st.confirmed = true
				st.event.Type = events.EventTypeNew
				if err := p.store.InsertEvent(ctx, st.event); err != nil {
					slog.Error("pipeline: insert event (on movement)", "err", err, "camera", p.camera)
				}
				p.saveSnapshot(ctx, st.event, frame)
				p.bus.Publish(*st.event)
				slog.Info("event confirmed on movement",
					"id", st.event.ID,
					"camera", p.camera,
					"label", td.Label,
					"score", td.Score,
					"moved", math.Hypot(dx, dy),
				)
			}
		}

		if st.stationary {
			continue
		}

		// Still held (never translated yet) — no updates until it is confirmed.
		if !st.confirmed {
			continue
		}

		// Accumulate zone inertia.
		for _, z := range p.zones {
			if !z.TracksObject(td.Label) {
				continue
			}
			if z.ContainsBox(nx1, ny1, nx2, ny2) {
				st.zonesHit[z.Name]++
			} else {
				delete(st.zonesHit, z.Name)
			}
		}

		// Update zones on event.
		var enteredZones []string
		for zoneName, frames := range st.zonesHit {
			z := p.zoneByName(zoneName)
			if z != nil && frames >= z.Inertia {
				enteredZones = append(enteredZones, zoneName)
			}
		}
		st.event.CurrentZones = currentZones
		if len(enteredZones) > 0 {
			st.event.EnteredZones = mergeZones(st.event.EnteredZones, enteredZones)
		}

		st.event.Score = td.Score
		st.event.TopScore = st.topScore
		st.event.Box = boxToEventBox(td.Box, frame.Width, frame.Height)
		st.event.Area = (nx2 - nx1) * (ny2 - ny1)
		st.event.Type = events.EventTypeUpdate

		if err := p.store.UpdateEvent(ctx, st.event); err != nil {
			slog.Error("pipeline: update event", "err", err, "camera", p.camera)
		}
		p.bus.Publish(*st.event)
	}

	// End events for tracks that disappeared beyond the cooldown window.
	cooldown := 5 * time.Second
	for trackID, st := range p.active {
		if seenIDs[trackID] {
			continue
		}
		if now.Sub(st.lastSeen) >= cooldown {
			p.endEvent(ctx, st, now)
			delete(p.active, trackID)
		}
	}
}

// endEvent marks an active event as ended and persists/publishes it.
func (p *Pipeline) endEvent(ctx context.Context, st *eventState, endTime time.Time) {
	// An object that was never confirmed never moved from where it appeared
	// (a parked car / static object). No DB row was ever created — discard it.
	if !st.confirmed {
		slog.Debug("discarding unconfirmed (never-moved) object",
			"camera", p.camera, "label", st.event.Label)
		return
	}

	t := float64(endTime.UnixMicro()) / 1e6
	st.event.EndTime = &t
	st.event.Type = events.EventTypeEnd
	st.event.TopScore = st.topScore

	if err := p.store.UpdateEvent(ctx, st.event); err != nil {
		slog.Error("pipeline: end event update", "err", err, "id", st.event.ID)
	}
	p.bus.Publish(*st.event)
	slog.Info("event ended",
		"id", st.event.ID,
		"camera", p.camera,
		"label", st.event.Label,
		"duration", endTime.Sub(time.Unix(int64(st.event.StartTime), 0)).Round(time.Millisecond),
	)

	// Extract the event clip from the continuous recording segments. ffmpeg is
	// slow, so run it off the pipeline lock.
	go p.saveClip(st.event.ID, st.event.Camera, st.event.StartTime, t)
}

// FlushAll ends all active events immediately. Called on shutdown.
func (p *Pipeline) FlushAll(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for id, st := range p.active {
		p.endEvent(ctx, st, now)
		delete(p.active, id)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func (p *Pipeline) createEvent(td TrackedDetection, frame *camera.Frame, nx1, ny1, nx2, ny2 float32, zones []string) *events.Event {
	startSecs := float64(frame.Timestamp.UnixMicro()) / 1e6
	id := fmt.Sprintf("%.6f-%s", startSecs, uuid.New().String()[:8])

	return &events.Event{
		ID:           id,
		Camera:       p.camera,
		Label:        td.Label,
		Type:         events.EventTypeNew,
		Score:        td.Score,
		TopScore:     td.Score,
		StartTime:    startSecs,
		Box:          boxToEventBox(td.Box, frame.Width, frame.Height),
		Area:         (nx2 - nx1) * (ny2 - ny1),
		CurrentZones: zones,
		EnteredZones: zones,
		TrackID:      td.TrackID,
		FrameTime:    frame.Timestamp,
		Data:         make(map[string]any),
	}
}

// holdUntilMoves reports whether an object of this label must move before it
// generates an event. Persons alert immediately (a person standing at the door
// matters); everything else is held so parked cars never flood events.
func holdUntilMoves(label string) bool { return label != "person" }

// saveSnapshot writes the best-frame JPEG for an event (sets has_snapshot).
// Runs inline — encoding is fast and needs the live frame buffer.
func (p *Pipeline) saveSnapshot(ctx context.Context, e *events.Event, frame *camera.Frame) {
	if p.snap == nil {
		return
	}
	if err := p.snap.Save(ctx, e, frame); err != nil {
		slog.Warn("pipeline: snapshot save", "err", err, "id", e.ID)
	}
}

// maybeRecognize kicks off async face recognition for a person event. The frame
// is copied because recognition runs off the pipeline lock and the capture
// buffer is reused.
func (p *Pipeline) maybeRecognize(e *events.Event, frame *camera.Frame) {
	if p.face == nil || !p.face.Enabled() || e.Label != "person" {
		return
	}
	fc := *frame
	fc.Data = append([]byte(nil), frame.Data...)
	id := e.ID
	go p.recognizeFaces(id, &fc)
}

// recognizeFaces detects + embeds faces in the frame, matches the largest
// against enrolled identities, and sets the event's sub_label on a hit.
func (p *Pipeline) recognizeFaces(eventID string, frame *camera.Frame) {
	ctx := context.Background()
	faces, err := p.face.DetectAndEmbed(frame)
	if err != nil {
		slog.Warn("pipeline: face detect", "err", err, "id", eventID)
		return
	}
	if len(faces) == 0 {
		return
	}
	best, bestArea := 0, 0
	for i, f := range faces {
		if a := f.Box.Dx() * f.Box.Dy(); a > bestArea {
			bestArea, best = a, i
		}
	}
	name, sim, ok, err := p.store.MatchFace(ctx, faces[best].Embedding, p.faceThld)
	if err != nil {
		slog.Warn("pipeline: face match", "err", err, "id", eventID)
		return
	}
	if !ok {
		slog.Debug("pipeline: face seen but unmatched", "id", eventID, "best_sim", sim, "faces", len(faces))
		return
	}
	if err := p.store.SetEventSubLabel(ctx, eventID, []string{name}); err != nil {
		slog.Warn("pipeline: set sub_label", "err", err, "id", eventID)
		return
	}
	slog.Info("pipeline: face recognized", "id", eventID, "name", name, "sim", sim, "camera", p.camera)
}

// saveClip extracts an event clip by concatenating the continuous recording
// segments that overlap [start-pre, end+post] into /clips/<id>.mp4, then sets
// has_clip. Called in its own goroutine (ffmpeg is slow).
func (p *Pipeline) saveClip(id, cam string, startSecs, endSecs float64) {
	if p.stor == nil {
		return
	}

	pre, post := 5.0, 5.0
	if p.cfg.Record != nil {
		if p.cfg.Record.Events.PreCapture > 0 {
			pre = float64(p.cfg.Record.Events.PreCapture)
		}
		if p.cfg.Record.Events.PostCapture > 0 {
			post = float64(p.cfg.Record.Events.PostCapture)
		}
	}
	from := startSecs - pre
	to := endSecs + post

	// The recording segment covering `to` is still being written when the event ends:
	// ffmpeg writes an mp4's moov index only when a segment CLOSES (rolls over every
	// segment_duration), so concatenating that unfinalized file fails with "moov atom
	// not found" — which was silently dropping the clip for the majority of events.
	// Wait until the trailing segment has rolled over + finalized, then extract, with a
	// couple of retries as a safety net for rollover-timing jitter.
	segDur := 10.0
	if p.cfg.Record != nil && p.cfg.Record.SegmentDuration > 0 {
		segDur = float64(p.cfg.Record.SegmentDuration)
	}
	if wait := time.Until(time.Unix(int64(to), 0).Add(time.Duration(segDur+2) * time.Second)); wait > 0 {
		time.Sleep(wait)
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(segDur) * time.Second / 2)
		}
		n, err := p.extractClip(id, cam, from, to)
		if err == nil {
			slog.Info("event clip saved", "id", id, "camera", cam, "segments", n, "attempt", attempt)
			return
		}
		lastErr = err
	}
	slog.Warn("pipeline: clip extraction failed after retries", "id", id, "camera", cam, "err", lastErr)
}

// extractClip concatenates the recording segments overlapping [from,to] into the
// event's clip file and sets has_clip. Returns the segment count and any error, so
// saveClip can retry — a segment may still be finalizing on an early attempt.
func (p *Pipeline) extractClip(id, cam string, from, to float64) (int, error) {
	ctx := context.Background()

	// Query a padded window (one segment length either side) then keep only the
	// segments that actually overlap [from,to] — ListRecordings' filter is
	// fully-contained, so padding catches the boundary segments.
	qa, qb := from-15, to+15
	recs, err := p.store.ListRecordings(ctx, events.RecordingFilter{
		Camera: cam, After: &qa, Before: &qb, Limit: 2000,
	})
	if err != nil {
		return 0, fmt.Errorf("list recordings: %w", err)
	}
	var segs []*events.Recording
	for _, r := range recs {
		if r.EndTime > from && r.StartTime < to {
			segs = append(segs, r)
		}
	}
	if len(segs) == 0 {
		return 0, fmt.Errorf("no recording segments")
	}

	lf, err := os.CreateTemp("", "sentinel-clip-*.txt")
	if err != nil {
		return 0, fmt.Errorf("tmp file: %w", err)
	}
	tmpName := lf.Name()
	defer os.Remove(tmpName)
	var b strings.Builder
	for _, s := range segs {
		// ffmpeg concat requires single-quoted paths with embedded quotes escaped.
		b.WriteString("file '")
		b.WriteString(strings.ReplaceAll(s.Path, "'", `'\''`))
		b.WriteString("'\n")
	}
	if _, err := lf.WriteString(b.String()); err != nil {
		lf.Close()
		return 0, fmt.Errorf("write list: %w", err)
	}
	lf.Close()

	clipPath := p.stor.ClipPath(id)
	if err := os.MkdirAll(filepath.Dir(clipPath), 0o755); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}

	// Stream-copy concat: the clip is the union of the overlapping segments (event
	// window ± up to one segment). No re-encode — fast and lossless.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "concat", "-safe", "0", "-i", tmpName,
		"-c", "copy", "-movflags", "+faststart", clipPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return len(segs), fmt.Errorf("ffmpeg: %s", strings.TrimSpace(string(out)))
	}

	if err := p.store.SetHasClip(ctx, id); err != nil {
		return len(segs), fmt.Errorf("set has_clip: %w", err)
	}
	return len(segs), nil
}

func (p *Pipeline) isTracked(label string) bool {
	tracked := []string{"person", "car", "dog", "cat"}
	if p.cfg.Objects != nil && len(p.cfg.Objects.Track) > 0 {
		tracked = p.cfg.Objects.Track
	}
	for _, t := range tracked {
		if t == label {
			return true
		}
	}
	return false
}

func (p *Pipeline) passesFilter(d detector.Detection, frameW, frameH int) bool {
	if p.cfg.Objects == nil {
		return true
	}
	f, ok := p.cfg.Objects.Filters[d.Label]
	if !ok {
		return true
	}
	if d.Score < f.MinScore {
		return false
	}
	w := d.Box.Dx()
	h := d.Box.Dy()
	if f.MinWidth > 0 && w < f.MinWidth {
		return false
	}
	if f.MinHeight > 0 && h < f.MinHeight {
		return false
	}
	// Area filters: bounding-box area as a fraction of the frame.
	if (f.MinArea > 0 || f.MaxArea > 0) && frameW > 0 && frameH > 0 {
		areaFrac := float64(w*h) / float64(frameW*frameH)
		if f.MinArea > 0 && areaFrac < f.MinArea {
			return false
		}
		if f.MaxArea > 0 && areaFrac > f.MaxArea {
			return false
		}
	}
	// Mask: exclude detections whose box center falls inside the masked polygon
	// (e.g. mask out the street so passing cars don't create driveway events).
	// Mask coordinates are detect-resolution pixels, same space as the box.
	if len(f.Mask) >= 3 {
		cx := (d.Box.Min.X + d.Box.Max.X) / 2
		cy := (d.Box.Min.Y + d.Box.Max.Y) / 2
		if pointInPolyInts(cx, cy, f.Mask) {
			return false
		}
	}
	return true
}

// pointInPolyInts is a ray-casting point-in-polygon test for integer pixel
// polygons ([[x,y],...]).
func pointInPolyInts(x, y int, poly [][]int) bool {
	inside := false
	n := len(poly)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		if len(poly[i]) < 2 || len(poly[j]) < 2 {
			continue
		}
		xi, yi := poly[i][0], poly[i][1]
		xj, yj := poly[j][0], poly[j][1]
		if (yi > y) != (yj > y) &&
			float64(x) < float64(xj-xi)*float64(y-yi)/float64(yj-yi)+float64(xi) {
			inside = !inside
		}
	}
	return inside
}

func (p *Pipeline) zoneByName(name string) *Zone {
	for _, z := range p.zones {
		if z.Name == name {
			return z
		}
	}
	return nil
}

func boxIoU(a, b image.Rectangle) float64 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	interArea := float64(inter.Dx() * inter.Dy())
	aArea := float64(a.Dx() * a.Dy())
	bArea := float64(b.Dx() * b.Dy())
	union := aArea + bArea - interArea
	if union <= 0 {
		return 0
	}
	return interArea / union
}

func normBox(box image.Rectangle, w, h int) (x1, y1, x2, y2 float32) {
	if w == 0 || h == 0 {
		return 0, 0, 0, 0
	}
	return float32(box.Min.X) / float32(w),
		float32(box.Min.Y) / float32(h),
		float32(box.Max.X) / float32(w),
		float32(box.Max.Y) / float32(h)
}

func boxToEventBox(box image.Rectangle, w, h int) events.Box {
	x1, y1, x2, y2 := normBox(box, w, h)
	return events.Box{X1: x1, Y1: y1, X2: x2, Y2: y2}
}

func mergeZones(existing, newZones []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, z := range existing {
		seen[z] = true
	}
	out := append([]string(nil), existing...)
	for _, z := range newZones {
		if !seen[z] {
			out = append(out, z)
			seen[z] = true
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Manager
// ─────────────────────────────────────────────────────────────────────────────

// Manager runs one Pipeline per camera.
type Manager struct {
	pipelines map[string]*Pipeline
	mu        sync.RWMutex
}

// NewPipelineManager creates a pipeline manager.
func NewPipelineManager() *Manager {
	return &Manager{pipelines: make(map[string]*Pipeline)}
}

// Add registers a pipeline for camera.
func (m *Manager) Add(camera string, p *Pipeline) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pipelines[camera] = p
}

// Process routes a frame+detections to the appropriate camera pipeline.
func (m *Manager) Process(ctx context.Context, frame *camera.Frame, detections []detector.Detection) {
	m.mu.RLock()
	p, ok := m.pipelines[frame.CameraID]
	m.mu.RUnlock()
	if !ok {
		return
	}
	p.Process(ctx, frame, detections)
}

// FlushAll calls FlushAll on every pipeline (used during shutdown).
func (m *Manager) FlushAll(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.pipelines {
		p.FlushAll(ctx)
	}
}

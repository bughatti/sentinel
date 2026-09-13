package pipeline

import (
	"image"
	"sync/atomic"
)

// nextTrackID is an atomic counter that assigns globally-unique track IDs.
var nextTrackID uint64 = 1

// track holds the state of one tracked object.
type track struct {
	ID          uint64
	Label       string
	Box         image.Rectangle
	Score       float32
	Age         int // frames since last matched detection
	TotalFrames int
}

// Tracker implements a simple IoU-based centroid tracker. On each frame it
// matches incoming detections to existing tracks by maximising IoU overlap,
// assigns existing IDs to matched detections, and creates new tracks for
// unmatched detections. Tracks that go unmatched for more than maxDisappeared
// frames are dropped.
type Tracker struct {
	tracks         []*track
	maxDisappeared int
}

// NewTracker creates a Tracker. maxDisappeared is the number of consecutive
// frames a track may be unmatched before it is removed.
func NewTracker(maxDisappeared int) *Tracker {
	if maxDisappeared < 1 {
		maxDisappeared = 5
	}
	return &Tracker{maxDisappeared: maxDisappeared}
}

// TrackedDetection pairs a detection bounding box + score with the TrackID
// assigned by the tracker.
type TrackedDetection struct {
	Label   string
	Score   float32
	Box     image.Rectangle
	TrackID uint64
	IsNew   bool // true on the frame the track was first created
}

// Update matches detections against existing tracks and returns TrackedDetections
// with stable TrackIDs. detections must contain (Label, Score, Box) populated;
// TrackID is set by this function.
func (t *Tracker) Update(detections []inputDetection) []TrackedDetection {
	// Step 1: match existing tracks to incoming detections.
	matched := make([]bool, len(detections))
	trackMatched := make([]bool, len(t.tracks))

	for ti, tr := range t.tracks {
		bestIoU := float32(0.3) // minimum IoU to consider a match
		bestDi := -1
		for di, d := range detections {
			if matched[di] {
				continue
			}
			if d.Label != tr.Label {
				continue
			}
			v := iouRect(tr.Box, d.Box)
			if v > bestIoU {
				bestIoU = v
				bestDi = di
			}
		}
		if bestDi >= 0 {
			matched[bestDi] = true
			trackMatched[ti] = true
			// Update track.
			t.tracks[ti].Box = detections[bestDi].Box
			t.tracks[ti].Score = detections[bestDi].Score
			t.tracks[ti].Age = 0
			t.tracks[ti].TotalFrames++
		} else {
			t.tracks[ti].Age++
		}
	}

	// Step 2: create new tracks for unmatched detections.
	var newTracks []*track
	var out []TrackedDetection
	isNew := make(map[uint64]bool)

	for di, d := range detections {
		if matched[di] {
			continue
		}
		id := atomic.AddUint64(&nextTrackID, 1)
		newTracks = append(newTracks, &track{
			ID:    id,
			Label: d.Label,
			Box:   d.Box,
			Score: d.Score,
			Age:   0,
		})
		isNew[id] = true
	}

	// Step 3: remove expired tracks, collect output.
	surviving := t.tracks[:0]
	for ti, tr := range t.tracks {
		if trackMatched[ti] || tr.Age <= t.maxDisappeared {
			surviving = append(surviving, tr)
			if trackMatched[ti] {
				out = append(out, TrackedDetection{
					Label:   tr.Label,
					Score:   tr.Score,
					Box:     tr.Box,
					TrackID: tr.ID,
					IsNew:   isNew[tr.ID],
				})
			}
		}
	}
	t.tracks = surviving
	t.tracks = append(t.tracks, newTracks...)

	for _, tr := range newTracks {
		out = append(out, TrackedDetection{
			Label:   tr.Label,
			Score:   tr.Score,
			Box:     tr.Box,
			TrackID: tr.ID,
			IsNew:   true,
		})
	}

	return out
}

// Reset clears all tracks.
func (t *Tracker) Reset() {
	t.tracks = nil
}

// ActiveCount returns the number of currently-tracked objects.
func (t *Tracker) ActiveCount() int {
	return len(t.tracks)
}

// inputDetection is the input type for Tracker.Update.
type inputDetection struct {
	Label string
	Score float32
	Box   image.Rectangle
}

// iouRect computes IoU for two image.Rectangles.
func iouRect(a, b image.Rectangle) float32 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	areaA := float32(a.Dx() * a.Dy())
	areaB := float32(b.Dx() * b.Dy())
	areaI := float32(inter.Dx() * inter.Dy())
	union := areaA + areaB - areaI
	if union <= 0 {
		return 0
	}
	return areaI / union
}

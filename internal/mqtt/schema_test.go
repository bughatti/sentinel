package mqtt

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bughatti/sentinel/internal/events"
)

// The prefix is what every published topic hangs off, and it changed from a
// third-party namespace to sentinel. Pin both the default and the override.
func TestTopicsPrefix(t *testing.T) {
	def := &Topics{}
	if got := def.Events(); got != "sentinel/events" {
		t.Errorf("empty prefix should fall back to sentinel, got %q", got)
	}
	custom := &Topics{Prefix: "house"}
	cases := map[string]string{
		custom.Available():                       "house/available",
		custom.Stats():                           "house/stats",
		custom.Events():                          "house/events",
		custom.CameraMotion("drive"):             "house/drive/motion",
		custom.CameraDetect("drive", "car"):      "house/drive/car",
		custom.CameraState("drive"):              "house/drive/state",
		custom.CameraObjectEnter("drive", "car"): "house/drive/car/snapshot",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("topic = %q, want %q", got, want)
		}
	}
}

func TestBuildEventData(t *testing.T) {
	end := 1700000100.5
	e := events.Event{
		ID: "1700000000.abc", Camera: "drive", Label: "person",
		SubLabel: []string{"Alice"}, Score: 0.81, TopScore: 0.93,
		StartTime: 1700000000.25, EndTime: &end, Area: 0.05,
		EnteredZones: []string{"driveway"}, CurrentZones: []string{"driveway"},
		HasClip: true, HasSnapshot: true, FalsePositive: false,
	}

	// With no FrameTime, frame_time falls back to start_time.
	got := buildEventData(e)
	if got.FrameTime != e.StartTime || got.SnapshotTime != e.StartTime {
		t.Errorf("frame/snapshot time should fall back to start_time, got %v/%v", got.FrameTime, got.SnapshotTime)
	}
	if got.ID != e.ID || got.Camera != e.Camera || got.Label != e.Label {
		t.Errorf("identity fields not carried: %+v", got)
	}
	if got.EndTime == nil || *got.EndTime != end {
		t.Errorf("end_time should carry through for an ended event")
	}
	if got.Attributes == nil {
		t.Error("attributes must be an empty map, not nil, so it serialises as {}")
	}

	// With FrameTime set, it wins and is expressed in fractional seconds.
	e.FrameTime = time.Unix(1700000050, 500000000).UTC()
	got = buildEventData(e)
	if got.FrameTime != 1700000050.5 {
		t.Errorf("frame_time = %v, want 1700000050.5", got.FrameTime)
	}

	// An event still in progress must serialise end_time as null, not 0.
	e.EndTime = nil
	blob, err := json.Marshal(buildEventData(e))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(blob, &raw)
	if v, ok := raw["end_time"]; !ok || v != nil {
		t.Errorf("in-progress event should serialise end_time as null, got %v", v)
	}
	if raw["attributes"] == nil {
		t.Error("attributes should serialise as an object")
	}
}

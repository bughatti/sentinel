// Package events defines the Event type and related structures that flow
// through Sentinel NVR. The JSON schema is integration-friendly so existing
// Home Assistant integrations work without modification.
package events

import "time"

// EventType distinguishes the kind of event bus message.
type EventType string

const (
	EventTypeNew    EventType = "new"
	EventTypeUpdate EventType = "update"
	EventTypeEnd    EventType = "end"
)

// Box is a normalised bounding box (values 0..1 relative to frame dimensions).
type Box struct {
	X1 float32 `json:"x1"`
	Y1 float32 `json:"y1"`
	X2 float32 `json:"x2"`
	Y2 float32 `json:"y2"`
}

// Region is the crop region of the frame sent to the detector.
type Region struct {
	X1 float32 `json:"x1"`
	Y1 float32 `json:"y1"`
	X2 float32 `json:"x2"`
	Y2 float32 `json:"y2"`
}

// Event mirrors the reference event payload exactly, with additional Sentinel
// fields in the Data map. All float time fields use Unix seconds with
// sub-second precision as the reference implementation does.
type Event struct {
	// Core identity
	ID     string    `json:"id"` // "unixtime.shortuuid"
	Camera string    `json:"camera"`
	Type   EventType `json:"type"` // "new" | "update" | "end"

	// Classification
	Label    string   `json:"label"`
	SubLabel []string `json:"sub_label"` // e.g. ["Alice"] for face recognition

	// Scores
	Score    float32 `json:"score"`
	TopScore float32 `json:"top_score"`

	// Temporal
	StartTime float64  `json:"start_time"` // Unix epoch with fractional seconds
	EndTime   *float64 `json:"end_time"`   // nil until event ends

	// Spatial
	Box    Box     `json:"box"`
	Region Region  `json:"region"`
	Area   float32 `json:"area"` // fraction of frame area

	// Zones
	EnteredZones []string `json:"entered_zones"`
	CurrentZones []string `json:"current_zones"`

	// Media
	HasClip     bool `json:"has_clip"`
	HasSnapshot bool `json:"has_snapshot"`

	// Retention
	RetainIndefinitely bool `json:"retain_indefinitely"`
	FalsePositive      bool `json:"false_positive"`

	// the reference implementation detector compat fields
	ModelHash    string `json:"model_hash,omitempty"`
	DetectorType string `json:"detector_type,omitempty"`
	ModelType    string `json:"model_type,omitempty"`

	// Sentinel extensions — stored in data JSONB column
	Data map[string]any `json:"data,omitempty"`

	// Internal fields not serialised to MQTT
	FrameTime time.Time `json:"-"`
	TrackID   uint64    `json:"-"`
}

// Recording represents a single continuous segment file on disk.
type Recording struct {
	ID          string   `json:"id"`
	Camera      string   `json:"camera"`
	Path        string   `json:"path"`
	StartTime   float64  `json:"start_time"`
	EndTime     float64  `json:"end_time"`
	Duration    float32  `json:"duration"`
	Motion      bool     `json:"motion"`
	Objects     []string `json:"objects"`
	SegmentSize int64    `json:"segment_size"`
}

// RecordingSummary is returned by the summary endpoint.
type RecordingSummary struct {
	Day      string  `json:"day"` // "YYYY-MM-DD"
	Camera   string  `json:"camera"`
	Duration float32 `json:"duration"` // total recorded seconds that day
	Motion   float32 `json:"motion"`   // seconds with motion
	Objects  float32 `json:"objects"`  // seconds with detected objects
}

// EventFilter is passed to store.ListEvents.
type EventFilter struct {
	Camera        string
	Label         string
	SubLabel      string
	After         *float64
	Before        *float64
	HasClip       *bool
	HasSnapshot   *bool
	FalsePositive *bool
	Zones         []string
	Limit         int
	Skip          int
}

// RecordingFilter is passed to store.ListRecordings.
type RecordingFilter struct {
	Camera string
	After  *float64
	Before *float64
	Motion *bool
	Limit  int
	Skip   int
}

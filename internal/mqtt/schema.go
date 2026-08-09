// Package mqtt provides a Paho MQTT client with auto-reconnect, a publisher,
// and integration-friendly topic/payload schemas.
package mqtt

import (
	"fmt"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Topic helpers — integration-friendly
// ─────────────────────────────────────────────────────────────────────────────

// Topics constructs MQTT topic strings matching the reference published schema.
// The prefix defaults to "sentinel" for Home Assistant integration compat.
type Topics struct {
	Prefix string
}

func (t *Topics) prefix() string {
	if t.Prefix == "" {
		return "sentinel"
	}
	return t.Prefix
}

func (t *Topics) Available() string    { return fmt.Sprintf("%s/available", t.prefix()) }
func (t *Topics) Stats() string        { return fmt.Sprintf("%s/stats", t.prefix()) }
func (t *Topics) Events() string       { return fmt.Sprintf("%s/events", t.prefix()) }
func (t *Topics) CameraMotion(cam string) string {
	return fmt.Sprintf("%s/%s/motion", t.prefix(), cam)
}
func (t *Topics) CameraDetect(cam, label string) string {
	return fmt.Sprintf("%s/%s/%s", t.prefix(), cam, label)
}
func (t *Topics) CameraState(cam string) string {
	return fmt.Sprintf("%s/%s/state", t.prefix(), cam)
}
func (t *Topics) CameraObjectEnter(cam, label string) string {
	return fmt.Sprintf("%s/%s/%s/snapshot", t.prefix(), cam, label)
}

// ─────────────────────────────────────────────────────────────────────────────
// Payload schemas (integration-friendly JSON structs)
// ─────────────────────────────────────────────────────────────────────────────

// EventPayload is the MQTT payload published to <prefix>/events.
// Matches the reference before/after event schema.
type EventPayload struct {
	Type   string     `json:"type"`   // "new" | "update" | "end"
	Before *EventData `json:"before"` // nil for new events
	After  EventData  `json:"after"`
}

// EventData mirrors the reference event data fields.
type EventData struct {
	ID               string   `json:"id"`
	Camera           string   `json:"camera"`
	FrameTime        float64  `json:"frame_time"`
	SnapshotTime     float64  `json:"snapshot_time"`
	Label            string   `json:"label"`
	SubLabel         []string `json:"sub_label"`
	TopScore         float32  `json:"top_score"`
	FalsePositive    bool     `json:"false_positive"`
	StartTime        float64  `json:"start_time"`
	EndTime          *float64 `json:"end_time"`
	Score            float32  `json:"score"`
	Box              []int    `json:"box"`    // [x1,y1,x2,y2] pixel coords
	Area             float32  `json:"area"`
	Ratio            float32  `json:"ratio"`
	Region           []int    `json:"region"` // [x1,y1,x2,y2] pixel coords
	Stationary       bool     `json:"stationary"`
	MotionlessCount  int      `json:"motionless_count"`
	PositionChanges  int      `json:"position_changes"`
	CurrentZones     []string `json:"current_zones"`
	EnteredZones     []string `json:"entered_zones"`
	HasClip          bool     `json:"has_clip"`
	HasSnapshot      bool     `json:"has_snapshot"`
	Attributes       map[string]float32 `json:"attributes"`
}

// MotionPayload is the payload for camera motion topics ("ON" or "OFF").
type MotionPayload struct {
	State string `json:"state"` // "ON" or "OFF"
}

// StatsPayload is the payload for the stats topic.
type StatsPayload struct {
	Detection   DetectionStats             `json:"detection"`
	Cameras     map[string]CameraStats     `json:"cameras"`
	Service     ServiceStats               `json:"service"`
	Timestamp   float64                    `json:"timestamp"`
}

// DetectionStats holds aggregate detection performance metrics.
type DetectionStats struct {
	Enabled       bool    `json:"detection_enabled"`
	TotalFrames   int64   `json:"total_frames"`
	TotalDetTime  float64 `json:"total_detection_time"`
	DetectionFPS  float32 `json:"detection_fps"`
	InferenceSpeed float32 `json:"avg_inference_speed"` // ms
}

// CameraStats holds per-camera performance metrics.
type CameraStats struct {
	CameraFPS       float32 `json:"camera_fps"`
	DetectFPS       float32 `json:"detect_fps"`
	ProcessFPS      float32 `json:"process_fps"`
	SkippedFPS      float32 `json:"skipped_fps"`
	CapturePID      int     `json:"capture_pid"`
	DetectPID       int     `json:"detect_pid"`
}

// ServiceStats holds service-level stats.
type ServiceStats struct {
	Uptime     float64 `json:"uptime"`
	Version    string  `json:"version"`
	StorageGB  float32 `json:"storage_free_gb"`
	FreeMemGB  float32 `json:"free_mem_gb"`
}

// startTime is used to compute uptime.
var startTime = time.Now()

// BuildStatsPayload constructs a StatsPayload with the current uptime.
func BuildStatsPayload(version string, cameras []string) StatsPayload {
	cams := make(map[string]CameraStats)
	for _, c := range cameras {
		cams[c] = CameraStats{}
	}
	return StatsPayload{
		Detection: DetectionStats{Enabled: true},
		Cameras:   cams,
		Service: ServiceStats{
			Uptime:  time.Since(startTime).Seconds(),
			Version: version,
		},
		Timestamp: float64(time.Now().UnixMicro()) / 1e6,
	}
}

package mqtt

import (
	"encoding/json"
	"log/slog"

	"github.com/bughatti/sentinel/internal/events"
)

// Publisher wraps a Client and provides typed publish methods for Sentinel
// events, motion state, and system stats.
type Publisher struct {
	client *Client
}

// NewPublisher creates a Publisher.
func NewPublisher(c *Client) *Publisher {
	return &Publisher{client: c}
}

// PublishEvent publishes an event to <prefix>/events.
// The payload matches the reference before/after JSON structure.
func (p *Publisher) PublishEvent(e events.Event) {
	after := buildEventData(e)
	payload := EventPayload{
		Type:  string(e.Type),
		After: after,
	}
	if e.Type != events.EventTypeNew {
		// Include before snapshot as a copy with end_time nil for updates.
		beforeCopy := after
		beforeCopy.EndTime = nil
		payload.Before = &beforeCopy
	}

	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("mqtt: marshal event payload", "err", err)
		return
	}
	p.client.Publish(p.client.topics.Events(), false, data)
}

// PublishMotion publishes a motion state change for a camera ("ON"/"OFF").
func (p *Publisher) PublishMotion(camera string, motion bool) {
	state := "OFF"
	if motion {
		state = "ON"
	}
	p.client.Publish(p.client.topics.CameraMotion(camera), false, state)
}

// PublishStats publishes the stats payload.
func (p *Publisher) PublishStats(payload StatsPayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("mqtt: marshal stats payload", "err", err)
		return
	}
	p.client.Publish(p.client.topics.Stats(), false, data)
}

// PublishAvailable publishes "online" to the availability topic.
func (p *Publisher) PublishAvailable() {
	p.client.Publish(p.client.topics.Available(), true, "online")
}

// buildEventData maps an events.Event to the integration-friendly EventData
// struct.
func buildEventData(e events.Event) EventData {
	frameTime := e.StartTime
	if !e.FrameTime.IsZero() {
		frameTime = float64(e.FrameTime.UnixMicro()) / 1e6
	}

	return EventData{
		ID:            e.ID,
		Camera:        e.Camera,
		FrameTime:     frameTime,
		SnapshotTime:  frameTime,
		Label:         e.Label,
		SubLabel:      e.SubLabel,
		TopScore:      e.TopScore,
		FalsePositive: e.FalsePositive,
		StartTime:     e.StartTime,
		EndTime:       e.EndTime,
		Score:         e.Score,
		Area:          e.Area,
		CurrentZones:  e.CurrentZones,
		EnteredZones:  e.EnteredZones,
		HasClip:       e.HasClip,
		HasSnapshot:   e.HasSnapshot,
		Attributes:    map[string]float32{},
	}
}

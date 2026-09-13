package mqtt

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"

	"github.com/bughatti/sentinel/internal/events"
)

// publishQueueDepth is how many messages may be waiting for the broker before
// new ones are dropped. Deep enough to ride out a brief broker stall, small
// enough that it cannot grow without bound.
const publishQueueDepth = 256

// Publisher wraps a Client and provides typed publish methods for Sentinel
// events, motion state, and system stats.
//
// Publishing is asynchronous by design. Client.Publish uses QoS 1 and waits
// for the broker to acknowledge, which can block for as long as the broker is
// unreachable. Callers include the per-camera motion loop, and stalling that
// loop would stall motion detection and recording. Losing an MQTT message is a
// minor annoyance; losing footage is not. So publishes are queued and dropped
// under pressure rather than allowed to block the caller.
type Publisher struct {
	client *Client
	set    PublishSet

	queue   chan func()
	dropped atomic.Int64
}

// NewPublisher creates a Publisher that publishes only the topic families in
// set. Call Run to start draining the queue.
func NewPublisher(c *Client, set PublishSet) *Publisher {
	return &Publisher{
		client: c,
		set:    set,
		queue:  make(chan func(), publishQueueDepth),
	}
}

// Run drains queued publishes until ctx is cancelled. It should be started in
// its own goroutine.
func (p *Publisher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			if n := p.dropped.Load(); n > 0 {
				slog.Warn("mqtt: publisher stopping", "dropped_total", n)
			}
			return
		case fn := <-p.queue:
			fn()
		}
	}
}

// Publishes reports whether a topic family is enabled, so callers can skip
// expensive payload building for topics nobody asked for.
func (p *Publisher) Publishes(kind string) bool { return p.set.Has(kind) }

// enqueue hands a publish to the drain goroutine, dropping it if the queue is
// full. A full queue means the broker is not keeping up, and the alternative
// is blocking a caller that must not block.
func (p *Publisher) enqueue(topic string, retained bool, payload any) {
	select {
	case p.queue <- func() { p.client.Publish(topic, retained, payload) }:
	default:
		n := p.dropped.Add(1)
		// Log the first drop, then every hundredth, so a broker outage leaves
		// evidence without flooding the log.
		if n == 1 || n%100 == 0 {
			slog.Warn("mqtt: publish queue full, message dropped",
				"topic", topic, "dropped_total", n)
		}
	}
}

// PublishEvent publishes an event to <prefix>/events.
// The payload uses a before/after JSON structure.
func (p *Publisher) PublishEvent(e events.Event) {
	if !p.set.Has(KindEvents) {
		return
	}

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
	p.enqueue(p.client.topics.Events(), false, data)
}

// PublishMotion publishes a motion state change for a camera ("ON"/"OFF").
// Call it on transitions only; it is not meant to be called per frame.
func (p *Publisher) PublishMotion(camera string, motion bool) {
	if !p.set.Has(KindMotion) {
		return
	}
	state := "OFF"
	if motion {
		state = "ON"
	}
	// Retained, so a subscriber that connects later learns the current state
	// instead of waiting for the next transition.
	p.enqueue(p.client.topics.CameraMotion(camera), true, state)
}

// PublishStats publishes the stats payload.
func (p *Publisher) PublishStats(payload StatsPayload) {
	if !p.set.Has(KindStats) {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("mqtt: marshal stats payload", "err", err)
		return
	}
	p.enqueue(p.client.topics.Stats(), false, data)
}

// PublishAvailable publishes "online" to the availability topic.
func (p *Publisher) PublishAvailable() {
	if !p.set.Has(KindAvailability) {
		return
	}
	p.enqueue(p.client.topics.Available(), true, "online")
}

// buildEventData maps an events.Event to the published EventData
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

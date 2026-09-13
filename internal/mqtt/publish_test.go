package mqtt

import (
	"testing"
	"time"
)

// An operator who has never heard of this setting must keep the behaviour
// they already had, and an operator who explicitly asks for nothing must get
// nothing. Those are different states, so nil and empty cannot be conflated.
func TestParsePublishSetNilVersusEmpty(t *testing.T) {
	set, unknown := ParsePublishSet(nil)
	if len(unknown) != 0 {
		t.Errorf("nil list should report no unknown entries, got %v", unknown)
	}
	if !set.Has(KindEvents) || !set.Has(KindAvailability) {
		t.Error("an unconfigured publish list must keep publishing events and availability")
	}
	if set.Has(KindMotion) || set.Has(KindStats) {
		t.Error("an unconfigured publish list must not silently start publishing new topics")
	}

	empty, _ := ParsePublishSet([]string{})
	for _, kind := range PublishKinds {
		if empty.Has(kind) {
			t.Errorf("an explicitly empty list must publish nothing, but %q is enabled", kind)
		}
	}
}

func TestParsePublishSetSelection(t *testing.T) {
	set, unknown := ParsePublishSet([]string{"motion", "stats"})
	if len(unknown) != 0 {
		t.Fatalf("unexpected unknown entries: %v", unknown)
	}
	if !set.Has(KindMotion) || !set.Has(KindStats) {
		t.Error("selected kinds should be enabled")
	}
	if set.Has(KindEvents) || set.Has(KindAvailability) {
		t.Error("kinds that were not selected must stay off")
	}
}

// Config files are typed by hand, so tolerate case and stray spaces.
func TestParsePublishSetNormalises(t *testing.T) {
	set, unknown := ParsePublishSet([]string{"  Events ", "MOTION", ""})
	if len(unknown) != 0 {
		t.Fatalf("unexpected unknown entries: %v", unknown)
	}
	if !set.Has(KindEvents) || !set.Has(KindMotion) {
		t.Error("entries should be matched case-insensitively and trimmed")
	}
}

// A typo must be reported, not swallowed. Silently ignoring it leaves someone
// debugging a topic that was never going to arrive.
func TestParsePublishSetReportsUnknown(t *testing.T) {
	set, unknown := ParsePublishSet([]string{"events", "moton"})
	if len(unknown) != 1 || unknown[0] != "moton" {
		t.Errorf("unknown entries = %v, want [moton]", unknown)
	}
	if !set.Has(KindEvents) {
		t.Error("a bad entry must not discard the good ones")
	}
}

func TestPublishSetKindsIsStable(t *testing.T) {
	set := PublishSet{KindStats: true, KindEvents: true}
	got := set.Kinds()
	if len(got) != 2 || got[0] != KindEvents || got[1] != KindStats {
		t.Errorf("Kinds() = %v, want [events stats] in declaration order", got)
	}
}

// testPublisher builds a Publisher whose client can name topics but has no
// broker connection. Nothing ever reaches the network because the drain
// goroutine is never started, so queue contents alone tell us what would have
// been sent.
func testPublisher(set PublishSet) *Publisher {
	return NewPublisher(&Client{topics: Topics{Prefix: "test"}}, set)
}

// A publisher must not hand anything to the broker for a topic family that is
// switched off.
func TestPublisherGatesDisabledKinds(t *testing.T) {
	p := testPublisher(PublishSet{KindEvents: true})

	p.PublishMotion("drive", true)
	p.PublishStats(StatsPayload{})
	p.PublishAvailable()

	if n := len(p.queue); n != 0 {
		t.Errorf("disabled kinds queued %d messages, want 0", n)
	}

	p.PublishMotion("drive", true)
	if len(p.queue) != 0 {
		t.Error("motion is disabled and must stay disabled on repeat calls")
	}
}

func TestPublisherQueuesEnabledKind(t *testing.T) {
	p := testPublisher(PublishSet{KindMotion: true})
	p.PublishMotion("drive", true)
	if n := len(p.queue); n != 1 {
		t.Errorf("enabled kind queued %d messages, want 1", n)
	}
}

// The queue is bounded, and overflow must drop rather than block. If this
// regresses, a stalled broker would freeze the motion goroutine and with it
// motion detection and recording.
func TestPublisherDropsRatherThanBlocks(t *testing.T) {
	p := testPublisher(PublishSet{KindMotion: true})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < publishQueueDepth+50; i++ {
			p.PublishMotion("drive", i%2 == 0)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked when the queue filled; it must drop instead")
	}

	if len(p.queue) != publishQueueDepth {
		t.Errorf("queue holds %d, want it capped at %d", len(p.queue), publishQueueDepth)
	}
	if got := p.dropped.Load(); got != 50 {
		t.Errorf("dropped = %d, want 50", got)
	}
}

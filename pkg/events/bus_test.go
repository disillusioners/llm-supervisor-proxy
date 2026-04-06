package events

import (
	"sync"
	"testing"
	"time"
)

func TestNewBus(t *testing.T) {
	bus := NewBus()

	if bus == nil {
		t.Fatal("NewBus returned nil")
	}

	if bus.subscribers == nil {
		t.Error("subscribers slice is nil")
	}

	if cap(bus.subscribers) != 0 {
		t.Errorf("subscribers slice capacity = %d, want 0", cap(bus.subscribers))
	}

	if len(bus.subscribers) != 0 {
		t.Errorf("subscribers slice length = %d, want 0", len(bus.subscribers))
	}

	if bus.history == nil {
		t.Error("history slice is nil")
	}

	if cap(bus.history) != 100 {
		t.Errorf("history capacity = %d, want 100", cap(bus.history))
	}
}

func TestSubscribe(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v, want nil", err)
	}

	if ch == nil {
		t.Fatal("Subscribe() returned nil channel")
	}

	if cap(ch) != 100 {
		t.Errorf("channel capacity = %d, want 100", cap(ch))
	}

	if len(bus.subscribers) != 1 {
		t.Errorf("subscribers length = %d, want 1", len(bus.subscribers))
	}

	// Subscribe again
	ch2, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("second Subscribe() error = %v, want nil", err)
	}

	if len(bus.subscribers) != 2 {
		t.Errorf("subscribers length = %d, want 2", len(bus.subscribers))
	}

	// Cleanup
	bus.Unsubscribe(ch)
	bus.Unsubscribe(ch2)
}

func TestUnsubscribe(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// Verify channel is in subscribers
	found := false
	for _, sub := range bus.subscribers {
		if sub == ch {
			found = true
			break
		}
	}
	if !found {
		t.Error("subscribed channel not found in bus.subscribers")
	}

	bus.Unsubscribe(ch)

	// Verify channel is removed
	found = false
	for _, sub := range bus.subscribers {
		if sub == ch {
			found = true
			break
		}
	}
	if found {
		t.Error("channel still in bus.subscribers after Unsubscribe")
	}

	// Channel should be closed - sending should panic, but reading closed channel returns zero value
	// We can't easily test panic in Go without recover, so we verify it's not in subscribers list

	// Unsubscribe non-existent channel should not panic
	nonExistent := make(chan Event)
	bus.Unsubscribe(nonExistent)
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	bus.Unsubscribe(ch)

	// Try to receive from closed channel (should return zero value immediately)
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel, got value")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for channel close")
	}
}

func TestPublish(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer bus.Unsubscribe(ch)

	evt := Event{
		Type:      "test_event",
		Timestamp: time.Now().Unix(),
		Data:      map[string]string{"key": "value"},
	}

	bus.Publish(evt)

	select {
	case received := <-ch:
		if received.Type != evt.Type {
			t.Errorf("event type = %q, want %q", received.Type, evt.Type)
		}
		if received.Timestamp != evt.Timestamp {
			t.Errorf("event timestamp = %d, want %d", received.Timestamp, evt.Timestamp)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for event")
	}
}

func TestPublishToMultipleSubscribers(t *testing.T) {
	bus := NewBus()

	subCount := 5
	channels := make([]chan Event, subCount)

	for i := 0; i < subCount; i++ {
		ch, err := bus.Subscribe()
		if err != nil {
			t.Fatalf("Subscribe() error = %v", err)
		}
		channels[i] = ch
		defer bus.Unsubscribe(ch)
	}

	evt := Event{Type: "multi_broadcast", Timestamp: time.Now().Unix()}

	bus.Publish(evt)

	for i, ch := range channels {
		select {
		case received := <-ch:
			if received.Type != evt.Type {
				t.Errorf("subscriber %d: event type = %q, want %q", i, received.Type, evt.Type)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("subscriber %d: timeout waiting for event", i)
		}
	}
}

func TestMaxSubscribers(t *testing.T) {
	bus := NewBus()

	// Fill up to MaxSubscribers
	channels := make([]chan Event, 0, MaxSubscribers)
	for i := 0; i < MaxSubscribers; i++ {
		ch, err := bus.Subscribe()
		if err != nil {
			t.Fatalf("Subscribe() error = %v, want nil at iteration %d", err, i)
		}
		channels = append(channels, ch)
	}

	// One more should fail
	ch, err := bus.Subscribe()
	if err != ErrMaxSubscribers {
		t.Errorf("Subscribe() error = %v, want ErrMaxSubscribers", err)
	}
	if ch != nil {
		t.Error("Subscribe() should return nil channel on error")
	}

	// Cleanup
	for _, ch := range channels {
		bus.Unsubscribe(ch)
	}
}

func TestHistoryReplay(t *testing.T) {
	bus := NewBus()

	// Publish some events before subscribing
	events := []Event{
		{Type: "event_1", Timestamp: 1},
		{Type: "event_2", Timestamp: 2},
		{Type: "event_3", Timestamp: 3},
	}

	for _, evt := range events {
		bus.Publish(evt)
	}

	// Subscribe after events were published
	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer bus.Unsubscribe(ch)

	// Should receive history replay
	for i := 0; i < len(events); i++ {
		select {
		case received := <-ch:
			if received.Type != events[i].Type {
				t.Errorf("event %d: type = %q, want %q", i, received.Type, events[i].Type)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("timeout waiting for replayed event %d", i)
		}
	}
}

func TestHistoryBufferLimit(t *testing.T) {
	bus := NewBus()

	// Publish more events than history buffer capacity
	eventCount := 150
	for i := 0; i < eventCount; i++ {
		evt := Event{Type: "overflow_test", Timestamp: int64(i)}
		bus.Publish(evt)
	}

	// Subscribe to get history
	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer bus.Unsubscribe(ch)

	// Should receive at most 100 events (history buffer size)
	received := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-ch:
			received++
			if received >= 100 {
				return
			}
		case <-timeout:
			// If we got some events but not all, that's expected
			if received < 50 {
				t.Errorf("received only %d events, expected up to 100", received)
			}
			return
		}
	}
}

func TestConcurrentSubscribe(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup
	subCount := 100

	for i := 0; i < subCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, err := bus.Subscribe()
			if err != nil {
				t.Errorf("concurrent Subscribe() error = %v", err)
				return
			}
			bus.Unsubscribe(ch)
		}()
	}

	wg.Wait()

	if len(bus.subscribers) != 0 {
		t.Errorf("subscribers length = %d, want 0 after concurrent unsubscribe", len(bus.subscribers))
	}
}

func TestConcurrentPublish(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer bus.Unsubscribe(ch)

	var wg sync.WaitGroup
	publishCount := 100

	for i := 0; i < publishCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			evt := Event{Type: "concurrent", Timestamp: int64(id)}
			bus.Publish(evt)
		}(i)
	}

	wg.Wait()

	// Drain the channel
	received := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-ch:
			received++
			if received >= publishCount {
				return
			}
		case <-timeout:
			// Some events may be dropped if channel is full
			if received == 0 {
				t.Error("no events received during concurrent publish")
			}
			return
		}
	}
}

func TestConcurrentSubscribeAndPublish(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup

	// Concurrent subscribers
	subCount := 50
	channels := make([]chan Event, subCount)
	for i := 0; i < subCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ch, err := bus.Subscribe()
			if err != nil {
				t.Errorf("Subscribe() error = %v", err)
				return
			}
			channels[idx] = ch
		}(i)
	}

	wg.Wait()

	// Publish events concurrently
	pubCount := 50
	for i := 0; i < pubCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			evt := Event{Type: "mixed_concurrent", Timestamp: int64(id)}
			bus.Publish(evt)
		}(i)
	}

	wg.Wait()

	// Cleanup
	for _, ch := range channels {
		if ch != nil {
			bus.Unsubscribe(ch)
		}
	}
}

func TestUnsubscribeDuringPublish(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Publisher
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			evt := Event{Type: "rapid", Timestamp: int64(i)}
			bus.Publish(evt)
		}
	}()

	// Unsubscribe mid-publish
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		bus.Unsubscribe(ch)
	}()

	wg.Wait()

	// Should not panic
}

func TestEventTypes(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer bus.Unsubscribe(ch)

	testCases := []struct {
		name string
		evt  Event
	}{
		{"FallbackEvent", Event{Type: "fallback", Data: FallbackEvent{FromModel: "gpt-4", ToModel: "gpt-3.5", Reason: "max_retries"}}},
		{"LoopDetectionEvent", Event{Type: "loop_detection", Data: LoopDetectionEvent{RequestID: "req-123", Strategy: "exact_match", Severity: "warning", Confidence: 0.95}}},
		{"ToolRepairEvent", Event{Type: "tool_repair", Data: ToolRepairEvent{RequestID: "req-456", TotalToolCalls: 5, Repaired: 3, Failed: 1}}},
		{"StreamChunkDeadlineEvent", Event{Type: "stream_deadline", Data: StreamChunkDeadlineEvent{RequestID: "req-789", BufferSize: 1024}}},
		{"StreamNormalizeEvent", Event{Type: "stream_normalize", Data: StreamNormalizeEvent{RequestID: "req-101", Normalizer: "json_fixer", Provider: "glm-5"}}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bus.Publish(tc.evt)

			select {
			case received := <-ch:
				if received.Type != tc.evt.Type {
					t.Errorf("event type = %q, want %q", received.Type, tc.evt.Type)
				}
				if received.Data == nil {
					t.Error("event data is nil")
				}
			case <-time.After(100 * time.Millisecond):
				t.Error("timeout waiting for event")
			}
		})
	}
}

func TestPublishEmptyBus(t *testing.T) {
	bus := NewBus()

	// Publishing to bus with no subscribers should not panic
	evt := Event{Type: "no_subscribers", Timestamp: time.Now().Unix()}
	bus.Publish(evt)

	// History should still be recorded
	if len(bus.history) != 1 {
		t.Errorf("history length = %d, want 1", len(bus.history))
	}
}

func TestDoubleUnsubscribe(t *testing.T) {
	bus := NewBus()

	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// First unsubscribe
	bus.Unsubscribe(ch)

	// Second unsubscribe should be no-op
	bus.Unsubscribe(ch)

	// Should not panic
	if len(bus.subscribers) != 0 {
		t.Errorf("subscribers length = %d, want 0", len(bus.subscribers))
	}
}

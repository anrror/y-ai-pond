package realtime

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/cloud/alert"
)

func TestHubSubscribeAndPublish(t *testing.T) {
	hub := NewHub()
	sub, unsub := hub.Subscribe("test-1", "room-a")
	defer unsub()

	hub.Publish("room-a", "hello")
	select {
	case ev := <-sub.Events:
		if ev != "hello" {
			t.Errorf("expected 'hello', got %v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestHubRoomIsolation(t *testing.T) {
	hub := NewHub()

	subA, unsubA := hub.Subscribe("sub-a", "room-a")
	defer unsubA()
	subB, unsubB := hub.Subscribe("sub-b", "room-b")
	defer unsubB()

	hub.Publish("room-a", "for-a")

	// subA receives the event
	select {
	case ev := <-subA.Events:
		if ev != "for-a" {
			t.Errorf("subA got %v, want 'for-a'", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("subA timed out")
	}

	// subB does NOT receive the event (room isolation)
	select {
	case ev := <-subB.Events:
		t.Errorf("subB unexpectedly received %v", ev)
	case <-time.After(100 * time.Millisecond):
		// expected — subB should not get the event
	}
}

func TestHubUnsubscribe(t *testing.T) {
	hub := NewHub()

	sub, unsub := hub.Subscribe("sub-1", "room-x")
	if hub.SubscriberCount("room-x") != 1 {
		t.Fatalf("expected 1 subscriber, got %d", hub.SubscriberCount("room-x"))
	}

	unsub()

	// Channel should be closed after unsubscribe.
	if _, ok := <-sub.Events; ok {
		t.Error("channel should be closed after unsubscribe")
	}

	if hub.SubscriberCount("room-x") != 0 {
		t.Errorf("expected 0 subscribers after unsub, got %d", hub.SubscriberCount("room-x"))
	}
}

func TestHubPublishNonBlocking(t *testing.T) {
	hub := NewHub()

	// Create a subscriber with the default 256-capacity channel and fill it.
	_, unsub := hub.Subscribe("sub-1", "room-nb")
	defer unsub()

	// Fill the channel to capacity.
	for i := 0; i < 256; i++ {
		hub.Publish("room-nb", i)
	}

	// Publish one more — should not block (dropped silently).
	done := make(chan struct{})
	go func() {
		hub.Publish("room-nb", "overflow")
		close(done)
	}()

	select {
	case <-done:
		// Non-blocking publish succeeded.
	case <-time.After(time.Second):
		t.Fatal("Publish blocked — should be non-blocking")
	}
}

func TestHubSubscriberMultiRoom(t *testing.T) {
	hub := NewHub()

	sub, unsub := hub.Subscribe("multi", "room-1", "room-2")
	defer unsub()

	hub.Publish("room-1", "from-1")
	select {
	case ev := <-sub.Events:
		if ev != "from-1" {
			t.Errorf("got %v, want 'from-1'", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	hub.Publish("room-2", "from-2")
	select {
	case ev := <-sub.Events:
		if ev != "from-2" {
			t.Errorf("got %v, want 'from-2'", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestPublishSensor(t *testing.T) {
	hub := NewHub()
	room := SensorRoom("farm-1", "pond-a")
	sub, unsub := hub.Subscribe("sub", room)
	defer unsub()

	ev := SensorEvent{
		FarmID:     "farm-1",
		PondID:     "pond-a",
		SensorType: "do",
		Value:      6.5,
		Timestamp:  "2026-08-10T12:00:00Z",
	}
	hub.PublishSensor("farm-1", "pond-a", ev)

	select {
	case got := <-sub.Events:
		gotEv, ok := got.(SensorEvent)
		if !ok {
			t.Fatalf("expected SensorEvent, got %T", got)
		}
		if gotEv.Value != 6.5 {
			t.Errorf("expected value 6.5, got %f", gotEv.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestPublishAlert(t *testing.T) {
	hub := NewHub()
	room := AlertRoom("farm-1")
	sub, unsub := hub.Subscribe("sub", room)
	defer unsub()

	a := alert.Alert{
		ID:        "alert-1",
		FarmID:    "farm-1",
		PondID:    "pond-a",
		Type:      "do_low",
		Level:     alert.LevelCritical,
		Message:   "DO too low",
		Value:     3.2,
		Timestamp: time.Now(),
	}
	hub.PublishAlert(a)

	select {
	case got := <-sub.Events:
		gotAlert, ok := got.(alert.Alert)
		if !ok {
			t.Fatalf("expected alert.Alert, got %T", got)
		}
		if gotAlert.Message != "DO too low" {
			t.Errorf("got %q, want 'DO too low'", gotAlert.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestPublishDashboard(t *testing.T) {
	hub := NewHub()
	room := DashboardRoom("farm-1")
	sub, unsub := hub.Subscribe("sub", room)
	defer unsub()

	hub.PublishDashboard("farm-1", map[string]string{"status": "ok"})

	select {
	case ev := <-sub.Events:
		m, ok := ev.(map[string]string)
		if !ok {
			t.Fatalf("expected map[string]string, got %T", ev)
		}
		if m["status"] != "ok" {
			t.Errorf("got %v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestHubConcurrentPublish(t *testing.T) {
	hub := NewHub()
	sub, unsub := hub.Subscribe("sub", "room")
	defer unsub()

	const goroutines = 20
	const eventsPerGoroutine = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				hub.Publish("room", id*100+j)
			}
		}(i)
	}
	wg.Wait()

	// Read events with timeout.
	received := 0
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case <-sub.Events:
			received++
			if received >= goroutines*eventsPerGoroutine {
				break loop
			}
		case <-timeout:
			break loop
		}
	}
	if received != goroutines*eventsPerGoroutine {
		t.Errorf("received %d events, want %d", received, goroutines*eventsPerGoroutine)
	}
}

func TestHubShutdown(t *testing.T) {
	hub := NewHub()
	sub, _ := hub.Subscribe("sub", "room")

	if err := hub.Shutdown(context.TODO()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// After shutdown, channel should be closed.
	if _, ok := <-sub.Events; ok {
		t.Error("channel should be closed after shutdown")
	}
}

func TestRoomNaming(t *testing.T) {
	if got := SensorRoom("f1", "p1"); got != "sensor:f1:p1" {
		t.Errorf("SensorRoom: %q", got)
	}
	if got := AlertRoom("f1"); got != "alert:f1" {
		t.Errorf("AlertRoom: %q", got)
	}
	if got := DashboardRoom("f1"); got != "dashboard:f1" {
		t.Errorf("DashboardRoom: %q", got)
	}
}

func TestWriteSSE(t *testing.T) {
	var buf bytes.Buffer
	ev := map[string]string{"msg": "hello"}
	if err := WriteSSE(&buf, ev); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}

	expected := `data: {"msg":"hello"}` + "\n\n"
	if buf.String() != expected {
		t.Errorf("got %q, want %q", buf.String(), expected)
	}
}

func TestWriteSSEComment(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSSEComment(&buf, "ping"); err != nil {
		t.Fatalf("WriteSSEComment: %v", err)
	}
	if !strings.Contains(buf.String(), ": ping") {
		t.Errorf("comment missing 'ping': %q", buf.String())
	}
}

func TestSSEWriter(t *testing.T) {
	hub := NewHub()
	sub, unsub := hub.Subscribe("sse-test", "room")
	// We intentionally don't call unsub — the test will end and GC will clean up.
	_ = unsub

	var buf bytes.Buffer
	cfg := SSEConfig{
		HeartbeatInterval: 50 * time.Millisecond,
		BufferSize:        256,
	}

	// Publish an event and stop the writer after enough data is collected.
	done := make(chan struct{})
	writer := NewSSEWriter(sub, &buf, func() {}, nil, cfg)

	go func() {
		time.Sleep(30 * time.Millisecond)
		hub.Publish("room", SensorEvent{FarmID: "f1", PondID: "p1", SensorType: "do", Value: 7.0})
		// Allow time for heartbeat and event to be written.
		time.Sleep(150 * time.Millisecond)
		close(done)
	}()

	// Run the writer in a goroutine; we'll stop by unsubscribing.
	go func() {
		_ = writer.Run()
	}()

	<-done
	// Signal the writer to stop by unsubscribing (closes the channel).
	unsub()

	// Wait briefly for the writer to finish.
	time.Sleep(50 * time.Millisecond)

	output := buf.String()
	if !strings.Contains(output, `"sensor_type":"do"`) {
		t.Errorf("SSE output missing sensor event: %q", output)
	}
	if !strings.Contains(output, ": ping") {
		t.Errorf("SSE output missing heartbeat: %q", output)
	}
}

func TestSSEWriterChannelClosed(t *testing.T) {
	hub := NewHub()
	sub, unsub := hub.Subscribe("sse-close", "room")
	unsub() // close immediately

	var buf bytes.Buffer
	cfg := DefaultSSEConfig()
	writer := NewSSEWriter(sub, &buf, func() {}, nil, cfg)
	if err := writer.Run(); err != nil {
		t.Fatalf("expected nil on channel close, got %v", err)
	}
}

func TestDefaultSSEConfig(t *testing.T) {
	cfg := DefaultSSEConfig()
	if cfg.HeartbeatInterval != 15*time.Second {
		t.Errorf("HeartbeatInterval: %v", cfg.HeartbeatInterval)
	}
	if cfg.BufferSize != 256 {
		t.Errorf("BufferSize: %d", cfg.BufferSize)
	}
}

package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Mocks
// ============================================================================

type mockCommander struct {
	mu     sync.Mutex
	calls  []publishCall
	err    error // if set, PublishCommand returns this
	errCnt int   // if > 0, fail the first N calls
}

type publishCall struct {
	DeviceID string
	Topic    string
	Payload  []byte
}

func (m *mockCommander) PublishCommand(_ context.Context, deviceID, topic string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, publishCall{DeviceID: deviceID, Topic: topic, Payload: payload})
	if m.errCnt > 0 {
		m.errCnt--
		return m.err
	}
	return m.err
}

type mockAlerts struct {
	mu    sync.Mutex
	calls []offlineCall
}

type offlineCall struct {
	DeviceID string
	Reason   string
}

func (m *mockAlerts) ReportOffline(_ context.Context, d Device, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, offlineCall{DeviceID: d.ID, Reason: reason})
	return nil
}

// ============================================================================
// Helpers
// ============================================================================

func testDevices() []Device {
	now := time.Now()
	return []Device{
		{ID: "d1", FarmID: "f1", PondID: "p1", Type: "feeding_motor", Status: "online", LastHeartbeat: now},
		{ID: "d2", FarmID: "f1", PondID: "p2", Type: "feeding_motor", Status: "online", LastHeartbeat: now},
		{ID: "d3", FarmID: "f1", PondID: "p3", Type: "feeding_motor", Status: "online", LastHeartbeat: now},
		{ID: "d4", FarmID: "f1", PondID: "p1", Type: "aerator", Status: "online", LastHeartbeat: now},
		{ID: "d5", FarmID: "f1", PondID: "p2", Type: "aerator", Status: "online", LastHeartbeat: now},
	}
}

func testDevicesWithOffline() []Device {
	now := time.Now()
	return []Device{
		{ID: "d1", FarmID: "f1", PondID: "p1", Type: "feeding_motor", Status: "online", LastHeartbeat: now},
		{ID: "d2", FarmID: "f1", PondID: "p2", Type: "feeding_motor", Status: "offline", LastHeartbeat: now},
		{ID: "d3", FarmID: "f1", PondID: "p3", Type: "feeding_motor", Status: "online", LastHeartbeat: now},
	}
}

func newTestScheduler(cmd *mockCommander) *Scheduler {
	return NewScheduler(cmd, &mockAlerts{}, nil)
}

// ============================================================================
// TestBatchFeeding — 3 ponds, 1 online feeding device each → 3 MQTT commands
// ============================================================================

func TestBatchFeeding(t *testing.T) {
	cmd := &mockCommander{}
	s := newTestScheduler(cmd)
	s.SetDevices(testDevices())

	ctx := context.Background()
	results := s.BatchFeeding(ctx, "f1", []string{"p1", "p2", "p3"}, map[string]any{"speed": 50})

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "sent" {
			t.Errorf("device %s: expected sent, got %s", r.DeviceID, r.Status)
		}
	}

	if len(cmd.calls) != 3 {
		t.Fatalf("expected 3 publish calls, got %d", len(cmd.calls))
	}

	wantTopics := map[string]bool{
		"cloud/f1/p1/cmd/feeding/start": false,
		"cloud/f1/p2/cmd/feeding/start": false,
		"cloud/f1/p3/cmd/feeding/start": false,
	}
	for _, c := range cmd.calls {
		if _, ok := wantTopics[c.Topic]; !ok {
			t.Errorf("unexpected topic: %s", c.Topic)
		}
		wantTopics[c.Topic] = true
	}
	for topic, found := range wantTopics {
		if !found {
			t.Errorf("topic not published: %s", topic)
		}
	}
}

// ============================================================================
// TestBatchFeedingSkipsOffline — offline device skipped, others still sent
// ============================================================================

func TestBatchFeedingSkipsOffline(t *testing.T) {
	cmd := &mockCommander{}
	s := newTestScheduler(cmd)
	s.SetDevices(testDevicesWithOffline())

	ctx := context.Background()
	results := s.BatchFeeding(ctx, "f1", []string{"p1", "p2", "p3"}, map[string]any{"speed": 50})

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	var sent, skipped int
	for _, r := range results {
		switch r.Status {
		case "sent":
			sent++
		case "skipped":
			skipped++
			if r.DeviceID != "d2" {
				t.Errorf("expected d2 to be skipped, got %s", r.DeviceID)
			}
		default:
			t.Errorf("unexpected status %s for device %s", r.Status, r.DeviceID)
		}
	}
	if sent != 2 {
		t.Errorf("expected 2 sent, got %d", sent)
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", skipped)
	}
	if len(cmd.calls) != 2 {
		t.Errorf("expected 2 publish calls, got %d", len(cmd.calls))
	}
}

// ============================================================================
// TestBatchFeedingCommanderError — commander error produces "failed" result
// ============================================================================

func TestBatchFeedingCommanderError(t *testing.T) {
	cmd := &mockCommander{err: fmt.Errorf("mqtt timeout")}
	s := newTestScheduler(cmd)
	s.SetDevices([]Device{
		{ID: "d1", FarmID: "f1", PondID: "p1", Type: "feeding_motor", Status: "online"},
	})

	results := s.BatchFeeding(context.Background(), "f1", []string{"p1"}, map[string]any{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "failed" {
		t.Errorf("expected failed, got %s", results[0].Status)
	}
}

// ============================================================================
// TestHeartbeatCheck — stale heartbeat → OFFLINE alert, no duplicate alerts
// ============================================================================

func TestHeartbeatCheck(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	timeout := 2 * time.Minute
	alerts := &mockAlerts{}
	hm := NewHealthMonitor(timeout, nil)
	hm.setNow(func() time.Time { return now })

	devices := []Device{
		{ID: "d1", FarmID: "f1", PondID: "p1", Type: "sensor_node", Status: "online", LastHeartbeat: now.Add(-5 * time.Minute)},
	}

	// First check: device should be marked offline and alert reported.
	out := hm.Check(context.Background(), devices, alerts)
	if len(out) != 1 {
		t.Fatalf("expected 1 device, got %d", len(out))
	}
	if out[0].Status != "offline" {
		t.Errorf("expected offline status, got %s", out[0].Status)
	}
	if len(alerts.calls) != 1 {
		t.Errorf("expected 1 alert, got %d", len(alerts.calls))
	}

	// Second check with same stale device: no duplicate alert.
	alerts.calls = nil
	out2 := hm.Check(context.Background(), devices, alerts)
	if out2[0].Status != "offline" {
		t.Errorf("expected still offline, got %s", out2[0].Status)
	}
	if len(alerts.calls) != 0 {
		t.Errorf("expected 0 duplicate alerts, got %d", len(alerts.calls))
	}

	// Third check with fresh heartbeat: device back online, flag cleared.
	fresh := []Device{
		{ID: "d1", FarmID: "f1", PondID: "p1", Type: "sensor_node", Status: "online", LastHeartbeat: now},
	}
	out3 := hm.Check(context.Background(), fresh, alerts)
	if out3[0].Status != "online" {
		t.Errorf("expected online after recovery, got %s", out3[0].Status)
	}

	// Fourth check with stale again: alert fires again (new episode).
	alerts.calls = nil
	out4 := hm.Check(context.Background(), devices, alerts)
	if out4[0].Status != "offline" {
		t.Errorf("expected offline again, got %s", out4[0].Status)
	}
	if len(alerts.calls) != 1 {
		t.Errorf("expected 1 alert for new offline episode, got %d", len(alerts.calls))
	}
}

// ============================================================================
// TestAerationRotation — round-robin across pool of 2 aerators
// ============================================================================

func TestAerationRotation(t *testing.T) {
	r := NewRotator()

	tests := []struct {
		call int
		want int
	}{
		{1, 0},
		{2, 1},
		{3, 0},
		{4, 1},
	}
	for _, tc := range tests {
		got := r.Next("pond1-aerators", 2)
		if got != tc.want {
			t.Errorf("call %d: expected index %d, got %d", tc.call, tc.want, got)
		}
	}
}

// ============================================================================
// TestRotatorZeroPoolSize
// ============================================================================

func TestRotatorZeroPoolSize(t *testing.T) {
	r := NewRotator()
	if got := r.Next("key", 0); got != 0 {
		t.Errorf("expected 0 for zero pool size, got %d", got)
	}
	if got := r.Next("key", -1); got != 0 {
		t.Errorf("expected 0 for negative pool size, got %d", got)
	}
}

// ============================================================================
// TestBatchAeration — 2 ponds → aerator on/off commands with correct topics
// ============================================================================

func TestBatchAeration(t *testing.T) {
	cmd := &mockCommander{}
	s := newTestScheduler(cmd)
	s.SetDevices(testDevices())

	results := s.BatchAeration(context.Background(), "f1", []string{"p1", "p2"}, true)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "sent" {
			t.Errorf("device %s: expected sent, got %s", r.DeviceID, r.Status)
		}
	}

	if len(cmd.calls) != 2 {
		t.Fatalf("expected 2 publish calls, got %d", len(cmd.calls))
	}
	wantTopics := map[string]bool{
		"cloud/f1/p1/cmd/aerator/on": false,
		"cloud/f1/p2/cmd/aerator/on": false,
	}
	for _, c := range cmd.calls {
		if _, ok := wantTopics[c.Topic]; !ok {
			t.Errorf("unexpected topic: %s", c.Topic)
		}
		wantTopics[c.Topic] = true
	}
	for topic, found := range wantTopics {
		if !found {
			t.Errorf("topic not published: %s", topic)
		}
	}
}

// ============================================================================
// TestBatchAerationOff
// ============================================================================

func TestBatchAerationOff(t *testing.T) {
	cmd := &mockCommander{}
	s := newTestScheduler(cmd)
	s.SetDevices(testDevices())

	results := s.BatchAeration(context.Background(), "f1", []string{"p1"}, false)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(cmd.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(cmd.calls))
	}
	if cmd.calls[0].Topic != "cloud/f1/p1/cmd/aerator/off" {
		t.Errorf("expected aerator/off topic, got %s", cmd.calls[0].Topic)
	}
}

// ============================================================================
// TestBuildTree — farm→pond→device grouping
// ============================================================================

func TestBuildTree(t *testing.T) {
	s := newTestScheduler(nil)
	devices := []Device{
		{ID: "d1", FarmID: "f1", PondID: "p1", Type: "feeding_motor"},
		{ID: "d2", FarmID: "f1", PondID: "p2", Type: "feeding_motor"},
		{ID: "d3", FarmID: "f2", PondID: "p1", Type: "aerator"},
	}

	tree := s.BuildTree(devices)

	if len(tree) != 2 {
		t.Fatalf("expected 2 farms, got %d", len(tree))
	}
	if len(tree["f1"].Ponds) != 2 {
		t.Errorf("farm f1: expected 2 ponds, got %d", len(tree["f1"].Ponds))
	}
	if len(tree["f2"].Ponds) != 1 {
		t.Errorf("farm f2: expected 1 pond, got %d", len(tree["f2"].Ponds))
	}
	if len(tree["f1"].Ponds["p1"]) != 1 {
		t.Errorf("farm f1 pond p1: expected 1 device, got %d", len(tree["f1"].Ponds["p1"]))
	}
}

// ============================================================================
// TestBatchFeedingPayloadRoundTrip — verify payload is JSON-marshalled
// ============================================================================

func TestBatchFeedingPayloadRoundTrip(t *testing.T) {
	cmd := &mockCommander{}
	s := newTestScheduler(cmd)
	s.SetDevices(testDevices())

	payload := map[string]any{"speed": 75, "duration": 30}
	s.BatchFeeding(context.Background(), "f1", []string{"p1"}, payload)

	if len(cmd.calls) == 0 {
		t.Fatal("expected at least 1 publish call")
	}
	var decoded map[string]any
	if err := json.Unmarshal(cmd.calls[0].Payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if v, ok := decoded["speed"].(float64); !ok || v != 75 {
		t.Errorf("speed: want 75, got %v", decoded["speed"])
	}
}

package alert

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// TestThresholdAlert — DO=3.5 → do_low alert with LevelCritical
// ============================================================================

func TestThresholdAlert_DOBelowThreshold(t *testing.T) {
	cfg := DefaultConfig()
	snap := SensorSnapshot{DO: 3.5, PH: 7.0, Temp: 25.0, NH3: 0.1}
	alerts := thresholdAlert("farm-1", "pond-1", snap, cfg)

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	a := alerts[0]
	if a.Type != "do_low" {
		t.Errorf("type: want do_low, got %s", a.Type)
	}
	if a.Level != LevelCritical {
		t.Errorf("level: want CRITICAL, got %s", a.Level)
	}
	if a.Value != 3.5 {
		t.Errorf("value: want 3.5, got %.2f", a.Value)
	}
}

// ============================================================================
// TestThresholdAlertNormal — DO=6, pH=7, Temp=25, NH3=0.1 → no alerts
// ============================================================================

func TestThresholdAlert_Normal(t *testing.T) {
	cfg := DefaultConfig()
	snap := SensorSnapshot{DO: 6.0, PH: 7.0, Temp: 25.0, NH3: 0.1}
	alerts := thresholdAlert("farm-1", "pond-1", snap, cfg)
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for normal readings, got %d", len(alerts))
	}
}

// ============================================================================
// TestThresholdAlert — table-driven for all threshold violations
// ============================================================================

func TestThresholdAlert_AllViolations(t *testing.T) {
	cfg := DefaultConfig()
	tests := []struct {
		name     string
		snap     SensorSnapshot
		wantType string
		wantLvl  Level
	}{
		{"do_low", SensorSnapshot{DO: 3.0, PH: 7.0, Temp: 25.0, NH3: 0.1}, "do_low", LevelCritical},
		{"ph_low", SensorSnapshot{DO: 6.0, PH: 6.0, Temp: 25.0, NH3: 0.1}, "ph_low", LevelCritical},
		{"ph_high", SensorSnapshot{DO: 6.0, PH: 9.0, Temp: 25.0, NH3: 0.1}, "ph_high", LevelCritical},
		{"temp_high", SensorSnapshot{DO: 6.0, PH: 7.0, Temp: 36.0, NH3: 0.1}, "temp_high", LevelWarning},
		{"nh3_high", SensorSnapshot{DO: 6.0, PH: 7.0, Temp: 25.0, NH3: 0.6}, "nh3_high", LevelCritical},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alerts := thresholdAlert("farm-1", "pond-1", tc.snap, cfg)
			if len(alerts) < 1 {
				t.Fatalf("expected at least 1 alert, got %d", len(alerts))
			}
			found := false
			for _, a := range alerts {
				if a.Type == tc.wantType {
					found = true
					if a.Level != tc.wantLvl {
						t.Errorf("level for %s: want %s, got %s", tc.wantType, tc.wantLvl, a.Level)
					}
				}
			}
			if !found {
				t.Errorf("expected alert type %s, got %v", tc.wantType, alertTypes(alerts))
			}
		})
	}
}

func alertTypes(alerts []Alert) []string {
	types := make([]string, len(alerts))
	for i, a := range alerts {
		types[i] = a.Type
	}
	return types
}

// ============================================================================
// TestAlertDedup — same key within 60s → only one alert
// ============================================================================

func TestAlertDedup(t *testing.T) {
	ctx := context.Background()
	d := NewMemoryDeduper(60 * time.Second)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	key := DedupKey(Alert{FarmID: "farm-1", PondID: "pond-1", Type: "do_low"})

	if !d.Allow(ctx, key, now) {
		t.Error("first call: expected allow=true")
	}

	// Same key, same time — should be suppressed.
	if d.Allow(ctx, key, now) {
		t.Error("second call within window: expected allow=false")
	}

	// Same key, 30s later — still within window.
	if d.Allow(ctx, key, now.Add(30*time.Second)) {
		t.Error("30s later within window: expected allow=false")
	}

	// Same key, 61s later — outside window, should allow.
	if !d.Allow(ctx, key, now.Add(61*time.Second)) {
		t.Error("61s later outside window: expected allow=true")
	}

	// Different key — should always allow.
	key2 := DedupKey(Alert{FarmID: "farm-1", PondID: "pond-1", Type: "ph_low"})
	if !d.Allow(ctx, key2, now) {
		t.Error("different key: expected allow=true")
	}
}

// ============================================================================
// TestAlertEscalation — WARNING persisting 30min → CRITICAL
// ============================================================================

func TestAlertEscalation(t *testing.T) {
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	now := start
	clock := func() time.Time { return now }

	e := NewEscalator(30*time.Minute, clock)

	a := Alert{
		FarmID:  "farm-1",
		PondID:  "pond-1",
		Type:    "temp_high",
		Level:   LevelWarning,
		Message: "Temperature too high",
	}

	// First call records first-seen, level unchanged.
	a1 := e.Escalate(a, now)
	if a1.Level != LevelWarning {
		t.Errorf("first appearance: want WARNING, got %s", a1.Level)
	}

	// 29 minutes later — still within escalation window.
	now = start.Add(29 * time.Minute)
	a2 := e.Escalate(a, now)
	if a2.Level != LevelWarning {
		t.Errorf("29 min: want WARNING, got %s", a2.Level)
	}

	// 30 minutes later — escalation triggered.
	now = start.Add(30 * time.Minute)
	a3 := e.Escalate(a, now)
	if a3.Level != LevelCritical {
		t.Errorf("30 min: want CRITICAL, got %s", a3.Level)
	}
	if a3.Message[:11] != "[ESCALATED]" {
		t.Errorf("30 min: want [ESCALATED] prefix, got %q", a3.Message[:20])
	}

	// Non-WARNING alerts pass through unchanged.
	aCrit := Alert{FarmID: "farm-1", PondID: "pond-1", Type: "do_low", Level: LevelCritical}
	a4 := e.Escalate(aCrit, now)
	if a4.Level != LevelCritical {
		t.Errorf("CRITICAL input: want unchanged CRITICAL, got %s", a4.Level)
	}
}

// ============================================================================
// TestAnomalyDetection — stable series then a spike → anomaly alert
// ============================================================================

func TestAnomalyDetection(t *testing.T) {
	ctx := context.Background()
	d := NewAnomalyDetector(3.0, 15)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	// Feed 14 stable values: DO ~ 6.5 (very narrow band).
	for i := 0; i < 14; i++ {
		a, err := d.Detect(ctx, "farm-1", "pond-1", 6.5, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a != nil {
			t.Fatalf("stable value %d: expected nil alert, got %v", i, a.Type)
		}
		now = now.Add(1 * time.Minute)
	}

	// 15th value fills the window (15 values), still stable.
	a, err := d.Detect(ctx, "farm-1", "pond-1", 6.5, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != nil {
		t.Fatalf("window full with stable values: expected nil alert, got %s", a.Type)
	}
	now = now.Add(1 * time.Minute)

	// Now feed a spike: DO drops to 2.0 (should be anomalous).
	a, err = d.Detect(ctx, "farm-1", "pond-1", 2.0, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("spike DO=2.0: expected anomaly alert")
	}
	if a.Type != "anomaly_low" {
		t.Errorf("spike direction: want anomaly_low, got %s", a.Type)
	}
	if a.Level != LevelWarning {
		t.Errorf("anomaly level: want WARNING, got %s", a.Level)
	}
}

// ============================================================================
// TestAnomalyDetectionStableOnly — no false positives on stable data
// ============================================================================

func TestAnomalyDetectionStableOnly(t *testing.T) {
	ctx := context.Background()
	d := NewAnomalyDetector(3.0, 10)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 30; i++ {
		a, err := d.Detect(ctx, "farm-1", "pond-1", 6.5, now)
		if err != nil {
			t.Fatalf("unexpected error at %d: %v", i, err)
		}
		if a != nil {
			t.Errorf("stable series index %d: unexpected anomaly alert", i)
		}
		now = now.Add(1 * time.Minute)
	}
}

// ============================================================================
// TestEngineRateLimit — notifier called at most once per RateLimit
// ============================================================================

func TestEngineRateLimit(t *testing.T) {
	cfg := DefaultConfig()
	// Use a small rate limit for the test.
	cfg.RateLimit = 200 * time.Millisecond

	var count int32
	rn := &countingNotifier{count: &count}

	engine := NewEngine(cfg, nil, nil, nil, rn)
	ctx := context.Background()

	// Fire two threshold-violating snapshots rapidly.
	snap1 := SensorSnapshot{DO: 3.0, PH: 7.0, Temp: 25.0, NH3: 0.1}
	snap2 := SensorSnapshot{DO: 3.0, PH: 6.0, Temp: 25.0, NH3: 0.6}

	engine.Evaluate(ctx, "farm-1", "pond-1", snap1)
	engine.Evaluate(ctx, "farm-1", "pond-1", snap2)

	// Wait for async notifications.
	time.Sleep(500 * time.Millisecond)

	c := atomic.LoadInt32(&count)
	if c < 1 {
		t.Errorf("expected at least 1 notification, got %d", c)
	}
	// Rate limit means the second set should be rate-limited.
}

// countingNotifier is a test helper that counts notifications.
type countingNotifier struct {
	count *int32
}

func (n *countingNotifier) Notify(_ context.Context, _ Alert) error {
	atomic.AddInt32(n.count, 1)
	return nil
}

// ============================================================================
// TestDefaultConfig — verify defaults match spec
// ============================================================================

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.PHMin != 6.5 {
		t.Errorf("PHMin: want 6.5, got %.1f", cfg.PHMin)
	}
	if cfg.PHMax != 8.5 {
		t.Errorf("PHMax: want 8.5, got %.1f", cfg.PHMax)
	}
	if cfg.DOMin != 4.0 {
		t.Errorf("DOMin: want 4.0, got %.1f", cfg.DOMin)
	}
	if cfg.TempMax != 35.0 {
		t.Errorf("TempMax: want 35.0, got %.1f", cfg.TempMax)
	}
	if cfg.NH3Max != 0.5 {
		t.Errorf("NH3Max: want 0.5, got %.1f", cfg.NH3Max)
	}
	if cfg.DedupWindow != 60*time.Second {
		t.Errorf("DedupWindow: want 60s, got %s", cfg.DedupWindow)
	}
	if cfg.EscalationDuration != 30*time.Minute {
		t.Errorf("EscalationDuration: want 30m, got %s", cfg.EscalationDuration)
	}
	if cfg.RateLimit != time.Second {
		t.Errorf("RateLimit: want 1s, got %s", cfg.RateLimit)
	}
	if cfg.AnomalySigma != 3.0 {
		t.Errorf("AnomalySigma: want 3.0, got %.1f", cfg.AnomalySigma)
	}
}

// ============================================================================
// TestMultiViolation — single snapshot triggers multiple alert types
// ============================================================================

func TestMultiViolation(t *testing.T) {
	cfg := DefaultConfig()
	snap := SensorSnapshot{DO: 3.0, PH: 5.0, Temp: 36.0, NH3: 0.7}
	alerts := thresholdAlert("farm-1", "pond-1", snap, cfg)

	expectedTypes := map[string]bool{
		"do_low":    false,
		"ph_low":    false,
		"temp_high": false,
		"nh3_high":  false,
	}
	for _, a := range alerts {
		expectedTypes[a.Type] = true
	}
	for typ, found := range expectedTypes {
		if !found {
			t.Errorf("expected alert type %s not generated", typ)
		}
	}
}

// ============================================================================
// TestDedupKey
// ============================================================================

func TestDedupKey(t *testing.T) {
	a := Alert{FarmID: "farm-1", PondID: "pond-2", Type: "do_low"}
	key := DedupKey(a)
	expected := "alert:dedup:farm-1:pond-2:do_low"
	if key != expected {
		t.Errorf("DedupKey: want %q, got %q", expected, key)
	}
}

// ============================================================================
// TestEscalatorMultipleKeys — different alert types tracked independently
// ============================================================================

func TestEscalatorMultipleKeys(t *testing.T) {
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	now := start
	clock := func() time.Time { return now }

	e := NewEscalator(10*time.Minute, clock)

	a1 := Alert{FarmID: "farm-1", PondID: "pond-1", Type: "temp_high", Level: LevelWarning}
	a2 := Alert{FarmID: "farm-1", PondID: "pond-1", Type: "ph_high", Level: LevelWarning}

	_ = e.Escalate(a1, now)
	_ = e.Escalate(a2, now)

	// Advance 10m — temp_high should escalate, ph_high should too.
	now = start.Add(10 * time.Minute)
	r1 := e.Escalate(a1, now)
	r2 := e.Escalate(a2, now)

	if r1.Level != LevelCritical {
		t.Errorf("temp_high after 10m: want CRITICAL, got %s", r1.Level)
	}
	if r2.Level != LevelCritical {
		t.Errorf("ph_high after 10m: want CRITICAL, got %s", r2.Level)
	}
}

// ============================================================================
// TestLogNotifier
// ============================================================================

func TestLogNotifier(t *testing.T) {
	n := NewLogNotifier(nil)
	ctx := context.Background()
	a := Alert{ID: "test-1", FarmID: "f1", PondID: "p1", Type: "do_low", Level: LevelCritical, Value: 3.0}
	if err := n.Notify(ctx, a); err != nil {
		t.Errorf("LogNotifier.Notify returned error: %v", err)
	}
}

// ============================================================================
// TestMultiNotifier
// ============================================================================

func TestMultiNotifier(t *testing.T) {
	var mu sync.Mutex
	var received []Alert

	n1 := &captureNotifier{fn: func(a Alert) {
		mu.Lock()
		received = append(received, a)
		mu.Unlock()
	}}
	n2 := &captureNotifier{fn: func(a Alert) {
		mu.Lock()
		received = append(received, a)
		mu.Unlock()
	}}

	mn := NewMultiNotifier(nil, n1, n2)
	ctx := context.Background()
	a := Alert{ID: "test-1", FarmID: "f1", PondID: "p1", Type: "do_low", Level: LevelCritical, Value: 3.0}

	if err := mn.Notify(ctx, a); err != nil {
		t.Errorf("MultiNotifier error: %v", err)
	}

	mu.Lock()
	if len(received) != 2 {
		t.Errorf("expected 2 notifications, got %d", len(received))
	}
	mu.Unlock()
}

type captureNotifier struct {
	fn func(Alert)
}

func (n *captureNotifier) Notify(_ context.Context, a Alert) error {
	if n.fn != nil {
		n.fn(a)
	}
	return nil
}

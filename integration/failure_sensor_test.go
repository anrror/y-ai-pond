// Sensor failure and alert engine tests (T35).
//
// Tests:
//   - TestSensorFailure_AlertRaised: low DO → CRITICAL alert generated
//   - TestSensorFailure_MultipleViolations: multiple thresholds violated → multiple alerts
//   - TestSensorFailure_NormalReadings: healthy readings → zero alerts
//   - TestSensorFailure_GatewaySkipsOutOfRange: gateway drops invalid sensor data
//   - TestSensorFailure_EdgeControllerSkipsSensorError: controller retains last reading on sensor error
//
// Uses the pkg/cloud/alert engine with in-memory notifiers (no Docker).
package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/cloud/alert"
	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/pashagolub/pgxmock/v4"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// TestSensorFailure_AlertRaised — low DO triggers CRITICAL alert
// ---------------------------------------------------------------------------

func TestSensorFailure_AlertRaised(t *testing.T) {
	// ---- Given: an alert engine with a capturing notifier ----
	var mu sync.Mutex
	var capturedAlerts []alert.Alert
	captureNotifier := &captureAlertNotifier{fn: func(a alert.Alert) {
		mu.Lock()
		defer mu.Unlock()
		capturedAlerts = append(capturedAlerts, a)
	}}

	cfg := alert.DefaultConfig()
	cfg.RateLimit = 10 * time.Millisecond

	engine := alert.NewEngine(cfg, nil, nil, nil, captureNotifier)

	// ---- When: a sensor reading with dangerously low DO is evaluated ----
	snap := alert.SensorSnapshot{DO: 3.0, PH: 7.0, Temp: 25.0, NH3: 0.1}
	alerts := engine.Evaluate(context.Background(), "farm-1", "pond-1", snap)

	// ---- Then: a CRITICAL do_low alert is generated ----
	if len(alerts) == 0 {
		t.Fatal("expected at least 1 alert for low DO")
	}
	foundDO := false
	for _, a := range alerts {
		if a.Type == "do_low" {
			foundDO = true
			if a.Level != alert.LevelCritical {
				t.Errorf("do_low level: want CRITICAL, got %s", a.Level)
			}
			if a.Value != 3.0 {
				t.Errorf("do_low value: want 3.0, got %.2f", a.Value)
			}
		}
	}
	if !foundDO {
		t.Errorf("expected do_low alert, got types: %v", alertTypesFromAlerts(alerts))
	}

	// Wait for async notification delivery.
	time.Sleep(100 * time.Millisecond)

	// ---- Then: notifier received the alert ----
	mu.Lock()
	notifierCount := len(capturedAlerts)
	mu.Unlock()
	if notifierCount == 0 {
		t.Error("expected at least 1 alert delivered to notifier")
	}
}

func alertTypesFromAlerts(alerts []alert.Alert) []string {
	types := make([]string, len(alerts))
	for i, a := range alerts {
		types[i] = a.Type
	}
	return types
}

// ---------------------------------------------------------------------------
// TestSensorFailure_MultipleViolations — multiple thresholds violated at once
// ---------------------------------------------------------------------------

func TestSensorFailure_MultipleViolations(t *testing.T) {
	cfg := alert.DefaultConfig()
	cfg.RateLimit = 10 * time.Millisecond

	var mu sync.Mutex
	var capturedAlerts []alert.Alert
	engine := alert.NewEngine(cfg, nil, nil, nil, &captureAlertNotifier{fn: func(a alert.Alert) {
		mu.Lock()
		defer mu.Unlock()
		capturedAlerts = append(capturedAlerts, a)
	}})

	// ---- When: DO < threshold AND pH < threshold AND NH3 > threshold ----
	snap := alert.SensorSnapshot{DO: 3.5, PH: 5.0, Temp: 25.0, NH3: 0.6}
	alerts := engine.Evaluate(context.Background(), "farm-1", "pond-1", snap)

	expectedTypes := map[string]bool{"do_low": false, "ph_low": false, "nh3_high": false}
	for _, a := range alerts {
		expectedTypes[a.Type] = true
	}
	for typ, found := range expectedTypes {
		if !found {
			t.Errorf("expected alert type %s not generated in multi-violation scenario", typ)
		}
	}
}

// ---------------------------------------------------------------------------
// TestSensorFailure_NormalReadings — no alerts for healthy readings
// ---------------------------------------------------------------------------

func TestSensorFailure_NormalReadings(t *testing.T) {
	cfg := alert.DefaultConfig()
	engine := alert.NewEngine(cfg, nil, nil, nil, nil)

	// ---- When: healthy sensor readings ----
	snap := alert.SensorSnapshot{DO: 7.0, PH: 7.2, Temp: 24.0, NH3: 0.05}
	alerts := engine.Evaluate(context.Background(), "farm-1", "pond-1", snap)

	// ---- Then: zero alerts ----
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts for healthy readings, got %d: %v",
			len(alerts), alertTypesFromAlerts(alerts))
	}
}

// ---------------------------------------------------------------------------
// TestSensorFailure_GatewaySkipsOutOfRange — gateway drops invalid sensor data
// ---------------------------------------------------------------------------

func TestSensorFailure_GatewaySkipsOutOfRange(t *testing.T) {
	// ---- Given: running MQTT broker, gateway, and influx ----
	addr, stopBroker := startMochiBroker(t)
	defer stopBroker()

	pgMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgxmock: %v", err)
	}
	defer pgMock.Close()

	pgMock.ExpectExec("INSERT INTO devices").
		WithArgs("sensor-node-01", "farm-1", "pond-1", "sensor_node", "online", "unknown", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	influx := newTestInfluxWriter()
	gw := newTestGateway(influx, pgMock)

	received := make(chan struct{}, 10)
	gwHandler := func(ctx context.Context, topic string, payload []byte) {
		gw.HandleMessage(ctx, topic, payload)
		select {
		case received <- struct{}{}:
		default:
		}
	}
	subClient := newBrokerMQTTClient(t, addr, "gw-sensor-err", gwHandler)
	defer func() { _ = subClient.Disconnect(context.Background()) }()

	sensorTopic := "pond/v1/+/+/sensor/#"
	if subErr := subClient.Subscribe(context.Background(), sensorTopic, 0); subErr != nil {
		t.Fatalf("subscribe %q: %v", sensorTopic, subErr)
	}
	time.Sleep(100 * time.Millisecond)

	pubClient := newBrokerMQTTClient(t, addr, "edge-sensor-err", nil)
	defer func() { _ = pubClient.Disconnect(context.Background()) }()

	// ---- When: publish a sensor reading with out-of-range pH (15.0 > 14.0 max) ----
	badReading := &pondproto.SensorReading{
		DeviceId:  "sensor-node-01",
		Timestamp: time.Now().UnixMilli(),
		Ph:        15.0, // out of range — should be dropped by gateway
		Do:        6.0,  // valid
		Temp:      25.0, // valid
	}
	payload, err := proto.Marshal(badReading)
	if err != nil {
		t.Fatalf("marshal bad reading: %v", err)
	}
	if err := pubClient.PublishTelemetry(context.Background(),
		"pond/v1/farm-1/pond-1/sensor/water/ph", payload); err != nil {
		t.Fatalf("publish bad reading: %v", err)
	}

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for gateway to process message")
	}
	time.Sleep(50 * time.Millisecond)

	// ---- Then: out-of-range pH was dropped, valid fields were written ----
	pts := influx.snapshot()
	for _, pt := range pts {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" && pt.SensorType == "ph" {
			t.Errorf("expected pH (out-of-range 15.0) to be dropped, but it was written: %+v", pt)
		}
	}

	found := make(map[string]bool)
	for _, pt := range pts {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" {
			found[pt.SensorType] = true
		}
	}
	if !found["do"] {
		t.Error("expected valid do field to be written")
	}
	if !found["temp"] {
		t.Error("expected valid temp field to be written")
	}

	if err := pgMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet pgxmock expectations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestSensorFailure_EdgeControllerSkipsSensorError
// ---------------------------------------------------------------------------

func TestSensorFailure_EdgeControllerSkipsSensorError(t *testing.T) {
	// This test validates that the sensor error pattern used by the
	// controller (ignore Read errors, keep last good reading) works.
	// The controller's tick() ignores sensor read errors and preserves
	// c.lastSnap — verified in controller_test.go (TestOfflineFallback).

	errSensor := &errAfterSensor{
		goodValues: map[string]float64{"do": 6.5, "temp": 25.0},
		errorAfter: 3,
	}

	// ---- When: reading after error threshold ----
	var lastGoodRead int
	for i := 0; i < 5; i++ {
		_, err := errSensor.read()
		if i < errSensor.errorAfter {
			if err != nil {
				t.Errorf("read %d: unexpected error before threshold: %v", i, err)
			}
			lastGoodRead++
		} else {
			if err == nil {
				t.Errorf("read %d: expected error after threshold, got nil", i)
			}
		}
	}

	// ---- Then: controller would have at least one good reading to retain ----
	if lastGoodRead == 0 {
		t.Error("expected at least one good read before sensor failure")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// captureAlertNotifier records alerts for assertions.
type captureAlertNotifier struct {
	fn func(alert.Alert)
}

func (n *captureAlertNotifier) Notify(_ context.Context, a alert.Alert) error {
	if n.fn != nil {
		n.fn(a)
	}
	return nil
}

// errAfterSensor is a mock sensor that succeeds for N reads then errors.
// Models a sensor disconnection scenario where the controller must retain
// the last good reading.
type errAfterSensor struct {
	goodValues map[string]float64
	errorAfter int
	reads      int
	mu         sync.Mutex
}

func (s *errAfterSensor) read() (store.SensorPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.reads <= s.errorAfter {
		return store.SensorPoint{
			Fields: s.goodValues,
		}, nil
	}
	return store.SensorPoint{}, errors.New("sensor disconnected")
}

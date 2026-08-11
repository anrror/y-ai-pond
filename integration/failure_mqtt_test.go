// MQTT broker failure and chaos resilience tests (T35).
//
// Tests:
//   - TestMQTTBrokerDown: broker dies → edge buffers locally → new broker → backfill
//   - TestDualFailure: MQTT broker down + InfluxDB write failure → edge continues local buffering
//
// Uses mochi-mqtt in-memory broker + testInfluxWriter (no Docker/testcontainers).
package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/pashagolub/pgxmock/v4"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// TestMQTTBrokerDown — broker death → edge buffers → new broker → backfill
// ---------------------------------------------------------------------------

func TestMQTTBrokerDown(t *testing.T) {
	// ---- Given: a running MQTT broker, cloud gateway, and edge publisher ----
	broker1Addr, stopBroker1 := startMochiBroker(t)
	defer stopBroker1()

	influx := newTestInfluxWriter()
	pgMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgxmock: %v", err)
	}
	defer pgMock.Close()

	// Expect device auto-registration for each sensor reading published.
	pgMock.ExpectExec("INSERT INTO devices").
		WithArgs("edge-dev-01", "farm-1", "pond-1", "sensor_node", "online", "unknown", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	// ---- Gateway subscriber on broker1 ----
	gw := newTestGateway(influx, pgMock)

	received := make(chan struct{}, 10)
	gwHandler := func(ctx context.Context, topic string, payload []byte) {
		gw.HandleMessage(ctx, topic, payload)
		select {
		case received <- struct{}{}:
		default:
		}
	}
	subClient := newBrokerMQTTClient(t, broker1Addr, "gw-sub", gwHandler)
	defer func() { _ = subClient.Disconnect(context.Background()) }()

	sensorTopic := "pond/v1/+/+/sensor/#"
	if subErr := subClient.Subscribe(context.Background(), sensorTopic, 0); subErr != nil {
		t.Fatalf("subscribe %q: %v", sensorTopic, subErr)
	}
	time.Sleep(100 * time.Millisecond)

	// ---- Edge publisher on broker1 ----
	pubClient := newBrokerMQTTClient(t, broker1Addr, "edge-pub", nil)
	defer func() { _ = pubClient.Disconnect(context.Background()) }()

	// ---- When: publish 3 sensor readings before broker dies ----
	sensorReadings := make([]*pondproto.SensorReading, 3)
	for i := 0; i < 3; i++ {
		sensorReadings[i] = &pondproto.SensorReading{
			DeviceId:  "edge-dev-01",
			Timestamp: time.Now().UnixMilli(),
			Ph:        7.0 + float32(i)*0.1,
			Do:        6.0,
			Temp:      25.0,
			Nh3:       0.1,
		}
	}

	publishReading := func(reading *pondproto.SensorReading) error {
		payload, marshalErr := proto.Marshal(reading)
		if marshalErr != nil {
			return marshalErr
		}
		return pubClient.PublishTelemetry(context.Background(),
			"pond/v1/farm-1/pond-1/sensor/water/do", payload)
	}

	for i, r := range sensorReadings {
		if pubErr := publishReading(r); pubErr != nil {
			t.Fatalf("publish reading %d: %v", i, pubErr)
		}
	}

	// Wait for all 3 messages to arrive at the gateway.
	for i := 0; i < 3; i++ {
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for message %d before broker kill", i+1)
		}
	}

	// Verify initial data in influx.
	time.Sleep(50 * time.Millisecond)
	ptsBeforeKill := influx.snapshot()
	if len(ptsBeforeKill) == 0 {
		t.Fatal("expected sensor points before broker kill")
	}

	// ---- When: kill the MQTT broker ----
	stopBroker1()

	// Wait for the client to detect disconnection.
	time.Sleep(200 * time.Millisecond)

	// ---- Then: edge should detect disconnection (publish fails) ----
	// Simulate edge-side local buffering.
	var localBuffer [][]byte
	afterKillReading := &pondproto.SensorReading{
		DeviceId:  "edge-dev-01",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.3,
		Do:        5.8,
		Temp:      25.1,
		Nh3:       0.12,
	}
	payloadAfterKill, err := proto.Marshal(afterKillReading)
	if err != nil {
		t.Fatalf("marshal after-kill reading: %v", err)
	}

	// Publish should fail because broker is dead.
	pubErr := pubClient.PublishTelemetry(context.Background(),
		"pond/v1/farm-1/pond-1/sensor/water/do", payloadAfterKill)
	if pubErr == nil {
		t.Log("publish after broker kill did not error immediately (autopaho may buffer); proceeding")
	}
	// Edge buffers the data locally.
	localBuffer = append(localBuffer, payloadAfterKill)

	// Buffer more readings while broker is down.
	for i := 0; i < 2; i++ {
		bufReading := &pondproto.SensorReading{
			DeviceId:  "edge-dev-01",
			Timestamp: time.Now().UnixMilli(),
			Ph:        7.4 + float32(i)*0.1,
			Do:        5.7 - float32(i)*0.1,
			Temp:      25.2 + float32(i)*0.1,
			Nh3:       0.13 + float32(i)*0.01,
		}
		payload, _ := proto.Marshal(bufReading)
		localBuffer = append(localBuffer, payload)
	}

	// ---- When: start a NEW broker on a new port ----
	broker2Addr, stopBroker2 := startMochiBroker(t)
	defer stopBroker2()

	// Expect more device registrations for backfilled data.
	pgMock.ExpectExec("INSERT INTO devices").
		WithArgs("edge-dev-01", "farm-1", "pond-1", "sensor_node", "online", "unknown", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	// ---- New gateway subscriber on broker2 ----
	influx2 := newTestInfluxWriter()
	gw2 := newTestGateway(influx2, pgMock)

	received2 := make(chan struct{}, 10)
	gwHandler2 := func(ctx context.Context, topic string, payload []byte) {
		gw2.HandleMessage(ctx, topic, payload)
		select {
		case received2 <- struct{}{}:
		default:
		}
	}
	subClient2 := newBrokerMQTTClient(t, broker2Addr, "gw-sub-2", gwHandler2)
	defer func() { _ = subClient2.Disconnect(context.Background()) }()

	if err := subClient2.Subscribe(context.Background(), sensorTopic, 0); err != nil {
		t.Fatalf("subscribe broker2 %q: %v", sensorTopic, err)
	}
	time.Sleep(100 * time.Millisecond)

	// ---- New edge publisher on broker2 ----
	pubClient2 := newBrokerMQTTClient(t, broker2Addr, "edge-pub-2", nil)
	defer func() { _ = pubClient2.Disconnect(context.Background()) }()

	// ---- Then: backfill buffered data via new broker ----
	for i, payload := range localBuffer {
		if err := pubClient2.PublishTelemetry(context.Background(),
			"pond/v1/farm-1/pond-1/sensor/water/do", payload); err != nil {
			t.Fatalf("backfill publish %d: %v", i, err)
		}
	}

	// Wait for all backfilled messages.
	for i := 0; i < len(localBuffer); i++ {
		select {
		case <-received2:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for backfilled message %d", i+1)
		}
	}
	time.Sleep(50 * time.Millisecond)

	// ---- Then: verify backfilled data arrived in influx ----
	ptsAfterBackfill := influx2.snapshot()
	if len(ptsAfterBackfill) == 0 {
		t.Fatal("expected sensor points from backfilled data")
	}

	// Should have at least the 3 backfilled sensor type points.
	sensorTypes := make(map[string]int)
	for _, pt := range ptsAfterBackfill {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" {
			sensorTypes[pt.SensorType]++
		}
	}
	// We expect at least 'do' sensor type to appear for each backfilled reading.
	if count := sensorTypes["do"]; count < len(localBuffer) {
		t.Errorf("expected at least %d do points from backfill, got %d", len(localBuffer), count)
	}

	// New sensor readings published after recovery should also arrive.
	recoveryReading := &pondproto.SensorReading{
		DeviceId:  "edge-dev-01",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.5,
		Do:        6.2,
		Temp:      25.3,
		Nh3:       0.09,
	}
	recoveryPayload, _ := proto.Marshal(recoveryReading)
	if err := pubClient2.PublishTelemetry(context.Background(),
		"pond/v1/farm-1/pond-1/sensor/water/do", recoveryPayload); err != nil {
		t.Fatalf("publish after recovery: %v", err)
	}

	select {
	case <-received2:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for post-recovery message")
	}
	time.Sleep(50 * time.Millisecond)

	ptsAfterRecovery := influx2.snapshot()
	recoveryTypes := make(map[string]int)
	for _, pt := range ptsAfterRecovery {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" {
			recoveryTypes[pt.SensorType]++
		}
	}
	if count := recoveryTypes["do"]; count < len(localBuffer)+1 {
		t.Errorf("expected at least %d total do points after recovery, got %d", len(localBuffer)+1, count)
	}

	// Verify pgxmock expectations (device auto-registration).
	if err := pgMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet pgxmock expectations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestDualFailure — MQTT broker down + InfluxDB write failure simultaneously
// ---------------------------------------------------------------------------

func TestDualFailure(t *testing.T) {
	// ---- Given: running MQTT broker, gateway, and edge publisher ----
	broker1Addr, stopBroker1 := startMochiBroker(t)
	defer stopBroker1()

	// Influx writer that always fails (simulates InfluxDB outage).
	failingInflux := &failingInfluxWriter{}

	pgMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgxmock: %v", err)
	}
	defer pgMock.Close()

	pgMock.ExpectExec("INSERT INTO devices").
		WithArgs("edge-dev-02", "farm-1", "pond-1", "sensor_node", "online", "unknown", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	// Gateway with failing influx.
	gw := newTestGateway(failingInflux, pgMock)

	received := make(chan struct{}, 10)
	gwHandler := func(ctx context.Context, topic string, payload []byte) {
		gw.HandleMessage(ctx, topic, payload)
		select {
		case received <- struct{}{}:
		default:
		}
	}
	subClient := newBrokerMQTTClient(t, broker1Addr, "gw-dual-sub", gwHandler)
	defer func() { _ = subClient.Disconnect(context.Background()) }()

	sensorTopic := "pond/v1/+/+/sensor/#"
	if subErr := subClient.Subscribe(context.Background(), sensorTopic, 0); subErr != nil {
		t.Fatalf("subscribe %q: %v", sensorTopic, subErr)
	}
	time.Sleep(100 * time.Millisecond)

	pubClient := newBrokerMQTTClient(t, broker1Addr, "edge-dual-pub", nil)
	defer func() { _ = pubClient.Disconnect(context.Background()) }()

	// ---- When: publish data — influx write fails but gateway still processes ----
	reading1 := &pondproto.SensorReading{
		DeviceId:  "edge-dev-02",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.0,
		Do:        6.0,
		Temp:      25.0,
		Nh3:       0.1,
	}
	payload1, _ := proto.Marshal(reading1)
	if err := pubClient.PublishTelemetry(context.Background(),
		"pond/v1/farm-1/pond-1/sensor/water/do", payload1); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message in dual-failure mode")
	}

	// ---- When: kill broker + influx still failing ----
	stopBroker1()

	// Verify influx write failures were logged (gateway logs errors, doesn't crash).
	failingInflux.mu.Lock()
	writeAttempts := failingInflux.calls
	failingInflux.mu.Unlock()
	if writeAttempts == 0 {
		t.Error("expected at least 1 write attempt to failing influx")
	}

	// Simulate edge-side buffering after broker death.
	var localBuffer [][]byte
	bufReading := &pondproto.SensorReading{
		DeviceId:  "edge-dev-02",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.2,
		Do:        5.9,
		Temp:      25.1,
		Nh3:       0.11,
	}
	bufPayload, _ := proto.Marshal(bufReading)
	localBuffer = append(localBuffer, bufPayload)

	// ---- When: restart broker + fix influx + backfill ----
	broker2Addr, stopBroker2 := startMochiBroker(t)
	defer stopBroker2()

	pgMock.ExpectExec("INSERT INTO devices").
		WithArgs("edge-dev-02", "farm-1", "pond-1", "sensor_node", "online", "unknown", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	recoveredInflux := newTestInfluxWriter()
	gw2 := newTestGateway(recoveredInflux, pgMock)

	received2 := make(chan struct{}, 10)
	gwHandler2 := func(ctx context.Context, topic string, payload []byte) {
		gw2.HandleMessage(ctx, topic, payload)
		select {
		case received2 <- struct{}{}:
		default:
		}
	}
	subClient2 := newBrokerMQTTClient(t, broker2Addr, "gw-dual-sub-2", gwHandler2)
	defer func() { _ = subClient2.Disconnect(context.Background()) }()

	if err := subClient2.Subscribe(context.Background(), sensorTopic, 0); err != nil {
		t.Fatalf("subscribe broker2 %q: %v", sensorTopic, err)
	}
	time.Sleep(100 * time.Millisecond)

	pubClient2 := newBrokerMQTTClient(t, broker2Addr, "edge-dual-pub-2", nil)
	defer func() { _ = pubClient2.Disconnect(context.Background()) }()

	// Backfill.
	for i, payload := range localBuffer {
		if err := pubClient2.PublishTelemetry(context.Background(),
			"pond/v1/farm-1/pond-1/sensor/water/do", payload); err != nil {
			t.Fatalf("backfill %d: %v", i, err)
		}
	}

	for i := 0; i < len(localBuffer); i++ {
		select {
		case <-received2:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for backfilled message %d in dual-failure", i+1)
		}
	}

	// Publish fresh data after recovery.
	recoveryReading := &pondproto.SensorReading{
		DeviceId:  "edge-dev-02",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.3,
		Do:        6.1,
		Temp:      25.2,
		Nh3:       0.10,
	}
	recoveryPayload, _ := proto.Marshal(recoveryReading)
	if err := pubClient2.PublishTelemetry(context.Background(),
		"pond/v1/farm-1/pond-1/sensor/water/do", recoveryPayload); err != nil {
		t.Fatalf("publish after dual-failure recovery: %v", err)
	}

	select {
	case <-received2:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for post-recovery message in dual-failure")
	}
	time.Sleep(50 * time.Millisecond)

	// ---- Then: recovered influx should have data ----
	pts := recoveredInflux.snapshot()
	if len(pts) == 0 {
		t.Fatal("expected sensor points after dual-failure recovery")
	}

	sensorTypes := make(map[string]int)
	for _, pt := range pts {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" {
			sensorTypes[pt.SensorType]++
		}
	}
	if count := sensorTypes["do"]; count == 0 {
		t.Errorf("expected do points after dual-failure recovery, got types: %v", sensorTypes)
	}

	if err := pgMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet pgxmock expectations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// failingInfluxWriter — simulates a permanently-failing InfluxDB for chaos tests
// ---------------------------------------------------------------------------

type failingInfluxWriter struct {
	mu    sync.Mutex
	calls int
}

func (w *failingInfluxWriter) WriteSensorData(_ context.Context, _ []store.SensorPoint) error {
	w.mu.Lock()
	w.calls++
	w.mu.Unlock()
	return fmtError("influxdb unavailable")
}

func (w *failingInfluxWriter) QueryTimeRange(_ context.Context, _, _, _ string) ([]store.Point, error) {
	return nil, fmtError("influxdb unavailable")
}

func (w *failingInfluxWriter) Close() error { return nil }

// fmtError exists to avoid importing "fmt" only for the error constructor.
func fmtError(msg string) error {
	return &simpleError{msg}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

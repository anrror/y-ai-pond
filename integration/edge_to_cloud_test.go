// Edge-to-cloud telemetry integration test (T31).
//
// Flow: mock sensor → MQTT → InfluxDB → API → data visible.
//
// Uses mochi-mqtt in-memory broker instead of Docker/EMQX.
// Uses testInfluxWriter instead of real InfluxDB.
// Uses pgxmock instead of real PostgreSQL.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"github.com/pashagolub/pgxmock/v4"
	"google.golang.org/protobuf/proto"
)

// TestEdgeToCloudFlow verifies the full sensor telemetry pipeline:
//  1. Start in-memory MQTT broker (mochi)
//  2. Publish a SensorReading protobuf on pond/v1/farm-1/pond-1/sensor/water/do
//  3. Cloud Gateway ingests the message, validates, and writes to InfluxDB
//  4. Query GET /api/v1/sensors/latest?pond_id=pond-1 via the Handler API
//  5. Verify the published data is visible in the API response
func TestEdgeToCloudFlow(t *testing.T) {
	// ---- Step 1: Start MQTT broker ----
	addr, stopBroker := startMochiBroker(t)
	defer stopBroker()

	// ---- Step 2: Create storage stubs ----
	influx := newTestInfluxWriter()
	pgMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgxmock: %v", err)
	}
	defer pgMock.Close()

	// The gateway auto-registers devices on first contact.
	// Set up the pg expectation (device_id = esp32-s3-test-01 from the
	// protobuf payload below).
	pgMock.ExpectExec("INSERT INTO devices").
		WithArgs("esp32-s3-test-01", "farm-1", "pond-1", "sensor_node", "online", "unknown", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	// ---- Step 3: Create gateway ----
	gw := newTestGateway(influx, pgMock)

	// ---- Step 4: Create subscriber MQTT client (cloud gateway side) ----
	// Wrap the gateway handler so we can synchronize on message delivery.
	received := make(chan struct{}, 1)
	subHandler := func(ctx context.Context, topic string, payload []byte) {
		gw.HandleMessage(ctx, topic, payload)
		received <- struct{}{}
	}
	subClient := newBrokerMQTTClient(t, addr, "gw-sub", subHandler)
	defer func() {
		_ = subClient.Disconnect(context.Background())
	}()

	// Subscribe to sensor telemetry topics.
	sensorTopic := "pond/v1/+/+/sensor/#"
	if subErr := subClient.Subscribe(context.Background(), sensorTopic, 0); subErr != nil {
		t.Fatalf("subscribe %q: %v", sensorTopic, subErr)
	}
	// Small grace period for the SUBACK to land.
	time.Sleep(100 * time.Millisecond)

	// ---- Step 5: Create publisher MQTT client (edge sensor side) ----
	pubClient := newBrokerMQTTClient(t, addr, "sensor-pub", nil)
	defer func() {
		_ = pubClient.Disconnect(context.Background())
	}()

	// ---- Step 6: Publish sensor reading via MQTT ----
	sensorReading := &pondproto.SensorReading{
		DeviceId:  "esp32-s3-test-01",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.2,
		Do:        6.5,
		Temp:      25.3,
		Nh3:       0.1,
		Turbidity: 15.0,
	}
	payload, err := proto.Marshal(sensorReading)
	if err != nil {
		t.Fatalf("marshal sensor reading: %v", err)
	}

	topic := "pond/v1/farm-1/pond-1/sensor/water/do"
	if err := pubClient.PublishTelemetry(context.Background(), topic, payload); err != nil {
		t.Fatalf("publish sensor reading: %v", err)
	}

	// ---- Step 7: Wait for gateway to ingest the message ----
	select {
	case <-received:
		// Gateway handler processed the message.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for gateway to ingest sensor message")
	}

	// Small additional wait for device registration goroutine.
	time.Sleep(50 * time.Millisecond)

	// ---- Step 8: Verify data landed in InfluxDB ----
	pts := influx.snapshot()
	if len(pts) == 0 {
		t.Fatal("expected sensor points in influx, got none")
	}

	// At least one point should contain the DO reading.
	foundDO := false
	for _, pt := range pts {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" && pt.SensorType == "do" {
			if v, ok := pt.Fields["do"]; ok && v > 0 {
				foundDO = true
				break
			}
		}
	}
	if !foundDO {
		t.Fatalf("expected DO point for farm-1/pond-1, got points: %+v", pts)
	}

	// Verify all 6 sensor fields were written (ph=7.2, do=6.5, temp=25.3,
	// nh3=0.1, turbidity=15.0). Use approximate comparison because protobuf
	// stores float32 values; float32→float64 conversion introduces sub-ppm
	// rounding differences.
	sensorTypes := make(map[string]float64)
	for _, pt := range pts {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" {
			for k, v := range pt.Fields {
				sensorTypes[k] = v
			}
		}
	}
	assertFloatApprox(t, "ph", sensorTypes["ph"], 7.2)
	assertFloatApprox(t, "do", sensorTypes["do"], 6.5)
	assertFloatApprox(t, "temp", sensorTypes["temp"], 25.3)
	assertFloatApprox(t, "nh3", sensorTypes["nh3"], 0.1)
	assertFloatApprox(t, "turbidity", sensorTypes["turbidity"], 15.0)

	// ---- Step 9: Query handler API for latest sensors ----
	h, svc := setupTestHandler(t, pgMock, influx)
	router := newTestRouter(t, h)
	token := authToken(t, svc, []string{"farm-1"})

	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/sensors/latest?pond_id=pond-1", token, "")

	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/sensors/latest status = %d, want 200: %s",
			resp.Code, resp.Body.String())
	}

	// ---- Step 10: Verify API response ----
	var readings []struct {
		FarmID     string  `json:"farm_id"`
		PondID     string  `json:"pond_id"`
		SensorType string  `json:"sensor_type"`
		Value      float64 `json:"value"`
		Timestamp  string  `json:"timestamp"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &readings); err != nil {
		t.Fatalf("decode sensor readings: %v\nbody: %s", err, resp.Body.String())
	}

	// We should have at least the 5 published sensor types.
	if len(readings) < 5 {
		t.Fatalf("expected >= 5 sensor readings, got %d: %+v", len(readings), readings)
	}

	// Verify specific values are present in the API response.
	values := map[string]float64{}
	for _, r := range readings {
		if r.PondID == "pond-1" {
			values[r.SensorType] = r.Value
		}
	}
	assertFloatApprox(t, "API ph", values["ph"], 7.2)
	assertFloatApprox(t, "API do", values["do"], 6.5)
	assertFloatApprox(t, "API temp", values["temp"], 25.3)

	// Verify pgxmock expectations (device auto-registration).
	if err := pgMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet pgxmock expectations: %v", err)
	}
}

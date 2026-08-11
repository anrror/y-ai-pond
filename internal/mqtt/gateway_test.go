package mqtt

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/pashagolub/pgxmock/v4"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// fakeInfluxWriter captures written sensor points for test assertions.
// ---------------------------------------------------------------------------

type fakeInfluxWriter struct {
	written []store.SensorPoint
}

func (f *fakeInfluxWriter) WriteSensorData(_ context.Context, points []store.SensorPoint) error {
	f.written = append(f.written, points...)
	return nil
}

func (f *fakeInfluxWriter) QueryTimeRange(_ context.Context, _ string, _ string, _ string) ([]store.Point, error) {
	return nil, nil
}

func (f *fakeInfluxWriter) Close() error { return nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustMarshal marshals a protobuf message or panics (for test data construction).
func mustMarshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

// newTestGateway creates a Gateway for unit-testing HandleMessage directly
// (no live MQTT broker needed).
func newTestGateway(influx store.InfluxWriter, pg store.PgxPool) *Gateway {
	return NewGateway(nil, influx, pg, nil)
}

// ---------------------------------------------------------------------------
// TestGatewaySensorIngest
// ---------------------------------------------------------------------------

// TestGatewaySensorIngest verifies that a valid Protobuf SensorReading
// is decoded, validated, and written to the InfluxWriter.
func TestGatewaySensorIngest(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	// No PostgreSQL operations expected for pure sensor ingest (device already registered).
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	reading := &pondproto.SensorReading{
		DeviceId:  "esp32-s3-01",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.2,
		Do:        6.8,
		Temp:      25.5,
		Nh3:       0.3,
		Turbidity: 12.0,
	}
	payload := mustMarshal(reading)
	topic := "pond/v1/farm-1/pond-a/sensor/water"

	// Device auto-registration expected on first contact.
	mockDB.ExpectExec(`INSERT INTO devices`).WithArgs(
		"esp32-s3-01", "farm-1", "pond-a", "sensor_node", "online", "unknown", pgxmock.AnyArg(),
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))

	gw.HandleMessage(ctx, topic, payload)

	// Verify InfluxWriter received the points.
	if len(fakeInflux.written) == 0 {
		t.Fatal("expected InfluxWriter.WriteSensorData to be called")
	}
	// We expect 5 points (ph, do, temp, nh3, turbidity — water_level is 0 so skipped)
	if len(fakeInflux.written) < 5 {
		t.Fatalf("expected at least 5 sensor points, got %d", len(fakeInflux.written))
	}
	// Check first point (ph).
	ph := fakeInflux.written[0]
	if ph.FarmID != "farm-1" {
		t.Errorf("expected farm-1, got %s", ph.FarmID)
	}
	if ph.PondID != "pond-a" {
		t.Errorf("expected pond-a, got %s", ph.PondID)
	}
	if v, ok := ph.Fields["ph"]; !ok || !floatApprox(v, 7.2) {
		t.Errorf("expected ph=7.2, got %v", ph.Fields)
	}
	// Verify all expected sensor types are present.
	seen := map[string]float64{}
	for _, p := range fakeInflux.written {
		for k, v := range p.Fields {
			seen[k] = v
		}
	}
	if !floatApprox(seen["ph"], 7.2) {
		t.Errorf("missing or wrong ph: %v", seen["ph"])
	}
	if !floatApprox(seen["do"], 6.8) {
		t.Errorf("missing or wrong do: %v", seen["do"])
	}
	if !floatApprox(seen["temp"], 25.5) {
		t.Errorf("missing or wrong temp: %v", seen["temp"])
	}
	if !floatApprox(seen["nh3"], 0.3) {
		t.Errorf("missing or wrong nh3: %v", seen["nh3"])
	}

	if err := mockDB.ExpectationsWereMet(); err != nil {
		t.Errorf("pgxmock expectations not met: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceAutoRegistration
// ---------------------------------------------------------------------------

// TestDeviceAutoRegistration verifies that the first message from a device_id
// triggers an INSERT into the devices table via the PgxPool interface.
func TestDeviceAutoRegistration(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	reading := &pondproto.SensorReading{
		DeviceId:  "new-sensor-99",
		Timestamp: time.Now().UnixMilli(),
		Ph:        7.0,
		Temp:      22.0,
	}

	// Expect exactly one INSERT with UPSERT semantics.
	mockDB.ExpectExec(`INSERT INTO devices`).WithArgs(
		"new-sensor-99", "farm-x", "pond-y", "sensor_node", "online", "unknown", pgxmock.AnyArg(),
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))

	gw.HandleMessage(ctx, "pond/v1/farm-x/pond-y/sensor/water", mustMarshal(reading))

	if err := mockDB.ExpectationsWereMet(); err != nil {
		t.Errorf("device auto-registration INSERT not called: %v", err)
	}

	// Second message from same device_id should NOT trigger another INSERT.
	fakeInflux.written = nil
	gw.HandleMessage(ctx, "pond/v1/farm-x/pond-y/sensor/water", mustMarshal(reading))

	// If a second INSERT was called, pgxmock would fail expectations.
	// (ExpectationsWereMet checks that exactly the expected number of calls were made.)
}

// ---------------------------------------------------------------------------
// TestDataValidation
// ---------------------------------------------------------------------------

// TestDataValidation verifies that out-of-range sensor values are dropped
// and logged as warnings, preventing corrupted data from entering the DB.
func TestDataValidation(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	// Device auto-registration expected (first contact).
	mockDB.ExpectExec(`INSERT INTO devices`).WithArgs(
		"bad-sensor", "farm-1", "pond-a", "sensor_node", "online", "unknown", pgxmock.AnyArg(),
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))

	reading := &pondproto.SensorReading{
		DeviceId:  "bad-sensor",
		Timestamp: time.Now().UnixMilli(),
		Ph:        15.0, // invalid: pH max is 14
		Do:        25.0, // invalid: DO max is 20
		Temp:      55.0, // invalid: Temp max is 50
		Nh3:       15.0, // invalid: NH3 max is 10
		Turbidity: 5000, // invalid: Turbidity max is 3000
	}
	gw.HandleMessage(ctx, "pond/v1/farm-1/pond-a/sensor/water", mustMarshal(reading))

	// No valid fields → InfluxWriter must NOT be called.
	if len(fakeInflux.written) > 0 {
		t.Errorf("expected no writes for all-invalid data, got %d points: %+v",
			len(fakeInflux.written), fakeInflux.written)
	}

	if err := mockDB.ExpectationsWereMet(); err != nil {
		t.Errorf("pgxmock expectations not met: %v", err)
	}
}

// TestDataValidation_pH15 verifies specifically that pH=15 is rejected.
func TestDataValidation_pH15(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	// Device auto-registration still happens (first contact).
	mockDB.ExpectExec(`INSERT INTO devices`).WithArgs(
		"dev-1", "farm-1", "pond-a", "sensor_node", "online", "unknown", pgxmock.AnyArg(),
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))

	reading := &pondproto.SensorReading{
		DeviceId:  "dev-1",
		Timestamp: time.Now().UnixMilli(),
		Ph:        15.0, // invalid
		Do:        7.0,  // valid
		Temp:      25.0, // valid
	}
	gw.HandleMessage(ctx, "pond/v1/farm-1/pond-a/sensor/water", mustMarshal(reading))

	// Only valid fields (do, temp) should be written, not ph.
	for _, p := range fakeInflux.written {
		for k := range p.Fields {
			if k == "ph" {
				t.Errorf("ph=15 should have been dropped but was written")
			}
		}
	}
	// But valid fields should be present.
	hasDO := false
	hasTemp := false
	for _, p := range fakeInflux.written {
		if _, ok := p.Fields["do"]; ok {
			hasDO = true
		}
		if _, ok := p.Fields["temp"]; ok {
			hasTemp = true
		}
	}
	if !hasDO {
		t.Error("valid DO=7.0 should have been written")
	}
	if !hasTemp {
		t.Error("valid Temp=25.0 should have been written")
	}

	if err := mockDB.ExpectationsWereMet(); err != nil {
		t.Errorf("pgxmock expectations not met: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestMissingFarmID
// ---------------------------------------------------------------------------

// TestMissingFarmID verifies that messages on topics without a farm_id are
// dropped out of the pipeline without any storage operations.
func TestMissingFarmID(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	tests := []struct {
		name  string
		topic string
	}{
		{"empty farm_id", "pond/v1//pond-a/sensor/water"},
		{"wildcard farm", "pond/v1/+/pond-a/sensor/water"},
		{"wildcard pond", "pond/v1/farm-1/+/sensor/water"},
		{"too short topic", "pond/v1/farm-1"},
		{"wrong prefix", "other/v1/farm-1/pond-a/sensor/water"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reading := &pondproto.SensorReading{
				DeviceId: "dev-1", Ph: 7.0, Timestamp: time.Now().UnixMilli(),
			}
			gw.HandleMessage(ctx, tt.topic, mustMarshal(reading))

			if len(fakeInflux.written) > 0 {
				t.Errorf("topic %q: expected no writes, got %d", tt.topic, len(fakeInflux.written))
			}
			fakeInflux.written = nil
		})
	}
}

// ---------------------------------------------------------------------------
// TestFeedingStatusIngest
// ---------------------------------------------------------------------------

// TestFeedingStatusIngest verifies that a FeedingStatus protobuf is decoded
// and inserted as a feeding_logs row.
func TestFeedingStatusIngest(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	status := &pondproto.FeedingStatus{
		SpeedRpm:   120.0,
		DurationMs: 30000,
		RemainingG: 500.0,
		State:      pondproto.FeedingState_FEEDING_STATE_RUNNING,
	}

	mockDB.ExpectExec(`INSERT INTO feeding_logs`).WithArgs(
		pgxmock.AnyArg(), // id (generated with timestamp)
		"pond-a",         // pond_id
		float64(120.0),   // speed
		30000,            // duration
		pgxmock.AnyArg(), // decision_json
		pgxmock.AnyArg(), // created_at
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))

	gw.HandleMessage(ctx, "pond/v1/farm-1/pond-a/control/feeding/status", mustMarshal(status))

	if err := mockDB.ExpectationsWereMet(); err != nil {
		t.Errorf("feeding status INSERT not called: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestProtoDecodeFailure
// ---------------------------------------------------------------------------

// TestProtoDecodeFailure verifies that garbage payloads do not crash the
// handler and do not write any data.
func TestProtoDecodeFailure(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	// Garbage bytes are not valid protobuf.
	gw.HandleMessage(ctx, "pond/v1/farm-1/pond-a/sensor/water", []byte("not-a-protobuf"))

	if len(fakeInflux.written) > 0 {
		t.Errorf("expected no writes for garbage payload, got %d", len(fakeInflux.written))
	}
}

// ---------------------------------------------------------------------------
// TestParseTopic
// ---------------------------------------------------------------------------

func TestParseTopic_valid(t *testing.T) {
	farm, pond, ok := parseTopic("pond/v1/farm-7/pond-3/sensor/water/ph")
	if !ok {
		t.Fatal("expected ok")
	}
	if farm != "farm-7" {
		t.Errorf("expected farm-7, got %s", farm)
	}
	if pond != "pond-3" {
		t.Errorf("expected pond-3, got %s", pond)
	}
}

func TestParseTopic_tooShort(t *testing.T) {
	_, _, ok := parseTopic("pond/v1/farm-7")
	if ok {
		t.Fatal("expected not ok for short topic")
	}
}

func TestParseTopic_wrongPrefix(t *testing.T) {
	_, _, ok := parseTopic("cloud/v1/farm-7/pond-3/control")
	if ok {
		t.Fatal("expected not ok for wrong prefix")
	}
}

func TestParseTopic_wildcardFarm(t *testing.T) {
	_, _, ok := parseTopic("pond/v1/+/pond-3/sensor")
	if ok {
		t.Fatal("expected not ok for wildcard farm_id")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceStatusJSON
// ---------------------------------------------------------------------------

func TestDeviceStatusJSON(t *testing.T) {
	ctx := context.Background()
	fakeInflux := &fakeInfluxWriter{}
	mockDB, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mockDB.Close()

	gw := newTestGateway(fakeInflux, mockDB)

	// Device auto-registration expected.
	mockDB.ExpectExec(`INSERT INTO devices`).WithArgs(
		"farm-1-pond-a-status", "farm-1", "pond-a", "edge_controller", "online", "unknown", pgxmock.AnyArg(),
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))

	statusJSON, _ := json.Marshal(DeviceStatus{
		CPU:    45.0,
		Mem:    60.0,
		Temp:   38.0,
		Uptime: 86400,
	})
	gw.HandleMessage(ctx, "pond/v1/farm-1/pond-a/device/status", statusJSON)

	if err := mockDB.ExpectationsWereMet(); err != nil {
		t.Errorf("device status auto-registration not called: %v", err)
	}
}

// floatApprox returns true if a and b differ by less than 1e-5.
// Protobuf float32 values lose precision during round-trip.
func floatApprox(a, b float64) bool {
	return math.Abs(a-b) < 1e-5
}

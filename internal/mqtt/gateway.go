// Package mqtt implements the cloud-side MQTT Gateway that ingests telemetry
// from edge devices (sensor nodes, cameras, controllers), validates Protobuf
// payloads, and routes data to InfluxDB (time-series sensor data) and
// PostgreSQL (device registry, feeding logs).
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mqttclient "github.com/anrror/y-ai-pond/pkg/mqtt"
	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"github.com/anrror/y-ai-pond/pkg/store"
	"google.golang.org/protobuf/proto"
)

// Valid ranges for sensor data field validation.
const (
	PhMin        = 0
	PhMax        = 14
	DoMin        = 0
	DoMax        = 20
	TempMin      = 0
	TempMax      = 50
	Nh3Min       = 0
	Nh3Max       = 10
	TurbidityMin = 0
	TurbidityMax = 3000
)

// DeviceStatus is a decoded device/status JSON payload from an edge device.
type DeviceStatus struct {
	CPU         float64 `json:"cpu"`
	Mem         float64 `json:"mem"`
	Temp        float64 `json:"temp"`
	Uptime      int64   `json:"uptime"`
	FirmwareVer string  `json:"firmware_version"`
}

// DeviceAlarm is a decoded device/alarm JSON payload from an edge device.
type DeviceAlarm struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Gateway ingests MQTT messages from edge devices, decodes Protobuf payloads,
// validates sensor data, and persists to InfluxDB and PostgreSQL.
//
// HandleMessage is invoked by the mqtt.Client dispatch in a fresh goroutine
// per message (the autopaho read loop is never blocked).
type Gateway struct {
	mqttClient  *mqttclient.Client
	influx      store.InfluxWriter
	pg          store.PgxPool
	log         *slog.Logger
	seenDevices map[string]bool // tracks device-ids seen for auto-registration
	mu          sync.Mutex
}

// NewGateway creates a new cloud MQTT Gateway.
//
// The mqtt.Client's MessageHandler must be set to g.HandleMessage before
// the client is connected:
//
//	gw := NewGateway(nil, influx, pg, log)
//	client := mqtt.New(cfg, gw.HandleMessage, log)
//	gw.mqttClient = client
//
// For convenience, SetClient is provided.
func NewGateway(client *mqttclient.Client, influx store.InfluxWriter, pg store.PgxPool, log *slog.Logger) *Gateway {
	if log == nil {
		log = slog.Default()
	}
	return &Gateway{
		mqttClient:  client,
		influx:      influx,
		pg:          pg,
		log:         log,
		seenDevices: make(map[string]bool),
	}
}

// SetClient wires an already-constructed mqtt.Client into the gateway.
// Use when the handler must be set at client creation time.
func (g *Gateway) SetClient(c *mqttclient.Client) {
	g.mqttClient = c
}

// Start subscribes to the four MQTT topic patterns and waits for the first
// successful CONNACK. Returns after subscription or on context cancellation.
func (g *Gateway) Start(ctx context.Context) error {
	if g.mqttClient == nil {
		return fmt.Errorf("mqtt gateway: client not set")
	}

	subs := []struct {
		topic string
		qos   byte
	}{
		{"pond/v1/+/+/sensor/#", 0},  // high-frequency telemetry, fire-and-forget
		{"pond/v1/+/+/camera/#", 0},  // high-frequency inference, fire-and-forget
		{"pond/v1/+/+/control/#", 1}, // feeding status/decision, at-least-once
		{"pond/v1/+/+/device/#", 1},  // device status/alarm, at-least-once
	}
	for _, s := range subs {
		if err := g.mqttClient.Subscribe(ctx, s.topic, s.qos); err != nil {
			return fmt.Errorf("mqtt gateway: subscribe %q: %w", s.topic, err)
		}
	}
	g.log.Info("mqtt gateway: subscribed to all topic patterns",
		"topics", []string{
			"pond/v1/+/+/sensor/#",
			"pond/v1/+/+/camera/#",
			"pond/v1/+/+/control/#",
			"pond/v1/+/+/device/#",
		})
	return nil
}

// HandleMessage routes an inbound MQTT message by topic prefix.
// This is the MessageHandler callback registered with pkg/mqtt.Client.
// It runs in a goroutine per message (autopaho dispatch) and must never block
// the read loop — any long operations happen asynchronously within.
func (g *Gateway) HandleMessage(ctx context.Context, topic string, payload []byte) {
	farmID, pondID, ok := parseTopic(topic)
	if !ok {
		g.log.Warn("mqtt gateway: dropped message, missing farm_id",
			"topic", topic, "payload_len", len(payload))
		return
	}

	switch {
	case strings.Contains(topic, "/sensor/"):
		g.handleSensor(ctx, farmID, pondID, topic, payload)
	case strings.Contains(topic, "/camera/"):
		g.handleCamera(ctx, farmID, pondID, payload)
	case strings.Contains(topic, "/control/feeding/status"):
		g.handleFeedingStatus(ctx, farmID, pondID, payload)
	case strings.Contains(topic, "/control/feeding/decision"):
		g.handleControlDecision(ctx, farmID, pondID, payload)
	case strings.Contains(topic, "/device/status"):
		g.handleDeviceStatus(ctx, farmID, pondID, topic, payload)
	case strings.Contains(topic, "/device/alarm"):
		g.handleDeviceAlarm(ctx, farmID, pondID, payload)
	default:
		g.log.Debug("mqtt gateway: unhandled topic", "topic", topic)
	}
}

// ---------------------------------------------------------------------------
// Sensor handler
// ---------------------------------------------------------------------------

// handleSensor decodes a SensorReading protobuf, validates each scalar field
// against its acceptable range, writes valid readings to InfluxDB, and
// auto-registers the device on first contact.
func (g *Gateway) handleSensor(ctx context.Context, farmID, pondID, topic string, payload []byte) {
	var reading pondproto.SensorReading
	if err := proto.Unmarshal(payload, &reading); err != nil {
		g.log.Warn("mqtt gateway: sensor protobuf decode failed",
			"farm_id", farmID, "pond_id", pondID, "topic", topic, "error", err)
		return
	}

	deviceID := reading.GetDeviceId()
	if deviceID == "" {
		g.log.Warn("mqtt gateway: sensor reading without device_id",
			"farm_id", farmID, "pond_id", pondID)
		return
	}

	// Auto-register device on first contact.
	g.maybeRegisterDevice(ctx, farmID, pondID, deviceID, "sensor_node", "online")

	ts := time.UnixMilli(reading.GetTimestamp())
	if reading.GetTimestamp() == 0 {
		ts = time.Now()
	}

	// Validate each field and build InfluxDB points only for valid readings.
	points := g.buildSensorPoints(farmID, pondID, &reading, ts)
	if len(points) == 0 {
		// All fields invalid — already logged individually in buildSensorPoints.
		return
	}

	if err := g.influx.WriteSensorData(ctx, points); err != nil {
		g.log.Error("mqtt gateway: influxdb write failed",
			"farm_id", farmID, "pond_id", pondID, "device_id", deviceID, "error", err)
	}
}

// buildSensorPoints validates each field of a SensorReading and returns
// InfluxDB SensorPoints for valid fields. Invalid fields are logged as
// warnings and excluded.
func (g *Gateway) buildSensorPoints(farmID, pondID string, r *pondproto.SensorReading, ts time.Time) []store.SensorPoint {
	// Using non-zero values as "present" — zero means "not reported" for most
	// sensors (pH=0 is technically valid but physically improbable in aquaculture).
	fields := []struct {
		sensorType string
		value      float32
		min, max   float32
	}{
		{"ph", r.GetPh(), PhMin, PhMax},
		{"do", r.GetDo(), DoMin, DoMax},
		{"temp", r.GetTemp(), TempMin, TempMax},
		{"nh3", r.GetNh3(), Nh3Min, Nh3Max},
		{"turbidity", r.GetTurbidity(), TurbidityMin, TurbidityMax},
		{"water_level", r.GetWaterLevel(), 0, 10000}, // cm, broad upper bound
	}

	var points []store.SensorPoint
	for _, f := range fields {
		if f.value == 0 {
			continue // not reported
		}
		if f.value < f.min || f.value > f.max {
			g.log.Warn("mqtt gateway: sensor value out of range, dropped",
				"farm_id", farmID, "pond_id", pondID,
				"sensor", f.sensorType, "value", f.value,
				"range", fmt.Sprintf("[%.0f,%.0f]", f.min, f.max))
			continue
		}
		points = append(points, store.SensorPoint{
			FarmID:     farmID,
			PondID:     pondID,
			SensorType: f.sensorType,
			Timestamp:  ts,
			Fields:     map[string]float64{f.sensorType: float64(f.value)},
		})
	}
	return points
}

// ---------------------------------------------------------------------------
// Camera handler
// ---------------------------------------------------------------------------

// handleCamera decodes an InferenceResult protobuf from edge YOLOv8n pipeline
// and writes it to InfluxDB.
func (g *Gateway) handleCamera(ctx context.Context, farmID, pondID string, payload []byte) {
	var result pondproto.InferenceResult
	if err := proto.Unmarshal(payload, &result); err != nil {
		g.log.Warn("mqtt gateway: camera protobuf decode failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
		return
	}

	// Publish inference as a single sensor point with compound fields.
	point := store.SensorPoint{
		FarmID:     farmID,
		PondID:     pondID,
		SensorType: "camera_inference",
		Timestamp:  time.Now(),
		Fields: map[string]float64{
			"fish_count":     float64(result.GetFishCount()),
			"texture_energy": float64(result.GetTextureEnergy()),
			"behavior":       float64(result.GetBehavior()),
		},
	}
	if err := g.influx.WriteSensorData(ctx, []store.SensorPoint{point}); err != nil {
		g.log.Error("mqtt gateway: influxdb camera write failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Feeding status handler
// ---------------------------------------------------------------------------

// handleFeedingStatus decodes a FeedingStatus protobuf and persists it as a
// feeding log in PostgreSQL.
func (g *Gateway) handleFeedingStatus(ctx context.Context, farmID, pondID string, payload []byte) {
	var status pondproto.FeedingStatus
	if err := proto.Unmarshal(payload, &status); err != nil {
		g.log.Warn("mqtt gateway: feeding status protobuf decode failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
		return
	}

	// Persist as a feeding log in PostgreSQL.
	stateJSON, _ := json.Marshal(map[string]any{
		"state":        status.GetState().String(),
		"speed_rpm":    status.GetSpeedRpm(),
		"remaining_g":  status.GetRemainingG(),
	})
	id := fmt.Sprintf("%s-%s-%d", farmID, pondID, time.Now().UnixMilli())
	_, err := g.pg.Exec(ctx,
		`INSERT INTO feeding_logs (id, pond_id, speed, duration, decision_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO NOTHING`,
		id, pondID, float64(status.GetSpeedRpm()), int(status.GetDurationMs()), stateJSON, time.Now(),
	)
	if err != nil {
		g.log.Error("mqtt gateway: insert feeding log failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Control decision handler
// ---------------------------------------------------------------------------

// handleControlDecision decodes a ControlDecision protobuf and persists it as
// a feeding log in PostgreSQL (audit trail for RL training data).
func (g *Gateway) handleControlDecision(ctx context.Context, farmID, pondID string, payload []byte) {
	var decision pondproto.ControlDecision
	if err := proto.Unmarshal(payload, &decision); err != nil {
		g.log.Warn("mqtt gateway: control decision protobuf decode failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
		return
	}

	decisionJSON, _ := json.Marshal(map[string]any{
		"fuzzy_inputs":       decision.GetFuzzyInputs(),
		"rules_fired":        decision.GetRulesFired(),
		"output_speed":       decision.GetOutputSpeed(),
		"output_duration_ms": decision.GetOutputDurationMs(),
	})
	id := fmt.Sprintf("decision-%s-%s-%d", farmID, pondID, time.Now().UnixMilli())
	_, err := g.pg.Exec(ctx,
		`INSERT INTO feeding_logs (id, pond_id, speed, duration, decision_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO NOTHING`,
		id, pondID, float64(decision.GetOutputSpeed()), int(decision.GetOutputDurationMs()),
		decisionJSON, time.Now(),
	)
	if err != nil {
		g.log.Error("mqtt gateway: insert control decision failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Device status / alarm handlers
// ---------------------------------------------------------------------------

// handleDeviceStatus processes a JSON device heartbeat message.
func (g *Gateway) handleDeviceStatus(ctx context.Context, farmID, pondID, topic string, payload []byte) {
	deviceID := extractDeviceID(topic)
	var status DeviceStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		g.log.Warn("mqtt gateway: device status json decode failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
		return
	}

	// Auto-register device if first contact.
	g.maybeRegisterDevice(ctx, farmID, pondID, deviceID, "edge_controller", "online")
}

// handleDeviceAlarm processes a JSON device alarm message.
func (g *Gateway) handleDeviceAlarm(ctx context.Context, farmID, pondID string, payload []byte) {
	var alarm DeviceAlarm
	if err := json.Unmarshal(payload, &alarm); err != nil {
		g.log.Warn("mqtt gateway: device alarm json decode failed",
			"farm_id", farmID, "pond_id", pondID, "error", err)
		return
	}

	// Auto-register device if first contact (alarm may come before status).
	// device_id is not in the alarm payload, so we derive from farm+pond.
	g.log.Info("mqtt gateway: device alarm received",
		"farm_id", farmID, "pond_id", pondID,
		"level", alarm.Level, "code", alarm.Code, "message", alarm.Message)
}

// ---------------------------------------------------------------------------
// Device auto-registration
// ---------------------------------------------------------------------------

// maybeRegisterDevice registers a device on first contact (idempotent via
// ON CONFLICT). A device is tracked per (farm_id, pond_id, device_id) triplet.
func (g *Gateway) maybeRegisterDevice(ctx context.Context, farmID, pondID, deviceID, deviceType, status string) {
	if deviceID == "" {
		return
	}

	g.mu.Lock()
	if g.seenDevices[deviceID] {
		g.mu.Unlock()
		return
	}
	g.seenDevices[deviceID] = true
	g.mu.Unlock()

	now := time.Now()
	_, err := g.pg.Exec(ctx,
		`INSERT INTO devices (id, farm_id, pond_id, type, status, firmware_version, last_heartbeat)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET
		   last_heartbeat = EXCLUDED.last_heartbeat,
		   status = EXCLUDED.status`,
		deviceID, farmID, pondID, deviceType, status, "unknown", now,
	)
	if err != nil {
		g.log.Error("mqtt gateway: device registration failed",
			"device_id", deviceID, "farm_id", farmID, "pond_id", pondID, "error", err)
		return
	}
	g.log.Info("mqtt gateway: device auto-registered",
		"device_id", deviceID, "farm_id", farmID, "pond_id", pondID, "type", deviceType)
}

// ---------------------------------------------------------------------------
// Topic parsing
// ---------------------------------------------------------------------------

// parseTopic extracts farm_id and pond_id from an MQTT topic.
//
// Expected format: pond/v1/{farm_id}/{pond_id}/...
//
// Returns false if farm_id or pond_id is missing or is a wildcard (+).
func parseTopic(topic string) (farmID, pondID string, ok bool) {
	parts := strings.Split(topic, "/")
	if len(parts) < 4 {
		return "", "", false
	}
	if parts[0] != "pond" || parts[1] != "v1" {
		return "", "", false
	}
	farmID = parts[2]
	pondID = parts[3]
	if farmID == "" || farmID == "+" || pondID == "" || pondID == "+" {
		return "", "", false
	}
	return farmID, pondID, true
}

// extractDeviceID derives a pseudo device_id from a topic when the JSON
// payload does not contain one. Uses the last topic segment as a hint.
func extractDeviceID(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) >= 4 {
		// Use farm/pond as device scope; actual ID from last segment if available
		if len(parts) >= 5 {
			return fmt.Sprintf("%s-%s-%s", parts[2], parts[3], parts[len(parts)-1])
		}
		return fmt.Sprintf("%s-%s-device", parts[2], parts[3])
	}
	return "unknown-device"
}

// Feeding decision flow integration test (T31).
//
// Flow: mock YOLO output → edge controller → MQTT decision log → cloud saves.
//
// Uses in-memory substitutes: no Docker/EMQX, no real NPU/GPU.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/edge/controller"
	"github.com/anrror/y-ai-pond/pkg/edge/detector"
	"github.com/anrror/y-ai-pond/pkg/edge/fuzzypid"
	"github.com/anrror/y-ai-pond/pkg/edge/hal"
	"github.com/anrror/y-ai-pond/pkg/edge/safety"
	"github.com/anrror/y-ai-pond/pkg/edge/texture"
	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/pashagolub/pgxmock/v4"
	"google.golang.org/protobuf/proto"
)

// ---- Mock types for the edge controller ----

// mockYOLODetector returns a pre-configured Detection (simulates YOLOv8n output).
type mockYOLODetector struct {
	det detector.Detection
}

func (d *mockYOLODetector) Detect(_ []byte) (detector.Detection, error) {
	return d.det, nil
}

// mockCameraSource returns pre-loaded frames on each call.
type mockCameraSource struct {
	frames []controller.CameraFrame
	pos    int
}

func (m *mockCameraSource) NextFrame(_ context.Context) (controller.CameraFrame, error) {
	if len(m.frames) == 0 {
		return controller.CameraFrame{}, nil
	}
	f := m.frames[m.pos%len(m.frames)]
	m.pos++
	return f, nil
}

// mockSensorReader returns a pre-configured SensorSnapshot.
type mockSensorReader struct {
	snap controller.SensorSnapshot
}

func (s *mockSensorReader) Read(_ context.Context) (controller.SensorSnapshot, error) {
	return s.snap, nil
}

// mockActuatorDriver records the last command for verification.
type mockActuatorDriver struct {
	mu      sync.Mutex
	speed   float64
	forward bool
	stopped bool
}

func (a *mockActuatorDriver) SetSpeed(pct float64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.speed = pct
	a.stopped = false
	return nil
}

func (a *mockActuatorDriver) SetDirection(forward bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forward = forward
	return nil
}

func (a *mockActuatorDriver) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped = true
	a.speed = 0
	return nil
}

func (a *mockActuatorDriver) Status() hal.Health {
	return hal.StatusOK
}

// mockReporter captures StatusReports for verification.
type mockReporter struct {
	mu      sync.Mutex
	reports []controller.StatusReport
}

func (r *mockReporter) Report(_ context.Context, sr controller.StatusReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, sr)
	return nil
}

func (r *mockReporter) snapshot() []controller.StatusReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]controller.StatusReport, len(r.reports))
	copy(out, r.reports)
	return out
}

// grayFrame creates a 64×64 grayscale frame filled with the given value.
func grayFrame(v uint8) texture.Frame {
	gray := make([]uint8, 64*64)
	for i := range gray {
		gray[i] = v
	}
	return texture.Frame{Gray: gray, Width: 64, Height: 64}
}

// TestFeedingDecisionFlow verifies the feeding decision pipeline:
//  1. Set up edge controller with mock YOLO output, sensors, and reporter.
//  2. Run the control loop for several ticks.
//  3. Capture the reporter output (StatusReport).
//  4. Convert the report to a ControlDecision protobuf and simulate MQTT
//     delivery to the cloud gateway.
//  5. Verify the decision log is saved in PostgreSQL (pgxmock).
//  6. Query GET /api/v1/feeding/logs?pond_id=pond-1 via the Handler API.
func TestFeedingDecisionFlow(t *testing.T) {
	// ---- Step 1: Create mock edge controller deps ----
	// Simulate YOLOv8n detecting 50 fish with average size 200K px².
	frameA := controller.CameraFrame{Raw: []byte{0x01}, Gray: grayFrame(120)}
	frameB := controller.CameraFrame{Raw: []byte{0x02}, Gray: grayFrame(140)}
	cam := &mockCameraSource{frames: []controller.CameraFrame{frameA, frameB}}

	yolo := &mockYOLODetector{
		det: detector.Detection{
			Count:     50,
			AvgSizePx: 200000,
		},
	}

	sensors := &mockSensorReader{
		snap: controller.SensorSnapshot{DO: 6.5, Temp: 25.0, NH3: 0.05},
	}

	act := &mockActuatorDriver{}
	rep := &mockReporter{}

	cfg := controller.DefaultConfig()
	cfg.Period = 5 * time.Millisecond    // fast tick for test
	cfg.Heartbeat = 50 * time.Millisecond // report quickly

	ctrl, err := controller.New(cfg, controller.Deps{
		Camera:   cam,
		Detector: yolo,
		Analyzer: controller.DefaultTextureAnalyzer{},
		Sensors:  sensors,
		Fuzzy:    fuzzypid.NewDefault(),
		Safety:   safety.NewEvaluator(safety.DefaultConfig()),
		Actuator: act,
		Reporter: rep,
	}, nil)
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	// ---- Step 2: Run the controller briefly ----
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if runErr := ctrl.Run(ctx); runErr != nil {
		// Run returns nil on context cancellation (normal shutdown).
		t.Logf("controller run ended: %v", runErr)
	}

	// ---- Step 3: Verify reporter captured feeding decisions ----
	reports := rep.snapshot()
	if len(reports) == 0 {
		t.Fatal("expected at least one status report, got none")
	}

	// At least one report should have Decision=true (feeding decision).
	var decisionReport controller.StatusReport
	hasDecision := false
	for _, r := range reports {
		if r.Decision {
			decisionReport = r
			hasDecision = true
			break
		}
	}
	if !hasDecision {
		t.Fatalf("expected at least one decision report (Decision=true), got %d reports", len(reports))
	}

	// The decision report should contain meaningful data.
	if decisionReport.FishCount == 0 {
		t.Error("expected FishCount > 0 in decision report")
	}
	if decisionReport.PWM <= 0 {
		t.Errorf("expected PWM > 0 in decision report, got %.2f", decisionReport.PWM)
	}
	if decisionReport.Temp <= 0 {
		t.Error("expected Temp > 0 in decision report")
	}
	if decisionReport.DO <= 0 {
		t.Error("expected DO > 0 in decision report")
	}

	t.Logf("Controller decision: Mode=%s FishCount=%d PWM=%.2f DO=%.1f Temp=%.1f",
		decisionReport.Mode, decisionReport.FishCount, decisionReport.PWM,
		decisionReport.DO, decisionReport.Temp)

	// ---- Step 4: Simulate MQTT delivery — convert to ControlDecision protobuf ----
	decision := &pondproto.ControlDecision{
		FuzzyInputs: map[string]float32{
			"fish_density":     float32(decisionReport.Density),
			"feeding_intensity": float32(decisionReport.FeedingIntensity),
			"do_mg_l":          float32(decisionReport.DO),
			"temp_c":           float32(decisionReport.Temp),
			"nh3_mg_l":         float32(decisionReport.NH3),
		},
		RulesFired:       []string{"R23: VH+M→MAX"},
		OutputSpeed:      float32(decisionReport.PWM),
		OutputDurationMs: 5000,
	}

	decisionPayload, err := proto.Marshal(decision)
	if err != nil {
		t.Fatalf("marshal control decision: %v", err)
	}

	// ---- Step 5: Feed through the cloud gateway ----
	pgMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgxmock: %v", err)
	}
	defer pgMock.Close()

	// Expect the gateway to insert a feeding_log for the control decision.
	pgMock.ExpectExec("INSERT INTO feeding_logs").
		WithArgs(pgxmock.AnyArg(), "pond-1", float64(decision.OutputSpeed), int(decision.OutputDurationMs), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	// Also expect device auto-registration from maybeRegisterDevice
	// (device ID is derived from farm+pond by extractDeviceID).
	// Since the gateway's handleControlDecision does NOT call maybeRegisterDevice,
	// we verify no INSERT INTO devices expectation is needed.

	influx := newTestInfluxWriter()
	gw := newTestGateway(influx, pgMock)

	// Call the gateway handler directly to process the decision.
	gw.HandleMessage(context.Background(),
		"pond/v1/farm-1/pond-1/control/feeding/decision", decisionPayload)

	// ---- Step 6: Verify feeding log via API ----
	// Seed pgxmock for the feeding logs query.
	decisionJSON, _ := json.Marshal(map[string]any{
		"fuzzy_inputs":       decision.GetFuzzyInputs(),
		"rules_fired":        decision.GetRulesFired(),
		"output_speed":       decision.GetOutputSpeed(),
		"output_duration_ms": decision.GetOutputDurationMs(),
	})

	pgMock.ExpectQuery("FROM feeding_logs").
		WithArgs("pond-1", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "pond_id", "speed", "duration", "decision_json", "created_at"}).
			AddRow("decision-farm-1-pond-1-1", "pond-1", float64(decision.OutputSpeed), int(decision.OutputDurationMs), decisionJSON, time.Now()))

	h, svc := setupTestHandler(t, pgMock, influx)
	router := newTestRouter(t, h)
	token := authToken(t, svc, []string{"farm-1"})

	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/feeding/logs?pond_id=pond-1", token, "")

	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/feeding/logs status = %d, want 200: %s",
			resp.Code, resp.Body.String())
	}

	var result struct {
		Logs []store.FeedingLog `json:"logs"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode feeding logs: %v\nbody: %s", err, resp.Body.String())
	}

	if len(result.Logs) < 1 {
		t.Fatal("expected at least 1 feeding log in API response")
	}

	log := result.Logs[0]
	if log.PondID != "pond-1" {
		t.Errorf("feeding log pond_id = %q, want pond-1", log.PondID)
	}
	if log.Speed <= 0 {
		t.Errorf("feeding log speed = %.1f, want > 0", log.Speed)
	}
	if len(log.DecisionJSON) == 0 {
		t.Error("feeding log decision_json is empty")
	}

	// Verify decision JSON contains the expected fields.
	var decMap map[string]any
	if err := json.Unmarshal(log.DecisionJSON, &decMap); err != nil {
		t.Fatalf("decode decision_json: %v", err)
	}
	if _, ok := decMap["output_speed"]; !ok {
		t.Error("decision_json missing output_speed")
	}
	if _, ok := decMap["rules_fired"]; !ok {
		t.Error("decision_json missing rules_fired")
	}

	t.Logf("Feeding decision flow: log ID=%s pond=%s speed=%.1f duration=%d",
		log.ID, log.PondID, log.Speed, log.Duration)

	// Verify pgxmock insert expectation was met.
	if err := pgMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet pgxmock expectations: %v", err)
	}
}

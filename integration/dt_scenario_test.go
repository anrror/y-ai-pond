// Digital twin scenario simulation integration test (T31).
//
// Flow: simulation → API → results.
//
// Uses the visual package's Visualizer with deterministic PondSimulator
// (stdlib-only, no Docker/GPU/ML runtime needed). The HeatWave scenario
// (SSP585 +4°C, 7 days) is run through the DT handler API.
package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anrror/y-ai-pond/pkg/dt/visual"
)

// TestDTScenarioFlow verifies the digital twin scenario pipeline:
//  1. Create a Visualizer with the default deterministic pond simulator.
//  2. Wire the Visualizer into the Handler's DT engine.
//  3. Query GET /api/v1/dt/pond/pond-1/trajectory?scenario=heatwave.
//  4. Verify the trajectory JSON contains water-quality time-series data
//     showing the expected heat wave effects (↑ temperature, ↓ DO).
func TestDTScenarioFlow(t *testing.T) {
	// ---- Step 1: Create Visualizer with deterministic pond simulator ----
	viz := visual.NewVisualizer(nil) // nil → falls back to PondSimulator

	// ---- Step 2: Create Handler and wire DT engine ----
	// DT API queries don't need PostgreSQL or InfluxDB — they use the
	// in-memory Visualizer exclusively.
	h, svc := setupTestHandler(t, nil, nil)
	h.SetDTEngine(viz)

	router := newTestRouter(t, h)
	token := authToken(t, svc, []string{"farm-1"})

	// ---- Step 3: Query trajectory API ----
	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/dt/pond/pond-1/trajectory?scenario=heatwave", token, "")

	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/dt/pond/pond-1/trajectory status = %d, want 200: %s",
			resp.Code, resp.Body.String())
	}

	// ---- Step 4: Verify trajectory response ----
	var trajectory struct {
		PondID   string `json:"pond_id"`
		Scenario string `json:"scenario"`
		Points   []struct {
			Step         int     `json:"step"`
			TemperatureC float64 `json:"temperature_c"`
			DO           float64 `json:"do_mg_l"`
			Turbidity    float64 `json:"turbidity_ntu"`
			NH3          float64 `json:"nh3_mg_l"`
		} `json:"points"`
		Total int `json:"total"`
	}

	if err := json.Unmarshal(resp.Body.Bytes(), &trajectory); err != nil {
		t.Fatalf("decode trajectory: %v\nbody: %s", err, resp.Body.String())
	}

	// Verify metadata.
	if trajectory.PondID != "pond-1" {
		t.Errorf("trajectory pond_id = %q, want pond-1", trajectory.PondID)
	}
	if trajectory.Scenario != "heatwave" {
		t.Errorf("trajectory scenario = %q, want heatwave", trajectory.Scenario)
	}

	// HeatWave scenario: 168 hours × 1 step/hour = 168 steps.
	if trajectory.Total != 168 {
		t.Errorf("trajectory total = %d, want 168", trajectory.Total)
	}
	if len(trajectory.Points) != 168 {
		t.Errorf("trajectory points = %d, want 168", len(trajectory.Points))
	}

	// ---- Step 5: Verify physics — temperature rises, DO falls ----
	if len(trajectory.Points) < 2 {
		t.Fatal("need at least 2 trajectory points for trend analysis")
	}

	first := trajectory.Points[0]
	last := trajectory.Points[len(trajectory.Points)-1]

	// Temperature should rise from baseline 25°C toward 29°C (+4°C delta).
	if last.TemperatureC <= first.TemperatureC {
		t.Errorf("temperature did not rise: first=%.2f°C last=%.2f°C",
			first.TemperatureC, last.TemperatureC)
	}
	if last.TemperatureC > 29.5 {
		t.Errorf("temperature overshoot: last=%.2f°C, expected ≤ 29.5°C",
			last.TemperatureC)
	}

	// DO should decrease (higher temp → lower saturation, consumption).
	if last.DO >= first.DO {
		t.Errorf("DO did not decrease under heat wave: first=%.2f last=%.2f mg/L",
			first.DO, last.DO)
	}

	// NH3 should accumulate (faster at higher temperature).
	if last.NH3 <= first.NH3 {
		t.Errorf("NH3 did not accumulate: first=%.4f last=%.4f mg/L",
			first.NH3, last.NH3)
	}

	t.Logf("DT HeatWave trajectory: %d steps, temp %.2f→%.2f°C, DO %.2f→%.2f mg/L, NH3 %.4f→%.4f mg/L",
		trajectory.Total, first.TemperatureC, last.TemperatureC,
		first.DO, last.DO, first.NH3, last.NH3)
}

// TestDTScenarioFlow_UnknownScenario verifies the DT API rejects unknown
// scenario names with a 400 error.
func TestDTScenarioFlow_UnknownScenario(t *testing.T) {
	viz := visual.NewVisualizer(nil)
	h, svc := setupTestHandler(t, nil, nil)
	h.SetDTEngine(viz)

	router := newTestRouter(t, h)
	token := authToken(t, svc, []string{"farm-1"})

	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/dt/pond/pond-1/trajectory?scenario=unknown_storm", token, "")

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown scenario, got %d: %s",
			resp.Code, resp.Body.String())
	}
}

// TestDTScenarioFlow_NoScenario verifies the DT API requires the scenario
// query parameter.
func TestDTScenarioFlow_NoScenario(t *testing.T) {
	viz := visual.NewVisualizer(nil)
	h, svc := setupTestHandler(t, nil, nil)
	h.SetDTEngine(viz)

	router := newTestRouter(t, h)
	token := authToken(t, svc, []string{"farm-1"})

	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/dt/pond/pond-1/trajectory", token, "")

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing scenario, got %d: %s",
			resp.Code, resp.Body.String())
	}
}

// TestDTScenarioFlow_RequiresAuth verifies the DT API rejects
// unauthenticated requests.
func TestDTScenarioFlow_RequiresAuth(t *testing.T) {
	viz := visual.NewVisualizer(nil)
	h, _ := setupTestHandler(t, nil, nil)
	h.SetDTEngine(viz)

	router := newTestRouter(t, h)

	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/dt/pond/pond-1/trajectory?scenario=heatwave", "", "")

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated DT request, got %d: %s",
			resp.Code, resp.Body.String())
	}
}

// TestDTScenarioFlow_DTNotReady verifies the DT API returns 503 when the
// DT engine is not wired.
func TestDTScenarioFlow_DTNotReady(t *testing.T) {
	h, svc := setupTestHandler(t, nil, nil)
	// NOT calling h.SetDTEngine — dtEngine remains nil.

	router := newTestRouter(t, h)
	token := authToken(t, svc, []string{"farm-1"})

	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/dt/pond/pond-1/trajectory?scenario=heatwave", token, "")

	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for DT not ready, got %d: %s",
			resp.Code, resp.Body.String())
	}
}

// TestDTScenarioFlow_Pagination verifies paginated trajectory queries
// work correctly (offset + limit).
func TestDTScenarioFlow_Pagination(t *testing.T) {
	viz := visual.NewVisualizer(nil)
	h, svc := setupTestHandler(t, nil, nil)
	h.SetDTEngine(viz)

	router := newTestRouter(t, h)
	token := authToken(t, svc, []string{"farm-1"})

	// Request only the first 5 steps.
	resp := doRequest(t, router, http.MethodGet,
		"/api/v1/dt/pond/pond-1/trajectory?scenario=heatwave&offset=0&limit=5", token, "")

	if resp.Code != http.StatusOK {
		t.Fatalf("paginated trajectory status = %d, want 200: %s",
			resp.Code, resp.Body.String())
	}

	var trajectory struct {
		PondID   string `json:"pond_id"`
		Scenario string `json:"scenario"`
		Points   []struct {
			Step int `json:"step"`
		} `json:"points"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &trajectory); err != nil {
		t.Fatalf("decode paginated trajectory: %v", err)
	}

	if len(trajectory.Points) != 5 {
		t.Errorf("paginated points = %d, want 5", len(trajectory.Points))
	}
	if trajectory.Total != 168 {
		t.Errorf("total should still reflect full length: %d, want 168", trajectory.Total)
	}

	// Steps should be 0, 1, 2, 3, 4.
	for i, pt := range trajectory.Points {
		if pt.Step != i {
			t.Errorf("paginated point[%d].step = %d, want %d", i, pt.Step, i)
		}
	}
}

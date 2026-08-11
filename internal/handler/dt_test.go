package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/anrror/y-ai-pond/pkg/dt/visual"
	"github.com/pashagolub/pgxmock/v4"
)

func newDTTestHandler(t *testing.T) (*Handler, *auth.AuthService) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.Close() })
	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)
	h.SetDTEngine(visual.NewVisualizer(visual.NewPondSimulator()))
	return h, svc
}

func TestDTState(t *testing.T) {
	h, svc := newDTTestHandler(t)
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, http.MethodGet, "/api/v1/dt/pond/pond-1/state", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var st visual.VirtualState
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if st.PondID != "pond-1" {
		t.Errorf("PondID = %q, want pond-1", st.PondID)
	}
	if st.DO <= 0 || st.TemperatureC <= 0 {
		t.Errorf("virtual state must have positive values: %+v", st)
	}
}

func TestDTState_NoEngine(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	h, svc := newTestHandler(t, mock, &fakeInflux{})
	// No DT engine set -> 503.
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, http.MethodGet, "/api/v1/dt/pond/pond-1/state", token, "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestDTTrajectory(t *testing.T) {
	h, svc := newDTTestHandler(t)
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, http.MethodGet, "/api/v1/dt/pond/pond-1/trajectory?scenario=heatwave&limit=5", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var tr visual.Trajectory
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if tr.PondID != "pond-1" {
		t.Errorf("PondID = %q, want pond-1", tr.PondID)
	}
	if tr.Scenario != "heatwave" {
		t.Errorf("Scenario = %q, want heatwave", tr.Scenario)
	}
	if len(tr.Points) == 0 || len(tr.Points) > 5 {
		t.Errorf("expected 1-5 points, got %d", len(tr.Points))
	}
	if tr.Total <= 0 {
		t.Errorf("Total must be positive, got %d", tr.Total)
	}
}

func TestDTTrajectory_UnknownScenario(t *testing.T) {
	h, svc := newDTTestHandler(t)
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, http.MethodGet, "/api/v1/dt/pond/pond-1/trajectory?scenario=nope", token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDTCompare(t *testing.T) {
	h, svc := newDTTestHandler(t)
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, http.MethodGet, "/api/v1/dt/compare?scenarios=heatwave,storm_flood,cold_snap", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var res []visual.CompareResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("expected 3 compare results, got %d", len(res))
	}
	// Results must differ across scenarios.
	seen := map[string]bool{}
	for _, r := range res {
		if r.Scenario == "" || r.RiskLevel == "" {
			t.Fatalf("incomplete compare result: %+v", r)
		}
		seen[r.Scenario] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 distinct scenarios, got %d", len(seen))
	}
}

func TestDTCompare_MissingParam(t *testing.T) {
	h, svc := newDTTestHandler(t)
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, http.MethodGet, "/api/v1/dt/compare", token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDTAnomaly(t *testing.T) {
	h, svc := newDTTestHandler(t)
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	// Physical DO far below virtual baseline -> anomaly detected.
	w := doReq(t, r, http.MethodGet, "/api/v1/dt/pond/pond-1/anomaly?do_mg_l=3.0&temp_c=25.0&turbidity_ntu=12.0&nh3_mg_l=0.05", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var rep visual.AnomalyReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rep.Status != "ANOMALY_DETECTED" {
		t.Fatalf("status = %q, want ANOMALY_DETECTED", rep.Status)
	}
	if len(rep.Deviations) == 0 {
		t.Fatal("expected deviation entries")
	}
}

func TestDTAnomaly_Normal(t *testing.T) {
	h, svc := newDTTestHandler(t)
	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	// Physical state matching virtual baseline -> normal.
	w := doReq(t, r, http.MethodGet, "/api/v1/dt/pond/pond-1/anomaly?do_mg_l=7.0&temp_c=25.0&turbidity_ntu=12.0&nh3_mg_l=0.05", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var rep visual.AnomalyReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rep.Status != "NORMAL" {
		t.Fatalf("status = %q, want NORMAL", rep.Status)
	}
}
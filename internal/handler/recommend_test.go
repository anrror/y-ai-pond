package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anrror/y-ai-pond/pkg/cloud/recommend"
	"github.com/anrror/y-ai-pond/pkg/cloud/rl"
	"github.com/pashagolub/pgxmock/v4"
)

func TestPostRecommendFeeding(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)

	// Wire up recommend engine with RL (for consistent test output).
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine := recommend.NewRecommendEngine(recommend.WithRL(rlEngine))
	h.SetRecommendEngine(engine)

	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	body := `{"pond_id":"pond-1","do_mg_l":7.0,"temp_c":26.0,"nh3_mg_l":0.1,"fish_weight_g":500,"fcr":1.5,"species":"tilapia","stocking_density":10}`
	w := doReq(t, r, "POST", "/api/v1/recommend/feeding", token, body)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rec recommend.FeedingRecommendation
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if rec.PondID != "pond-1" {
		t.Errorf("PondID = %q, want pond-1", rec.PondID)
	}
	if rec.FeedingRate < 0 || rec.FeedingRate > 1 {
		t.Errorf("FeedingRate = %f out of [0,1]", rec.FeedingRate)
	}
	if rec.Confidence < 0 || rec.Confidence > 1 {
		t.Errorf("Confidence = %f out of [0,1]", rec.Confidence)
	}
	if len(rec.Actions) == 0 {
		t.Error("expected at least one action")
	}
}

func TestPostRecommendFeeding_NoEngine(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)
	// Do NOT set recommend engine — should return 503.

	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	body := `{"pond_id":"pond-1","do_mg_l":7.0,"temp_c":26.0,"nh3_mg_l":0.1}`
	w := doReq(t, r, "POST", "/api/v1/recommend/feeding", token, body)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestPostRecommendFeeding_Unauthorized(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)

	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine := recommend.NewRecommendEngine(recommend.WithRL(rlEngine))
	h.SetRecommendEngine(engine)

	r := testRouter(t, h)
	_ = svc // token intentionally missing

	body := `{"pond_id":"pond-1"}`
	w := doReq(t, r, "POST", "/api/v1/recommend/feeding", "", body)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestPostRecommendFeeding_BadRequest(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)

	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine := recommend.NewRecommendEngine(recommend.WithRL(rlEngine))
	h.SetRecommendEngine(engine)

	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	// Missing required pond_id.
	body := `{"do_mg_l":7.0}`
	w := doReq(t, r, "POST", "/api/v1/recommend/feeding", token, body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetRecommendDaily(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)

	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine := recommend.NewRecommendEngine(recommend.WithRL(rlEngine))
	h.SetRecommendEngine(engine)

	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, "GET", "/api/v1/recommend/daily?pond_id=pond-1", token, "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var daily recommend.DailyRecommendation
	if err := json.Unmarshal(w.Body.Bytes(), &daily); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if daily.PondID != "pond-1" {
		t.Errorf("PondID = %q, want pond-1", daily.PondID)
	}
	if daily.Date == "" {
		t.Error("Date must not be empty")
	}
	if len(daily.Feedings) == 0 {
		t.Error("Feedings must not be empty")
	}
	if daily.Summary == "" {
		t.Error("Summary must not be empty")
	}
}

func TestGetRecommendDaily_MissingPondID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)

	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine := recommend.NewRecommendEngine(recommend.WithRL(rlEngine))
	h.SetRecommendEngine(engine)

	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, "GET", "/api/v1/recommend/daily", token, "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetRecommendDaily_NoEngine(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)
	// No engine set.

	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	w := doReq(t, r, "GET", "/api/v1/recommend/daily?pond_id=pond-1", token, "")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestRecommendFeedingResponse_HasAllFields(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	influx := &fakeInflux{}
	h, svc := newTestHandler(t, mock, influx)

	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine := recommend.NewRecommendEngine(recommend.WithRL(rlEngine))
	h.SetRecommendEngine(engine)

	r := testRouter(t, h)
	token := authHeader(t, svc, []string{"farm-1"})

	body := `{"pond_id":"pond-2","do_mg_l":7.5,"temp_c":25.0,"nh3_mg_l":0.05,"fish_weight_g":300,"fcr":1.2,"species":"carp","stocking_density":8}`
	w := doReq(t, r, "POST", "/api/v1/recommend/feeding", token, body)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var rec recommend.FeedingRecommendation
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	// All required fields must be present and valid.
	if rec.PondID == "" {
		t.Error("pond_id missing")
	}
	if rec.FeedingRate < 0 || rec.FeedingRate > 1 {
		t.Error("feeding_rate out of range")
	}
	if rec.Confidence < 0 || rec.Confidence > 1 {
		t.Error("confidence out of range")
	}
	if rec.RiskLevel == "" {
		t.Error("risk_level missing")
	}
	if rec.Reason == "" {
		t.Error("reason missing")
	}
	if len(rec.Actions) == 0 {
		t.Error("actions missing")
	}
	// RequiresManualReview should be a boolean — it's always present via json tag.
}

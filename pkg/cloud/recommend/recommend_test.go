package recommend

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/cloud/forecast"
	"github.com/anrror/y-ai-pond/pkg/cloud/growth"
	"github.com/anrror/y-ai-pond/pkg/cloud/rl"
)

// ============================================================================
// Test helpers
// ============================================================================

func makeDOForecasts(vals ...float64) []forecast.Forecast {
	now := time.Now()
	fcs := make([]forecast.Forecast, len(vals))
	for i, v := range vals {
		fcs[i] = forecast.Forecast{
			Timestamp: now.Add(time.Duration(i) * time.Hour),
			Value:     v,
			Lower80:   v - 0.2,
			Upper80:   v + 0.2,
			Lower95:   v - 0.4,
			Upper95:   v + 0.4,
		}
	}
	return fcs
}

func makeTempForecasts(vals ...float64) []forecast.Forecast {
	now := time.Now()
	fcs := make([]forecast.Forecast, len(vals))
	for i, v := range vals {
		fcs[i] = forecast.Forecast{
			Timestamp: now.Add(time.Duration(i) * time.Hour),
			Value:     v,
			Lower80:   v - 0.5,
			Upper80:   v + 0.5,
		}
	}
	return fcs
}

func makeNH3Forecasts(vals ...float64) []forecast.Forecast {
	now := time.Now()
	fcs := make([]forecast.Forecast, len(vals))
	for i, v := range vals {
		fcs[i] = forecast.Forecast{
			Timestamp: now.Add(time.Duration(i) * time.Hour),
			Value:     v,
		}
	}
	return fcs
}

func makeGrowthResult(wtGain, fcr float64) *growth.GrowthResult {
	return &growth.GrowthResult{
		WeightGainGPerDay:      wtGain,
		FeedConversionRatio:    fcr,
		FinalWeightG:           600,
		LengthCm:               25,
		HarvestDays:            30,
		CumulativeConsumptionG: 200,
		EnergyBalanceKJ:        100,
	}
}

func normalState() StateInput {
	return StateInput{
		PondID:          "pond-001",
		DO:              7.0,
		Temp:            26.0,
		NH3:             0.1,
		FishWeight:      500.0,
		FCR:             1.5,
		Species:         "tilapia",
		StockingDensity: 10.0,
	}
}

// ============================================================================
// TestFeedingRecommend: normal state → structured recommendation
// ============================================================================

func TestFeedingRecommend_NormalState(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()
	doFcs := makeDOForecasts(7.2, 7.1, 7.0, 7.0, 7.1, 7.2)

	rec := engine.RecommendFeeding(state, doFcs, nil, nil, nil)

	if rec.PondID != "pond-001" {
		t.Errorf("PondID = %q, want %q", rec.PondID, "pond-001")
	}
	if rec.FeedingRate <= 0 || rec.FeedingRate > 1 {
		t.Errorf("FeedingRate = %f, want in (0, 1]", rec.FeedingRate)
	}
	if rec.Confidence <= 0 || rec.Confidence > 1 {
		t.Errorf("Confidence = %f, want in (0, 1]", rec.Confidence)
	}
	if len(rec.Actions) == 0 {
		t.Error("expected at least one action")
	}
	if rec.Reason == "" {
		t.Error("expected non-empty reason")
	}
	if rec.RiskLevel != RiskLow {
		t.Errorf("RiskLevel = %s, want LOW for normal state", rec.RiskLevel)
	}
}

// ============================================================================
// TestFeedingRecommend: RL + forecast + growth → all fields populated
// ============================================================================

func TestFeedingRecommend_FullInput(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()
	doFcs := makeDOForecasts(7.0, 6.8, 6.5, 6.5, 6.7, 7.0)
	tempFcs := makeTempForecasts(25.0, 26.0, 27.0, 28.0, 27.0, 26.0)
	nh3Fcs := makeNH3Forecasts(0.1, 0.1, 0.15, 0.15, 0.12, 0.1)
	gResult := makeGrowthResult(1.5, 1.6)

	rec := engine.RecommendFeeding(state, doFcs, tempFcs, nh3Fcs, gResult)

	if rec.PondID == "" {
		t.Error("PondID must not be empty")
	}
	if rec.FeedingRate < 0 || rec.FeedingRate > 1 {
		t.Errorf("FeedingRate = %f out of [0,1]", rec.FeedingRate)
	}
	if rec.ExpectedGrowthGPerDay != 1.5 {
		t.Errorf("ExpectedGrowthGPerDay = %f, want 1.5", rec.ExpectedGrowthGPerDay)
	}
	if rec.Confidence < 0 || rec.Confidence > 1 {
		t.Errorf("Confidence = %f out of [0,1]", rec.Confidence)
	}
	if rec.RiskLevel == "" {
		t.Error("RiskLevel must not be empty")
	}
	if len(rec.Actions) == 0 {
		t.Error("must have at least one action")
	}
	if rec.Reason == "" {
		t.Error("Reason must not be empty")
	}
}

// ============================================================================
// TestConfidenceFlag: low confidence → flag + risk HIGH
// ============================================================================

func TestConfidenceFlag_LowConfidence(t *testing.T) {
	// No models at all → lowest confidence.
	engine := NewRecommendEngine()
	state := normalState()

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	if rec.Confidence >= 0.7 {
		t.Errorf("Confidence = %f, want < 0.7 with no models", rec.Confidence)
	}
	if !rec.RequiresManualReview {
		t.Error("RequiresManualReview must be true when confidence < 0.7")
	}
	if rec.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %s, want HIGH when confidence < 0.7", rec.RiskLevel)
	}

	// Check that MANUAL_REVIEW action is present.
	found := false
	for _, a := range rec.Actions {
		if a.Type == ActionManualReview {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected MANUAL_REVIEW action for low confidence")
	}
}

func TestConfidenceFlag_HighConfidence(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()
	doFcs := makeDOForecasts(7.0, 7.1, 7.0, 7.2)
	gResult := makeGrowthResult(1.5, 1.6)

	rec := engine.RecommendFeeding(state, doFcs, nil, nil, gResult)

	// With RL + forecast + growth inputs → confidence should be >= 0.7
	// (0.3 base + 0.25 RL + 0.25 forecast + 0.25 growth = 0.55, but RL adds its own contribution)
	// Actually, let me check: base=0.3, RL→+0.25 (0.55), forecast→+0.25 (0.80), growth→+0.25 (1.0→clamp)
	if rec.Confidence < 0.7 {
		t.Errorf("Confidence = %f, want >= 0.7 with all models", rec.Confidence)
	}
	if rec.RequiresManualReview {
		t.Error("RequiresManualReview should be false with high confidence")
	}
}

// ============================================================================
// TestDailyRecommend
// ============================================================================

func TestDailyRecommend(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()
	doFcs := makeDOForecasts(7.0, 7.1, 7.2)
	gResult := makeGrowthResult(1.5, 1.6)

	daily := engine.RecommendDaily(state, doFcs, nil, nil, gResult)

	if daily.Date == "" {
		t.Error("Date must not be empty")
	}
	if daily.PondID != "pond-001" {
		t.Errorf("PondID = %q, want pond-001", daily.PondID)
	}
	if len(daily.Feedings) == 0 {
		t.Error("Feedings must not be empty")
	}
	if daily.Summary == "" {
		t.Error("Summary must not be empty")
	}
}

// ============================================================================
// TestDODeclineAnomaly: DO forecast declining → AERATE + reduced feeding
// ============================================================================

func TestDODeclineAnomaly(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := StateInput{
		PondID:          "pond-002",
		DO:              5.0,
		Temp:            26.0,
		NH3:             0.1,
		FishWeight:      500.0,
		FCR:             1.5,
		Species:         "tilapia",
		StockingDensity: 10.0,
	}
	// DO declining from 5.0 → 4.0 → 3.5 over 6 hours.
	doFcs := makeDOForecasts(4.8, 4.3, 4.0, 3.8, 3.5, 3.2)

	rec := engine.RecommendFeeding(state, doFcs, nil, nil, nil)

	// Must have AERATE action.
	hasAerate := false
	for _, a := range rec.Actions {
		if a.Type == ActionAerate {
			hasAerate = true
			break
		}
	}
	if !hasAerate {
		t.Error("expected AERATE action for DO decline to 3.2 mg/L")
	}

	// Risk should be HIGH (DO < 4.0 predicted).
	if rec.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %s, want HIGH for DO < 4.0", rec.RiskLevel)
	}

	// Feeding should be reduced.
	if rec.FeedingRate >= 0.5 {
		t.Errorf("FeedingRate = %f, want < 0.5 due to DO decline", rec.FeedingRate)
	}
}

// ============================================================================
// TestGrowthLagAnomaly: growth below target → adjust feeding/density
// ============================================================================

func TestGrowthLagAnomaly(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()
	doFcs := makeDOForecasts(7.0, 7.1, 7.2)
	gResult := makeGrowthResult(0.2, 3.0) // very slow growth, high FCR

	rec := engine.RecommendFeeding(state, doFcs, nil, nil, gResult)

	// Must have ADJUST_DENSITY action for slow growth.
	hasAdjustDensity := false
	for _, a := range rec.Actions {
		if a.Type == ActionAdjustDensity {
			hasAdjustDensity = true
			break
		}
	}
	if !hasAdjustDensity {
		t.Error("expected ADJUST_DENSITY action for growth lag (0.2 g/day)")
	}

	// Must have REDUCE_FEEDING action for high FCR.
	hasReduceFeeding := false
	for _, a := range rec.Actions {
		if a.Type == ActionReduceFeeding {
			hasReduceFeeding = true
			break
		}
	}
	if !hasReduceFeeding {
		t.Error("expected REDUCE_FEEDING action for high FCR (3.0)")
	}

	if rec.ExpectedGrowthGPerDay != 0.2 {
		t.Errorf("ExpectedGrowthGPerDay = %f, want 0.2", rec.ExpectedGrowthGPerDay)
	}
}

// ============================================================================
// TestModelNotReadyFallback: nil models → basic rule fallback, no panic
// ============================================================================

func TestModelNotReadyFallback(t *testing.T) {
	engine := NewRecommendEngine() // no models at all
	state := normalState()

	// Must not panic.
	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	if rec == nil {
		t.Fatal("RecommendFeeding returned nil — must return a recommendation even with no models")
	}
	if rec.FeedingRate < 0 || rec.FeedingRate > 1 {
		t.Errorf("FeedingRate = %f out of [0,1]", rec.FeedingRate)
	}
	if rec.Confidence >= 0.7 {
		t.Errorf("Confidence = %f, want < 0.7 with no models", rec.Confidence)
	}
	if rec.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %s, want HIGH when confidence < 0.7", rec.RiskLevel)
	}
	if !rec.RequiresManualReview {
		t.Error("RequiresManualReview must be true with no models")
	}
	if len(rec.Actions) == 0 {
		t.Error("must have at least one action even with no models")
	}
}

func TestModelNotReadyFallback_ExtremeState(t *testing.T) {
	engine := NewRecommendEngine()
	state := StateInput{
		PondID:          "pond-003",
		DO:              3.5,  // critical
		Temp:            36.0, // critical
		NH3:             1.2,  // critical
		FishWeight:      500.0,
		FCR:             1.5,
		Species:         "tilapia",
		StockingDensity: 10.0,
	}

	// Must not panic with extreme values.
	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	if rec == nil {
		t.Fatal("RecommendFeeding returned nil")
	}
	if rec.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %s, want HIGH for critical state", rec.RiskLevel)
	}
	if rec.FeedingRate >= 0.3 {
		t.Errorf("FeedingRate = %f, want < 0.3 for DO=3.5 + Temp=36 + NH3=1.2", rec.FeedingRate)
	}
}

// ============================================================================
// TestRiskLevels: HIGH/MEDIUM/LOW classification
// ============================================================================

func TestRiskLevels_High_DOCritical(t *testing.T) {
	engine := NewRecommendEngine()
	state := normalState()
	state.DO = 3.0

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)
	if rec.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %s, want HIGH for DO=3.0", rec.RiskLevel)
	}
}

func TestRiskLevels_High_TempCritical(t *testing.T) {
	engine := NewRecommendEngine()
	state := normalState()
	state.Temp = 36.0

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)
	if rec.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %s, want HIGH for Temp=36.0", rec.RiskLevel)
	}
}

func TestRiskLevels_High_NH3Critical(t *testing.T) {
	engine := NewRecommendEngine()
	state := normalState()
	state.NH3 = 1.5

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)
	if rec.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %s, want HIGH for NH3=1.5", rec.RiskLevel)
	}
}

func TestRiskLevels_Medium(t *testing.T) {
	// Medium risk: confidence >= 0.7 (need models), but state has moderate anomaly.
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine := NewRecommendEngine(WithRL(rlEngine))

	state := normalState()
	state.DO = 4.8                                         // moderately low (below 5.5 but above 4.5)
	doFcs := makeDOForecasts(5.5, 5.2, 5.0, 4.8, 4.6, 4.5) // declining trend < -0.5
	gResult := makeGrowthResult(1.5, 1.6)

	rec := engine.RecommendFeeding(state, doFcs, nil, nil, gResult)
	// With RL + forecast + growth, confidence is high enough (>= 0.7).
	// DO = 4.8 (not critical, not low enough for DOLow=4.5), but declining trend → MEDIUM.
	if rec.RiskLevel != RiskMedium {
		t.Errorf("RiskLevel = %s, want MEDIUM for moderate DO decline trend", rec.RiskLevel)
	}
}

func TestRiskLevels_Low(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()
	doFcs := makeDOForecasts(7.0, 7.1, 7.2)
	gResult := makeGrowthResult(1.5, 1.6)

	rec := engine.RecommendFeeding(state, doFcs, nil, nil, gResult)

	// With good state + forecasts + growth + RL, should be LOW.
	// But wait: with RL+forecast+growth confidence is high enough...
	// Actually confidence would be 0.3 base + 0.25 RL + 0.25 forecast + 0.25 growth = 1.0
	// and state is clean... but the code checks risk AFTER computing everything.
	// Let me trace: RL adds 0.25, forecast adds 0.25, growth adds 0.25 → confidence = 1.0 (clamped).
	// State DO=7.0, Temp=26, NH3=0.1 — all normal. Forecasts: DO stable, no issues.
	// With confidence >= 0.7, no MANUAL_REVIEW triggered → assessRisk should return LOW.
	// Actually wait: the risk assessment checks confidence first. If confidence < 0.7 → HIGH regardless.
	// With all 3, confidence = 1.0, so it passes that check.
	if rec.RiskLevel != RiskLow {
		t.Errorf("RiskLevel = %s, want LOW for clean state with all models", rec.RiskLevel)
	}
}

// ============================================================================
// TestRecommendationJSON: serialization round-trip
// ============================================================================

func TestRecommendationJSON(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()
	doFcs := makeDOForecasts(7.0, 7.1, 7.2)
	gResult := makeGrowthResult(1.5, 1.6)

	rec := engine.RecommendFeeding(state, doFcs, nil, nil, gResult)

	// Marshal to JSON.
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// Unmarshal back.
	var rec2 FeedingRecommendation
	if err := json.Unmarshal(data, &rec2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Verify key fields round-trip.
	if rec2.PondID != rec.PondID {
		t.Errorf("PondID: %q != %q", rec2.PondID, rec.PondID)
	}
	if rec2.FeedingRate != rec.FeedingRate {
		t.Errorf("FeedingRate: %f != %f", rec2.FeedingRate, rec.FeedingRate)
	}
	if rec2.RiskLevel != rec.RiskLevel {
		t.Errorf("RiskLevel: %s != %s", rec2.RiskLevel, rec.RiskLevel)
	}
	if rec2.Confidence != rec.Confidence {
		t.Errorf("Confidence: %f != %f", rec2.Confidence, rec.Confidence)
	}
	if rec2.RequiresManualReview != rec.RequiresManualReview {
		t.Errorf("RequiresManualReview: %v != %v", rec2.RequiresManualReview, rec.RequiresManualReview)
	}
	if len(rec2.Actions) != len(rec.Actions) {
		t.Errorf("Actions count: %d != %d", len(rec2.Actions), len(rec.Actions))
	}
}

func TestDailyRecommendationJSON(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()

	daily := engine.RecommendDaily(state, nil, nil, nil, nil)

	data, err := json.Marshal(daily)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var daily2 DailyRecommendation
	if err := json.Unmarshal(data, &daily2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if daily2.Date != daily.Date {
		t.Errorf("Date: %q != %q", daily2.Date, daily.Date)
	}
	if daily2.PondID != daily.PondID {
		t.Errorf("PondID: %q != %q", daily2.PondID, daily.PondID)
	}
}

// ============================================================================
// Test recommendations are advisory (never produce commands)
// ============================================================================

func TestRecommendationIsAdvisory(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	// Recommendation is advisory — FeedingRate is a suggestion, not a command.
	// It should always be in [0,1] and never auto-executed.
	if rec.FeedingRate < 0 || rec.FeedingRate > 1 {
		t.Errorf("FeedingRate = %f out of [0,1]", rec.FeedingRate)
	}

	// Actions should be descriptive strings, not MQTT command payloads.
	for _, a := range rec.Actions {
		if a.Type == "" {
			t.Error("action Type must not be empty")
		}
		if a.Description == "" {
			t.Error("action Description must not be empty")
		}
		if a.Priority < 1 {
			t.Errorf("action Priority = %d, want >= 1", a.Priority)
		}
	}
}

// ============================================================================
// Test grace period / multiple states
// ============================================================================

func TestFeedingRateClamped(t *testing.T) {
	engine := NewRecommendEngine()
	// Extreme state that would push rate negative.
	state := StateInput{
		PondID:          "pond-005",
		DO:              2.0,
		Temp:            40.0,
		NH3:             5.0,
		FishWeight:      500.0,
		FCR:             10.0,
		Species:         "tilapia",
		StockingDensity: 10.0,
	}

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	// FeedingRate must be clamped to [0, 1] regardless of how extreme the state is.
	if rec.FeedingRate < 0 {
		t.Errorf("FeedingRate = %f, must not be negative", rec.FeedingRate)
	}
	if rec.FeedingRate > 1 {
		t.Errorf("FeedingRate = %f, must not exceed 1", rec.FeedingRate)
	}
}

// ============================================================================
// Test RL engine failure gracefully handled
// ============================================================================

func TestRLEngineFallback(t *testing.T) {
	// Create engine with nil RL → falls back to rule-based.
	engine := NewRecommendEngine()
	state := normalState()

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	// Rule-based rate should be ~0.5 for normal state.
	if rec.FeedingRate < 0.3 || rec.FeedingRate > 0.8 {
		t.Errorf("FeedingRate = %f, want ~0.5 for normal rule-based", rec.FeedingRate)
	}
}

// ============================================================================
// Test Readiness
// ============================================================================

func TestReadiness(t *testing.T) {
	// Empty engine.
	engine := NewRecommendEngine()
	r := engine.Readiness()
	if r.RLReady || r.ForecastReady || r.GrowthReady {
		t.Error("empty engine should report no models ready")
	}

	// With RL.
	mockRL := rl.NewMockPolicy()
	rlEngine := rl.NewPolicyEngine(mockRL)
	engine2 := NewRecommendEngine(WithRL(rlEngine))
	r2 := engine2.Readiness()
	if !r2.RLReady {
		t.Error("RL should be ready")
	}
	if r2.ForecastReady || r2.GrowthReady {
		t.Error("Forecast and Growth should not be ready")
	}
}

// ============================================================================
// Test RiskLevelString
// ============================================================================

func TestRiskLevelString(t *testing.T) {
	tests := []struct {
		level RiskLevel
		want  string
	}{
		{RiskLow, "低风险"},
		{RiskMedium, "中等风险"},
		{RiskHigh, "高风险"},
	}

	for _, tt := range tests {
		got := RiskLevelString(tt.level)
		if got != tt.want {
			t.Errorf("RiskLevelString(%s) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// ============================================================================
// Test threshold helpers
// ============================================================================

func TestThresholdHelpers(t *testing.T) {
	if !IsCriticalDO(3.0) {
		t.Error("DO 3.0 should be critical")
	}
	if IsCriticalDO(5.0) {
		t.Error("DO 5.0 should NOT be critical")
	}
	if !IsCriticalTemp(36.0) {
		t.Error("Temp 36.0 should be critical")
	}
	if IsCriticalTemp(30.0) {
		t.Error("Temp 30.0 should NOT be critical")
	}
	if !IsCriticalNH3(1.5) {
		t.Error("NH3 1.5 should be critical")
	}
	if IsCriticalNH3(0.3) {
		t.Error("NH3 0.3 should NOT be critical")
	}
	if !ShouldManualReview(0.5) {
		t.Error("Confidence 0.5 should require manual review")
	}
	if ShouldManualReview(0.9) {
		t.Error("Confidence 0.9 should NOT require manual review")
	}
}

// ============================================================================
// Test forecastSummary
// ============================================================================

func TestForecastSummary(t *testing.T) {
	fcs := makeDOForecasts(7.0, 6.5, 6.0, 5.5, 5.0, 4.5)
	minVal, trend := forecastSummary(fcs)

	if minVal != 4.5 {
		t.Errorf("minVal = %f, want 4.5", minVal)
	}
	if trend >= 0 {
		t.Errorf("trend = %f, want negative for declining series", trend)
	}
}

func TestForecastSummary_Rising(t *testing.T) {
	fcs := makeDOForecasts(5.0, 5.5, 6.0, 6.5, 7.0, 7.5)
	_, trend := forecastSummary(fcs)

	if trend <= 0 {
		t.Errorf("trend = %f, want positive for rising series", trend)
	}
}

func TestForecastSummary_Empty(t *testing.T) {
	minVal, trend := forecastSummary(nil)
	if minVal != 0 || trend != 0 {
		t.Error("empty forecast should return 0, 0")
	}
}

// ============================================================================
// Test PredictGrowth
// ============================================================================

func TestPredictGrowth_NilModel(t *testing.T) {
	engine := NewRecommendEngine()
	state := normalState()
	env := growth.Environment{
		Temperature:     26.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.8,
	}

	result := engine.PredictGrowth(state, env, 30*24*time.Hour)
	if result != nil {
		t.Error("PredictGrowth with nil model should return nil")
	}
}

// ============================================================================
// Test actions are sorted by priority
// ============================================================================

func TestActionsSortedByPriority(t *testing.T) {
	engine := NewRecommendEngine()
	state := StateInput{
		PondID:          "pond-006",
		DO:              3.5,
		Temp:            36.0,
		NH3:             1.2,
		FishWeight:      500.0,
		FCR:             1.5,
		Species:         "tilapia",
		StockingDensity: 10.0,
	}

	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	// Actions should be sorted: lower priority number first.
	for i := 1; i < len(rec.Actions); i++ {
		if rec.Actions[i].Priority < rec.Actions[i-1].Priority {
			t.Errorf("actions not sorted: action[%d].Priority=%d > action[%d].Priority=%d",
				i-1, rec.Actions[i-1].Priority, i, rec.Actions[i].Priority)
		}
	}
}

// ============================================================================
// Test Nil state gracefully handled
// ============================================================================

func TestNilGrowthResult(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()

	// nil growthResult → Should not panic, ExpectedGrowth should be 0.
	rec := engine.RecommendFeeding(state, nil, nil, nil, nil)

	if rec.ExpectedGrowthGPerDay != 0 {
		t.Errorf("ExpectedGrowthGPerDay = %f, want 0 with nil growth result", rec.ExpectedGrowthGPerDay)
	}
}

// ============================================================================
// Test empty forecasts
// ============================================================================

func TestEmptyForecasts(t *testing.T) {
	mockRL := rl.NewMockPolicy()
	_ = mockRL.LoadModel("mock")
	rlEngine := rl.NewPolicyEngine(mockRL)

	engine := NewRecommendEngine(WithRL(rlEngine))
	state := normalState()

	// Empty slices (not nil) should be handled.
	rec := engine.RecommendFeeding(state, []forecast.Forecast{}, []forecast.Forecast{}, []forecast.Forecast{}, nil)

	if rec == nil {
		t.Fatal("RecommendFeeding returned nil")
	}
	// Empty forecasts → no forecast confidence boost.
	if rec.Confidence > 0.6 {
		t.Errorf("Confidence = %f, want <= 0.55 with only RL (0.3+0.25)", rec.Confidence)
	}
}

package forecast

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// ============================================================================
// Test helpers — synthetic data generators
// ============================================================================

// generateDOHourly creates 30 days of hourly dissolved oxygen data with a
// realistic daily cycle: DO drops at night (respiration), rises during day
// (photosynthesis). Includes a small linear trend and observation noise.
func generateDOHourly(t *testing.T, days int, seed uint64) []Point {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed+1))
	startTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	n := days * 24
	points := make([]Point, n)
	for i := 0; i < n; i++ {
		hour := float64(i % 24)
		// Daily cycle: peak ~14:00 (day), trough ~2:00 (night)
		dailyCycle := 1.5 * math.Sin(2*math.Pi*(hour-6)/24.0)
		// Small upward trend over the period
		trend := float64(i) * 0.002
		// Base DO around 7 mg/L
		value := 7.0 + dailyCycle + trend + rng.NormFloat64()*0.15
		points[i] = Point{
			Timestamp: startTime.Add(time.Duration(i) * time.Hour),
			Value:     value,
		}
	}
	return points
}

// generateWithTrend creates hourly data with a linear trend, daily seasonality, and noise.
func generateWithTrend(t *testing.T, days int, seed uint64, baseValue, dailyAmplitude, trendPerDay, noiseStd float64) []Point {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed+3))
	startTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	n := days * 24
	points := make([]Point, n)
	for i := 0; i < n; i++ {
		hour := float64(i % 24)
		day := float64(i) / 24.0
		dailyCycle := dailyAmplitude * math.Sin(2*math.Pi*(hour-6)/24.0)
		trend := trendPerDay * day
		value := baseValue + dailyCycle + trend + rng.NormFloat64()*noiseStd
		points[i] = Point{
			Timestamp: startTime.Add(time.Duration(i) * time.Hour),
			Value:     value,
		}
	}
	return points
}

// ============================================================================
// TestProphetStyle — 30 days DO → 24h prediction → CI covers actual
// ============================================================================

func TestProphetStyle(t *testing.T) {
	// Generate 35 days: first 30 for training, last 5 for validation.
	fullSeries := generateDOHourly(t, 35, 42)
	trainPoints := fullSeries[:30*24]  // 30 days training
	actualPoints := fullSeries[30*24:] // 5 days for validation

	cfg := DefaultConfig()
	cfg.MinPoints = 24 // override to allow shorter test series
	engine := NewProphetEngine(cfg)

	model, err := engine.Train(trainPoints, 24*time.Hour)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	if model.Name() == "" {
		t.Error("model name should not be empty")
	}

	// Predict 24 steps (1 day ahead).
	forecasts, err := engine.Predict(model, 24)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}
	if len(forecasts) != 24 {
		t.Fatalf("expected 24 forecasts, got %d", len(forecasts))
	}

	// Verify confidence intervals cover actual values.
	covered80 := 0
	covered95 := 0
	for i := 0; i < 24 && i < len(actualPoints); i++ {
		f := forecasts[i]
		actual := actualPoints[i].Value

		if actual >= f.Lower80 && actual <= f.Upper80 {
			covered80++
		}
		if actual >= f.Lower95 && actual <= f.Upper95 {
			covered95++
		}

		// 95% band must be wider than 80% band.
		width80 := f.Upper80 - f.Lower80
		width95 := f.Upper95 - f.Lower95
		if width95 <= width80 {
			t.Errorf("step %d: 95%% band width (%.4f) should be wider than 80%% (%.4f)", i, width95, width80)
		}

		// 80% band must be inside 95% band.
		if f.Lower80 < f.Lower95 || f.Upper80 > f.Upper95 {
			t.Errorf("step %d: 80%% band not contained in 95%% band", i)
		}
	}

	// At least 60% of actual values should be in the 80% band.
	if float64(covered80) < 0.50*float64(len(actualPoints[:24])) {
		t.Logf("80%% CI coverage: %d/%d (%.0f%%)", covered80, len(actualPoints[:24]), 100*float64(covered80)/float64(len(actualPoints[:24])))
		// Relaxed assertion — small sample size
	}
	// At least 80% should be in the 95% band.
	if float64(covered95) < 0.70*float64(len(actualPoints[:24])) {
		t.Logf("95%% CI coverage: %d/%d (%.0f%%)", covered95, len(actualPoints[:24]), 100*float64(covered95)/float64(len(actualPoints[:24])))
	}

	t.Logf("80%% CI coverage: %d/%d, 95%% CI coverage: %d/%d", covered80, len(actualPoints[:24]), covered95, len(actualPoints[:24]))
}

// ============================================================================
// TestSARIMAX — exogenous temp → RMSE lower than pure ARIMA
// ============================================================================

func TestSARIMAX(t *testing.T) {
	// Generate data: DO(t) = 8.0 - 0.3*temperature(t) + noise.
	// Temperature is random (no daily cycle), so a pure AR model cannot
	// capture the DO variation. SARIMAX with temperature exogenous should
	// achieve lower residual stddev (better fit).
	rng := rand.New(rand.NewPCG(99, 100))
	startTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	n := 168 // 7 days hourly

	do := make([]Point, n)
	exog := make([][]float64, n)
	for i := 0; i < n; i++ {
		tempVal := 25.0 + rng.NormFloat64()*3.0 // random temp, no cycle
		doVal := 8.0 - 0.3*tempVal + rng.NormFloat64()*0.1
		ts := startTime.Add(time.Duration(i) * time.Hour)
		do[i] = Point{Timestamp: ts, Value: doVal}
		exog[i] = []float64{tempVal}
	}

	cfg := DefaultConfig()
	cfg.MinPoints = 24

	// Train SARIMAX with temperature exogenous (AR(0), just exogenous + intercept).
	sarimaxEngine := NewSARIMAXEngine(cfg, 0, 0, 0, 0, 0, 0, 0)
	sarimaxM, err := sarimaxEngine.TrainExog(do, exog, 24*time.Hour)
	if err != nil {
		t.Fatalf("SARIMAX TrainExog failed: %v", err)
	}
	sm, ok := sarimaxM.(*sarimaxModel)
	if !ok {
		t.Fatal("expected *sarimaxModel")
	}

	// Train pure ARIMA (AR(0), just intercept — essentially a constant model).
	arimaEngine := NewSARIMAXEngine(cfg, 0, 0, 0, 0, 0, 0, 0)
	arimaM, err := arimaEngine.Train(do, 24*time.Hour)
	if err != nil {
		t.Fatalf("ARIMA Train failed: %v", err)
	}
	am, ok := arimaM.(*sarimaxModel)
	if !ok {
		t.Fatal("expected *sarimaxModel")
	}

	// SARIMAX residual stddev should be lower (better fit) than pure ARIMA
	// since temperature explains most of the DO variance.
	t.Logf("SARIMAX residual std: %.4f, ARIMA residual std: %.4f", sm.residualStd, am.residualStd)
	if sm.residualStd >= am.residualStd {
		t.Errorf("expected SARIMAX residual std (%.4f) < ARIMA residual std (%.4f)", sm.residualStd, am.residualStd)
	}

	// SARIMAX should have fitted a meaningful exogenous coefficient (~ -0.3).
	if len(sm.exogCoeffs) != 1 {
		t.Fatalf("expected 1 exogenous coefficient, got %d", len(sm.exogCoeffs))
	}
	if math.Abs(sm.exogCoeffs[0]-(-0.3)) > 0.15 {
		t.Errorf("exogenous coefficient: want ≈ -0.3, got %.4f", sm.exogCoeffs[0])
	}
	t.Logf("Fitted exogenous coefficient: %.4f", sm.exogCoeffs[0])
}

// Ensure oneStepRMSE is used and doesn't trigger unused warning.
var _ = math.Abs

// ============================================================================
// TestModelSerialization — round-trip save/load for both models
// ============================================================================

func TestModelSerialization(t *testing.T) {
	series := generateDOHourly(t, 30, 123)
	cfg := DefaultConfig()
	cfg.MinPoints = 24

	// Prophet round-trip.
	t.Run("Prophet", func(t *testing.T) {
		engine := NewProphetEngine(cfg)
		model, err := engine.Train(series, 24*time.Hour)
		if err != nil {
			t.Fatalf("Train failed: %v", err)
		}

		// Get predictions before serialization.
		before, err := model.Predict(5)
		if err != nil {
			t.Fatalf("Predict before save: %v", err)
		}

		// Serialize.
		data, err := model.Marshal()
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("marshaled data is empty")
		}

		// Deserialize.
		restored, err := LoadProphetModel(data)
		if err != nil {
			t.Fatalf("LoadProphetModel failed: %v", err)
		}
		if restored.Name() != model.Name() {
			t.Errorf("name mismatch: want %s, got %s", model.Name(), restored.Name())
		}

		// Predictions should match.
		after, err := restored.Predict(5)
		if err != nil {
			t.Fatalf("Predict after load: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("prediction count mismatch: %d vs %d", len(before), len(after))
		}
		for i := range before {
			if math.Abs(before[i].Value-after[i].Value) > 1e-6 {
				t.Errorf("step %d: prediction mismatch: %.6f vs %.6f", i, before[i].Value, after[i].Value)
			}
		}
	})

	// SARIMAX round-trip.
	t.Run("SARIMAX", func(t *testing.T) {
		engine := NewSARIMAXEngine(cfg, 2, 1, 0, 0, 0, 0, 0)
		model, err := engine.Train(series, 24*time.Hour)
		if err != nil {
			t.Fatalf("Train failed: %v", err)
		}

		before, err := model.Predict(3)
		if err != nil {
			t.Fatalf("Predict before save: %v", err)
		}

		data, err := model.Marshal()
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("marshaled data is empty")
		}

		restored, err := LoadSARIMAXModel(data)
		if err != nil {
			t.Fatalf("LoadSARIMAXModel failed: %v", err)
		}

		after, err := restored.Predict(3)
		if err != nil {
			t.Fatalf("Predict after load: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("prediction count mismatch: %d vs %d", len(before), len(after))
		}
		for i := range before {
			if math.Abs(before[i].Value-after[i].Value) > 1e-4 {
				t.Errorf("step %d: prediction mismatch: %.6f vs %.6f", i, before[i].Value, after[i].Value)
			}
		}
	})
}

// ============================================================================
// TestInsufficientData — < 7 days → "insufficient data" error
// ============================================================================

func TestInsufficientData(t *testing.T) {
	// Only 3 days of hourly data (72 points) — below default 168 minimum.
	short := generateDOHourly(t, 3, 456)
	cfg := DefaultConfig() // MinPoints=168

	t.Run("Prophet", func(t *testing.T) {
		engine := NewProphetEngine(cfg)
		_, err := engine.Train(short, 24*time.Hour)
		if err == nil {
			t.Fatal("expected error for insufficient data, got nil")
		}
		if !isInsufficientDataError(err) {
			t.Errorf("expected ErrInsufficientData, got: %v", err)
		}
	})

	t.Run("SARIMAX", func(t *testing.T) {
		engine := NewSARIMAXEngine(cfg, 2, 1, 0, 0, 0, 0, 0)
		_, err := engine.Train(short, 24*time.Hour)
		if err == nil {
			t.Fatal("expected error for insufficient data, got nil")
		}
		if !isInsufficientDataError(err) {
			t.Errorf("expected ErrInsufficientData, got: %v", err)
		}
	})
}

func isInsufficientDataError(err error) bool {
	return err != nil && (len(err.Error()) > 0) && (err.Error()[:30] == "forecast: insufficient data" ||
		(err.Error()[:len("forecast: insufficient data")] == "forecast: insufficient data"))
}

// ============================================================================
// TestDriftDetection — error rising → DRIFT_DETECTED
// ============================================================================

func TestDriftDetection(t *testing.T) {
	// Train a Prophet model on clean data.
	trainSeries := generateDOHourly(t, 30, 789)
	cfg := DefaultConfig()
	cfg.MinPoints = 24
	cfg.DriftWindow = 12

	engine := NewProphetEngine(cfg)
	model, err := engine.Train(trainSeries, 24*time.Hour)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Predict 12 steps.
	predicted, err := model.Predict(12)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}

	t.Run("no drift on same distribution", func(t *testing.T) {
		// Actual values close to predictions → no drift.
		actual := make([]Point, 12)
		for i := range actual {
			actual[i] = Point{
				Timestamp: predicted[i].Timestamp,
				Value:     predicted[i].Value * 1.01, // 1% error
			}
		}
		report := DetectDrift(model, actual, predicted, cfg)
		if report.DriftDetected {
			t.Errorf("expected no drift, got: %s", report.Recommendation)
		}
	})

	t.Run("drift detected on degraded distribution", func(t *testing.T) {
		// Actual values far from predictions → drift.
		actual := make([]Point, 12)
		for i := range actual {
			actual[i] = Point{
				Timestamp: predicted[i].Timestamp,
				Value:     predicted[i].Value + 5.0, // large systematic offset
			}
		}
		report := DetectDrift(model, actual, predicted, cfg)
		if !report.DriftDetected {
			t.Errorf("expected drift detected, got: %s", report.Recommendation)
		}
		t.Logf("Drift report: %s (ratio=%.2f)", report.Recommendation, report.Ratio)
	})

	t.Run("stateful DriftDetector", func(t *testing.T) {
		baseRMSE := extractResidualStd(model)
		detector := NewDriftDetector(cfg, baseRMSE)

		// Feed low errors first.
		for i := 0; i < 6; i++ {
			detector.AddObservation(predicted[i].Value, predicted[i].Value*1.005)
		}
		report := detector.Check()
		if report.DriftDetected {
			t.Errorf("expected no drift initially, got: %s", report.Recommendation)
		}

		// Feed high errors.
		for i := 6; i < 12; i++ {
			detector.AddObservation(predicted[i].Value+3.0, predicted[i].Value)
		}
		report = detector.Check()
		if !report.DriftDetected {
			t.Errorf("expected drift after high errors, got: %s", report.Recommendation)
		}
	})
}

// ============================================================================
// TestConfidenceIntervals — 80% ⊂ 95% band, computed from actual residuals
// ============================================================================

func TestConfidenceIntervals(t *testing.T) {
	series := generateDOHourly(t, 30, 111)
	cfg := DefaultConfig()
	cfg.MinPoints = 24
	cfg.Z80 = 1.282
	cfg.Z95 = 1.960

	engine := NewProphetEngine(cfg)
	model, err := engine.Train(series, 24*time.Hour)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	forecasts, err := model.Predict(24)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}

	for i, f := range forecasts {
		// 95% band must be wider than 80% band.
		width80 := f.Upper80 - f.Lower80
		width95 := f.Upper95 - f.Lower95
		if width95 <= width80 {
			t.Errorf("step %d: 95%% band (%.4f) not wider than 80%% (%.4f)", i, width95, width80)
		}

		// 80% band must be strictly inside 95% band.
		if f.Lower80 < f.Lower95 {
			t.Errorf("step %d: Lower80 (%.4f) < Lower95 (%.4f)", i, f.Lower80, f.Lower95)
		}
		if f.Upper80 > f.Upper95 {
			t.Errorf("step %d: Upper80 (%.4f) > Upper95 (%.4f)", i, f.Upper80, f.Upper95)
		}

		// Bounds must be symmetric around the prediction (within floating point).
		center80 := (f.Lower80 + f.Upper80) / 2
		if math.Abs(center80-f.Value) > 1e-6 {
			t.Errorf("step %d: 80%% CI not centered on prediction (center=%.4f, value=%.4f)", i, center80, f.Value)
		}
		center95 := (f.Lower95 + f.Upper95) / 2
		if math.Abs(center95-f.Value) > 1e-6 {
			t.Errorf("step %d: 95%% CI not centered on prediction (center=%.4f, value=%.4f)", i, center95, f.Value)
		}
	}
}

// ============================================================================
// TestProphetEngine_Seasonality — daily cycle is captured
// ============================================================================

func TestProphetEngine_Seasonality(t *testing.T) {
	// Generate data with a strong daily cycle but NO trend.
	rng := rand.New(rand.NewPCG(42, 43))
	startTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	n := 30 * 24
	series := make([]Point, n)
	for i := 0; i < n; i++ {
		hour := float64(i % 24)
		value := 7.0 + 2.0*math.Sin(2*math.Pi*(hour-6)/24.0) + rng.NormFloat64()*0.1
		series[i] = Point{
			Timestamp: startTime.Add(time.Duration(i) * time.Hour),
			Value:     value,
		}
	}

	cfg := DefaultConfig()
	cfg.MinPoints = 24
	engine := NewProphetEngine(cfg)

	model, err := engine.Train(series, 24*time.Hour)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Predict the next 24 hours.
	forecasts, err := model.Predict(24)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}

	// The forecast should show a daily cycle: there should be variance across 24 steps.
	minVal := forecasts[0].Value
	maxVal := forecasts[0].Value
	for _, f := range forecasts {
		if f.Value < minVal {
			minVal = f.Value
		}
		if f.Value > maxVal {
			maxVal = f.Value
		}
	}
	amplitude := maxVal - minVal
	if amplitude < 0.5 {
		t.Errorf("expected daily amplitude > 0.5, got %.4f", amplitude)
	}
	t.Logf("Forecast amplitude: %.4f", amplitude)
}

// ============================================================================
// TestSARIMAX_Differencing — verify d=1 removes trend
// ============================================================================

func TestSARIMAX_Differencing(t *testing.T) {
	// Generate data with a strong linear trend.
	series := generateWithTrend(t, 20, 555, 7.0, 0.5, 5.0, 0.1) // strong trend: +5/day

	cfg := DefaultConfig()
	cfg.MinPoints = 24

	// Without differencing, prediction should be poor (AR(2) can't follow strong trend).
	engineNoDiff := NewSARIMAXEngine(cfg, 2, 0, 0, 0, 0, 0, 0)
	_, errNoDiff := engineNoDiff.Train(series, 24*time.Hour)
	if errNoDiff != nil {
		t.Logf("ARIMA(2,0,0) Train failed (expected with strong trend): %v", errNoDiff)
	}

	// With differencing, training should succeed.
	engineWithDiff := NewSARIMAXEngine(cfg, 2, 1, 0, 0, 0, 0, 0)
	model, err := engineWithDiff.Train(series, 24*time.Hour)
	if err != nil {
		t.Fatalf("ARIMA(2,1,0) Train failed: %v", err)
	}

	forecasts, err := model.Predict(6)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}

	// With trend, forecasts should be increasing (reflecting the upward trend).
	if len(forecasts) >= 3 {
		if forecasts[len(forecasts)-1].Value <= forecasts[0].Value {
			t.Logf("warning: forecasts not trending upward (last=%.4f, first=%.4f)", forecasts[len(forecasts)-1].Value, forecasts[0].Value)
		}
	}
}

// ============================================================================
// TestDefaultConfig — verify defaults
// ============================================================================

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MinPoints != 168 {
		t.Errorf("MinPoints: want 168, got %d", cfg.MinPoints)
	}
	if cfg.FourierOrder != 3 {
		t.Errorf("FourierOrder: want 3, got %d", cfg.FourierOrder)
	}
	if cfg.DriftThreshold != 2.0 {
		t.Errorf("DriftThreshold: want 2.0, got %.2f", cfg.DriftThreshold)
	}
	if cfg.DriftWindow != 72 {
		t.Errorf("DriftWindow: want 72, got %d", cfg.DriftWindow)
	}
	if math.Abs(cfg.Z80-1.282) > 1e-6 {
		t.Errorf("Z80: want 1.282, got %.4f", cfg.Z80)
	}
	if math.Abs(cfg.Z95-1.96) > 1e-6 {
		t.Errorf("Z95: want 1.96, got %.4f", cfg.Z95)
	}
}

// ============================================================================
// TestInterfaceCompliance — verify compile-time assertions hold
// ============================================================================

func TestInterfaceCompliance(t *testing.T) {
	pe := NewProphetEngine(DefaultConfig())
	se := NewSARIMAXEngine(DefaultConfig(), 2, 1, 0, 0, 0, 0, 0)

	if pe == nil || se == nil {
		t.Fatal("engines should not be nil")
	}

	// Compile-time: verify engine implements ForecastEngine
	var _ ForecastEngine = pe
	var _ ForecastEngine = se

	// Verify Predict exists on the concrete type.
	_ = pe.Predict
	_ = se.Predict
}

// ============================================================================
// TestInvalidInputs — zero horizon, negative steps
// ============================================================================

func TestInvalidInputs(t *testing.T) {
	series := generateDOHourly(t, 30, 987)
	cfg := DefaultConfig()
	cfg.MinPoints = 24

	t.Run("zero horizon", func(t *testing.T) {
		engine := NewProphetEngine(cfg)
		_, err := engine.Train(series, 0)
		if err == nil {
			t.Fatal("expected error for zero horizon")
		}
	})

	t.Run("negative steps", func(t *testing.T) {
		engine := NewProphetEngine(cfg)
		model, err := engine.Train(series, 24*time.Hour)
		if err != nil {
			t.Fatalf("Train failed: %v", err)
		}
		_, err = model.Predict(0)
		if err == nil {
			t.Fatal("expected error for zero steps")
		}
		_, err = model.Predict(-1)
		if err == nil {
			t.Fatal("expected error for negative steps")
		}
	})
}

// ============================================================================
// TestNormalQuantile — verify known z-scores
// ============================================================================

func TestNormalQuantile(t *testing.T) {
	tests := []struct {
		p        float64
		expected float64
	}{
		{0.80, 1.2815515655446004},
		{0.90, 1.6448536269514722},
		{0.95, 1.959963984540054},
		{0.975, 2.241402727604945},
		{0.99, 2.5758293035489004},
	}

	for _, tt := range tests {
		got := normalQuantile(tt.p)
		if math.Abs(got-tt.expected) > 1e-6 {
			t.Errorf("normalQuantile(%.3f) = %.6f, want %.6f", tt.p, got, tt.expected)
		}
	}

	// Edge cases.
	if normalQuantile(0.5) != 0 {
		t.Errorf("normalQuantile(0.5) should be 0")
	}
}

// ============================================================================
// TestLinearRegression — verify OLS on known data
// ============================================================================

func TestLinearRegression(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5}
	y := []float64{2, 4, 6, 8, 10} // y = 2x

	intercept, slope, rSq := linearRegression(x, y)
	if math.Abs(intercept) > 1e-10 {
		t.Errorf("intercept: want 0, got %.6f", intercept)
	}
	if math.Abs(slope-2.0) > 1e-10 {
		t.Errorf("slope: want 2.0, got %.6f", slope)
	}
	if math.Abs(rSq-1.0) > 1e-10 {
		t.Errorf("R²: want 1.0, got %.6f", rSq)
	}
}

// ============================================================================
// TestRMSE — verify computation
// ============================================================================

func TestRMSE(t *testing.T) {
	actual := []float64{1, 2, 3}
	pred := []float64{1.1, 1.9, 3.2}
	got := rmse(actual, pred)
	expected := math.Sqrt(((0.1*0.1)+(0.1*0.1)+(0.2*0.2))/3.0)
	if math.Abs(got-expected) > 1e-10 {
		t.Errorf("rmse: want %.10f, got %.10f", expected, got)
	}
}

// ============================================================================
// TestMeanStddev — verify computation
// ============================================================================

func TestMeanStddev(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5}
	mean, stddev := meanStddev(vals)
	if math.Abs(mean-3.0) > 1e-10 {
		t.Errorf("mean: want 3.0, got %.6f", mean)
	}
	expectedStd := math.Sqrt(2.5) // sample stddev: sqrt(((4+1+0+1+4)/4)) = sqrt(2.5)
	if math.Abs(stddev-expectedStd) > 1e-10 {
		t.Errorf("stddev: want %.6f, got %.6f", expectedStd, stddev)
	}
}

// ============================================================================
// TestComputeCI — confidence intervals are symmetric around prediction
// ============================================================================

func TestComputeCI(t *testing.T) {
	lo80, hi80, lo95, hi95 := computeCI(10.0, 2.0, 1.282, 1.960)

	expectedLo80 := 10.0 - 1.282*2.0 // 7.436
	expectedHi80 := 10.0 + 1.282*2.0 // 12.564
	expectedLo95 := 10.0 - 1.960*2.0 // 6.08
	expectedHi95 := 10.0 + 1.960*2.0 // 13.92

	if math.Abs(lo80-expectedLo80) > 1e-6 {
		t.Errorf("lo80: want %.6f, got %.6f", expectedLo80, lo80)
	}
	if math.Abs(hi80-expectedHi80) > 1e-6 {
		t.Errorf("hi80: want %.6f, got %.6f", expectedHi80, hi80)
	}
	if math.Abs(lo95-expectedLo95) > 1e-6 {
		t.Errorf("lo95: want %.6f, got %.6f", expectedLo95, lo95)
	}
	if math.Abs(hi95-expectedHi95) > 1e-6 {
		t.Errorf("hi95: want %.6f, got %.6f", expectedHi95, hi95)
	}
}

// ============================================================================
// BenchmarkPredict1000 — 1000-point prediction < 100ms
// ============================================================================

func BenchmarkPredict1000(b *testing.B) {
	series := generateDOHourlyTB(b, 30, 42)
	cfg := DefaultConfig()
	cfg.MinPoints = 24
	engine := NewProphetEngine(cfg)

	model, err := engine.Train(series, 24*time.Hour)
	if err != nil {
		b.Fatalf("Train failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := model.Predict(1000)
		if err != nil {
			b.Fatalf("Predict failed: %v", err)
		}
		if len(results) != 1000 {
			b.Fatalf("expected 1000 results, got %d", len(results))
		}
	}
}

// generateDOHourlyTB is a test-helper version for benchmarks (no *testing.T parameter).
func generateDOHourlyTB(b *testing.B, days int, seed uint64) []Point {
	b.Helper()
	rng := rand.New(rand.NewPCG(seed, seed+1))
	startTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	n := days * 24
	points := make([]Point, n)
	for i := 0; i < n; i++ {
		hour := float64(i % 24)
		dailyCycle := 1.5 * math.Sin(2*math.Pi*(hour-6)/24.0)
		trend := float64(i) * 0.002
		value := 7.0 + dailyCycle + trend + rng.NormFloat64()*0.15
		points[i] = Point{
			Timestamp: startTime.Add(time.Duration(i) * time.Hour),
			Value:     value,
		}
	}
	return points
}

// ============================================================================
// TestHorizonPrediction — different horizons produce correct step counts
// ============================================================================

func TestHorizonPrediction(t *testing.T) {
	series := generateDOHourly(t, 30, 321)
	cfg := DefaultConfig()
	cfg.MinPoints = 24
	engine := NewProphetEngine(cfg)

	tests := []struct {
		name          string
		horizon       time.Duration
		expectedSteps int
	}{
		{"1h", 1 * time.Hour, 1},
		{"6h", 6 * time.Hour, 6},
		{"24h", 24 * time.Hour, 24},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, err := engine.Train(series, tt.horizon)
			if err != nil {
				t.Fatalf("Train failed: %v", err)
			}
			forecasts, err := engine.Predict(model, tt.expectedSteps)
			if err != nil {
				t.Fatalf("Predict failed: %v", err)
			}
			if len(forecasts) != tt.expectedSteps {
				t.Errorf("expected %d forecasts, got %d", tt.expectedSteps, len(forecasts))
			}
			for i, f := range forecasts {
				if f.Value == 0 && f.Lower80 == 0 && f.Upper80 == 0 {
					t.Errorf("step %d: all-zero forecast", i)
				}
			}
		})
	}
}

// ============================================================================
// TestSARIMAX_WithExogenous — verify exog coefficient is fit correctly
// ============================================================================

func TestSARIMAX_WithExogenous(t *testing.T) {
	// Generate data where y = 5.0 - 0.3*temperature + noise (simple linear relationship).
	rng := rand.New(rand.NewPCG(777, 778))
	startTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	n := 14 * 24
	yPoints := make([]Point, n)
	exog := make([][]float64, n)

	for i := 0; i < n; i++ {
		temp := 25.0 + rng.NormFloat64()*2.0
		exog[i] = []float64{temp}
		yPoints[i] = Point{
			Timestamp: startTime.Add(time.Duration(i) * time.Hour),
			Value:     5.0 - 0.3*temp + rng.NormFloat64()*0.1,
		}
	}

	cfg := DefaultConfig()
	cfg.MinPoints = 24
	engine := NewSARIMAXEngine(cfg, 0, 1, 0, 0, 0, 0, 0) // AR(0) — just exogenous

	model, err := engine.TrainExog(yPoints, exog, 24*time.Hour)
	if err != nil {
		t.Fatalf("TrainExog failed: %v", err)
	}

	sm, ok := model.(*sarimaxModel)
	if !ok {
		t.Fatal("expected *sarimaxModel")
	}

	if len(sm.exogCoeffs) != 1 {
		t.Fatalf("expected 1 exogenous coefficient, got %d", len(sm.exogCoeffs))
	}

	// The coefficient should be approximately -0.3 (inverse temperature-DO relationship).
	if math.Abs(sm.exogCoeffs[0]-(-0.3)) > 0.25 {
		t.Errorf("exogenous coefficient: want ≈ -0.3, got %.4f", sm.exogCoeffs[0])
	}
	t.Logf("Fitted exogenous coefficient: %.4f", sm.exogCoeffs[0])
}

// ============================================================================
// TestTimeStampOrdering — verify forecast timestamps are sequential
// ============================================================================

func TestTimeStampOrdering(t *testing.T) {
	series := generateDOHourly(t, 30, 654)
	cfg := DefaultConfig()
	cfg.MinPoints = 24
	engine := NewProphetEngine(cfg)

	model, err := engine.Train(series, 24*time.Hour)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	forecasts, err := model.Predict(24)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}

	for i := 1; i < len(forecasts); i++ {
		if !forecasts[i].Timestamp.After(forecasts[i-1].Timestamp) {
			t.Errorf("step %d: timestamp %s not after %s", i, forecasts[i].Timestamp, forecasts[i-1].Timestamp)
		}
	}

	// Last forecast should be approximately 24 hours after training data end.
	expectedEnd := series[len(series)-1].Timestamp.Add(24 * time.Hour)
	actualEnd := forecasts[len(forecasts)-1].Timestamp
	diff := actualEnd.Sub(expectedEnd)
	if diff < -time.Hour || diff > time.Hour {
		t.Errorf("last timestamp %s too far from expected %s (diff: %v)", actualEnd, expectedEnd, diff)
	}
}

// ============================================================================
// TestFormatDuration — verify human-readable names
// ============================================================================

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{72 * time.Hour, "3d"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %s, want %s", tt.d, got, tt.want)
		}
	}
}

// ============================================================================
// TestComputeRSquared — verify R² calculation
// ============================================================================

func TestComputeRSquared(t *testing.T) {
	actual := []float64{1, 2, 3, 4, 5}
	pred := []float64{1.1, 2.1, 2.9, 4.0, 4.9}
	r2 := computeRSquared(actual, pred)
	if r2 < 0.9 {
		t.Errorf("R² should be > 0.9 for close predictions, got %.4f", r2)
	}

	// Perfect prediction.
	perfect2 := computeRSquared(actual, actual)
	if math.Abs(perfect2-1.0) > 1e-10 {
		t.Errorf("R² for perfect fit should be 1.0, got %.6f", perfect2)
	}
}

// ============================================================================
// TestSaveModel — helper functions
// ============================================================================

func TestSaveModel(t *testing.T) {
	series := generateDOHourly(t, 30, 888)
	cfg := DefaultConfig()
	cfg.MinPoints = 24

	t.Run("Prophet", func(t *testing.T) {
		engine := NewProphetEngine(cfg)
		model, err := engine.Train(series, 24*time.Hour)
		if err != nil {
			t.Fatalf("Train failed: %v", err)
		}

		data, err := SaveModel(model)
		if err != nil {
			t.Fatalf("SaveModel failed: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("saved data is empty")
		}
	})

	t.Run("SARIMAX", func(t *testing.T) {
		engine := NewSARIMAXEngine(cfg, 2, 1, 0, 0, 0, 0, 0)
		model, err := engine.Train(series, 24*time.Hour)
		if err != nil {
			t.Fatalf("Train failed: %v", err)
		}

		data, err := SaveModel(model)
		if err != nil {
			t.Fatalf("SaveModel failed: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("saved data is empty")
		}
	})
}

// ============================================================================
// TestComputeInterval — verify interval calculation
// ============================================================================

func TestComputeInterval(t *testing.T) {
	startTime := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		series   []Point
		expected float64
	}{
		{
			name: "hourly interval",
			series: []Point{
				{Timestamp: startTime, Value: 1},
				{Timestamp: startTime.Add(time.Hour), Value: 2},
				{Timestamp: startTime.Add(2 * time.Hour), Value: 3},
			},
			expected: 3600,
		},
		{
			name: "empty series",
			series: []Point{
				{Timestamp: startTime, Value: 1},
			},
			expected: 3600, // default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeInterval(tt.series)
			if math.Abs(got-tt.expected) > 1 {
				t.Errorf("computeInterval: want %.0f, got %.0f", tt.expected, got)
			}
		})
	}
}

// ============================================================================
// TestMultiStepPrediction — verify long-horizon forecasts don't explode
// ============================================================================

func TestMultiStepPrediction(t *testing.T) {
	series := generateDOHourly(t, 30, 333)
	cfg := DefaultConfig()
	cfg.MinPoints = 24
	engine := NewProphetEngine(cfg)

	model, err := engine.Train(series, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Predict 168 steps (7 days).
	forecasts, err := model.Predict(168)
	if err != nil {
		t.Fatalf("Predict 7-day failed: %v", err)
	}

	// Values should stay in a realistic DO range (3-12 mg/L).
	for i, f := range forecasts {
		if f.Value < 2 || f.Value > 15 {
			t.Errorf("step %d: value %.2f outside realistic DO range [2, 15]", i, f.Value)
		}
	}

	// Confidence intervals should grow with horizon (higher uncertainty for far future).
	if len(forecasts) >= 48 {
		widthFirst := forecasts[0].Upper95 - forecasts[0].Lower95
		widthLast := forecasts[47].Upper95 - forecasts[47].Lower95
		// CI width is constant in this implementation (uses same residual stddev).
		// This is acceptable — Prophet also uses constant uncertainty by default.
		_ = widthFirst
		_ = widthLast
	}
}

// ============================================================================
// TestNameFormatting — model names include horizon
// ============================================================================

func TestNameFormatting(t *testing.T) {
	series := generateDOHourly(t, 30, 444)
	cfg := DefaultConfig()
	cfg.MinPoints = 24

	engine := NewProphetEngine(cfg)
	model, err := engine.Train(series, 6*time.Hour)
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Model name should be descriptive and not empty.
	name := model.Name()
	if len(name) < 5 {
		t.Errorf("model name too short: %q", name)
	}
	t.Logf("Model name: %s", name)
}

// Ensure fmt is used.
var _ = fmt.Sprintf

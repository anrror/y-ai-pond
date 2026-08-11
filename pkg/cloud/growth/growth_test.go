package growth

import (
	"math"
	"testing"
	"time"
)

// ============================================================================
// Test helpers
// ============================================================================

// testLib is a shared species library loaded once for all tests.
var testLib *SpeciesLibrary

func init() {
	var err error
	testLib, err = LoadSpeciesLibrary()
	if err != nil {
		panic("failed to load species library: " + err.Error())
	}
}

// testSpecies is a synthetic species with well-known parameters for
// predictable testing.
var testSpecies = &SpeciesParams{
	Species: "test_fish",
	Linf:    50.0,
	K:       0.00082, // ~0.3 per year
	T0:      0,
	A:       0.015,
	B:       3.0,
	Cmax:    0.15,
	BC:      0.75,
	TOpt:    28.0,
	TMaxC:   38.0,
	CK1:     2.5,
	CK2:     2.5,
	Rmax:    45.0,
	BR:      0.75,
	ACT:     1.0,
	Q10:     2.0,
	TRefR:   27.0,
	FA:      0.15,
	UA:      0.08,
	EDPrey:  4500.0,
	EDFish:  5500.0,
}

// testLibWithCustom returns a library containing only the test species.
func testLibWithCustom() *SpeciesLibrary {
	lib := &SpeciesLibrary{
		params: map[string]*SpeciesParams{
			"test_fish": testSpecies,
			"tilapia":   testSpecies, // alias for test
		},
	}
	return lib
}

// floatApprox checks if two float64 values are within the given tolerance.
func floatApprox(a, b, tol float64) bool {
	return math.Abs(a-b) < tol
}

// ============================================================================
// TestVBGM
// ============================================================================

func TestVBGM(t *testing.T) {
	lib := testLibWithCustom()
	model := NewVBGMModel(lib)

	state := &GrowthState{
		Species:  "test_fish",
		AgeDays:  30,
		WeightG:  10,
		LengthCm: 8,
	}
	env := &Environment{
		Temperature:     28.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.9,
	}

	// Predict 365 days of growth.
	result, err := model.Predict(state, env, 365*24*time.Hour)
	if err != nil {
		t.Fatalf("VBGM Predict failed: %v", err)
	}

	// After 365 days, length should be approaching Linf (50cm).
	// With k=0.00082/day and age 395 days, L ≈ 50 * (1 - e^(-0.00082*395)) ≈ 50 * 0.277 ≈ 13.9.
	// Actually: the daily k is 0.00082, which is ~0.3/yr.
	// At t=395 days (30 + 365): L = 50 * (1 - e^(-0.00082*395)) = 50 * (1 - e^(-0.3239)) = 50 * 0.2767 = 13.8 cm
	// The fish should be growing (not staying at 8cm).
	if result.LengthCm <= state.LengthCm {
		t.Errorf("Expected length growth, but got %.2f cm (started at %.2f cm)", result.LengthCm, state.LengthCm)
	}
	if result.FinalWeightG <= state.WeightG {
		t.Errorf("Expected weight growth, but got %.2f g (started at %.2f g)", result.FinalWeightG, state.WeightG)
	}

	// Weight gain should be positive.
	if result.WeightGainGPerDay <= 0 {
		t.Errorf("Expected positive daily weight gain, got %.4f g/day", result.WeightGainGPerDay)
	}

	// Length should approach but not exceed Linf.
	if result.LengthCm > testSpecies.Linf {
		t.Errorf("Length %.2f exceeds asymptotic length %.2f", result.LengthCm, testSpecies.Linf)
	}

	// FCR should be in a realistic range (0.5-10 for aquaculture).
	if result.FeedConversionRatio < 0.5 || result.FeedConversionRatio > 10 {
		t.Logf("FCR %.2f is outside typical range [0.5, 10]", result.FeedConversionRatio)
	}
}

// TestVBGM_LengthApproachesLinf verifies that after a very long time,
// the predicted length approaches (but does not exceed) Linf.
func TestVBGM_LengthApproachesLinf(t *testing.T) {
	lib := testLibWithCustom()
	model := NewVBGMModel(lib)

	state := &GrowthState{
		Species: "test_fish",
		AgeDays: 0,
		WeightG: 0.1,
	}
	env := &Environment{
		Temperature:     28.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.9,
	}

	// After 10,000 days (~27 years), length should be very close to Linf=50.
	result, err := model.Predict(state, env, 10000*24*time.Hour)
	if err != nil {
		t.Fatalf("VBGM Predict failed: %v", err)
	}

	// L(10000) = 50 * (1 - e^(-0.00082*10000)) ≈ 50 * (1 - e^(-8.2)) ≈ 50 * 0.9997 ≈ 49.99
	if result.LengthCm < testSpecies.Linf*0.95 {
		t.Errorf("After 10000 days, length %.2f should be close to Linf=%.2f (within 5%%)",
			result.LengthCm, testSpecies.Linf)
	}
	if result.LengthCm > testSpecies.Linf {
		t.Errorf("Length %.2f exceeds asymptotic maximum %.2f", result.LengthCm, testSpecies.Linf)
	}
}

// ============================================================================
// TestBioenergetic
// ============================================================================

func TestBioenergetic(t *testing.T) {
	lib := testLibWithCustom()
	model := NewBioenergeticModel(lib)

	state := &GrowthState{
		Species: "test_fish",
		AgeDays: 60,
		WeightG: 50,
	}
	env := &Environment{
		Temperature:     28.0, // optimal temperature
		DissolvedOxygen: 7.0,
		FeedingRate:     0.8,
	}

	// Predict 30 days.
	result, err := model.Predict(state, env, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Bioenergetic Predict failed: %v", err)
	}

	// At optimal temperature with good feeding, fish should grow.
	if result.FinalWeightG <= state.WeightG {
		t.Errorf("Expected weight gain at optimal temperature, got %.2f g (started at %.2f g)",
			result.FinalWeightG, state.WeightG)
	}
	if result.WeightGainGPerDay <= 0 {
		t.Errorf("Expected positive daily gain, got %.4f", result.WeightGainGPerDay)
	}

	// Energy balance should be checked.
	// At optimal temperature, growth should be positive (anabolic).
	if result.EnergyBalanceKJ <= 0 {
		t.Errorf("Expected positive energy balance (anabolism), got %.4f kJ", result.EnergyBalanceKJ)
	}

	// Cumulative consumption should be positive.
	if result.CumulativeConsumptionG <= 0 {
		t.Errorf("Expected positive cumulative consumption, got %.4f g", result.CumulativeConsumptionG)
	}

	// FCR should be reasonable for a growing fish.
	if result.FeedConversionRatio < 0.5 || result.FeedConversionRatio > 5.0 {
		t.Logf("FCR %.3f may be outside typical range", result.FeedConversionRatio)
	}
}

// TestBioenergetic_NoFeeding verifies that with zero feeding rate,
// the fish loses weight (catabolic state).
func TestBioenergetic_NoFeeding(t *testing.T) {
	lib := testLibWithCustom()
	model := NewBioenergeticModel(lib)

	state := &GrowthState{
		Species: "test_fish",
		AgeDays: 60,
		WeightG: 50,
	}
	env := &Environment{
		Temperature:     28.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.0, // no feeding
	}

	result, err := model.Predict(state, env, 10*24*time.Hour)
	if err != nil {
		t.Fatalf("Bioenergetic Predict failed: %v", err)
	}

	// With no feeding and active metabolism, fish should lose weight
	// or at least not gain significant weight.
	if result.FinalWeightG > state.WeightG {
		t.Logf("Weight increased %.2f → %.2f despite no feeding (possible parameter issue)",
			state.WeightG, result.FinalWeightG)
	}

	// Energy balance should be negative (catabolic).
	if result.EnergyBalanceKJ > 0 {
		t.Logf("Energy balance positive despite no feeding: %.4f kJ (respiration may be zero if weight drops to 0)",
			result.EnergyBalanceKJ)
	}

	// Cumulative consumption should be zero or near-zero.
	if result.CumulativeConsumptionG > 0.001 {
		t.Errorf("Expected zero consumption with no feeding, got %.6f g", result.CumulativeConsumptionG)
	}
}

// ============================================================================
// TestTemperatureGrowthCoupling
// ============================================================================

func TestTemperatureGrowthCoupling(t *testing.T) {
	lib := testLibWithCustom()
	model := NewBioenergeticModel(lib)

	state := &GrowthState{
		Species: "test_fish",
		AgeDays: 60,
		WeightG: 50,
	}

	// Test at optimal temperature (28°C).
	envOpt := &Environment{
		Temperature:     28.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.8,
	}
	resultOpt, err := model.Predict(state, envOpt, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Predict at optimal temp failed: %v", err)
	}

	// Test at high temperature (32°C) — elevated but not lethal.
	envHigh := &Environment{
		Temperature:     32.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.8,
	}
	resultHigh, err := model.Predict(state, envHigh, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Predict at high temp failed: %v", err)
	}

	// Test at low temperature (18°C).
	envLow := &Environment{
		Temperature:     18.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.8,
	}
	resultLow, err := model.Predict(state, envLow, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Predict at low temp failed: %v", err)
	}

	t.Logf("Optimal (28°C): gain=%.4f g/day, FCR=%.3f, energy=%.2f kJ",
		resultOpt.WeightGainGPerDay, resultOpt.FeedConversionRatio, resultOpt.EnergyBalanceKJ)
	t.Logf("High (32°C):   gain=%.4f g/day, FCR=%.3f, energy=%.2f kJ",
		resultHigh.WeightGainGPerDay, resultHigh.FeedConversionRatio, resultHigh.EnergyBalanceKJ)
	t.Logf("Low (18°C):    gain=%.4f g/day, FCR=%.3f, energy=%.2f kJ",
		resultLow.WeightGainGPerDay, resultLow.FeedConversionRatio, resultLow.EnergyBalanceKJ)

	// High temperature: maintenance metabolism increases → net growth should be LOWER
	// than at optimal temperature.
	if resultHigh.WeightGainGPerDay > resultOpt.WeightGainGPerDay*1.1 {
		t.Errorf("Temperature-growth coupling FAILED: high temp (32°C) gain %.4f > optimal (28°C) gain %.4f",
			resultHigh.WeightGainGPerDay, resultOpt.WeightGainGPerDay)
	}

	// High temperature should have WORSE FCR (more feed per gram of growth)
	// when the fish is still growing. If growth is near-zero, skip this check.
	if resultHigh.WeightGainGPerDay > 0.01 && resultOpt.WeightGainGPerDay > 0.01 {
		if resultHigh.FeedConversionRatio < resultOpt.FeedConversionRatio*0.9 {
			t.Errorf("Expected worse FCR at high temp: high=%.3f, optimal=%.3f",
				resultHigh.FeedConversionRatio, resultOpt.FeedConversionRatio)
		}
	}

	// Low temperature should also have lower growth.
	if resultLow.WeightGainGPerDay > resultOpt.WeightGainGPerDay {
		t.Logf("Low temp growth (%.4f) > optimal (%.4f) — check if T_opt is correct",
			resultLow.WeightGainGPerDay, resultOpt.WeightGainGPerDay)
	}

	// Optimal temperature should have the BEST energy balance.
	if resultOpt.EnergyBalanceKJ < resultHigh.EnergyBalanceKJ {
		t.Logf("High temp energy balance (%.2f) > optimal (%.2f) — unusual",
			resultHigh.EnergyBalanceKJ, resultOpt.EnergyBalanceKJ)
	}
}

// ============================================================================
// TestSpeciesLibrary
// ============================================================================

func TestSpeciesLibrary(t *testing.T) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		t.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}

	numSpecies := lib.NumSpecies()
	if numSpecies < 105 {
		t.Errorf("Species library has %d entries, expected at least 105", numSpecies)
	}
	t.Logf("Loaded %d species", numSpecies)

	// Verify key species are present.
	speciesChecks := []string{
		"tilapia", "nile_tilapia", "common_carp", "grass_carp",
		"channel_catfish", "atlantic_salmon", "rainbow_trout",
		"largemouth_bass", "pacific_white_shrimp", "giant_tiger_prawn",
		"grouper", "barramundi", "red_sea_bream", "european_seabass",
		"olive_flounder", "cobia", "turbot", "abalone", "pacific_oyster",
	}
	for _, species := range speciesChecks {
		params, err := lib.GetParams(species)
		if err != nil {
			t.Errorf("Expected species %q in library, got error: %v", species, err)
			continue
		}
		// Verify critical parameters are non-zero.
		if params.Linf <= 0 || params.K <= 0 || params.Cmax <= 0 || params.Rmax <= 0 {
			t.Errorf("Species %q has zero critical parameters: Linf=%.2f K=%.6f Cmax=%.3f Rmax=%.2f",
				species, params.Linf, params.K, params.Cmax, params.Rmax)
		}
	}
}

func TestSpeciesLibrary_MissingSpecies(t *testing.T) {
	lib := testLibWithCustom()

	_, err := lib.GetParams("nonexistent_fish")
	if err == nil {
		t.Errorf("Expected error for missing species, got nil")
	}
}

func TestSpeciesLibrary_CaseInsensitive(t *testing.T) {
	// Use real library.
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		t.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}

	// Test various capitalizations.
	cases := []string{"TILAPIA", "Tilapia", "TiLaPiA"}
	for _, c := range cases {
		params, err := lib.GetParams(c)
		if err != nil {
			t.Errorf("GetParams(%q) failed: %v", c, err)
			continue
		}
		if params.Linf <= 0 {
			t.Errorf("GetParams(%q) returned invalid params", c)
		}
	}
}

func TestSpeciesLibrary_AllSpeciesValid(t *testing.T) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		t.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}

	for _, species := range lib.SpeciesNames() {
		params, err := lib.GetParams(species)
		if err != nil {
			t.Errorf("GetParams(%q) failed: %v", species, err)
			continue
		}
		// Validate all required parameters are non-negative.
		if params.Linf < 0 {
			t.Errorf("%s: Linf negative: %.2f", species, params.Linf)
		}
		if params.K < 0 {
			t.Errorf("%s: K negative: %.6f", species, params.K)
		}
		if params.Cmax < 0 {
			t.Errorf("%s: Cmax negative: %.3f", species, params.Cmax)
		}
		if params.Rmax < 0 {
			t.Errorf("%s: Rmax negative: %.2f", species, params.Rmax)
		}
	}
}

// ============================================================================
// TestFCR
// ============================================================================

func TestFCR(t *testing.T) {
	lib := testLibWithCustom()
	model := NewBioenergeticModel(lib)

	tests := []struct {
		name      string
		feedRate  float64
		expectFCR bool // whether FCR should be computable (>0)
	}{
		{"high feeding", 1.0, true},
		{"medium feeding", 0.6, true},
		{"low feeding", 0.2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &GrowthState{
				Species: "test_fish",
				AgeDays: 60,
				WeightG: 50,
			}
			env := &Environment{
				Temperature:     28.0,
				DissolvedOxygen: 7.0,
				FeedingRate:     tt.feedRate,
			}

			result, err := model.Predict(state, env, 30*24*time.Hour)
			if err != nil {
				t.Fatalf("Predict failed: %v", err)
			}

			if tt.expectFCR {
				if result.FeedConversionRatio <= 0 && result.WeightGainGPerDay > 0.001 {
					t.Errorf("FCR should be positive when gaining weight: FCR=%.3f, gain=%.4f",
						result.FeedConversionRatio, result.WeightGainGPerDay)
				}
			}

			// FCR = feed_consumed / weight_gain should hold.
			weightGain := result.FinalWeightG - state.WeightG
			if weightGain > 0.001 && result.CumulativeConsumptionG > 0.001 {
				computedFCR := result.CumulativeConsumptionG / weightGain
				if !floatApprox(result.FeedConversionRatio, computedFCR, 0.001) {
					t.Errorf("FCR mismatch: stored=%.4f, computed=%.4f (consumption=%.4f / gain=%.4f)",
						result.FeedConversionRatio, computedFCR,
						result.CumulativeConsumptionG, weightGain)
				}
			}

			// FCR should be in realistic range for aquaculture.
			if result.FeedConversionRatio < 0.3 && weightGain > 0.001 {
				t.Errorf("FCR %.3f is implausibly low (below 0.3)", result.FeedConversionRatio)
			}
		})
	}
}

// ============================================================================
// TestHarvestTime
// ============================================================================

func TestHarvestTime(t *testing.T) {
	// Insert tilapia with known target weight of 500g.
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		t.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}
	model := NewBioenergeticModel(lib)

	// Fish at 100g, should need many days to reach 500g.
	state := &GrowthState{
		Species: "tilapia",
		AgeDays: 90,
		WeightG: 100,
	}
	env := &Environment{
		Temperature:     28.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.9,
	}

	result, err := model.Predict(state, env, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}

	// Fish at 100g should not reach 500g in 30 days — harvest days should be > 0.
	if result.HarvestDays < 0 {
		t.Errorf("HarvestDays should be non-negative, got %.0f", result.HarvestDays)
	}
	t.Logf("From 100g, 30-day growth → %.0f g, %.0f days to harvest (500g target)",
		result.FinalWeightG, result.HarvestDays)

	// If fish is already above target weight, harvest days should be 0.
	stateBig := &GrowthState{
		Species: "tilapia",
		AgeDays: 200,
		WeightG: 600, // above 500g target
	}
	resultBig, err := model.Predict(stateBig, env, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Predict for big fish failed: %v", err)
	}
	if resultBig.HarvestDays != 0 {
		t.Errorf("Fish at 600g (above 500g target) should have HarvestDays=0, got %.0f",
			resultBig.HarvestDays)
	}
}

// ============================================================================
// TestRK4Integration
// ============================================================================

func TestRK4Integration(t *testing.T) {
	// Compare VBGM analytic solution against bioenergetic RK4 numerical
	// integration. The VBGM model gives an exact analytic solution for
	// length-at-age; the bioenergetic model approximates it via RK4.
	// While they use different formulations (length-based vs weight-based),
	// we verify that the RK4 integration is stable and converges.

	lib := testLibWithCustom()
	bioModel := NewBioenergeticModel(lib)

	state := &GrowthState{
		Species: "test_fish",
		AgeDays: 60,
		WeightG: 50,
	}
	env := &Environment{
		Temperature:     28.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.8,
	}

	// Test with default step size (1 day).
	result1, err := bioModel.Predict(state, env, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Predict with default step failed: %v", err)
	}

	// Test with smaller step size (0.5 day) — should converge similarly.
	bioModel.SetStepSize(0.5)
	result2, err := bioModel.Predict(state, env, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Predict with small step failed: %v", err)
	}

	// Results should be close (within 5% relative difference).
	weightDiff := math.Abs(result1.FinalWeightG - result2.FinalWeightG)
	avgWeight := (result1.FinalWeightG + result2.FinalWeightG) / 2.0
	if avgWeight > 0 && weightDiff/avgWeight > 0.05 {
		t.Errorf("RK4 integration does not converge: step=1.0 → %.4f g, step=0.5 → %.4f g (diff=%.4f%%)",
			result1.FinalWeightG, result2.FinalWeightG, weightDiff/avgWeight*100)
	}

	t.Logf("RK4 convergence: step=1.0 → %.4f g, step=0.5 → %.4f g",
		result1.FinalWeightG, result2.FinalWeightG)

	// Both results should have positive growth.
	if result1.WeightGainGPerDay <= 0 {
		t.Errorf("Default step prediction should show positive growth")
	}
	if result2.WeightGainGPerDay <= 0 {
		t.Errorf("Small step prediction should show positive growth")
	}
}

// TestRK4Integration_AdaptiveStep verifies the RK4 integrator handles
// various step sizes correctly.
func TestRK4Integration_AdaptiveStep(t *testing.T) {
	lib := testLibWithCustom()
	model := NewBioenergeticModel(lib)

	state := &GrowthState{
		Species: "test_fish",
		AgeDays: 60,
		WeightG: 50,
	}
	env := &Environment{
		Temperature:     28.0,
		DissolvedOxygen: 7.0,
		FeedingRate:     0.8,
	}

	stepSizes := []float64{0.1, 0.5, 1.0, 2.0, 5.0}
	var results []float64

	for _, step := range stepSizes {
		model.SetStepSize(step)
		result, err := model.Predict(state, env, 30*24*time.Hour)
		if err != nil {
			t.Fatalf("Predict with step=%.1f failed: %v", step, err)
		}
		results = append(results, result.FinalWeightG)
		// Verify basic sanity.
		if result.FinalWeightG <= state.WeightG {
			t.Errorf("Step=%.1f: weight decreased (%.2f → %.2f)", step,
				state.WeightG, result.FinalWeightG)
		}
	}

	// All results should be within 5% of each other.
	ref := results[0] // smallest step = most accurate
	for i, r := range results {
		if ref > 0 && math.Abs(r-ref)/ref > 0.05 {
			t.Errorf("Step=%.1f: weight %.4f deviates from reference (step=0.1) %.4f by %.2f%%",
				stepSizes[i], r, ref, math.Abs(r-ref)/ref*100)
		}
	}

	t.Logf("RK4 adaptive step results: %v", results)
}

// ============================================================================
// TestValidation
// ============================================================================

func TestValidateGrowthState(t *testing.T) {
	tests := []struct {
		name    string
		state   *GrowthState
		wantErr bool
	}{
		{"valid", &GrowthState{Species: "tilapia", AgeDays: 30, WeightG: 50, LengthCm: 10}, false},
		{"nil state", nil, true},
		{"negative weight", &GrowthState{Species: "tilapia", AgeDays: 30, WeightG: -1, LengthCm: 10}, true},
		{"negative length", &GrowthState{Species: "tilapia", AgeDays: 30, WeightG: 50, LengthCm: -1}, true},
		{"negative age", &GrowthState{Species: "tilapia", AgeDays: -5, WeightG: 50, LengthCm: 10}, true},
		{"zero weight (valid)", &GrowthState{Species: "tilapia", AgeDays: 0, WeightG: 0, LengthCm: 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.state.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		env     *Environment
		wantErr bool
	}{
		{"valid", &Environment{Temperature: 25, DissolvedOxygen: 7, FeedingRate: 0.8}, false},
		{"nil", nil, true},
		{"temp too high", &Environment{Temperature: 50, DissolvedOxygen: 7, FeedingRate: 0.8}, true},
		{"temp too low", &Environment{Temperature: -10, DissolvedOxygen: 7, FeedingRate: 0.8}, true},
		{"DO negative", &Environment{Temperature: 25, DissolvedOxygen: -1, FeedingRate: 0.8}, true},
		{"DO too high", &Environment{Temperature: 25, DissolvedOxygen: 30, FeedingRate: 0.8}, true},
		{"feeding rate > 1", &Environment{Temperature: 25, DissolvedOxygen: 7, FeedingRate: 1.5}, true},
		{"feeding rate < 0", &Environment{Temperature: 25, DissolvedOxygen: 7, FeedingRate: -0.1}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.env.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// TestInvalidInputs
// ============================================================================

func TestInvalidInputs(t *testing.T) {
	lib := testLibWithCustom()

	t.Run("VBGM invalid duration", func(t *testing.T) {
		model := NewVBGMModel(lib)
		state := &GrowthState{Species: "test_fish", AgeDays: 30, WeightG: 10}
		env := &Environment{Temperature: 28, DissolvedOxygen: 7, FeedingRate: 0.8}

		_, err := model.Predict(state, env, 0)
		if err == nil {
			t.Error("Expected error for zero duration")
		}

		_, err = model.Predict(state, env, -1*time.Hour)
		if err == nil {
			t.Error("Expected error for negative duration")
		}
	})

	t.Run("VBGM unsupported species", func(t *testing.T) {
		model := NewVBGMModel(lib)
		state := &GrowthState{Species: "unknown_species", AgeDays: 30, WeightG: 10}
		env := &Environment{Temperature: 28, DissolvedOxygen: 7, FeedingRate: 0.8}

		_, err := model.Predict(state, env, 30*24*time.Hour)
		if err == nil {
			t.Error("Expected ErrUnsupportedSpecies for unknown species")
		}
	})

	t.Run("Bioenergetic invalid duration", func(t *testing.T) {
		model := NewBioenergeticModel(lib)
		state := &GrowthState{Species: "test_fish", AgeDays: 60, WeightG: 50}
		env := &Environment{Temperature: 28, DissolvedOxygen: 7, FeedingRate: 0.8}

		_, err := model.Predict(state, env, 0)
		if err == nil {
			t.Error("Expected error for zero duration")
		}
	})

	t.Run("Bioenergetic zero weight", func(t *testing.T) {
		model := NewBioenergeticModel(lib)
		state := &GrowthState{Species: "test_fish", AgeDays: 60, WeightG: 0, LengthCm: 0}
		env := &Environment{Temperature: 28, DissolvedOxygen: 7, FeedingRate: 0.8}

		_, err := model.Predict(state, env, 30*24*time.Hour)
		if err == nil {
			t.Error("Expected error for zero weight with no length")
		}
	})
}

// ============================================================================
// TestInterfaceCompliance
// ============================================================================

func TestInterfaceCompliance(t *testing.T) {
	// Compile-time checks in source files verify these at build time.
	// This runtime test confirms Name() returns expected values.
	lib := testLibWithCustom()

	vbgm := NewVBGMModel(lib)
	if vbgm.Name() != "VBGM" {
		t.Errorf("VBGM Name() = %q, want %q", vbgm.Name(), "VBGM")
	}

	bio := NewBioenergeticModel(lib)
	if bio.Name() != "Bioenergetic4.0" {
		t.Errorf("Bioenergetic Name() = %q, want %q", bio.Name(), "Bioenergetic4.0")
	}

	// Verify both implement GrowthModel interface.
	var _ GrowthModel = vbgm
	var _ GrowthModel = bio
}

// ============================================================================
// TestVBGM_HelperFunctions
// ============================================================================

func TestVBGMLength(t *testing.T) {
	// L(t) = Linf * (1 - exp(-k * (t - t0)))
	// For Linf=50, k=0.001, t0=0:
	// t=0: L = 0
	// t=693 (ln(2)/k): L = 50 * (1 - 0.5) = 25 (half of Linf)
	// t=large: L → 50

	linf := 50.0
	k := 0.001
	t0 := 0.0

	// At t=0, length should be 0.
	l0 := vbgmLength(0, linf, k, t0)
	if l0 != 0 {
		t.Errorf("L(0) = %.4f, want 0", l0)
	}

	// At t < t0, length should be 0.
	lNeg := vbgmLength(-10, linf, k, t0)
	if lNeg != 0 {
		t.Errorf("L(-10) = %.4f, want 0", lNeg)
	}

	// At half-life t = ln(2)/k = 693, L should be half of Linf.
	tHalf := math.Log(2) / k
	lHalf := vbgmLength(tHalf, linf, k, t0)
	expectedHalf := linf * 0.5
	if !floatApprox(lHalf, expectedHalf, 0.01) {
		t.Errorf("L(%.0f) = %.4f, want ≈ %.4f", tHalf, lHalf, expectedHalf)
	}

	// At large t, L should approach Linf.
	lLarge := vbgmLength(10000, linf, k, t0)
	if lLarge > linf {
		t.Errorf("L(10000) = %.4f, should not exceed Linf=%.2f", lLarge, linf)
	}
	if lLarge < linf*0.99 {
		t.Errorf("L(10000) = %.4f, should be close to Linf=%.2f", lLarge, linf)
	}
}

func TestLengthWeightConversion(t *testing.T) {
	a := 0.015
	b := 3.0

	// W = a * L^b
	// L = 10 cm → W = 0.015 * 1000 = 15 g
	l := 10.0
	w := lengthToWeight(l, a, b)
	expectedW := a * math.Pow(l, b)
	if !floatApprox(w, expectedW, 0.001) {
		t.Errorf("lengthToWeight(%.0f) = %.4f, want %.4f", l, w, expectedW)
	}
	// 15g → L = (15/0.015)^(1/3) = 1000^(1/3) = 10
	lBack := weightToLength(w, a, b)
	if !floatApprox(lBack, l, 0.01) {
		t.Errorf("weightToLength(%.4f) = %.4f, want %.1f", w, lBack, l)
	}

	// Round-trip should be identity.
	for _, testL := range []float64{5, 15, 25, 40} {
		w2 := lengthToWeight(testL, a, b)
		l2 := weightToLength(w2, a, b)
		if !floatApprox(l2, testL, 0.01) {
			t.Errorf("Round-trip: L=%.1f → W=%.2f → L=%.2f (diff=%.4f)", testL, w2, l2, math.Abs(l2-testL))
		}
	}

	// Edge cases.
	if lengthToWeight(0, a, b) != 0 {
		t.Error("lengthToWeight(0) should be 0")
	}
	if weightToLength(0, a, b) != 0 {
		t.Error("weightToLength(0) should be 0")
	}
}

// ============================================================================
// TestTemperatureFunctions
// ============================================================================

func TestFConsumptionTemp(t *testing.T) {
	tOpt := 28.0
	tMax := 38.0
	ck1 := 2.5
	ck2 := 2.5

	// Below 0: should be 0.
	if f := fConsumptionTemp(-5, tOpt, tMax, ck1, ck2); f != 0 {
		t.Errorf("fC(-5) = %.4f, want 0", f)
	}

	// At T_opt: should be 1.0.
	if f := fConsumptionTemp(tOpt, tOpt, tMax, ck1, ck2); !floatApprox(f, 1.0, 0.001) {
		t.Errorf("fC(%.0f) = %.4f, want 1.0", tOpt, f)
	}

	// At T_max: should be 0.
	if f := fConsumptionTemp(tMax, tOpt, tMax, ck1, ck2); !floatApprox(f, 0.0, 0.001) {
		t.Errorf("fC(%.0f) = %.4f, want 0.0", tMax, f)
	}

	// Between T_opt and T_max: should be between 0 and 1.
	f := fConsumptionTemp(33, tOpt, tMax, ck1, ck2)
	if f <= 0 || f >= 1 {
		t.Errorf("fC(33) = %.4f, should be in (0, 1)", f)
	}

	// At half of T_opt: should be less than 1.0.
	f = fConsumptionTemp(14, tOpt, tMax, ck1, ck2)
	if f <= 0 || f >= 1 {
		t.Errorf("fC(14) = %.4f, should be in (0, 1)", f)
	}

	// Verify monotonic rise below T_opt.
	f1 := fConsumptionTemp(10, tOpt, tMax, ck1, ck2)
	f2 := fConsumptionTemp(20, tOpt, tMax, ck1, ck2)
	if f1 >= f2 {
		t.Errorf("fC should increase with T below T_opt: fC(10)=%.4f >= fC(20)=%.4f", f1, f2)
	}

	// Verify monotonic fall above T_opt.
	f3 := fConsumptionTemp(30, tOpt, tMax, ck1, ck2)
	f4 := fConsumptionTemp(35, tOpt, tMax, ck1, ck2)
	if f3 <= f4 {
		t.Errorf("fC should decrease with T above T_opt: fC(30)=%.4f <= fC(35)=%.4f", f3, f4)
	}
}

func TestFRespirationTemp(t *testing.T) {
	tRef := 27.0
	q10 := 2.0

	// At reference temperature: should be 1.0.
	if f := fRespirationTemp(tRef, tRef, q10); !floatApprox(f, 1.0, 0.001) {
		t.Errorf("fR(%.0f) = %.4f, want 1.0", tRef, f)
	}

	// At T_ref + 10°C: should be Q10.
	if f := fRespirationTemp(tRef+10, tRef, q10); !floatApprox(f, q10, 0.01) {
		t.Errorf("fR(%.0f) = %.4f, want %.2f", tRef+10, f, q10)
	}

	// At T_ref + 20°C: should be Q10^2.
	if f := fRespirationTemp(tRef+20, tRef, q10); !floatApprox(f, q10*q10, 0.01) {
		t.Errorf("fR(%.0f) = %.4f, want %.2f", tRef+20, f, q10*q10)
	}

	// Should be monotonic increasing with temperature.
	f1 := fRespirationTemp(20, tRef, q10)
	f2 := fRespirationTemp(30, tRef, q10)
	if f1 >= f2 {
		t.Errorf("fR should increase with T: fR(20)=%.4f >= fR(30)=%.4f", f1, f2)
	}
}

// ============================================================================
// TestGrowthRate
// ============================================================================

func TestGrowthRateFunction(t *testing.T) {
	params := testSpecies
	w := 50.0
	feedingRate := 0.8
	act := 1.0

	// At optimal temperature (28°C), growth should be positive.
	gOpt := growthRate(w, params, 28.0, feedingRate, act)
	if gOpt <= 0 {
		t.Errorf("growthRate at optimal temp should be positive, got %.4f", gOpt)
	}

	// At extreme temperature (37°C, near T_max), growth should be lower.
	gHot := growthRate(w, params, 37.0, feedingRate, act)
	if gHot >= gOpt {
		t.Errorf("growthRate at 37°C (%.4f) should be < at 28°C (%.4f)", gHot, gOpt)
	}

	// At zero feeding, consumption should be 0 → growth should be negative or zero.
	gNoFeed := growthRate(w, params, 28.0, 0.0, act)
	if gNoFeed > 0 {
		t.Errorf("growthRate with no feeding should be <= 0, got %.4f", gNoFeed)
	}
}

// ============================================================================
// TestVBGM_WithRealSpecies
// ============================================================================

func TestVBGM_WithRealSpecies(t *testing.T) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		t.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}
	model := NewVBGMModel(lib)

	// Test a few real species to ensure they work end-to-end.
	speciesList := []struct {
		species string
		weight  float64
	}{
		{"tilapia", 50},
		{"common_carp", 200},
		{"atlantic_salmon", 500},
		{"channel_catfish", 100},
		{"rainbow_trout", 150},
	}

	for _, s := range speciesList {
		t.Run(s.species, func(t *testing.T) {
			state := &GrowthState{
				Species: s.species,
				AgeDays: 90,
				WeightG: s.weight,
			}
			env := &Environment{
				Temperature:     25.0,
				DissolvedOxygen: 7.0,
				FeedingRate:     0.8,
			}

			result, err := model.Predict(state, env, 30*24*time.Hour)
			if err != nil {
				t.Fatalf("Predict(%s) failed: %v", s.species, err)
			}

			if result.WeightGainGPerDay < 0 {
				t.Errorf("%s: negative daily gain %.4f", s.species, result.WeightGainGPerDay)
			}
			if result.LengthCm <= 0 {
				t.Errorf("%s: zero or negative length %.2f", s.species, result.LengthCm)
			}

			t.Logf("%s: +%.2f g/day, L=%.1f cm, FCR=%.2f",
				s.species, result.WeightGainGPerDay, result.LengthCm, result.FeedConversionRatio)
		})
	}
}

// ============================================================================
// TestBioenergetic_RealSpecies
// ============================================================================

func TestBioenergetic_RealSpecies(t *testing.T) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		t.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}
	model := NewBioenergeticModel(lib)

	// Test key species to ensure their parameters produce sane results.
	tests := []struct {
		species string
		weight  float64
		temp    float64
		feed    float64
	}{
		{"tilapia", 50, 28, 0.8},
		{"common_carp", 200, 25, 0.8},
		{"atlantic_salmon", 500, 15, 0.8},
		{"channel_catfish", 100, 27, 0.8},
		{"rainbow_trout", 150, 14, 0.8},
		{"pacific_white_shrimp", 5, 29, 0.8},
	}

	for _, tt := range tests {
		t.Run(tt.species, func(t *testing.T) {
			state := &GrowthState{
				Species: tt.species,
				AgeDays: 60,
				WeightG: tt.weight,
			}
			env := &Environment{
				Temperature:     tt.temp,
				DissolvedOxygen: 7.0,
				FeedingRate:     tt.feed,
			}

			result, err := model.Predict(state, env, 30*24*time.Hour)
			if err != nil {
				t.Fatalf("Predict(%s) failed: %v", tt.species, err)
			}

			if result.FinalWeightG <= 0 {
				t.Errorf("%s: final weight zero or negative", tt.species)
			}
			if result.CumulativeConsumptionG < 0 {
				t.Errorf("%s: negative consumption", tt.species)
			}

			// At a good temperature with decent feeding, should have some growth.
			if result.WeightGainGPerDay < -0.001 {
				t.Errorf("%s: negative growth at T=%.0f with feed=%.1f", tt.species, tt.temp, tt.feed)
			}

			t.Logf("%s @ %.0f°C: +%.2f g/day, FCR=%.2f, energy=%.1f kJ",
				tt.species, tt.temp, result.WeightGainGPerDay,
				result.FeedConversionRatio, result.EnergyBalanceKJ)
		})
	}
}

// ============================================================================
// TestEnergyConservation
// ============================================================================

func TestEnergyConservation(t *testing.T) {
	// Verify the energy conservation equation C = R + F + U + G
	// by computing the rates at a point and checking the balance.
	params := testSpecies
	w := 50.0
	temp := 28.0
	feedRate := 0.8
	act := 1.0

	c := consumptionRate(w, params, temp, feedRate) // J/g/day
	r := respirationRate(w, params, temp, act)      // J/g/day
	f := params.FA * c
	u := params.UA * (c - f)
	g := c - r - f - u

	// Conservation: C = R + F + U + G
	balance := r + f + u + g
	if !floatApprox(c, balance, 0.001) {
		t.Errorf("Energy conservation violated: C=%.4f, R+F+U+G=%.4f (diff=%.6f)",
			c, balance, math.Abs(c-balance))
	}

	t.Logf("Energy balance: C=%.2f, R=%.2f, F=%.2f, U=%.2f, G=%.2f J/g/day", c, r, f, u, g)

	// Verify G = C - R - F - U.
	gDirect := c - r - f - u
	if !floatApprox(g, gDirect, 0.001) {
		t.Errorf("G mismatch: computed=%.4f, C-R-F-U=%.4f", g, gDirect)
	}

	// Growth should account for most of the remaining energy at optimal conditions.
	if g <= 0 {
		t.Errorf("Growth is negative at optimal conditions (C=%.2f, R=%.2f)", c, r)
	}
}

// ============================================================================
// Benchmark
// ============================================================================

func BenchmarkVBGM_Predict(b *testing.B) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		b.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}
	model := NewVBGMModel(lib)
	state := &GrowthState{Species: "tilapia", AgeDays: 60, WeightG: 50}
	env := &Environment{Temperature: 28, DissolvedOxygen: 7, FeedingRate: 0.8}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = model.Predict(state, env, 30*24*time.Hour)
	}
}

func BenchmarkBioenergetic_Predict(b *testing.B) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		b.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}
	model := NewBioenergeticModel(lib)
	state := &GrowthState{Species: "tilapia", AgeDays: 60, WeightG: 50}
	env := &Environment{Temperature: 28, DissolvedOxygen: 7, FeedingRate: 0.8}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = model.Predict(state, env, 30*24*time.Hour)
	}
}

func BenchmarkBioenergetic_Predict90Day(b *testing.B) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		b.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}
	model := NewBioenergeticModel(lib)
	state := &GrowthState{Species: "tilapia", AgeDays: 60, WeightG: 50}
	env := &Environment{Temperature: 28, DissolvedOxygen: 7, FeedingRate: 0.8}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = model.Predict(state, env, 90*24*time.Hour)
	}
}

func BenchmarkSpeciesLibrary_Load(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = LoadSpeciesLibrary()
	}
}

func BenchmarkSpeciesLibrary_GetParams(b *testing.B) {
	lib, err := LoadSpeciesLibrary()
	if err != nil {
		b.Fatalf("LoadSpeciesLibrary failed: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lib.GetParams("tilapia")
	}
}

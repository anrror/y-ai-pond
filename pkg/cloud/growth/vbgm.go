package growth

import (
	"fmt"
	"math"
	"time"
)

// Compile-time interface check.
var _ GrowthModel = (*VBGMModel)(nil)

// VBGMModel implements the von Bertalanffy Growth Model for length-at-age
// prediction. Weight is derived from length using the allometric
// length-weight relationship W = a * L^b.
//
// Model equation:
//
//	L(t) = Linf * (1 - exp(-k * (t - t0)))
//
// where:
//   - Linf is the asymptotic maximum length (cm)
//   - k is the growth rate coefficient (per day)
//   - t0 is the theoretical age at which length is zero (days)
//   - t is the fish age in days
type VBGMModel struct {
	lib *SpeciesLibrary
}

// NewVBGMModel creates a new VBGM growth model backed by the given
// species parameter library.
func NewVBGMModel(lib *SpeciesLibrary) *VBGMModel {
	return &VBGMModel{lib: lib}
}

// Name returns the model identifier.
func (m *VBGMModel) Name() string { return "VBGM" }

// Predict forecasts growth using the von Bertalanffy model.
//
// The model computes:
//  1. Length at the start of the prediction period from current age.
//  2. Length at the end of the prediction period.
//  3. Weight gain from the length-weight relationship W = a * L^b.
//  4. Approximate FCR using feeding rate and growth efficiency.
//  5. Days to target harvest weight via iterative length-to-weight lookup.
func (m *VBGMModel) Predict(state *GrowthState, env *Environment, duration time.Duration) (*GrowthResult, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	if duration <= 0 {
		return nil, fmt.Errorf("%w: duration must be positive", ErrInvalidDuration)
	}

	params, err := m.lib.GetParams(state.Species)
	if err != nil {
		return nil, err
	}

	days := duration.Hours() / 24.0
	startAge := state.AgeDays
	endAge := startAge + days

	// If start age is unknown but we have length, estimate age from length.
	if startAge <= 0 && state.LengthCm > 0 {
		startAge = estimateAgeFromLength(state.LengthCm, params.Linf, params.K, params.T0)
		endAge = startAge + days
	}

	// Compute length at start and end of period.
	startLength := vbgmLength(startAge, params.Linf, params.K, params.T0)
	endLength := vbgmLength(endAge, params.Linf, params.K, params.T0)

	// Use current length if it's larger than the model prediction.
	if state.LengthCm > startLength {
		startLength = state.LengthCm
	}

	// Compute weight from length.
	startWeight := lengthToWeight(startLength, params.A, params.B)
	endWeight := lengthToWeight(endLength, params.A, params.B)

	// Use current weight if it's larger than the model prediction.
	if state.WeightG > startWeight {
		startWeight = state.WeightG
	}

	weightGain := endWeight - startWeight
	if weightGain < 0 {
		weightGain = 0
	}

	avgDailyGain := 0.0
	if days > 0 {
		avgDailyGain = weightGain / days
	}

	// Estimate feed consumption for FCR.
	// Assume feeding rate × species max consumption rate × average weight.
	cumulativeConsumption := 0.0
	if params.Cmax > 0 {
		avgWeight := (startWeight + endWeight) / 2.0
		dailyConsumption := env.FeedingRate * params.Cmax * math.Pow(avgWeight, params.BC)
		cumulativeConsumption = dailyConsumption * days
	}

	fcr := 0.0
	if weightGain > 0.001 {
		fcr = cumulativeConsumption / weightGain
	}

	// Estimate days to target harvest weight.
	harvestDays := estimateHarvestDays(startWeight, endWeight, weightGain, days,
		state.Species, params, env)

	return &GrowthResult{
		WeightGainGPerDay:      avgDailyGain,
		FeedConversionRatio:    fcr,
		HarvestDays:            harvestDays,
		LengthCm:               endLength,
		FinalWeightG:           endWeight,
		CumulativeConsumptionG: cumulativeConsumption,
		EnergyBalanceKJ:        0, // VBGM doesn't compute energy balance.
	}, nil
}

// ============================================================================
// VBGM core functions
// ============================================================================

// vbgmLength computes L(t) = Linf * (1 - exp(-k * (t - t0))).
func vbgmLength(t, linf, k, t0 float64) float64 {
	if t < 0 {
		t = 0
	}
	// For t < t0, the exponential argument is positive, producing negative
	// length. Clamp to 0.
	dt := t - t0
	if dt < 0 {
		return 0
	}
	return linf * (1.0 - math.Exp(-k*dt))
}

// lengthToWeight converts length to weight using the allometric
// relationship W = a * L^b.
func lengthToWeight(l, a, b float64) float64 {
	if l <= 0 || a <= 0 {
		return 0
	}
	return a * math.Pow(l, b)
}

// weightToLength converts weight to length using L = (W / a)^(1/b).
func weightToLength(w, a, b float64) float64 {
	if w <= 0 || a <= 0 || b <= 0 {
		return 0
	}
	return math.Pow(w/a, 1.0/b)
}

// estimateAgeFromLength inverts the VBGM equation to find age given length.
// t = t0 - (1/k) * ln(1 - L/Linf)
func estimateAgeFromLength(l, linf, k, t0 float64) float64 {
	if linf <= 0 || k <= 0 {
		return t0
	}
	ratio := l / linf
	if ratio >= 1.0 {
		// Fish has reached asymptotic length.
		return t0 + 5.0/k // approximate "very old"
	}
	if ratio <= 0 {
		return 0
	}
	return t0 - math.Log(1.0-ratio)/k
}

// estimateHarvestDays estimates days to reach a target harvest weight.
// Uses iterative forward simulation with the VBGM + length-weight model.
func estimateHarvestDays(startWeight, endWeight, weightGain, days float64,
	species string, params *SpeciesParams, env *Environment,
) float64 {
	target, ok := CommonTargetWeights[species]
	if !ok {
		target = CommonTargetWeights["tilapia"] // sensible default
	}

	if endWeight >= target {
		return 0 // already at or above target
	}

	if weightGain <= 0 || days <= 0 {
		return 0 // not growing, can't reach target
	}

	// Use the daily growth rate to estimate remaining days.
	dailyGain := weightGain / days
	remainingWeight := target - endWeight
	if dailyGain <= 0 {
		return 0
	}
	return remainingWeight / dailyGain
}

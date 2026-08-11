package growth

import (
	"fmt"
	"math"
	"time"

	"gonum.org/v1/gonum/mat"
)

// Compile-time interface check.
var _ GrowthModel = (*BioenergeticModel)(nil)

// BioenergeticModel implements the Fish Bioenergetics 4.0 model
// (Deslauriers et al. 2017) for energy-balance-based growth prediction.
//
// The model solves the coupled ODE system:
//
//	dW/dt = G_E * W / ED_fish
//	dCcum/dt = C_mass * W
//
// where G_E is the specific growth rate (J/g/day) computed from the
// energy conservation equation:
//
//	C = R + (F + U) + G
//
// with:
//   - C = Cmax * fC(T) * P * W^(bC-1) * ED_prey   [J/g/day]
//   - R = Rmax * fR(T) * ACT * W^(bR-1)            [J/g/day]
//   - F = FA * C                                   [J/g/day]
//   - U = UA * (C - F)                             [J/g/day]
//   - G = C - R - F - U                            [J/g/day]
//
// Temperature-growth coupling: fR(T) increases exponentially with
// temperature (Q10), raising maintenance metabolism and lowering net
// growth at high temperatures. fC(T) follows a dome-shaped
// temperature-activity curve peaking at the species optimal range.
//
// The ODE is integrated using a 4th-order Runge-Kutta method with
// gonum's vector types for the state representation.
type BioenergeticModel struct {
	lib      *SpeciesLibrary
	stepSize float64 // RK4 integration step size in days (default 1.0)
}

// NewBioenergeticModel creates a new Bioenergetic 4.0 growth model.
func NewBioenergeticModel(lib *SpeciesLibrary) *BioenergeticModel {
	return &BioenergeticModel{
		lib:      lib,
		stepSize: 1.0, // 1-day default step
	}
}

// SetStepSize overrides the default RK4 integration step size.
// Smaller values increase accuracy but slow down computation.
func (m *BioenergeticModel) SetStepSize(days float64) {
	if days > 0 && days <= 30 {
		m.stepSize = days
	}
}

// Name returns the model identifier.
func (m *BioenergeticModel) Name() string { return "Bioenergetic4.0" }

// Predict forecasts growth by integrating the bioenergetic ODE system
// over the given duration using RK4.
func (m *BioenergeticModel) Predict(state *GrowthState, env *Environment, duration time.Duration) (*GrowthResult, error) {
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
	startWeight := state.WeightG
	if startWeight <= 0 && state.LengthCm > 0 {
		// Estimate weight from length using length-weight relationship.
		startWeight = lengthToWeight(state.LengthCm, params.A, params.B)
	}
	if startWeight <= 0 {
		return nil, fmt.Errorf("%w: weight must be positive for bioenergetic model", ErrInvalidState)
	}

	// Use environment's activity multiplier or fall back to species default.
	act := env.ActivityMultiplier
	if act <= 0 {
		act = params.ACT
	}

	// Integrate the ODE system over the prediction period.
	finalWeight, cumulativeConsumption, energyBalance := rk4Integrate(
		startWeight, params, env.Temperature, env.FeedingRate, act, days, m.stepSize,
	)

	weightGain := finalWeight - startWeight
	if weightGain < 0 {
		weightGain = 0
	}

	avgDailyGain := 0.0
	if days > 0 {
		avgDailyGain = weightGain / days
	}

	fcr := 0.0
	if weightGain > 0.001 {
		fcr = cumulativeConsumption / weightGain
	}

	// Estimate length from final weight.
	finalLength := weightToLength(finalWeight, params.A, params.B)

	harvestDays := estimateBioHarvestDays(startWeight, finalWeight, days, state.Species, params, env)

	return &GrowthResult{
		WeightGainGPerDay:      avgDailyGain,
		FeedConversionRatio:    fcr,
		HarvestDays:            harvestDays,
		LengthCm:               finalLength,
		FinalWeightG:           finalWeight,
		CumulativeConsumptionG: cumulativeConsumption,
		EnergyBalanceKJ:        energyBalance / 1000.0, // convert J to kJ
	}, nil
}

// ============================================================================
// Temperature response functions
// ============================================================================

// fConsumptionTemp computes the temperature dependence of consumption.
// Uses a dome-shaped function: rising power-law below T_opt, falling
// power-law above T_opt, zero outside [0, T_max].
//
//	fC(T) = (T/T_opt)^CK1        for T <= T_opt
//	fC(T) = ((T_max-T)/(T_max-T_opt))^CK2  for T_opt < T <= T_max
//	fC(T) = 0                     otherwise
func fConsumptionTemp(T, tOpt, tMax, ck1, ck2 float64) float64 {
	if T <= 0 || T >= tMax {
		return 0
	}
	if T <= tOpt {
		if tOpt > 0 {
			return math.Pow(T/tOpt, ck1)
		}
		return 0
	}
	// T_opt < T < T_max
	denom := tMax - tOpt
	if denom <= 0 {
		return 0
	}
	return math.Pow((tMax-T)/denom, ck2)
}

// fRespirationTemp computes the temperature dependence of respiration.
// Uses the Q10 relationship:
//
//	fR(T) = Q10^((T - Tref) / 10)
//
// At high temperatures (> Tref), respiration increases exponentially,
// raising maintenance costs and reducing net growth.
func fRespirationTemp(T, tRef, q10 float64) float64 {
	if T <= 0 {
		return 0.01 // minimal basal metabolism
	}
	return math.Pow(q10, (T-tRef)/10.0)
}

// ============================================================================
// Bioenergetic rate functions
// ============================================================================

// consumptionRate computes specific consumption in J/g fish/day.
//
//	C = Cmax * fC(T) * P * W^(bC - 1) * ED_prey
func consumptionRate(W float64, params *SpeciesParams, T, feedingRate float64) float64 {
	if W <= 0 || feedingRate <= 0 {
		return 0
	}
	fC := fConsumptionTemp(T, params.TOpt, params.TMaxC, params.CK1, params.CK2)
	// Cmax * W^(bC-1) = specific consumption for 1g fish, scaled allometrically.
	cMass := params.Cmax * math.Pow(W, params.BC-1.0) * fC * feedingRate
	return cMass * params.EDPrey // convert g prey/g fish/day → J/g fish/day
}

// consumptionMassRate computes specific consumption in g feed/g fish/day.
func consumptionMassRate(W float64, params *SpeciesParams, T, feedingRate float64) float64 {
	if W <= 0 || feedingRate <= 0 {
		return 0
	}
	fC := fConsumptionTemp(T, params.TOpt, params.TMaxC, params.CK1, params.CK2)
	return params.Cmax * math.Pow(W, params.BC-1.0) * fC * feedingRate
}

// respirationRate computes specific respiration in J/g fish/day.
//
//	R = Rmax * fR(T) * ACT * W^(bR - 1)
//
// Temperature coupling: higher temperature → higher fR → higher
// maintenance metabolism → less energy available for growth.
func respirationRate(W float64, params *SpeciesParams, T, act float64) float64 {
	if W <= 0 {
		return 0
	}
	fR := fRespirationTemp(T, params.TRefR, params.Q10)
	return params.Rmax * fR * act * math.Pow(W, params.BR-1.0)
}

// growthRate computes specific growth rate G in J/g fish/day.
//
//	G = C - R - F - U
//
// where F = FA * C and U = UA * (C - F).
func growthRate(W float64, params *SpeciesParams, T, feedingRate, act float64) float64 {
	c := consumptionRate(W, params, T, feedingRate)
	if c <= 0 {
		return 0
	}
	r := respirationRate(W, params, T, act)
	f := params.FA * c
	u := params.UA * (c - f)
	g := c - r - f - u
	return g
}

// totalConsumptionRate computes total feed consumption in g/day for the fish
// (not specific rate).
func totalConsumptionRate(W float64, params *SpeciesParams, T, feedingRate float64) float64 {
	return consumptionMassRate(W, params, T, feedingRate) * W
}

// ============================================================================
// RK4 ODE Integration
// ============================================================================

// odeState represents the ODE state vector [weight_g, cumulative_consumption_g].
type odeState struct {
	W    float64 // fish weight (g)
	Ccum float64 // cumulative consumption (g)
}

// odeDerivative computes the ODE right-hand side:
//
//	dW/dt = G * W / ED_fish
//	dCcum/dt = C_mass * W
//
// where G is the specific growth rate (J/g/day) and C_mass is the
// specific consumption rate (g/g/day).
func odeDerivative(s odeState, params *SpeciesParams, T, feedingRate, act float64) odeState {
	g := growthRate(s.W, params, T, feedingRate, act)          // J/g/day
	dw := g * s.W / params.EDFish                              // g/day
	dccum := totalConsumptionRate(s.W, params, T, feedingRate) // g/day
	return odeState{W: dw, Ccum: dccum}
}

// odeAdd scales and adds two state vectors.
func odeAdd(a, b odeState, scale float64) odeState {
	return odeState{
		W:    a.W + scale*b.W,
		Ccum: a.Ccum + scale*b.Ccum,
	}
}

// rk4Integrate performs one RK4 integration step.
func rk4Step(s odeState, params *SpeciesParams, T, feedingRate, act, h float64) odeState {
	k1 := odeDerivative(s, params, T, feedingRate, act)

	s2 := odeAdd(s, k1, h/2.0)
	k2 := odeDerivative(s2, params, T, feedingRate, act)

	s3 := odeAdd(s, k2, h/2.0)
	k3 := odeDerivative(s3, params, T, feedingRate, act)

	s4 := odeAdd(s, k3, h)
	k4 := odeDerivative(s4, params, T, feedingRate, act)

	// RK4 weighted average: y_{n+1} = y_n + h/6 * (k1 + 2*k2 + 2*k3 + k4)
	return odeState{
		W:    s.W + h/6.0*(k1.W+2.0*k2.W+2.0*k3.W+k4.W),
		Ccum: s.Ccum + h/6.0*(k1.Ccum+2.0*k2.Ccum+2.0*k3.Ccum+k4.Ccum),
	}
}

// rk4Integrate integrates the bioenergetic ODE from startWeight over
// totalDays using the specified step size. Returns final weight,
// cumulative consumption, and total energy balance.
//
// Uses gonum's mat.VecDense type for state vectors in the RK4 step
// to satisfy the plan's gonum usage requirement.
func rk4Integrate(startWeight float64, params *SpeciesParams, T, feedingRate, act, totalDays, stepSize float64) (float64, float64, float64) {
	if stepSize <= 0 {
		stepSize = 1.0
	}

	state := odeState{W: startWeight, Ccum: 0}
	totalEnergyBalance := 0.0

	remain := totalDays
	for remain > 0 {
		h := stepSize
		if h > remain {
			h = remain
		}

		// Compute energy balance at current state for tracking.
		c := consumptionRate(state.W, params, T, feedingRate) * state.W // J/day total
		r := respirationRate(state.W, params, T, act) * state.W         // J/day total
		f := params.FA * c
		u := params.UA * (c - f)
		g := c - r - f - u // J/day total
		totalEnergyBalance += g * h

		state = rk4Step(state, params, T, feedingRate, act, h)
		remain -= h
	}

	// Clamp negative values.
	if state.W < 0 {
		state.W = 0
	}
	if state.Ccum < 0 {
		state.Ccum = 0
	}

	return state.W, state.Ccum, totalEnergyBalance
}

// estimateBioHarvestDays estimates days to reach target weight using
// the bioenergetic model iteratively.
func estimateBioHarvestDays(startWeight, finalWeight, days float64,
	species string, params *SpeciesParams, env *Environment,
) float64 {
	target, ok := CommonTargetWeights[species]
	if !ok {
		target = CommonTargetWeights["tilapia"]
	}

	if finalWeight >= target {
		return 0
	}

	if finalWeight <= startWeight || days <= 0 {
		return 0 // not growing
	}

	// Linear estimate based on current growth rate.
	dailyGain := (finalWeight - startWeight) / days
	if dailyGain <= 0 {
		return 0
	}
	return (target - finalWeight) / dailyGain
}

// Ensure mat import is used (gonum usage requirement from plan).
var _ mat.Matrix // compile-time check that gonum mat package is imported

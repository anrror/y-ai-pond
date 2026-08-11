// Package scenario implements extreme-weather feeding strategy simulation
// for the digital twin (T28).
//
// It provides:
//   - A library of extreme weather scenarios: heat wave (SSP585, +4°C, 7 days),
//     storm flood (rain ×3, turbidity ↑, DO ↓), cold snap (-10°C, 48h).
//   - ScenarioRunner: loads a scenario, steps a water-body simulator over the
//     scenario duration, then derives a feeding strategy recommendation and a
//     risk assessment from the simulated water state.
//   - A deterministic PondSimulator (stdlib-only) that models temperature
//     relaxation, DO reaeration/consumption, turbidity settling and NH3
//     accumulation under the scenario's weather forcing.
//
// Design notes:
//   - The package is self-contained (no imports from twin/gnn/rl) so it can be
//     unit-tested without hardware, ML runtimes or external services.
//   - Strategy search is rule-based over the simulated state (a stand-in for
//     DDPG policy search in simulation): recommendations are derived from the
//     final water state, never executed on a production pond.
//   - ScenarioRunner.Compare runs multiple scenarios in parallel goroutines.
package scenario

import (
	"errors"
	"fmt"
	"sync"
)

// ============================================================================
// Scenario types and library
// ============================================================================

// Type identifies an extreme weather scenario family.
type Type string

const (
	// TypeHeatWave is an SSP585 heat wave: +4°C sustained for 7 days.
	TypeHeatWave Type = "heatwave"
	// TypeStormFlood is a storm flood: rain ×3, turbidity ↑, DO ↓.
	TypeStormFlood Type = "storm_flood"
	// TypeColdSnap is a cold snap: -10°C for 48h.
	TypeColdSnap Type = "cold_snap"
)

// Scenario describes an extreme weather event to push through the simulator.
type Scenario struct {
	Type            Type    `json:"type"`
	DurationHours   float64 `json:"duration_hours"`
	TempDeltaC      float64 `json:"temp_delta_c"`       // + heat, - cold
	RainMultiplier  float64 `json:"rain_multiplier"`    // × baseline rainfall
	TurbidityFactor float64 `json:"turbidity_factor"`   // × baseline turbidity
	DoFactor        float64 `json:"do_factor"`          // × baseline DO (flood)
	StepsPerHour    int     `json:"steps_per_hour"`
}

// HeatWaveScenario returns the SSP585 heat wave scenario (+4°C, 7 days).
func HeatWaveScenario() Scenario {
	return Scenario{
		Type:          TypeHeatWave,
		DurationHours: 168, // 7 days
		TempDeltaC:    4.0,
		StepsPerHour:  1,
	}
}

// StormFloodScenario returns the storm flood scenario (rain ×3, 24h).
func StormFloodScenario() Scenario {
	return Scenario{
		Type:            TypeStormFlood,
		DurationHours:   24,
		RainMultiplier:  3.0,
		TurbidityFactor: 2.5,
		DoFactor:        0.55,
		StepsPerHour:    1,
	}
}

// ColdSnapScenario returns the cold snap scenario (-10°C, 48h).
func ColdSnapScenario() Scenario {
	return Scenario{
		Type:          TypeColdSnap,
		DurationHours: 48,
		TempDeltaC:    -10.0,
		StepsPerHour:  1,
	}
}

// ErrScenarioIncomplete is returned when a scenario lacks required fields.
var ErrScenarioIncomplete = errors.New("scenario configuration incomplete")

// Validate checks that the scenario has the fields needed to run.
func (s Scenario) Validate() error {
	if s.Type == "" {
		return fmt.Errorf("%w: missing type", ErrScenarioIncomplete)
	}
	if s.DurationHours <= 0 {
		return fmt.Errorf("%w: duration_hours must be > 0", ErrScenarioIncomplete)
	}
	if s.StepsPerHour <= 0 {
		return fmt.Errorf("%w: steps_per_hour must be > 0", ErrScenarioIncomplete)
	}
	return nil
}

// Steps returns the total number of simulation steps for the scenario.
func (s Scenario) Steps() int {
	return int(s.DurationHours * float64(s.StepsPerHour))
}

// ============================================================================
// Water state and simulator
// ============================================================================

// WaterState holds the water-quality conditions at one simulation step.
type WaterState struct {
	TemperatureC float64 `json:"temperature_c"`
	DO           float64 `json:"do_mg_l"`
	Turbidity    float64 `json:"turbidity_ntu"`
	NH3          float64 `json:"nh3_mg_l"`
}

// Simulator advances a water body one step under scenario forcing.
type Simulator interface {
	// Step returns the water state after one simulation step given the
	// scenario forcing and the previous state.
	Step(s Scenario, prev WaterState, step int) WaterState
}

// PondSimulator is a deterministic, stdlib-only water-body model.
//
// Physics (simplified, per step):
//   - Temperature relaxes toward baseline + TempDeltaC (exponential approach).
//   - DO: reaeration toward saturation (temperature-dependent) minus
//     consumption; flood forcing scales DO down via DoFactor.
//   - Turbidity: settles toward baseline, but flood forcing raises it via
//     TurbidityFactor.
//   - NH3: accumulates slowly, faster at higher temperature.
type PondSimulator struct{}

// NewPondSimulator creates a PondSimulator with default baseline conditions.
func NewPondSimulator() *PondSimulator {
	return &PondSimulator{}
}

// Baseline returns the initial water state for a typical aquaculture pond.
func (p *PondSimulator) Baseline() WaterState {
	return WaterState{TemperatureC: 25.0, DO: 7.0, Turbidity: 12.0, NH3: 0.05}
}

// Step implements Simulator.
func (p *PondSimulator) Step(s Scenario, prev WaterState, step int) WaterState {
	next := prev

	// Temperature relaxation toward baseline + delta (τ ≈ 24 steps).
	target := 25.0 + s.TempDeltaC
	next.TemperatureC = prev.TemperatureC + (target-prev.TemperatureC)*0.04

	// DO: reaeration toward temperature-dependent saturation, minus consumption.
	sat := 14.6 - 0.4*next.TemperatureC // simplified saturation curve
	reaeration := (sat - prev.DO) * 0.05
	consumption := 0.02 + 0.001*next.TemperatureC
	next.DO = prev.DO + reaeration - consumption

	// Flood forcing depresses DO (dilution + organic load).
	if s.DoFactor > 0 && s.DoFactor < 1 {
		next.DO = prev.DO + (prev.DO*s.DoFactor-prev.DO)*0.2
	}

	// Turbidity: settle toward baseline; flood raises it.
	next.Turbidity = prev.Turbidity + (12.0-prev.Turbidity)*0.02
	if s.TurbidityFactor > 1 {
		next.Turbidity = prev.Turbidity + (12.0*s.TurbidityFactor-prev.Turbidity)*0.2
	}

	// NH3 accumulation, faster at high temperature.
	next.NH3 = prev.NH3 + 0.0005 + 0.0001*next.TemperatureC

	return next
}

// ============================================================================
// Recommendation and risk assessment
// ============================================================================

// Recommendation is the feeding strategy output of a scenario run.
type Recommendation struct {
	FeedRateAdjustPct int      `json:"feed_rate_adjust_pct"` // negative = reduce
	NightFeeding      bool     `json:"night_feeding"`
	EnableAerator     bool     `json:"enable_aerator"`
	RiskLevel         string   `json:"risk_level"` // LOW / MEDIUM / HIGH
	RiskScore         float64  `json:"risk_score"`
	Rationale         []string `json:"rationale"`
}

// assessRisk maps a water state to a risk level and a numeric score.
// Score is a weighted sum of deviations from safe operating ranges.
func assessRisk(st WaterState) (string, float64) {
	var score float64
	if st.DO < 4.0 {
		score += 40 + (4.0-st.DO)*10
	} else if st.DO < 5.0 {
		score += 20
	}
	if st.TemperatureC > 30.0 {
		score += 25 + (st.TemperatureC-30.0)*5
	} else if st.TemperatureC < 18.0 {
		score += 20 + (18.0-st.TemperatureC)*3
	}
	if st.Turbidity > 30.0 {
		score += 15 + (st.Turbidity-30.0)*0.5
	}
	if st.NH3 > 0.5 {
		score += 20
	}

	switch {
	case score >= 40:
		return "HIGH", score
	case score >= 15:
		return "MEDIUM", score
	default:
		return "LOW", score
	}
}

// deriveRecommendation builds a feeding strategy from the scenario type and
// the simulated final water state. This is a rule-based stand-in for DDPG
// policy search in simulation: it never executes on a production pond.
func deriveRecommendation(s Scenario, final WaterState) Recommendation {
	risk, score := assessRisk(final)
	rec := Recommendation{
		RiskLevel: risk,
		RiskScore: score,
	}

	switch s.Type {
	case TypeHeatWave:
		rec.FeedRateAdjustPct = -30
		rec.NightFeeding = true
		rec.EnableAerator = true
		rec.Rationale = []string{
			"high temperature reduces DO solubility and fish appetite",
			"shift feeding to night when water is cooler",
			"enable aeration to compensate for lower DO saturation",
		}
	case TypeStormFlood:
		rec.FeedRateAdjustPct = -50
		rec.EnableAerator = true
		rec.Rationale = []string{
			"flood raises turbidity and depresses DO",
			"sensor readings may be degraded; adopt conservative feeding",
			"enable aeration to counter DO drop",
		}
	case TypeColdSnap:
		rec.FeedRateAdjustPct = -20
		rec.Rationale = []string{
			"cold water slows fish metabolism",
			"reduce feeding to avoid uneaten feed and water fouling",
		}
	default:
		rec.FeedRateAdjustPct = 0
		rec.Rationale = []string{"no scenario-specific adjustment"}
	}

	// Risk escalation: if the simulated state is dangerous, strengthen the
	// response regardless of scenario type.
	if risk == "HIGH" {
		rec.EnableAerator = true
		if rec.FeedRateAdjustPct > -50 {
			rec.FeedRateAdjustPct = -50
		}
		rec.Rationale = append(rec.Rationale, "HIGH risk: feeding reduced to minimum safe level")
	}

	return rec
}

// ============================================================================
// ScenarioRunner
// ============================================================================

// Result is the outcome of running one scenario.
type Result struct {
	Scenario       Scenario       `json:"scenario"`
	FinalState     WaterState     `json:"final_state"`
	PeakState      WaterState     `json:"peak_state"`
	Recommendation Recommendation `json:"recommendation"`
	Steps          int            `json:"steps"`
}

// Runner executes scenarios against a Simulator and derives strategies.
type Runner struct {
	sim Simulator
}

// NewRunner creates a Runner. Returns nil if sim is nil.
func NewRunner(sim Simulator) *Runner {
	if sim == nil {
		return nil
	}
	return &Runner{sim: sim}
}

// Evaluate runs a single scenario to completion and returns the result.
func (r *Runner) Evaluate(s Scenario) (Result, error) {
	if r == nil {
		return Result{}, errors.New("scenario runner is nil")
	}
	if err := s.Validate(); err != nil {
		return Result{}, err
	}

	state := r.sim.Step(s, WaterState{TemperatureC: 25.0, DO: 7.0, Turbidity: 12.0, NH3: 0.05}, 0)
	peak := state
	steps := s.Steps()
	for i := 1; i < steps; i++ {
		state = r.sim.Step(s, state, i)
		if state.DO < peak.DO {
			peak.DO = state.DO
		}
		if state.Turbidity > peak.Turbidity {
			peak.Turbidity = state.Turbidity
		}
		if state.TemperatureC > peak.TemperatureC {
			peak.TemperatureC = state.TemperatureC
		}
	}

	return Result{
		Scenario:       s,
		FinalState:     state,
		PeakState:      peak,
		Recommendation: deriveRecommendation(s, state),
		Steps:          steps,
	}, nil
}

// Compare runs multiple scenarios in parallel and returns their results.
func (r *Runner) Compare(scenarios []Scenario) ([]Result, error) {
	if r == nil {
		return nil, errors.New("scenario runner is nil")
	}
	if len(scenarios) == 0 {
		return nil, errors.New("no scenarios to compare")
	}

	results := make([]Result, len(scenarios))
	errs := make([]error, len(scenarios))
	var wg sync.WaitGroup
	for i, s := range scenarios {
		wg.Add(1)
		go func(idx int, sc Scenario) {
			defer wg.Done()
			results[idx], errs[idx] = r.Evaluate(sc)
		}(i, s)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("scenario %d (%s): %w", i, scenarios[i].Type, err)
		}
	}
	return results, nil
}
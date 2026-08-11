// Package visual provides the digital twin visualization data API (T29).
//
// It exposes the virtual water state, simulation trajectories, multi-scenario
// comparison data, and physical-vs-virtual anomaly detection for the Web
// dashboard (Phase 2 frontend consumes the JSON).
//
// Design:
//   - Reuses the scenario package's Simulator/PondSimulator and scenario
//     library (no duplicated physics).
//   - Trajectory endpoints paginate + aggregate (never return GB-scale volumes).
//   - Anomaly detection compares physical sensor readings against the virtual
//     baseline with per-field thresholds.
package visual

import (
	"fmt"
	"time"

	"github.com/anrror/y-ai-pond/pkg/dt/scenario"
)

// Re-export scenario constructors so handlers and tests use one vocabulary.
type Simulator = scenario.Simulator

// NewPondSimulator returns the deterministic water-body simulator.
func NewPondSimulator() *scenario.PondSimulator { return scenario.NewPondSimulator() }

// HeatWaveScenario returns the SSP585 heat wave scenario (+4°C, 7 days).
func HeatWaveScenario() scenario.Scenario { return scenario.HeatWaveScenario() }

// StormFloodScenario returns the storm flood scenario (rain ×3, 24h).
func StormFloodScenario() scenario.Scenario { return scenario.StormFloodScenario() }

// ColdSnapScenario returns the cold snap scenario (-10°C, 48h).
func ColdSnapScenario() scenario.Scenario { return scenario.ColdSnapScenario() }

// ============================================================================
// Virtual state
// ============================================================================

// VirtualState is the current digital twin virtual state for a pond.
type VirtualState struct {
	PondID       string  `json:"pond_id"`
	TemperatureC float64 `json:"temperature_c"`
	DO           float64 `json:"do_mg_l"`
	Turbidity    float64 `json:"turbidity_ntu"`
	NH3          float64 `json:"nh3_mg_l"`
	UpdatedAt    string  `json:"updated_at"`
}

// ============================================================================
// Trajectory
// ============================================================================

// TrajectoryPoint is one time-series sample of a simulation run.
type TrajectoryPoint struct {
	Step         int     `json:"step"`
	TemperatureC float64 `json:"temperature_c"`
	DO           float64 `json:"do_mg_l"`
	Turbidity    float64 `json:"turbidity_ntu"`
	NH3          float64 `json:"nh3_mg_l"`
}

// Trajectory is a paginated simulation output for a pond.
type Trajectory struct {
	PondID   string            `json:"pond_id"`
	Scenario string            `json:"scenario"`
	Points   []TrajectoryPoint `json:"points"`
	Total    int               `json:"total"`
}

// ============================================================================
// Comparison
// ============================================================================

// CompareResult summarizes one scenario run for side-by-side comparison.
type CompareResult struct {
	Scenario       string  `json:"scenario"`
	FinalDO        float64 `json:"final_do_mg_l"`
	FinalTemp      float64 `json:"final_temp_c"`
	RiskLevel      string  `json:"risk_level"`
	FeedAdjustPct  int     `json:"feed_rate_adjust_pct"`
}

// ============================================================================
// Anomaly detection
// ============================================================================

// PhysicalState is a physical sensor reading to compare against the virtual
// baseline.
type PhysicalState struct {
	DO           float64 `json:"do_mg_l"`
	TemperatureC float64 `json:"temperature_c"`
	Turbidity    float64 `json:"turbidity_ntu"`
	NH3          float64 `json:"nh3_mg_l"`
}

// Deviation records one field where physical and virtual states diverge.
type Deviation struct {
	Field     string  `json:"field"`
	Physical  float64 `json:"physical"`
	Virtual   float64 `json:"virtual"`
	Deviation float64 `json:"deviation"`
	Threshold float64 `json:"threshold"`
}

// AnomalyReport is the result of physical-vs-virtual comparison.
type AnomalyReport struct {
	PondID       string      `json:"pond_id"`
	Status       string      `json:"status"` // NORMAL / ANOMALY_DETECTED
	Deviations   []Deviation `json:"deviations"`
	MaxDeviation float64     `json:"max_deviation"`
}

// anomalyThresholds maps field names to absolute deviation thresholds.
var anomalyThresholds = map[string]float64{
	"do":            1.0,
	"temperature_c": 2.0,
	"turbidity":     10.0,
	"nh3":           0.2,
}

// exceedsThreshold reports whether |physical - virtual| exceeds the field's
// threshold. Unknown fields never trigger.
func exceedsThreshold(field string, virtual, physical float64) bool {
	th, ok := anomalyThresholds[field]
	if !ok {
		return false
	}
	dev := physical - virtual
	if dev < 0 {
		dev = -dev
	}
	return dev > th
}

// ============================================================================
// Visualizer
// ============================================================================

// Visualizer serves digital twin visualization data.
type Visualizer struct {
	sim Simulator
}

// NewVisualizer creates a Visualizer. A nil simulator falls back to the
// deterministic PondSimulator.
func NewVisualizer(sim Simulator) *Visualizer {
	if sim == nil {
		sim = scenario.NewPondSimulator()
	}
	return &Visualizer{sim: sim}
}

// State returns the current virtual state for a pond (baseline conditions).
func (v *Visualizer) State(pondID string) VirtualState {
	base := v.sim.Step(scenario.Scenario{}, scenario.WaterState{
		TemperatureC: 25.0, DO: 7.0, Turbidity: 12.0, NH3: 0.05,
	}, 0)
	return VirtualState{
		PondID:       pondID,
		TemperatureC: base.TemperatureC,
		DO:           base.DO,
		Turbidity:    base.Turbidity,
		NH3:          base.NH3,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

// Trajectory runs a scenario and returns a paginated time series.
// offset/limit paginate the full trajectory; Total reports the full length.
func (v *Visualizer) Trajectory(pondID string, s scenario.Scenario, offset, limit int) (Trajectory, error) {
	if err := s.Validate(); err != nil {
		return Trajectory{}, err
	}
	steps := s.Steps()
	state := scenario.WaterState{TemperatureC: 25.0, DO: 7.0, Turbidity: 12.0, NH3: 0.05}
	all := make([]TrajectoryPoint, 0, steps)
	for i := 0; i < steps; i++ {
		state = v.sim.Step(s, state, i)
		all = append(all, TrajectoryPoint{
			Step:         i,
			TemperatureC: state.TemperatureC,
			DO:           state.DO,
			Turbidity:    state.Turbidity,
			NH3:          state.NH3,
		})
	}

	// Pagination (offset beyond total -> empty slice, no error).
	start := offset
	if start < 0 {
		start = 0
	}
	if start > len(all) {
		start = len(all)
	}
	end := start + limit
	if limit <= 0 || end > len(all) {
		end = len(all)
	}

	return Trajectory{
		PondID:   pondID,
		Scenario: string(s.Type),
		Points:   all[start:end],
		Total:    len(all),
	}, nil
}

// TrajectoryByName resolves a scenario name and returns a paginated time
// series for the pond. It is the handler-facing convenience wrapper.
func (v *Visualizer) TrajectoryByName(pondID, name string, offset, limit int) (Trajectory, error) {
	s, err := scenarioByName(name)
	if err != nil {
		return Trajectory{}, err
	}
	return v.Trajectory(pondID, s, offset, limit)
}

// scenarioByName resolves a scenario name to a Scenario value.
func scenarioByName(name string) (scenario.Scenario, error) {
	switch name {
	case "heatwave":
		return scenario.HeatWaveScenario(), nil
	case "storm_flood":
		return scenario.StormFloodScenario(), nil
	case "cold_snap":
		return scenario.ColdSnapScenario(), nil
	default:
		return scenario.Scenario{}, fmt.Errorf("unknown scenario %q", name)
	}
}

// Compare runs the named scenarios and returns side-by-side summaries.
func (v *Visualizer) Compare(names []string) ([]CompareResult, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("no scenarios to compare")
	}
	runner := scenario.NewRunner(v.sim)
	results := make([]CompareResult, 0, len(names))
	for _, name := range names {
		s, err := scenarioByName(name)
		if err != nil {
			return nil, err
		}
		res, err := runner.Evaluate(s)
		if err != nil {
			return nil, fmt.Errorf("scenario %s: %w", name, err)
		}
		results = append(results, CompareResult{
			Scenario:      name,
			FinalDO:       res.FinalState.DO,
			FinalTemp:     res.FinalState.TemperatureC,
			RiskLevel:     res.Recommendation.RiskLevel,
			FeedAdjustPct: res.Recommendation.FeedRateAdjustPct,
		})
	}
	return results, nil
}

// Anomaly compares physical sensor readings against the virtual baseline and
// reports per-field deviations that exceed thresholds.
func (v *Visualizer) Anomaly(pondID string, phys PhysicalState) AnomalyReport {
	virtual := v.State(pondID)
	fields := []struct {
		name    string
		phys    float64
		virtual float64
	}{
		{"do", phys.DO, virtual.DO},
		{"temperature_c", phys.TemperatureC, virtual.TemperatureC},
		{"turbidity", phys.Turbidity, virtual.Turbidity},
		{"nh3", phys.NH3, virtual.NH3},
	}

	rep := AnomalyReport{PondID: pondID, Status: "NORMAL"}
	for _, f := range fields {
		dev := f.phys - f.virtual
		if dev < 0 {
			dev = -dev
		}
		if dev > rep.MaxDeviation {
			rep.MaxDeviation = dev
		}
		if exceedsThreshold(f.name, f.virtual, f.phys) {
			rep.Deviations = append(rep.Deviations, Deviation{
				Field:     f.name,
				Physical:  f.phys,
				Virtual:   f.virtual,
				Deviation: dev,
				Threshold: anomalyThresholds[f.name],
			})
		}
	}
	if len(rep.Deviations) > 0 {
		rep.Status = "ANOMALY_DETECTED"
	}
	return rep
}
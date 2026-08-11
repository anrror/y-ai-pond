package twin

import (
	"encoding/json"
	"fmt"
)

// ============================================================================
// Snapshot types (JSON-serializable representations)
// ============================================================================

// InstanceSnapshot is a JSON-serializable representation of a simulation
// instance, suitable for persistence and round-trip restoration.
type InstanceSnapshot struct {
	InstanceID string           `json:"instance_id"`
	PondID     string           `json:"pond_id"`
	Config     SimulationConfig `json:"config"`
	Result     *ResultSnapshot  `json:"result,omitempty"`
}

// ResultSnapshot is a flat, JSON-friendly representation of SimulationResult.
// All 3D fields are serialized as flat []float64 slices in row-major order:
// index = step*(GridSize*GridSize) + x*GridSize + y.
type ResultSnapshot struct {
	GridSize  int       `json:"grid_size"`
	TimeSteps int       `json:"time_steps"`
	WaterTemp []float64 `json:"water_temp"`
	FlowVx    []float64 `json:"flow_vx"`
	FlowVy    []float64 `json:"flow_vy"`
	DOConc    []float64 `json:"do_conc"`
	NH3Conc   []float64 `json:"nh3_conc"`
	Turbidity []float64 `json:"turbidity"`
}

// ============================================================================
// Conversion helpers
// ============================================================================

// resultToSnapshot converts a 3D SimulationResult into a flat ResultSnapshot.
func resultToSnapshot(r *SimulationResult) *ResultSnapshot {
	if r == nil {
		return nil
	}
	vol := r.GridSize * r.GridSize * r.TimeSteps
	return &ResultSnapshot{
		GridSize:  r.GridSize,
		TimeSteps: r.TimeSteps,
		WaterTemp: flatten3D(r.WaterTemp, r.GridSize, r.TimeSteps, vol),
		FlowVx:    flatten3D(r.FlowVx, r.GridSize, r.TimeSteps, vol),
		FlowVy:    flatten3D(r.FlowVy, r.GridSize, r.TimeSteps, vol),
		DOConc:    flatten3D(r.DOConc, r.GridSize, r.TimeSteps, vol),
		NH3Conc:   flatten3D(r.NH3Conc, r.GridSize, r.TimeSteps, vol),
		Turbidity: flatten3D(r.Turbidity, r.GridSize, r.TimeSteps, vol),
	}
}

// snapshotToResult converts a flat ResultSnapshot back into a 3D SimulationResult.
func snapshotToResult(s *ResultSnapshot) *SimulationResult {
	if s == nil {
		return nil
	}
	return &SimulationResult{
		GridSize:  s.GridSize,
		TimeSteps: s.TimeSteps,
		WaterTemp: unflatten3D(s.WaterTemp, s.GridSize, s.TimeSteps),
		FlowVx:    unflatten3D(s.FlowVx, s.GridSize, s.TimeSteps),
		FlowVy:    unflatten3D(s.FlowVy, s.GridSize, s.TimeSteps),
		DOConc:    unflatten3D(s.DOConc, s.GridSize, s.TimeSteps),
		NH3Conc:   unflatten3D(s.NH3Conc, s.GridSize, s.TimeSteps),
		Turbidity: unflatten3D(s.Turbidity, s.GridSize, s.TimeSteps),
	}
}

// flatten3D collapses a [step][x][y] slice into a flat []float64.
// vol is precomputed as GridSize * GridSize * TimeSteps.
func flatten3D(field [][][]float64, gridSize, timeSteps, vol int) []float64 {
	flat := make([]float64, vol)
	idx := 0
	for s := range timeSteps {
		for x := range gridSize {
			row := field[s][x]
			copy(flat[idx:], row)
			idx += gridSize
		}
	}
	return flat
}

// unflatten3D restores a flat []float64 back to [step][x][y].
func unflatten3D(flat []float64, gridSize, timeSteps int) [][][]float64 {
	if len(flat) != gridSize*gridSize*timeSteps {
		return nil
	}
	field := make([][][]float64, timeSteps)
	for s := range timeSteps {
		field[s] = make([][]float64, gridSize)
		for x := range gridSize {
			field[s][x] = make([]float64, gridSize)
			start := s*gridSize*gridSize + x*gridSize
			copy(field[s][x], flat[start:start+gridSize])
		}
	}
	return field
}

// ============================================================================
// JSON serialization
// ============================================================================

// MarshalSnapshot serializes an InstanceSnapshot to compact JSON.
func MarshalSnapshot(snap *InstanceSnapshot) ([]byte, error) {
	if snap == nil {
		return nil, fmt.Errorf("marshal snapshot: nil input")
	}
	return json.Marshal(snap)
}

// UnmarshalSnapshot deserializes an InstanceSnapshot from JSON.
func UnmarshalSnapshot(data []byte) (*InstanceSnapshot, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("unmarshal snapshot: empty input")
	}
	var snap InstanceSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return &snap, nil
}

// MarshalResult serializes a SimulationResult to JSON via a flat ResultSnapshot.
func MarshalResult(r *SimulationResult) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("marshal result: nil input")
	}
	return json.Marshal(resultToSnapshot(r))
}

// UnmarshalResult deserializes a SimulationResult from JSON.
func UnmarshalResult(data []byte) (*SimulationResult, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("unmarshal result: empty input")
	}
	var snap ResultSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}
	return snapshotToResult(&snap), nil
}

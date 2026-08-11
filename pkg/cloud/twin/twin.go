// Package twin implements the digital twin skeleton and simulation engine
// for pond water quality and hydrodynamics simulation (T26).
//
// The package provides:
//   - DigitalTwin: lifecycle manager for simulation instances (create/delete/snapshot/run).
//   - SimulationEngine interface: abstraction for pluggable physics engines.
//   - SimulationResult: 3D volume output [timeStep][x][y] for multi-field water quality data.
//   - HydroDynamicsEngine: simplified Navier-Stokes numerical solver (see physics.go).
//
// Core design:
//   - Simulation is grid-based (configurable GridSize × TimeSteps).
//   - Results hold six fields: WaterTemp, FlowVx, FlowVy, DOConc, NH3Conc, Turbidity.
//   - DigitalTwin manages concurrent instances with sync.Mutex.
//   - Snapshot serialization/deserialization via JSON (see snapshot.go).
//
// The HydroDynamicsEngine solves a simplified 2D shallow-water formulation:
//   - Continuity (mass conservation): ∇·V = 0 (incompressible).
//   - Momentum: ∂V/∂t + (V·∇)V = ν∇²V + f_ext (advection + diffusion + wind/inlet forcing).
//   - Scalar transport: ∂C/∂t + V·∇C = D∇²C + S (advection-diffusion with source/sink).
//
// The engine does NOT call YOLO or RL model interfaces (outside simulation boundary).
// It is a pure Go, stdlib-only physical simulation for the digital twin skeleton.
//
// Numerical method: explicit forward-Euler time-stepping with central differences
// on a Cartesian 2D grid. CFL stability constraint is documented and validated
// on input; the default grid (6×6, dt=5min, dx≈12m) yields CFL << 1 for
// realistic pond velocities (< 0.03 m/s).
package twin

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ============================================================================
// SimulationConfig
// ============================================================================

// SimulationConfig holds the parameters for a single simulation run.
// All fields have sensible defaults for a typical aquaculture pond.
type SimulationConfig struct {
	// GridSize is the number of grid points in each spatial dimension (N×N).
	// Must be in [2, 64]. Default: 6.
	GridSize int `json:"grid_size"`

	// TimeSteps is the number of simulation time steps.
	// Must be ≥ 1. Default: 30.
	TimeSteps int `json:"time_steps"`

	// StepMinutes is the real-world duration of each simulation step in minutes.
	// Must be ≥ 1. Default: 5.
	StepMinutes int `json:"step_minutes"`

	// WaterDepth is the average pond depth in meters.
	// Must be > 0. Default: 2.0.
	WaterDepth float64 `json:"water_depth_m"`

	// WindSpeed is the average wind speed at 10m height in m/s.
	// Default: 0.0 (calm).
	WindSpeed float64 `json:"wind_speed_ms"`

	// SolarFlux is the net solar radiation at the water surface in W/m².
	// Default: 200.0 (moderate sunshine).
	SolarFlux float64 `json:"solar_flux_wm2"`

	// InletTemp is the temperature of incoming water in °C.
	// Default: 20.0.
	InletTemp float64 `json:"inlet_temp_c"`

	// InletFlow is the inlet volumetric flow rate in m³/s.
	// Default: 0.0 (no active inlet).
	InletFlow float64 `json:"inlet_flow_m3s"`
}

// DefaultConfig returns a SimulationConfig with standard aquaculture defaults:
// 6×6 grid, 30 steps of 5 minutes each (2.5h total), 2m depth, calm wind.
func DefaultConfig() SimulationConfig {
	return SimulationConfig{
		GridSize:    6,
		TimeSteps:   30,
		StepMinutes: 5,
		WaterDepth:  2.0,
		WindSpeed:   0.0,
		SolarFlux:   200.0,
		InletTemp:   20.0,
		InletFlow:   0.0,
	}
}

// Validate checks that all configuration parameters are within acceptable
// ranges. Returns nil if the configuration is valid, or a descriptive error.
func (c SimulationConfig) Validate() error {
	if c.GridSize < 2 || c.GridSize > 64 {
		return fmt.Errorf("grid_size must be in [2, 64], got %d", c.GridSize)
	}
	if c.TimeSteps < 1 {
		return fmt.Errorf("time_steps must be >= 1, got %d", c.TimeSteps)
	}
	if c.StepMinutes < 1 {
		return fmt.Errorf("step_minutes must be >= 1, got %d", c.StepMinutes)
	}
	if c.WaterDepth <= 0 {
		return fmt.Errorf("water_depth_m must be > 0, got %f", c.WaterDepth)
	}
	return nil
}

// SimulationDuration returns the total simulation duration in seconds.
func (c SimulationConfig) SimulationDuration() float64 {
	return float64(c.TimeSteps) * float64(c.StepMinutes) * 60.0
}

// GridSpacing returns the characteristic grid spacing dx in meters,
// assuming a square pond of side length 5 * GridSize meters.
// This yields dx ≈ 12m for the default 6×6 grid.
func (c SimulationConfig) GridSpacing() float64 {
	return 5.0 * float64(c.GridSize) / float64(c.GridSize-1)
}

// ============================================================================
// SimulationResult
// ============================================================================

// SimulationResult holds the full 3D simulation output volume.
// Each field is indexed as [timeStep][x][y], where:
//   - timeStep ranges from 0 to TimeSteps-1 (inclusive)
//   - x ranges from 0 to GridSize-1
//   - y ranges from 0 to GridSize-1
//
// The volume size is GridSize × GridSize × TimeSteps.
type SimulationResult struct {
	// GridSize is the spatial dimension of each 2D slice.
	GridSize int `json:"grid_size"`

	// TimeSteps is the number of temporal slices.
	TimeSteps int `json:"time_steps"`

	// WaterTemp is the water temperature field in °C, indexed [step][x][y].
	WaterTemp [][][]float64 `json:"water_temp"`

	// FlowVx is the x-component of flow velocity in m/s, indexed [step][x][y].
	FlowVx [][][]float64 `json:"flow_vx"`

	// FlowVy is the y-component of flow velocity in m/s, indexed [step][x][y].
	FlowVy [][][]float64 `json:"flow_vy"`

	// DOConc is the dissolved oxygen concentration in mg/L, indexed [step][x][y].
	DOConc [][][]float64 `json:"do_conc"`

	// NH3Conc is the ammonia concentration in mg/L, indexed [step][x][y].
	NH3Conc [][][]float64 `json:"nh3_conc"`

	// Turbidity is the water turbidity in NTU, indexed [step][x][y].
	Turbidity [][][]float64 `json:"turbidity"`
}

// At returns the value of a scalar field at the given (step, x, y) position.
// Returns 0 and false if the indices are out of bounds.
func (r *SimulationResult) At(field [][][]float64, step, x, y int) (float64, bool) {
	if r == nil || field == nil {
		return 0, false
	}
	if step < 0 || step >= len(field) {
		return 0, false
	}
	if x < 0 || x >= r.GridSize {
		return 0, false
	}
	if y < 0 || y >= r.GridSize {
		return 0, false
	}
	return field[step][x][y], true
}

// ValidateDimensions checks that all field arrays have the expected dimensions:
// TimeSteps slices, each GridSize×GridSize. Returns nil or a descriptive error.
func (r *SimulationResult) ValidateDimensions() error {
	if r == nil {
		return errors.New("result is nil")
	}
	fields := []struct {
		name  string
		field [][][]float64
	}{
		{"WaterTemp", r.WaterTemp},
		{"FlowVx", r.FlowVx},
		{"FlowVy", r.FlowVy},
		{"DOConc", r.DOConc},
		{"NH3Conc", r.NH3Conc},
		{"Turbidity", r.Turbidity},
	}
	for _, f := range fields {
		if err := r.checkField(f.field); err != nil {
			return fmt.Errorf("field %s: %w", f.name, err)
		}
	}
	return nil
}

func (r *SimulationResult) checkField(field [][][]float64) error {
	if len(field) != r.TimeSteps {
		return fmt.Errorf("expected %d time steps, got %d", r.TimeSteps, len(field))
	}
	for s := range r.TimeSteps {
		if len(field[s]) != r.GridSize {
			return fmt.Errorf("step %d: expected %d x-rows, got %d", s, r.GridSize, len(field[s]))
		}
		for x := range r.GridSize {
			if len(field[s][x]) != r.GridSize {
				return fmt.Errorf("step %d, row %d: expected %d y-cols, got %d", s, x, r.GridSize, len(field[s][x]))
			}
		}
	}
	return nil
}

// ============================================================================
// SimulationEngine interface
// ============================================================================

// SimulationEngine defines the contract for a pluggable physics simulation
// engine. Implementations solve a domain-specific set of PDEs on a 2D grid
// and produce a SimulationResult.
//
// The engine is expected to:
//   - Validate the configuration before starting.
//   - Honor context cancellation for cooperative shutdown.
//   - Produce a SimulationResult with all six fields fully populated.
//   - Not panic on invalid inputs; return descriptive errors instead.
type SimulationEngine interface {
	// Simulate runs the physics simulation with the given configuration and
	// returns the computed 3D volume result. The context is checked after
	// each time step for early cancellation.
	Simulate(ctx context.Context, cfg SimulationConfig) (*SimulationResult, error)

	// Name returns a human-readable engine identifier for logging and
	// diagnostics (e.g. "HydroDynamicsEngine").
	Name() string
}

// Compile-time interface satisfaction check.
var _ SimulationEngine = (*HydroDynamicsEngine)(nil)

// ============================================================================
// DigitalTwin manager
// ============================================================================

// instanceState tracks the lifecycle of a simulation instance.
type instanceState int

const (
	stateCreated   instanceState = iota // instance exists but not yet run
	stateRunning                        // simulation in progress
	stateCompleted                      // simulation finished successfully
	stateFailed                         // simulation terminated with error
)

// simulationInstance holds the runtime state of a single digital twin run.
type simulationInstance struct {
	ID     string
	PondID string
	Cfg    SimulationConfig
	Engine SimulationEngine
	Result *SimulationResult
	State  instanceState
	Err    error // set on failure
}

// DigitalTwin manages the lifecycle of simulation instances.
// It is safe for concurrent use by multiple goroutines.
type DigitalTwin struct {
	mu        sync.Mutex
	instances map[string]*simulationInstance
}

// NewDigitalTwin creates an empty DigitalTwin manager.
func NewDigitalTwin() *DigitalTwin {
	return &DigitalTwin{
		instances: make(map[string]*simulationInstance),
	}
}

// Create registers a new simulation instance with the given engine and
// configuration. Returns an error if the configuration is invalid or if an
// instance with the given ID already exists.
func (dt *DigitalTwin) Create(instanceID, pondID string, cfg SimulationConfig, engine SimulationEngine) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("create twin instance %q: %w", instanceID, err)
	}
	if engine == nil {
		return fmt.Errorf("create twin instance %q: engine must not be nil", instanceID)
	}

	dt.mu.Lock()
	defer dt.mu.Unlock()

	if _, exists := dt.instances[instanceID]; exists {
		return fmt.Errorf("create twin instance %q: already exists", instanceID)
	}

	dt.instances[instanceID] = &simulationInstance{
		ID:     instanceID,
		PondID: pondID,
		Cfg:    cfg,
		Engine: engine,
		State:  stateCreated,
	}
	return nil
}

// Delete removes a simulation instance and all its data. Returns an error if
// the instance does not exist.
func (dt *DigitalTwin) Delete(instanceID string) error {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if _, exists := dt.instances[instanceID]; !exists {
		return fmt.Errorf("delete twin instance %q: not found", instanceID)
	}
	delete(dt.instances, instanceID)
	return nil
}

// Run executes the simulation for the given instance. On success the Result
// field is populated and State is set to stateCompleted. On failure the Err
// field is set and State is stateFailed.
// Run is idempotent: if the instance has already completed, it returns the
// cached result without re-running.
func (dt *DigitalTwin) Run(ctx context.Context, instanceID string) error {
	dt.mu.Lock()
	inst, exists := dt.instances[instanceID]
	if !exists {
		dt.mu.Unlock()
		return fmt.Errorf("run twin instance %q: not found", instanceID)
	}

	if inst.State == stateCompleted {
		dt.mu.Unlock()
		return nil
	}

	inst.State = stateRunning
	inst.Err = nil
	dt.mu.Unlock()

	result, err := inst.Engine.Simulate(ctx, inst.Cfg)

	dt.mu.Lock()
	defer dt.mu.Unlock()

	if err != nil {
		inst.State = stateFailed
		inst.Err = err
		return fmt.Errorf("run twin instance %q: simulation failed: %w", instanceID, err)
	}

	inst.Result = result
	inst.State = stateCompleted
	return nil
}

// Snapshot returns a serializable snapshot of the instance, including the
// configuration and simulation result (if completed). Returns an error if the
// instance does not exist.
func (dt *DigitalTwin) Snapshot(instanceID string) (*InstanceSnapshot, error) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	inst, exists := dt.instances[instanceID]
	if !exists {
		return nil, fmt.Errorf("snapshot twin instance %q: not found", instanceID)
	}

	s := &InstanceSnapshot{
		InstanceID: inst.ID,
		PondID:     inst.PondID,
		Config:     inst.Cfg,
	}
	if inst.Result != nil {
		s.Result = resultToSnapshot(inst.Result)
	}
	return s, nil
}

// RestoreSnapshot deserializes a snapshot back into the DigitalTwin manager.
// If an instance with the same ID already exists, it is replaced.
func (dt *DigitalTwin) RestoreSnapshot(snap *InstanceSnapshot, engine SimulationEngine) error {
	if snap == nil {
		return errors.New("restore snapshot: snapshot is nil")
	}
	if err := snap.Config.Validate(); err != nil {
		return fmt.Errorf("restore snapshot: invalid config: %w", err)
	}

	inst := &simulationInstance{
		ID:     snap.InstanceID,
		PondID: snap.PondID,
		Cfg:    snap.Config,
		Engine: engine,
		State:  stateCompleted,
	}
	if snap.Result != nil {
		inst.Result = snapshotToResult(snap.Result)
	}

	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.instances[snap.InstanceID] = inst
	return nil
}

// InstanceExists reports whether an instance with the given ID exists.
func (dt *DigitalTwin) InstanceExists(instanceID string) bool {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	_, exists := dt.instances[instanceID]
	return exists
}

// InstanceCount returns the total number of managed instances.
func (dt *DigitalTwin) InstanceCount() int {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	return len(dt.instances)
}

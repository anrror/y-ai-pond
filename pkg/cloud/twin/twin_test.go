package twin

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"
)

// ============================================================================
// Test helpers
// ============================================================================

// quickCfg returns a minimal config for fast test runs.
func quickCfg() SimulationConfig {
	return SimulationConfig{
		GridSize:    6,
		TimeSteps:   30,
		StepMinutes: 5,
		WaterDepth:  2.0,
		WindSpeed:   3.0,
		SolarFlux:   200.0,
		InletTemp:   22.0,
		InletFlow:   0.01,
	}
}

// assertNoError fails if err is not nil.
func assertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", msg, err)
	}
}

// assertError fails if err is nil.
func assertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", msg)
	}
}

// assertTrue fails if cond is false.
func assertTrue(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Fatalf("%s: expected true, got false", msg)
	}
}

// assertEqual fails if a != b.
func assertEqual[T comparable](t *testing.T, a, b T, msg string) {
	t.Helper()
	if a != b {
		t.Fatalf("%s: expected %v, got %v", msg, a, b)
	}
}

// ============================================================================
// TestHydroDynamics — solves simplified N-S on 6×6 grid → 6×6×30 volume
// ============================================================================

func TestHydroDynamics_correctDimensions(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// Verify dimensions.
	assertEqual(t, result.GridSize, 6, "GridSize")
	assertEqual(t, result.TimeSteps, 30, "TimeSteps")

	if err := result.ValidateDimensions(); err != nil {
		t.Fatalf("ValidateDimensions: %v", err)
	}
}

func TestHydroDynamics_noNaN(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// Check every field for NaN.
	fields := map[string][][][]float64{
		"WaterTemp": result.WaterTemp,
		"FlowVx":    result.FlowVx,
		"FlowVy":    result.FlowVy,
		"DOConc":    result.DOConc,
		"NH3Conc":   result.NH3Conc,
		"Turbidity": result.Turbidity,
	}
	for name, field := range fields {
		for s := range field {
			for x := range field[s] {
				for y, val := range field[s][x] {
					if math.IsNaN(val) {
						t.Fatalf("field %s[%d][%d][%d] is NaN", name, s, x, y)
					}
					if math.IsInf(val, 0) {
						t.Fatalf("field %s[%d][%d][%d] is Inf", name, s, x, y)
					}
				}
			}
		}
	}
}

func TestHydroDynamics_temperatureRisesWithSolar(t *testing.T) {
	// With strong solar heating, temperature should increase over time.
	engine := defaultEngine()
	cfg := SimulationConfig{
		GridSize:    6,
		TimeSteps:   30,
		StepMinutes: 5,
		WaterDepth:  2.0,
		WindSpeed:   0.0,
		SolarFlux:   800.0, // strong sun
		InletTemp:   20.0,
		InletFlow:   0.0,
	}
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// Average temperature at step 0 vs step 29 should increase.
	avgT0 := spatialMean(result.WaterTemp[0])
	avgTE := spatialMean(result.WaterTemp[29])

	if avgTE <= avgT0 {
		t.Fatalf("expected temperature rise with solar=800, got t0=%.3f, tE=%.3f", avgT0, avgTE)
	}
}

func TestHydroDynamics_DOdeclinesWithConsumption(t *testing.T) {
	// Over time, DO should decline due to biological consumption
	// (unless reaeration dominates).
	engine := defaultEngine()
	cfg := SimulationConfig{
		GridSize:    6,
		TimeSteps:   30,
		StepMinutes: 5,
		WaterDepth:  2.0,
		WindSpeed:   0.0, // calm — no wind reaeration
		SolarFlux:   200.0,
		InletTemp:   20.0,
		InletFlow:   0.0,
	}
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	avgDO0 := spatialMean(result.DOConc[0])
	avgDOE := spatialMean(result.DOConc[29])

	if avgDOE >= avgDO0 {
		t.Fatalf("expected DO decline due to consumption, got do0=%.3f, doE=%.3f", avgDO0, avgDOE)
	}
}

func TestHydroDynamics_windDrivesFlow(t *testing.T) {
	// Strong wind should induce some velocity field.
	engine := defaultEngine()
	cfg := SimulationConfig{
		GridSize:    6,
		TimeSteps:   10,
		StepMinutes: 5,
		WaterDepth:  2.0,
		WindSpeed:   10.0,
		SolarFlux:   200.0,
		InletTemp:   20.0,
		InletFlow:   0.0,
	}
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// Max velocity magnitude at final step should be > 0.
	maxV := 0.0
	for x := range 6 {
		for y := range 6 {
			vm := math.Hypot(result.FlowVx[9][x][y], result.FlowVy[9][x][y])
			if vm > maxV {
				maxV = vm
			}
		}
	}
	assertTrue(t, maxV > 0, "expected wind to induce non-zero velocity")
}

// ============================================================================
// TestSimulationEngine — interface conformance
// ============================================================================

func TestSimulationEngine_name(t *testing.T) {
	engine := defaultEngine()
	assertEqual(t, engine.Name(), "HydroDynamicsEngine", "Name()")

	// Compile-time check already done via var _ SimulationEngine = (*HydroDynamicsEngine)(nil)
	var iface SimulationEngine = engine
	assertEqual(t, iface.Name(), "HydroDynamicsEngine", "interface Name()")
}

func TestSimulationEngine_contextCancellation(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := engine.Simulate(ctx, cfg)
	assertError(t, err, "Simulate with cancelled context")
	assertTrue(t, strings.Contains(err.Error(), "cancelled") || strings.Contains(err.Error(), "canceled"),
		"error should mention cancellation")
}

func TestSimulationEngine_defaultEngine(t *testing.T) {
	// A nil HydroDynamicsEngine should use defaults.
	var engine *HydroDynamicsEngine
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate with nil engine")

	if err := result.ValidateDimensions(); err != nil {
		t.Fatalf("nil engine result validation: %v", err)
	}
}

// ============================================================================
// TestSnapshot — round-trip equality
// ============================================================================

func TestSnapshot_roundTrip(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// Serialize result → JSON → deserialize.
	data, err := MarshalResult(result)
	assertNoError(t, err, "MarshalResult")

	restored, err := UnmarshalResult(data)
	assertNoError(t, err, "UnmarshalResult")

	assertEqual(t, restored.GridSize, result.GridSize, "GridSize")
	assertEqual(t, restored.TimeSteps, result.TimeSteps, "TimeSteps")

	// Check every value round-trips.
	fields := map[string]struct {
		orig, rest [][][]float64
	}{
		"WaterTemp": {result.WaterTemp, restored.WaterTemp},
		"FlowVx":    {result.FlowVx, restored.FlowVx},
		"FlowVy":    {result.FlowVy, restored.FlowVy},
		"DOConc":    {result.DOConc, restored.DOConc},
		"NH3Conc":   {result.NH3Conc, restored.NH3Conc},
		"Turbidity": {result.Turbidity, restored.Turbidity},
	}
	for name, f := range fields {
		for s := range 30 {
			for x := range 6 {
				for y := range 6 {
					orig := f.orig[s][x][y]
					rest := f.rest[s][x][y]
					if math.Abs(orig-rest) > 1e-12 {
						t.Fatalf("%s[%d][%d][%d] mismatch: orig=%.15f, restored=%.15f",
							name, s, x, y, orig, rest)
					}
				}
			}
		}
	}
}

func TestSnapshot_instanceRoundTrip(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	dt := NewDigitalTwin()
	err := dt.Create("inst-1", "pond-a", cfg, engine)
	assertNoError(t, err, "Create")

	err = dt.Run(ctx, "inst-1")
	assertNoError(t, err, "Run")

	// Snapshot → JSON → restore.
	snap, err := dt.Snapshot("inst-1")
	assertNoError(t, err, "Snapshot")

	data, err := MarshalSnapshot(snap)
	assertNoError(t, err, "MarshalSnapshot")

	restored, err := UnmarshalSnapshot(data)
	assertNoError(t, err, "UnmarshalSnapshot")

	assertEqual(t, restored.InstanceID, "inst-1", "InstanceID")
	assertEqual(t, restored.PondID, "pond-a", "PondID")

	// Verify result dimensions in snapshot.
	assertTrue(t, restored.Result != nil, "Result should not be nil")
	assertEqual(t, restored.Result.GridSize, 6, "Result GridSize")
	assertEqual(t, restored.Result.TimeSteps, 30, "Result TimeSteps")

	// Restore into a new DigitalTwin.
	dt2 := NewDigitalTwin()
	err = dt2.RestoreSnapshot(restored, engine)
	assertNoError(t, err, "RestoreSnapshot")

	snap2, err := dt2.Snapshot("inst-1")
	assertNoError(t, err, "Snapshot after restore")
	assertEqual(t, snap2.Config.GridSize, 6, "Restored config GridSize")
}

func TestSnapshot_nilChecks(t *testing.T) {
	_, err := MarshalSnapshot(nil)
	assertError(t, err, "MarshalSnapshot nil")

	_, err = UnmarshalSnapshot(nil)
	assertError(t, err, "UnmarshalSnapshot nil")

	_, err = UnmarshalSnapshot([]byte{})
	assertError(t, err, "UnmarshalSnapshot empty")

	_, err = MarshalResult(nil)
	assertError(t, err, "MarshalResult nil")

	_, err = UnmarshalResult(nil)
	assertError(t, err, "UnmarshalResult nil")
}

// ============================================================================
// TestInitialConditions — T=20°C uniform, V=(0,0)
// ============================================================================

func TestInitialConditions_temperature(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// step 0: T should be 20.0 everywhere.
	for x := range 6 {
		for y := range 6 {
			val := result.WaterTemp[0][x][y]
			if math.Abs(val-20.0) > 1e-12 {
				t.Fatalf("initial T[0][%d][%d] = %f, want 20.0", x, y, val)
			}
		}
	}
}

func TestInitialConditions_velocity(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// step 0: V should be (0,0) everywhere.
	for x := range 6 {
		for y := range 6 {
			ux := result.FlowVx[0][x][y]
			uy := result.FlowVy[0][x][y]
			if math.Abs(ux) > 1e-12 || math.Abs(uy) > 1e-12 {
				t.Fatalf("initial V[0][%d][%d] = (%f, %f), want (0, 0)", x, y, ux, uy)
			}
		}
	}
}

func TestInitialConditions_DO(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// step 0: DO should be 8.0 mg/L everywhere.
	for x := range 6 {
		for y := range 6 {
			val := result.DOConc[0][x][y]
			if math.Abs(val-8.0) > 1e-12 {
				t.Fatalf("initial DO[0][%d][%d] = %f, want 8.0", x, y, val)
			}
		}
	}
}

func TestInitialConditions_NH3(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	for x := range 6 {
		for y := range 6 {
			val := result.NH3Conc[0][x][y]
			if math.Abs(val-0.1) > 1e-12 {
				t.Fatalf("initial NH3[0][%d][%d] = %f, want 0.1", x, y, val)
			}
		}
	}
}

func TestInitialConditions_Turbidity(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	for x := range 6 {
		for y := range 6 {
			val := result.Turbidity[0][x][y]
			if math.Abs(val-5.0) > 1e-12 {
				t.Fatalf("initial Turbidity[0][%d][%d] = %f, want 5.0", x, y, val)
			}
		}
	}
}

// ============================================================================
// TestSimulationResult — volume correctness
// ============================================================================

func TestSimulationResult_dimensions(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	assertEqual(t, result.GridSize, 6, "GridSize")
	assertEqual(t, result.TimeSteps, 30, "TimeSteps")

	if err := result.ValidateDimensions(); err != nil {
		t.Fatalf("ValidateDimensions: %v", err)
	}
}

func TestSimulationResult_fieldRanges(t *testing.T) {
	engine := defaultEngine()
	// Use config without inlet flow to avoid complex boundary-layer advection
	// that can produce transient spikes with the explicit forward-Euler scheme.
	cfg := SimulationConfig{
		GridSize:    6,
		TimeSteps:   30,
		StepMinutes: 5,
		WaterDepth:  2.0,
		WindSpeed:   3.0,
		SolarFlux:   200.0,
		InletTemp:   20.0,
		InletFlow:   0.0,
	}
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate")

	// Temperature should stay in a reasonable range [10, 40] °C.
	for s := range 30 {
		for x := range 6 {
			for y := range 6 {
				tv := result.WaterTemp[s][x][y]
				if tv < 10 || tv > 40 {
					t.Fatalf("WaterTemp[%d][%d][%d] = %f, out of [10, 40]", s, x, y, tv)
				}
			}
		}
	}

	// DO should stay in [0, 20] mg/L.
	for s := range 30 {
		for x := range 6 {
			for y := range 6 {
				dv := result.DOConc[s][x][y]
				if dv < 0 || dv > 20 {
					t.Fatalf("DOConc[%d][%d][%d] = %f, out of [0, 20]", s, x, y, dv)
				}
			}
		}
	}

	// NH3 should stay in [0, 10] mg/L.
	for s := range 30 {
		for x := range 6 {
			for y := range 6 {
				nv := result.NH3Conc[s][x][y]
				if nv < 0 || nv > 10 {
					t.Fatalf("NH3Conc[%d][%d][%d] = %f, out of [0, 10]", s, x, y, nv)
				}
			}
		}
	}

	// Turbidity should stay in [0, 1000] NTU.
	for s := range 30 {
		for x := range 6 {
			for y := range 6 {
				tv := result.Turbidity[s][x][y]
				if tv < 0 || tv > 1000 {
					t.Fatalf("Turbidity[%d][%d][%d] = %f, out of [0, 1000]", s, x, y, tv)
				}
			}
		}
	}
}

func TestSimulationResult_nilValidation(t *testing.T) {
	var r *SimulationResult
	err := r.ValidateDimensions()
	assertError(t, err, "nil ValidateDimensions")
}

func TestSimulationResult_atBounds(t *testing.T) {
	result := &SimulationResult{
		GridSize:  2,
		TimeSteps: 2,
		WaterTemp: [][][]float64{
			{[]float64{1.0, 2.0}, []float64{3.0, 4.0}},
			{[]float64{5.0, 6.0}, []float64{7.0, 8.0}},
		},
	}

	val, ok := result.At(result.WaterTemp, 0, 0, 0)
	assertTrue(t, ok, "At(0,0,0)")
	assertEqual(t, val, 1.0, "At(0,0,0) value")

	_, ok = result.At(result.WaterTemp, -1, 0, 0)
	assertTrue(t, !ok, "At(-1,0,0) should be out of bounds")

	_, ok = result.At(result.WaterTemp, 0, 2, 0)
	assertTrue(t, !ok, "At(0,2,0) should be out of bounds")
}

// ============================================================================
// TestInvalidConfig — clear error on illegal inputs
// ============================================================================

func TestInvalidConfig_gridSizeTooSmall(t *testing.T) {
	cfg := quickCfg()
	cfg.GridSize = 1
	err := cfg.Validate()
	assertError(t, err, "GridSize=1")
	assertTrue(t, strings.Contains(err.Error(), "grid_size"), "error should mention grid_size")
}

func TestInvalidConfig_gridSizeTooLarge(t *testing.T) {
	cfg := quickCfg()
	cfg.GridSize = 65
	err := cfg.Validate()
	assertError(t, err, "GridSize=65")
	assertTrue(t, strings.Contains(err.Error(), "grid_size"), "error should mention grid_size")
}

func TestInvalidConfig_timeStepsZero(t *testing.T) {
	cfg := quickCfg()
	cfg.TimeSteps = 0
	err := cfg.Validate()
	assertError(t, err, "TimeSteps=0")
}

func TestInvalidConfig_stepMinutesZero(t *testing.T) {
	cfg := quickCfg()
	cfg.StepMinutes = 0
	err := cfg.Validate()
	assertError(t, err, "StepMinutes=0")
}

func TestInvalidConfig_waterDepthZero(t *testing.T) {
	cfg := quickCfg()
	cfg.WaterDepth = 0
	err := cfg.Validate()
	assertError(t, err, "WaterDepth=0")
}

func TestInvalidConfig_simulateInvalidGrid(t *testing.T) {
	engine := defaultEngine()
	cfg := quickCfg()
	cfg.GridSize = 0
	_, err := engine.Simulate(context.Background(), cfg)
	assertError(t, err, "Simulate with GridSize=0")
}

// ============================================================================
// TestDigitalTwinLifecycle — create → run → snapshot → delete
// ============================================================================

func TestDigitalTwinLifecycle_createRunSnapshotDelete(t *testing.T) {
	engine := defaultEngine()
	dt := NewDigitalTwin()
	cfg := quickCfg()
	ctx := context.Background()

	// CREATE
	err := dt.Create("tw-1", "pond-1", cfg, engine)
	assertNoError(t, err, "Create")
	assertTrue(t, dt.InstanceExists("tw-1"), "should exist after create")
	assertEqual(t, dt.InstanceCount(), 1, "count after create")

	// RUN
	err = dt.Run(ctx, "tw-1")
	assertNoError(t, err, "Run")

	// SNAPSHOT
	snap, err := dt.Snapshot("tw-1")
	assertNoError(t, err, "Snapshot")
	assertEqual(t, snap.InstanceID, "tw-1", "Snapshot instance ID")
	assertEqual(t, snap.PondID, "pond-1", "Snapshot pond ID")
	assertTrue(t, snap.Result != nil, "Snapshot should have result")

	// DELETE
	err = dt.Delete("tw-1")
	assertNoError(t, err, "Delete")
	assertTrue(t, !dt.InstanceExists("tw-1"), "should not exist after delete")
	assertEqual(t, dt.InstanceCount(), 0, "count after delete")
}

func TestDigitalTwin_createDuplicate(t *testing.T) {
	dt := NewDigitalTwin()
	cfg := quickCfg()

	err := dt.Create("dup", "pond", cfg, defaultEngine())
	assertNoError(t, err, "first Create")

	err = dt.Create("dup", "pond", cfg, defaultEngine())
	assertError(t, err, "duplicate Create")
	assertTrue(t, strings.Contains(err.Error(), "already exists"), "error should say already exists")
}

func TestDigitalTwin_runNonExistent(t *testing.T) {
	dt := NewDigitalTwin()
	err := dt.Run(context.Background(), "no-such")
	assertError(t, err, "Run non-existent")
}

func TestDigitalTwin_deleteNonExistent(t *testing.T) {
	dt := NewDigitalTwin()
	err := dt.Delete("no-such")
	assertError(t, err, "Delete non-existent")
}

func TestDigitalTwin_snapshotNonExistent(t *testing.T) {
	dt := NewDigitalTwin()
	_, err := dt.Snapshot("no-such")
	assertError(t, err, "Snapshot non-existent")
}

func TestDigitalTwin_idempotentRun(t *testing.T) {
	engine := defaultEngine()
	dt := NewDigitalTwin()
	cfg := quickCfg()

	err := dt.Create("idem", "pond-x", cfg, engine)
	assertNoError(t, err, "Create")

	err = dt.Run(context.Background(), "idem")
	assertNoError(t, err, "first Run")

	// Second Run should return nil without error (already completed).
	err = dt.Run(context.Background(), "idem")
	assertNoError(t, err, "second Run")
}

func TestDigitalTwin_concurrentInstances(t *testing.T) {
	dt := NewDigitalTwin()
	cfg := quickCfg()

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			instID := "conc-" + string(rune('A'+id))
			err := dt.Create(instID, "pond", cfg, defaultEngine())
			if err != nil {
				t.Errorf("concurrent Create %q: %v", instID, err)
				return
			}
			if err := dt.Run(context.Background(), instID); err != nil {
				t.Errorf("concurrent Run %q: %v", instID, err)
				return
			}
		}(i)
	}
	wg.Wait()

	assertEqual(t, dt.InstanceCount(), 10, "concurrent instance count")
}

func TestDigitalTwin_configValidation(t *testing.T) {
	dt := NewDigitalTwin()
	cfg := SimulationConfig{GridSize: 0} // invalid

	err := dt.Create("bad", "pond", cfg, defaultEngine())
	assertError(t, err, "Create with invalid config")
}

func TestDigitalTwin_nilEngine(t *testing.T) {
	dt := NewDigitalTwin()
	cfg := quickCfg()

	err := dt.Create("no-engine", "pond", cfg, nil)
	assertError(t, err, "Create with nil engine")
}

// ============================================================================
// TestDefaultConfig
// ============================================================================

func TestDefaultConfig_values(t *testing.T) {
	cfg := DefaultConfig()
	assertEqual(t, cfg.GridSize, 6, "GridSize")
	assertEqual(t, cfg.TimeSteps, 30, "TimeSteps")
	assertEqual(t, cfg.StepMinutes, 5, "StepMinutes")
	assertEqual(t, cfg.WaterDepth, 2.0, "WaterDepth")
	assertEqual(t, cfg.WindSpeed, 0.0, "WindSpeed")
	assertEqual(t, cfg.SolarFlux, 200.0, "SolarFlux")
	assertEqual(t, cfg.InletTemp, 20.0, "InletTemp")
	assertEqual(t, cfg.InletFlow, 0.0, "InletFlow")

	err := cfg.Validate()
	assertNoError(t, err, "DefaultConfig Validate")
}

// ============================================================================
// TestSimulationDuration
// ============================================================================

func TestSimulationDuration(t *testing.T) {
	cfg := quickCfg() // 30 steps × 5 min = 150 min = 9000s
	expected := 30.0 * 5.0 * 60.0
	assertEqual(t, cfg.SimulationDuration(), expected, "SimulationDuration")
}

// ============================================================================
// TestGridSpacing
// ============================================================================

func TestGridSpacing(t *testing.T) {
	cfg := quickCfg() // GridSize=6, spacing = 5*6/5 = 6.0
	assertEqual(t, cfg.GridSpacing(), 6.0, "GridSpacing")
}

// ============================================================================
// TestJSONSerialization
// ============================================================================

func TestConfigJSON(t *testing.T) {
	cfg := quickCfg()
	data, err := json.Marshal(cfg)
	assertNoError(t, err, "Marshal config")

	var restored SimulationConfig
	err = json.Unmarshal(data, &restored)
	assertNoError(t, err, "Unmarshal config")

	assertEqual(t, restored.GridSize, cfg.GridSize, "GridSize")
	assertEqual(t, restored.TimeSteps, cfg.TimeSteps, "TimeSteps")
	assertEqual(t, restored.WaterDepth, cfg.WaterDepth, "WaterDepth")
}

// ============================================================================
// TestPhysicalModels
// ============================================================================

func TestDoSaturation(t *testing.T) {
	// DO saturation decreases with temperature.
	do0 := doSaturation(0)
	do20 := doSaturation(20)
	do35 := doSaturation(35)

	assertTrue(t, do0 > do20, "DOsat(0) > DOsat(20)")
	assertTrue(t, do20 > do35, "DOsat(20) > DOsat(35)")
}

func TestTemperatureFactor(t *testing.T) {
	// At 20°C, factor should be 1.0.
	f20 := temperatureFactor(20.0)
	assertTrue(t, math.Abs(f20-1.0) < 0.01, "temperatureFactor(20) ≈ 1.0")

	// At higher temp, factor should increase.
	f30 := temperatureFactor(30.0)
	assertTrue(t, f30 > f20, "temperatureFactor(30) > temperatureFactor(20)")

	// Clamp at extremes.
	fNeg := temperatureFactor(-5)
	assertTrue(t, fNeg >= 0.05, "temperatureFactor(-5) >= 0.05")
}

// ============================================================================
// TestEdgeCases
// ============================================================================

func TestSimulation_minimalGrid(t *testing.T) {
	engine := defaultEngine()
	cfg := SimulationConfig{
		GridSize:    2,
		TimeSteps:   2,
		StepMinutes: 1,
		WaterDepth:  1.0,
	}
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate 2×2")

	if err := result.ValidateDimensions(); err != nil {
		t.Fatalf("2×2 ValidateDimensions: %v", err)
	}
}

func TestSimulation_noExternalForcing(t *testing.T) {
	// With zero wind, zero solar, zero inlet: fields should barely evolve.
	engine := defaultEngine()
	cfg := SimulationConfig{
		GridSize:    6,
		TimeSteps:   5,
		StepMinutes: 5,
		WaterDepth:  2.0,
		WindSpeed:   0.0,
		SolarFlux:   0.0,
		InletTemp:   20.0,
		InletFlow:   0.0,
	}
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "no-forcing simulate")

	// With zero forcing: temperature should be exactly unchanged
	// (no source, no advection, no diffusion for uniform fields).
	// DO, NH3, and turbidity change slightly due to internal source/sink
	// terms (consumption, decay, settling — not external forcing).
	for x := range 6 {
		for y := range 6 {
			if math.Abs(result.WaterTemp[4][x][y]-20.0) > 1e-12 {
				t.Fatalf("T changed at [%d][%d]: %.15f", x, y, result.WaterTemp[4][x][y])
			}
			// DO decreases from consumption (≈0.006 per step, 4 steps ≈0.024).
			if result.DOConc[4][x][y] >= 8.0 {
				t.Fatalf("DO should decrease from consumption, got %.15f at [%d][%d]",
					result.DOConc[4][x][y], x, y)
			}
			// NH3 decreases from decay.
			if result.NH3Conc[4][x][y] > 0.1 {
				t.Fatalf("NH3 should decrease from decay, got %.15f at [%d][%d]",
					result.NH3Conc[4][x][y], x, y)
			}
			// Turbidity decreases from settling (≈0.03 over 4 steps).
			if result.Turbidity[4][x][y] > 5.0 {
				t.Fatalf("Turbidity should decrease from settling, got %.15f at [%d][%d]",
					result.Turbidity[4][x][y], x, y)
			}
			if result.Turbidity[4][x][y] < 4.5 {
				t.Fatalf("Turbidity decreased too much: %.15f at [%d][%d]",
					result.Turbidity[4][x][y], x, y)
			}
		}
	}
}

func TestSimulation_largeGrid(t *testing.T) {
	engine := defaultEngine()
	cfg := SimulationConfig{
		GridSize:    20,
		TimeSteps:   5,
		StepMinutes: 1,
		WaterDepth:  2.0,
		WindSpeed:   3.0,
		SolarFlux:   200.0,
	}
	ctx := context.Background()

	result, err := engine.Simulate(ctx, cfg)
	assertNoError(t, err, "Simulate 20×20")

	if err := result.ValidateDimensions(); err != nil {
		t.Fatalf("20×20 ValidateDimensions: %v", err)
	}
}

// ============================================================================
// Benchmark
// ============================================================================

func BenchmarkHydroDynamics_default(b *testing.B) {
	engine := defaultEngine()
	cfg := quickCfg()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, err := engine.Simulate(ctx, cfg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ============================================================================
// Helper
// ============================================================================

func spatialMean(field [][]float64) float64 {
	sum := 0.0
	count := 0
	for x := range field {
		for _, v := range field[x] {
			sum += v
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

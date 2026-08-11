package visual

import (
	"testing"
)

func TestState(t *testing.T) {
	v := NewVisualizer(NewPondSimulator())
	st := v.State("pond-1")
	if st.PondID != "pond-1" {
		t.Fatalf("PondID = %q, want pond-1", st.PondID)
	}
	if st.TemperatureC <= 0 || st.DO <= 0 || st.Turbidity <= 0 || st.NH3 <= 0 {
		t.Fatalf("virtual state must have positive values: %+v", st)
	}
	if st.UpdatedAt == "" {
		t.Fatal("UpdatedAt must not be empty")
	}
}

func TestTrajectory(t *testing.T) {
	v := NewVisualizer(NewPondSimulator())
	tr, err := v.Trajectory("pond-1", HeatWaveScenario(), 0, 10)
	if err != nil {
		t.Fatalf("Trajectory: %v", err)
	}
	if tr.PondID != "pond-1" {
		t.Fatalf("PondID = %q", tr.PondID)
	}
	if len(tr.Points) == 0 {
		t.Fatal("expected trajectory points")
	}
	if tr.Total <= 0 {
		t.Fatal("Total must be positive")
	}
	// Heat wave: temperature should rise along the trajectory.
	first := tr.Points[0].TemperatureC
	last := tr.Points[len(tr.Points)-1].TemperatureC
	if last <= first {
		t.Fatalf("heat wave trajectory should warm up: first=%v last=%v", first, last)
	}
}

func TestTrajectoryPagination(t *testing.T) {
	v := NewVisualizer(NewPondSimulator())
	// offset beyond total -> empty points, no error.
	tr, err := v.Trajectory("pond-1", HeatWaveScenario(), 1000, 10)
	if err != nil {
		t.Fatalf("Trajectory: %v", err)
	}
	if len(tr.Points) != 0 {
		t.Fatalf("expected empty points beyond total, got %d", len(tr.Points))
	}
}

func TestCompare(t *testing.T) {
	v := NewVisualizer(NewPondSimulator())
	res, err := v.Compare([]string{"heatwave", "storm_flood", "cold_snap"})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("expected 3 compare results, got %d", len(res))
	}
	// Results must differ across scenarios (different final DO/temp).
	seen := map[string]bool{}
	for _, r := range res {
		if r.Scenario == "" {
			t.Fatal("scenario name empty")
		}
		if r.RiskLevel == "" {
			t.Fatal("risk level empty")
		}
		seen[r.Scenario] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 distinct scenarios, got %d", len(seen))
	}
}

func TestCompareUnknownScenario(t *testing.T) {
	v := NewVisualizer(NewPondSimulator())
	_, err := v.Compare([]string{"nuclear_winter"})
	if err == nil {
		t.Fatal("expected error for unknown scenario")
	}
}

func TestAnomalyDetected(t *testing.T) {
	v := NewVisualizer(NewPondSimulator())
	// Physical DO far below virtual baseline -> anomaly.
	rep := v.Anomaly("pond-1", PhysicalState{DO: 3.0, TemperatureC: 25.0, Turbidity: 12.0, NH3: 0.05})
	if rep.Status != "ANOMALY_DETECTED" {
		t.Fatalf("status = %q, want ANOMALY_DETECTED", rep.Status)
	}
	if len(rep.Deviations) == 0 {
		t.Fatal("expected deviation entries")
	}
	if rep.MaxDeviation <= 0 {
		t.Fatalf("MaxDeviation must be positive, got %v", rep.MaxDeviation)
	}
}

func TestAnomalyNormal(t *testing.T) {
	v := NewVisualizer(NewPondSimulator())
	// Physical state matching virtual baseline -> normal.
	rep := v.Anomaly("pond-1", PhysicalState{DO: 7.0, TemperatureC: 25.0, Turbidity: 12.0, NH3: 0.05})
	if rep.Status != "NORMAL" {
		t.Fatalf("status = %q, want NORMAL", rep.Status)
	}
}

func TestAnomalyThresholds(t *testing.T) {
	// DO threshold is 1.0 mg/L absolute deviation.
	if !exceedsThreshold("do", 7.0, 5.5) {
		t.Fatal("DO deviation 1.5 should exceed threshold")
	}
	if exceedsThreshold("do", 7.0, 6.5) {
		t.Fatal("DO deviation 0.5 should not exceed threshold")
	}
	// Temperature threshold is 2.0°C.
	if !exceedsThreshold("temperature_c", 25.0, 28.0) {
		t.Fatal("temp deviation 3.0 should exceed threshold")
	}
	if exceedsThreshold("temperature_c", 25.0, 26.0) {
		t.Fatal("temp deviation 1.0 should not exceed threshold")
	}
}
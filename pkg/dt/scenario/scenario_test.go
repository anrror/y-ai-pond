package scenario

import (
	"errors"
	"testing"
)

func TestHeatWaveScenario(t *testing.T) {
	r := NewRunner(NewPondSimulator())
	res, err := r.Evaluate(HeatWaveScenario())
	if err != nil {
		t.Fatalf("Evaluate heat wave: %v", err)
	}

	// SSP585 +4°C scenario must raise simulated water temperature.
	if res.FinalState.TemperatureC <= 25.0 {
		t.Fatalf("expected water temp to rise above baseline 25°C, got %v", res.FinalState.TemperatureC)
	}

	// Recommendation: reduce feeding + night feeding + aeration.
	rec := res.Recommendation
	if rec.FeedRateAdjustPct >= 0 {
		t.Fatalf("heat wave must reduce feeding rate, got %d%%", rec.FeedRateAdjustPct)
	}
	if !rec.NightFeeding {
		t.Fatal("heat wave should recommend night feeding")
	}
	if !rec.EnableAerator {
		t.Fatal("heat wave must enable aeration (high temp -> low DO solubility)")
	}
	if rec.RiskLevel != "HIGH" {
		t.Fatalf("heat wave should be HIGH risk, got %s", rec.RiskLevel)
	}
}

func TestFloodScenario(t *testing.T) {
	r := NewRunner(NewPondSimulator())
	res, err := r.Evaluate(StormFloodScenario())
	if err != nil {
		t.Fatalf("Evaluate flood: %v", err)
	}

	// Flood raises turbidity and depresses DO.
	if res.FinalState.Turbidity <= 20.0 {
		t.Fatalf("expected turbidity to rise above baseline, got %v", res.FinalState.Turbidity)
	}
	if res.FinalState.DO >= 7.0 {
		t.Fatalf("expected DO to drop below baseline 7.0, got %v", res.FinalState.DO)
	}

	// Conservative strategy (sensor degradation -> conservative feeding).
	rec := res.Recommendation
	if rec.FeedRateAdjustPct > -30 {
		t.Fatalf("flood should recommend conservative (strongly reduced) feeding, got %d%%", rec.FeedRateAdjustPct)
	}
	if !rec.EnableAerator {
		t.Fatal("flood (DO drop) should enable aeration")
	}
}

func TestColdSnapScenario(t *testing.T) {
	res, err := NewRunner(NewPondSimulator()).Evaluate(ColdSnapScenario())
	if err != nil {
		t.Fatalf("Evaluate cold snap: %v", err)
	}
	if res.FinalState.TemperatureC >= 25.0 {
		t.Fatalf("expected temp to drop below baseline, got %v", res.FinalState.TemperatureC)
	}
	if res.Recommendation.FeedRateAdjustPct >= 0 {
		t.Fatalf("cold snap must reduce feeding (slower metabolism), got %d%%", res.Recommendation.FeedRateAdjustPct)
	}
}

func TestScenarioRunnerParallelCompare(t *testing.T) {
	r := NewRunner(NewPondSimulator())
	res, err := r.Compare([]Scenario{
		HeatWaveScenario(),
		StormFloodScenario(),
		ColdSnapScenario(),
	})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("expected 3 scenario results, got %d", len(res))
	}
	for i, r := range res {
		if r.Recommendation.RiskLevel == "" {
			t.Fatalf("compare result %d: empty risk level", i)
		}
		if len(r.Recommendation.Rationale) == 0 {
			t.Fatalf("compare result %d: empty rationale", i)
		}
	}
}

func TestScenarioConfigurationIncomplete(t *testing.T) {
	r := NewRunner(NewPondSimulator())
	_, err := r.Evaluate(Scenario{Type: TypeHeatWave}) // missing duration
	if err == nil {
		t.Fatal("expected error for incomplete scenario configuration")
	}
	if !errors.Is(err, ErrScenarioIncomplete) {
		t.Fatalf("expected ErrScenarioIncomplete, got %v", err)
	}
}

func TestRunnerNilSimulator(t *testing.T) {
	if NewRunner(nil) != nil {
		t.Fatal("NewRunner(nil) must return nil")
	}
}

func TestRiskAssessment(t *testing.T) {
	// Extreme low DO => HIGH risk regardless of temperature.
	risk, score := assessRisk(WaterState{TemperatureC: 25, DO: 3.0, Turbidity: 10})
	if risk != "HIGH" {
		t.Fatalf("DO=3.0 should be HIGH risk, got %s", risk)
	}
	if score <= 0 {
		t.Fatalf("risk score must be positive, got %v", score)
	}

	// Normal water => LOW risk.
	risk, _ = assessRisk(WaterState{TemperatureC: 25, DO: 7.0, Turbidity: 12})
	if risk != "LOW" {
		t.Fatalf("normal water should be LOW risk, got %s", risk)
	}
}
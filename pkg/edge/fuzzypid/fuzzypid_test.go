package fuzzypid

import (
	"math"
	"testing"
)

// =========================================================================
// TestFuzzifier — verify membership function correctness at known points.
// =========================================================================

func TestFuzzifier_triangleMF_atPeak(t *testing.T) {
	mu := triangleMF(0.5, 0.25, 0.5, 0.75)
	if mu != 1.0 {
		t.Errorf("triangleMF(0.5) at peak = %v, want 1.0", mu)
	}
}

func TestFuzzifier_triangleMF_atEdge(t *testing.T) {
	mu := triangleMF(0.25, 0.25, 0.5, 0.75)
	if mu != 0 {
		t.Errorf("triangleMF(0.25) at left edge = %v, want 0", mu)
	}
}

func TestFuzzifier_triangleMF_outsideRange(t *testing.T) {
	mu := triangleMF(0.0, 0.25, 0.5, 0.75)
	if mu != 0 {
		t.Errorf("triangleMF(0.0) outside range = %v, want 0", mu)
	}
}

func TestFuzzifier_triangleMF_midpoint(t *testing.T) {
	mu := triangleMF(0.375, 0.25, 0.5, 0.75)
	if math.Abs(mu-0.5) > 1e-9 {
		t.Errorf("triangleMF(0.375) = %v, want 0.5", mu)
	}
}

func TestFuzzifier_trapezoidLeft_belowThreshold(t *testing.T) {
	mu := trapezoidLeft(0.05, 0.1, 0.3)
	if mu != 1.0 {
		t.Errorf("trapezoidLeft(0.05) = %v, want 1.0", mu)
	}
}

func TestFuzzifier_trapezoidLeft_aboveThreshold(t *testing.T) {
	mu := trapezoidLeft(0.5, 0.1, 0.3)
	if mu != 0 {
		t.Errorf("trapezoidLeft(0.5) = %v, want 0", mu)
	}
}

func TestFuzzifier_trapezoidLeft_midRamp(t *testing.T) {
	mu := trapezoidLeft(0.2, 0.1, 0.3)
	if math.Abs(mu-0.5) > 1e-9 {
		t.Errorf("trapezoidLeft(0.2) = %v, want 0.5", mu)
	}
}

func TestFuzzifier_trapezoidRight_belowThreshold(t *testing.T) {
	mu := trapezoidRight(0.5, 0.7, 0.9)
	if mu != 0 {
		t.Errorf("trapezoidRight(0.5) = %v, want 0", mu)
	}
}

func TestFuzzifier_trapezoidRight_aboveThreshold(t *testing.T) {
	mu := trapezoidRight(0.95, 0.7, 0.9)
	if mu != 1.0 {
		t.Errorf("trapezoidRight(0.95) = %v, want 1.0", mu)
	}
}

func TestFuzzifier_fuzzify_xZero(t *testing.T) {
	f := &Fuzzifier{}
	res := f.fuzzifyVar(0.0)

	if res[VL] != 1.0 {
		t.Errorf("fuzzify(0).VL = %v, want 1.0", res[VL])
	}
	if res[M] != 0 {
		t.Errorf("fuzzify(0).M = %v, want 0", res[M])
	}
	if res[VH] != 0 {
		t.Errorf("fuzzify(0).VH = %v, want 0", res[VH])
	}
}

func TestFuzzifier_fuzzify_xOne(t *testing.T) {
	f := &Fuzzifier{}
	res := f.fuzzifyVar(1.0)

	if res[VH] != 1.0 {
		t.Errorf("fuzzify(1).VH = %v, want 1.0", res[VH])
	}
	if res[VL] != 0 {
		t.Errorf("fuzzify(1).VL = %v, want 0", res[VL])
	}
}

func TestFuzzifier_fuzzify_xMid(t *testing.T) {
	f := &Fuzzifier{}
	res := f.fuzzifyVar(0.5)

	if res[M] != 1.0 {
		t.Errorf("fuzzify(0.5).M = %v, want 1.0", res[M])
	}
	// At x=0.5 the L-triangle (0.1,0.25,0.5) is at right boundary (x≥c → 0)
	// and H-triangle (0.5,0.75,0.9) is at left boundary (x≤a → 0).
	if res[L] != 0 {
		t.Errorf("fuzzify(0.5).L = %v, want 0 (at right edge of L triangle)", res[L])
	}
	if res[H] != 0 {
		t.Errorf("fuzzify(0.5).H = %v, want 0 (at left edge of H triangle)", res[H])
	}
}

func TestFuzzifier_fuzzify_xOverlapRegion(t *testing.T) {
	f := &Fuzzifier{}
	// At x=0.625 the M and H triangles overlap.
	res := f.fuzzifyVar(0.625)

	if res[M] <= 0 {
		t.Errorf("fuzzify(0.625).M = %v, want > 0", res[M])
	}
	if res[H] <= 0 {
		t.Errorf("fuzzify(0.625).H = %v, want > 0", res[H])
	}
	// Should be approximately 0.5 each.
	if math.Abs(res[M]-0.5) > 0.01 {
		t.Errorf("fuzzify(0.625).M = %v, want ~0.5", res[M])
	}
	if math.Abs(res[H]-0.5) > 0.01 {
		t.Errorf("fuzzify(0.625).H = %v, want ~0.5", res[H])
	}
}

func TestFuzzifier_InputNormalize(t *testing.T) {
	in := &Input{
		Density:          0.6,
		Size:             0.7,
		FeedingIntensity: 0.8,
		DO:               8.0,
		Temp:             25.0,
		NH3:              2.0,
	}
	ni := in.Normalize()

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"Density", ni.Density, 0.6},
		{"Size", ni.Size, 0.7},
		{"FeedingIntensity", ni.FeedingIntensity, 0.8},
		{"DO", ni.DO, 0.4},   // 8/20
		{"Temp", ni.Temp, 0.5}, // 25/50
		{"NH3", ni.NH3, 0.2},   // 2/10
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestFuzzifier_InputNormalize_clamps(t *testing.T) {
	in := &Input{
		Density:          -0.5,
		Size:             1.5,
		FeedingIntensity: 2.0,
		DO:               50.0,
		Temp:             -5.0,
		NH3:              15.0,
	}
	ni := in.Normalize()

	expect := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%s clamp = %v, want %v", name, got, want)
		}
	}
	expect("Density", ni.Density, 0)
	expect("Size", ni.Size, 1)
	expect("FeedingIntensity", ni.FeedingIntensity, 1)
	expect("DO", ni.DO, 1)
	expect("Temp", ni.Temp, 0)
	expect("NH3", ni.NH3, 1)
}

// =========================================================================
// TestRuleBase — verify rule count, representation, and inference.
// =========================================================================

func TestRuleBase_count(t *testing.T) {
	rb := DefaultRuleBase()
	n := len(rb.Rules)
	if n < 25 {
		t.Errorf("rule count = %d, want >= 25", n)
	}
	if n > 50 {
		t.Errorf("rule count = %d, want <= 50", n)
	}
}

func TestRuleBase_everyActionRepresented(t *testing.T) {
	rb := DefaultRuleBase()
	seen := map[OutputAction]bool{}
	for _, r := range rb.Rules {
		seen[r.Consequent] = true
	}
	for _, a := range []OutputAction{STOP, DECREASE, HOLD, INCREASE, MAX} {
		if !seen[a] {
			t.Errorf("no rule with consequent %v", a)
		}
	}
}

func TestRuleBase_infer_singleRuleFires(t *testing.T) {
	rb := &RuleBase{Rules: []Rule{
		{Antecedents: a(VarDensity, M), Consequent: INCREASE},
	}}
	mm := &MembershipMatrix{}
	mm.Density[M] = 1.0
	agg := rb.Infer(mm)

	if agg[INCREASE] != 1.0 {
		t.Errorf("INCREASE = %v, want 1.0", agg[INCREASE])
	}
}

func TestRuleBase_infer_minAcrossAntecedents(t *testing.T) {
	rb := &RuleBase{Rules: []Rule{
		{Antecedents: a(VarDensity, M, VarDO, M), Consequent: INCREASE},
	}}
	mm := &MembershipMatrix{}
	mm.Density[M] = 0.8
	mm.DO[M] = 0.3
	agg := rb.Infer(mm)

	if agg[INCREASE] != 0.3 {
		t.Errorf("min(0.8,0.3) = %v, want 0.3", agg[INCREASE])
	}
}

func TestRuleBase_infer_maxAggregation(t *testing.T) {
	rb := &RuleBase{Rules: []Rule{
		{Antecedents: a(VarDensity, M), Consequent: INCREASE},
		{Antecedents: a(VarDO, H), Consequent: INCREASE},
	}}
	mm := &MembershipMatrix{}
	mm.Density[M] = 0.4
	mm.DO[H] = 0.7
	agg := rb.Infer(mm)

	if agg[INCREASE] != 0.7 {
		t.Errorf("max(0.4,0.7) = %v, want 0.7", agg[INCREASE])
	}
}

func TestRuleBase_mixedConsequents(t *testing.T) {
	rb := &RuleBase{Rules: []Rule{
		{Antecedents: a(VarDensity, M), Consequent: INCREASE},
		{Antecedents: a(VarDO, L), Consequent: DECREASE},
	}}
	mm := &MembershipMatrix{}
	mm.Density[M] = 0.6
	mm.DO[L] = 0.9
	agg := rb.Infer(mm)

	if agg[INCREASE] != 0.6 {
		t.Errorf("INCREASE = %v, want 0.6", agg[INCREASE])
	}
	if agg[DECREASE] != 0.9 {
		t.Errorf("DECREASE = %v, want 0.9", agg[DECREASE])
	}
}

func TestRuleBase_defaultRules_produceNonZeroActivation(t *testing.T) {
	rb := DefaultRuleBase()
	f := &Fuzzifier{}
	ni := NormalizedInput{
		Density:          0.6,
		Size:             0.6,
		FeedingIntensity: 0.8,
		DO:               0.5,
		Temp:             0.5,
		NH3:              0.2,
	}
	mm := f.Fuzzify(ni)
	agg := rb.Infer(mm)

	total := 0.0
	for _, s := range agg {
		total += s
	}
	if total <= 0 {
		t.Error("default rules produced no activation for normal inputs")
	}
}

// =========================================================================
// TestDefuzzifier — verify COG output within expected ranges.
// =========================================================================

func TestDefuzzifier_cog_hold(t *testing.T) {
	d := NewDefuzzifier(101)
	agg := map[OutputAction]float64{HOLD: 1.0}
	result := d.Defuzzify(agg)
	if result < 40 || result > 60 {
		t.Errorf("COG(HOLD=1.0) = %v, want ~50", result)
	}
}

func TestDefuzzifier_cog_allZero(t *testing.T) {
	d := NewDefuzzifier(101)
	agg := map[OutputAction]float64{STOP: 0, DECREASE: 0, HOLD: 0, INCREASE: 0, MAX: 0}
	result := d.Defuzzify(agg)
	if result != 0 {
		t.Errorf("COG(all zero) = %v, want 0", result)
	}
}

func TestDefuzzifier_cog_decreaseHold(t *testing.T) {
	d := NewDefuzzifier(101)
	agg := map[OutputAction]float64{DECREASE: 0.5, HOLD: 0.5}
	result := d.Defuzzify(agg)
	if result < 25 || result > 50 {
		t.Errorf("COG(DECREASE=0.5,HOLD=0.5) = %v, want in [25, 50]", result)
	}
}

func TestDefuzzifier_cog_increase(t *testing.T) {
	d := NewDefuzzifier(101)
	agg := map[OutputAction]float64{INCREASE: 1.0}
	result := d.Defuzzify(agg)
	if result < 65 || result > 85 {
		t.Errorf("COG(INCREASE=1.0) = %v, want ~75", result)
	}
}

func TestDefuzzifier_cog_stop(t *testing.T) {
	d := NewDefuzzifier(101)
	agg := map[OutputAction]float64{STOP: 1.0}
	result := d.Defuzzify(agg)
	if result > 20 {
		t.Errorf("COG(STOP=1.0) = %v, want <= 20", result)
	}
}

func TestDefuzzifier_cog_max(t *testing.T) {
	d := NewDefuzzifier(101)
	agg := map[OutputAction]float64{MAX: 1.0}
	result := d.Defuzzify(agg)
	if result < 80 || result > 100 {
		t.Errorf("COG(MAX=1.0) = %v, want >= 80", result)
	}
}

// =========================================================================
// TestFuzzyPIDStep — end-to-end: given inputs → reasonable PWM.
// =========================================================================

func TestFuzzyPIDStep_normalWaterActiveFish(t *testing.T) {
	ctrl := NewDefault()
	ctrl.Reset()

	in := &Input{
		Density:          0.7,
		Size:             0.6,
		FeedingIntensity: 0.8,
		DO:               7.0,
		Temp:             25.0,
		NH3:              0.5,
	}

	var pwm float64
	for i := 0; i < 5; i++ {
		var override bool
		pwm, override = ctrl.Step(in)
		if override {
			t.Fatal("normal conditions: unexpected safety override")
		}
	}
	if pwm < 60 {
		t.Errorf("normal+active: final PWM = %v, want >= 60", pwm)
	}
}

func TestFuzzyPIDStep_badWaterInactiveFish(t *testing.T) {
	ctrl := NewDefault()
	ctrl.Reset()

	in := &Input{
		Density:          0.2,
		Size:             0.3,
		FeedingIntensity: 0.1,
		DO:               4.5,
		Temp:             35.0,
		NH3:              3.0,
	}

	var pwm float64
	for i := 0; i < 5; i++ {
		var override bool
		pwm, override = ctrl.Step(in)
		// DO=4.5 > 4.0 and Temp=35 < 38 → no override expected.
		if override {
			t.Errorf("step %d: unexpected safety override (DO=%.1f, Temp=%.1f)", i, in.DO, in.Temp)
		}
	}
	if pwm > 20 {
		t.Errorf("bad+inactive: final PWM = %v, want <= 20", pwm)
	}
}

// =========================================================================
// TestSafetyInterlock — verify safety rules override fuzzy output.
// =========================================================================

func TestSafetyInterlock_lowDO_overrides(t *testing.T) {
	ctrl := NewDefault()
	ctrl.Reset()

	in := &Input{
		Density:          0.8,
		Size:             0.7,
		FeedingIntensity: 0.9,
		DO:               3.5,
		Temp:             25.0,
		NH3:              0.1,
	}

	pwm, override := ctrl.Step(in)
	if !override {
		t.Error("DO=3.5: safety interlock should trigger")
	}
	if pwm != 0 {
		t.Errorf("DO=3.5: PWM = %v, want 0", pwm)
	}
}

func TestSafetyInterlock_highTemp_overrides(t *testing.T) {
	ctrl := NewDefault()
	ctrl.Reset()

	in := &Input{
		Density:          0.3,
		Size:             0.4,
		FeedingIntensity: 0.5,
		DO:               8.0,
		Temp:             39.0,
		NH3:              0.2,
	}

	pwm, override := ctrl.Step(in)
	if !override {
		t.Error("Temp=39.0: safety interlock should trigger")
	}
	if pwm != 0 {
		t.Errorf("Temp=39.0: PWM = %v, want 0", pwm)
	}
}

func TestSafetyInterlock_normalConditions_noOverride(t *testing.T) {
	ctrl := NewDefault()
	ctrl.Reset()

	in := &Input{
		Density:          0.5,
		Size:             0.5,
		FeedingIntensity: 0.5,
		DO:               8.0,
		Temp:             25.0,
		NH3:              0.5,
	}

	_, override := ctrl.Step(in)
	if override {
		t.Error("normal conditions: safety interlock should NOT trigger")
	}
}

func TestSafetyInterlock_boundaryDO(t *testing.T) {
	ctrl := NewDefault()
	ctrl.Reset()

	in := &Input{
		Density:          0.5,
		Size:             0.5,
		FeedingIntensity: 0.5,
		DO:               4.0,
		Temp:             25.0,
		NH3:              0.5,
	}

	_, override := ctrl.Step(in)
	if override {
		t.Error("DO=4.0 exactly: should NOT trigger (< 4.0 only)")
	}
}

func TestSafetyInterlock_boundaryTemp(t *testing.T) {
	ctrl := NewDefault()
	ctrl.Reset()

	in := &Input{
		Density:          0.5,
		Size:             0.5,
		FeedingIntensity: 0.5,
		DO:               8.0,
		Temp:             38.0,
		NH3:              0.5,
	}

	_, override := ctrl.Step(in)
	if override {
		t.Error("Temp=38.0 exactly: should NOT trigger (> 38.0 only)")
	}
}

// =========================================================================
// PID unit tests.
// =========================================================================

func TestPID_step_single(t *testing.T) {
	p := NewPID(1.0, 0.0, 0.0, 0, 100)
	out := p.Step(50, 0)
	if out != 50 {
		t.Errorf("PID(50,0) Kp=1: %v, want 50", out)
	}
}

func TestPID_step_integralAccumulates(t *testing.T) {
	p := NewPID(0.5, 0.2, 0.0, 0, 100)
	// Step 1: e=50, int=50, du=0.5*50+0.2*50=35, out=35
	out1 := p.Step(50, 0)
	// Step 2: e=50, int=100, du=0.5*50+0.2*100=45, out=80
	out2 := p.Step(50, 0)
	if out1 < 30 || out1 > 40 {
		t.Errorf("step1 = %v, want ~35", out1)
	}
	if out2 < 75 || out2 > 85 {
		t.Errorf("step2 = %v, want ~80", out2)
	}
}

func TestPID_step_upperClamp(t *testing.T) {
	p := NewPID(1.0, 0.0, 0.0, 0, 100)
	p.Step(200, 0)   // du=200, out=100 (clamped)
	out := p.Step(200, 100) // e=100, du=100, out=100 (still clamped)
	if out != 100 {
		t.Errorf("upper clamp: %v, want 100", out)
	}
}

func TestPID_step_lowerClamp(t *testing.T) {
	p := NewPID(0.5, 0.0, 0.0, 0, 100)
	p.Step(-50, 0)   // du=-25, clamped to 0
	out := p.Step(-50, 0) // du=-25 again, still 0
	if out != 0 {
		t.Errorf("lower clamp: %v, want 0", out)
	}
}

func TestPID_step_derivative(t *testing.T) {
	// Kd=0.5, Kp=0, Ki=0: output = Σ Kd * (e(k) - e(k-1))
	p := NewPID(0.0, 0.0, 0.5, 0, 100)
	out1 := p.Step(10, 0) // e=10, de=10, du=5, out=5
	if out1 != 5 {
		t.Errorf("step1: %v, want 5", out1)
	}
	out2 := p.Step(15, 0) // e=15, de=5, du=2.5, out=7.5
	if math.Abs(out2-7.5) > 1e-9 {
		t.Errorf("step2: %v, want 7.5", out2)
	}
}

// =========================================================================
// SafetyOverride interface compliance.
// =========================================================================

func TestSafetyOverride_satisfiesInterface(t *testing.T) {
	var s SafetyOverrideInterface = &SafetyOverride{
		DOThreshold:   4.0,
		TempThreshold: 38.0,
	}
	triggered, val := s.IsTriggered(&Input{DO: 3.0, Temp: 25.0})
	if !triggered || val != 0 {
		t.Errorf("IsTriggered(DO=3.0) = (%v, %v), want (true, 0)", triggered, val)
	}
}

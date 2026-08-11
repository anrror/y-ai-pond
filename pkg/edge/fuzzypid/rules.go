package fuzzypid

// Rule represents a single Mamdani fuzzy production rule.
// All Antecedents are AND-connected (conjunction).
type Rule struct {
	Antecedents []Antecedent
	Consequent  OutputAction
}

// Antecedent is a single fuzzy condition: "Variable IS Level".
type Antecedent struct {
	Variable Variable
	Level    MembershipLevel
}

// RuleBase holds the complete set of fuzzy rules.
type RuleBase struct {
	Rules []Rule
}

// Infer performs Mamdani min-max inference on the given membership matrix.
//
// For each rule:
//   - Firing strength = min(all antecedent membership values)  (AND = min).
//   - Implication: clip output at firing strength  (Mamdani = min).
//
// Aggregation (across rules with the same consequent):
//   - Max union (max of clipped values).
func (rb *RuleBase) Infer(mm *MembershipMatrix) map[OutputAction]float64 {
	agg := map[OutputAction]float64{
		STOP: 0, DECREASE: 0, HOLD: 0, INCREASE: 0, MAX: 0,
	}

	for _, rule := range rb.Rules {
		fire := 1.0
		for _, ant := range rule.Antecedents {
			mu := mm.Get(ant.Variable, ant.Level)
			if mu < fire {
				fire = mu
			}
		}

		// Max union across rules with the same consequent.
		if fire > agg[rule.Consequent] {
			agg[rule.Consequent] = fire
		}
	}

	return agg
}

// DefaultRuleBase returns the standard 30-rule Mamdani rule base covering
// the 6-input fuzzy space for aquaculture feeding control.
//
// Rule groups (in priority order):
//   1-5:   Safety-critical (DO/Temp/NH₃ extremes → STOP).
//   6-13:  Low density scenarios.
//   14-18: Medium density scenarios.
//   19-24: High density scenarios.
//   25-27: Water quality interactions (DO + density + appetite).
//   28-29: Fish size interactions.
//   30:    Temperature constraint.
func DefaultRuleBase() *RuleBase {
	return &RuleBase{Rules: []Rule{
		// ── Safety-critical rules ─────────────────────────────────────────
		{Antecedents: a(VarDO, VL), Consequent: STOP},                       // 1
		{Antecedents: a(VarTemp, VH), Consequent: STOP},                     // 2
		{Antecedents: a(VarNH3, VH), Consequent: STOP},                      // 3
		{Antecedents: a(VarDO, L, VarNH3, H), Consequent: STOP},            // 4
		{Antecedents: a(VarTemp, H, VarDO, L), Consequent: STOP},           // 5

		// ── Low density ───────────────────────────────────────────────────
		{Antecedents: a(VarDensity, VL, VarFeedingIntensity, VL), Consequent: DECREASE}, // 6
		{Antecedents: a(VarDensity, VL, VarFeedingIntensity, L), Consequent: DECREASE},  // 7
		{Antecedents: a(VarDensity, VL, VarFeedingIntensity, M), Consequent: HOLD},      // 8
		{Antecedents: a(VarDensity, L, VarFeedingIntensity, VL), Consequent: DECREASE},  // 9
		{Antecedents: a(VarDensity, L, VarFeedingIntensity, L), Consequent: HOLD},       // 10
		{Antecedents: a(VarDensity, L, VarFeedingIntensity, M), Consequent: HOLD},       // 11
		{Antecedents: a(VarDensity, L, VarFeedingIntensity, H), Consequent: INCREASE},   // 12
		{Antecedents: a(VarDensity, L, VarFeedingIntensity, VH), Consequent: INCREASE},  // 13

		// ── Medium density ────────────────────────────────────────────────
		{Antecedents: a(VarDensity, M, VarFeedingIntensity, VL), Consequent: HOLD},      // 14
		{Antecedents: a(VarDensity, M, VarFeedingIntensity, L), Consequent: HOLD},       // 15
		{Antecedents: a(VarDensity, M, VarFeedingIntensity, M), Consequent: INCREASE},   // 16
		{Antecedents: a(VarDensity, M, VarFeedingIntensity, H), Consequent: INCREASE},   // 17
		{Antecedents: a(VarDensity, M, VarFeedingIntensity, VH), Consequent: MAX},       // 18

		// ── High density ──────────────────────────────────────────────────
		{Antecedents: a(VarDensity, H, VarFeedingIntensity, L), Consequent: INCREASE},   // 19
		{Antecedents: a(VarDensity, H, VarFeedingIntensity, M), Consequent: INCREASE},   // 20
		{Antecedents: a(VarDensity, H, VarFeedingIntensity, H), Consequent: MAX},        // 21
		{Antecedents: a(VarDensity, H, VarFeedingIntensity, VH), Consequent: MAX},       // 22
		{Antecedents: a(VarDensity, VH, VarFeedingIntensity, M), Consequent: MAX},       // 23
		{Antecedents: a(VarDensity, VH, VarFeedingIntensity, H), Consequent: MAX},       // 24

		// ── Water quality interactions ────────────────────────────────────
		{Antecedents: a(VarDO, L, VarFeedingIntensity, H), Consequent: DECREASE},                                  // 25
		{Antecedents: a(VarDO, M, VarDensity, H, VarFeedingIntensity, H), Consequent: INCREASE},                   // 26
		{Antecedents: a(VarDO, H, VarDensity, M, VarFeedingIntensity, M), Consequent: INCREASE},                   // 27

		// ── Fish size interactions ────────────────────────────────────────
		{Antecedents: a(VarSize, L, VarFeedingIntensity, M), Consequent: HOLD},                                    // 28
		{Antecedents: a(VarSize, H, VarFeedingIntensity, M), Consequent: INCREASE},                                // 29

		// ── Temperature constraint ────────────────────────────────────────
		{Antecedents: a(VarTemp, H, VarFeedingIntensity, H), Consequent: HOLD},                                    // 30
	}}
}

// a builds an antecedent slice from alternating Variable-level pairs.
// Usage: a(VarDensity, M, VarDO, H) → two antecedents.
func a(pairs ...interface{}) []Antecedent {
	ants := make([]Antecedent, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		v, ok1 := pairs[i].(Variable)
		l, ok2 := pairs[i+1].(MembershipLevel)
		if ok1 && ok2 {
			ants = append(ants, Antecedent{Variable: v, Level: l})
		}
	}
	return ants
}

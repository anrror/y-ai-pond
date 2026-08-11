// Package recommend implements the AI recommendation engine that combines
// time-series forecasting (T20), growth modeling (T21), and reinforcement
// learning (T22) outputs into structured, advisory feeding recommendations.
//
// The engine is advisory only — it never auto-executes recommendations or
// publishes MQTT commands. The farmer retains final decision authority.
// At low confidence (< 0.7), recommendations are flagged for manual review
// and risk is elevated to HIGH.
//
// Core design:
//   - Start from RL action (feeding_rate ∈ [0,1]) as the base.
//   - Adjust using forecast trends (DO decline → aerate; temp rise → reduce).
//   - Adjust using growth model (lagging growth → adjust feeding/density).
//   - Compute confidence from model readiness and data sufficiency.
//   - Graceful degradation: nil models → rule-based fallback with lower confidence.
//
// API:
//   - POST /api/v1/recommend/feeding → FeedingRecommendation
//   - GET  /api/v1/recommend/daily   → DailyRecommendation
package recommend

import (
	"fmt"
	"math"
	"time"

	"github.com/anrror/y-ai-pond/pkg/cloud/forecast"
	"github.com/anrror/y-ai-pond/pkg/cloud/growth"
	"github.com/anrror/y-ai-pond/pkg/cloud/rl"
)

// ============================================================================
// Risk level
// ============================================================================

// RiskLevel classifies the urgency of a recommendation.
type RiskLevel string

const (
	// RiskLow indicates normal conditions; the recommendation is routine.
	RiskLow RiskLevel = "LOW"

	// RiskMedium indicates an adverse trend or moderate concern.
	RiskMedium RiskLevel = "MEDIUM"

	// RiskHigh indicates a critical condition (low DO, high NH3, high temp)
	// or low confidence that demands immediate attention.
	RiskHigh RiskLevel = "HIGH"
)

// ============================================================================
// Action types
// ============================================================================

// ActionType enumerates the concrete actions a recommendation can suggest.
type ActionType string

const (
	ActionAerate          ActionType = "AERATE"
	ActionReduceFeeding   ActionType = "REDUCE_FEEDING"
	ActionIncreaseFeeding ActionType = "INCREASE_FEEDING"
	ActionHold            ActionType = "HOLD"
	ActionManualReview    ActionType = "MANUAL_REVIEW"
	ActionAdjustDensity   ActionType = "ADJUST_DENSITY"
	ActionMonitorWater    ActionType = "MONITOR_WATER"
)

// RecommendationAction is a single suggested action with a priority.
// Lower Priority values indicate more urgent actions.
type RecommendationAction struct {
	// Type is the action identifier (e.g. "AERATE", "REDUCE_FEEDING").
	Type ActionType `json:"type"`

	// Description is a human-readable explanation.
	Description string `json:"description"`

	// Priority is the urgency (1 = most urgent).
	Priority int `json:"priority"`
}

// ============================================================================
// Core recommendation types
// ============================================================================

// FeedingRecommendation is the structured output of the AI recommendation
// engine for a single feeding moment. It combines RL strategy, forecast
// adjustments, and growth projections into an advisory action.
type FeedingRecommendation struct {
	// PondID identifies the target pond.
	PondID string `json:"pond_id"`

	// FeedingRate is the recommended feeding proportion [0, 1].
	FeedingRate float64 `json:"feeding_rate"`

	// ExpectedGrowthGPerDay is the projected daily weight gain in grams.
	ExpectedGrowthGPerDay float64 `json:"expected_growth_g_per_day"`

	// RiskLevel indicates the overall risk classification.
	RiskLevel RiskLevel `json:"risk_level"`

	// Confidence is the engine's self-assessed reliability [0, 1].
	// Values below 0.7 trigger RequiresManualReview.
	Confidence float64 `json:"confidence"`

	// Actions lists the suggested concrete actions, ordered by priority.
	Actions []RecommendationAction `json:"actions"`

	// Reason is a human-readable explanation of the recommendation logic.
	Reason string `json:"reason"`

	// RequiresManualReview is true when confidence < 0.7, indicating the
	// farmer should verify the recommendation before applying it.
	RequiresManualReview bool `json:"requires_manual_review"`
}

// DailyRecommendation aggregates feeding recommendations for a single day.
type DailyRecommendation struct {
	// Date is the ISO date string (YYYY-MM-DD).
	Date string `json:"date"`

	// PondID identifies the target pond.
	PondID string `json:"pond_id"`

	// Feedings is the list of individual feeding recommendations.
	Feedings []FeedingRecommendation `json:"feedings"`

	// Summary is a human-readable overview.
	Summary string `json:"summary"`
}

// ============================================================================
// Input types
// ============================================================================

// StateInput holds the current pond state for a recommendation.
type StateInput struct {
	PondID          string  `json:"pond_id"`
	DO              float64 `json:"do_mg_l"`          // dissolved oxygen (mg/L)
	Temp            float64 `json:"temp_c"`           // water temperature (°C)
	NH3             float64 `json:"nh3_mg_l"`         // ammonia (mg/L)
	FishWeight      float64 `json:"fish_weight_g"`    // average fish weight (g)
	FCR             float64 `json:"fcr"`              // feed conversion ratio
	Species         string  `json:"species"`          // fish species name
	StockingDensity float64 `json:"stocking_density"` // fish per m³
}

// ============================================================================
// RecommendEngine
// ============================================================================

// RecommendEngine combines forecast, growth, and RL models to generate
// structured feeding recommendations. All sub-engines are optional; when a
// sub-engine is nil the engine falls back to rule-based logic with lower
// confidence.
type RecommendEngine struct {
	forecastEngine forecast.ForecastEngine
	growthModel    growth.GrowthModel
	rlEngine       *rl.PolicyEngine
}

// EngineOption is a functional option for NewRecommendEngine.
type EngineOption func(*RecommendEngine)

// WithForecast sets the forecast engine for trend-based adjustments.
func WithForecast(fe forecast.ForecastEngine) EngineOption {
	return func(e *RecommendEngine) {
		e.forecastEngine = fe
	}
}

// WithGrowth sets the growth model for weight/FCR projections.
func WithGrowth(gm growth.GrowthModel) EngineOption {
	return func(e *RecommendEngine) {
		e.growthModel = gm
	}
}

// WithRL sets the RL policy engine for strategy-based feeding rate.
func WithRL(rle *rl.PolicyEngine) EngineOption {
	return func(e *RecommendEngine) {
		e.rlEngine = rle
	}
}

// NewRecommendEngine creates a RecommendEngine with the given options.
// All sub-engines are nil by default (graceful degradation).
func NewRecommendEngine(opts ...EngineOption) *RecommendEngine {
	e := &RecommendEngine{}
	for _, o := range opts {
		o(e)
	}
	return e
}

// ============================================================================
// RecommendFeeding
// ============================================================================

// RecommendFeeding generates a single feeding recommendation from the current
// state and optional model outputs.
//
// Parameters:
//   - state: current pond conditions (required)
//   - doForecasts: DO time-series forecasts (may be nil/empty)
//   - tempForecasts: temperature forecasts (may be nil/empty)
//   - nh3Forecasts: ammonia forecasts (may be nil/empty)
//   - growthResult: growth model prediction (may be nil)
//
// Never panics. All nil inputs are handled gracefully.
func (e *RecommendEngine) RecommendFeeding(
	state StateInput,
	doForecasts []forecast.Forecast,
	tempForecasts []forecast.Forecast,
	nh3Forecasts []forecast.Forecast,
	growthResult *growth.GrowthResult,
) *FeedingRecommendation {
	rec := &FeedingRecommendation{
		PondID:      state.PondID,
		FeedingRate: 0.5, // sensible default
		RiskLevel:   RiskLow,
		Confidence:  0.3, // baseline: rule-based only
		Actions:     nil,
		Reason:      "",
	}

	// --- Step 1: RL-based feeding rate ---
	rlAvailable := e.rlEngine != nil
	if rlAvailable {
		rlState := []float64{state.DO, state.Temp, state.NH3, state.FishWeight, state.FCR}
		rate, err := e.rlEngine.Predict(rlState)
		if err == nil {
			rec.FeedingRate = rate
			rec.Confidence += 0.25 // RL contributes confidence
		}
		// On error, keep the default rate.
	} else {
		// Fallback: rule-based feeding rate from current state.
		rec.FeedingRate = ruleBasedFeedingRate(state)
	}

	// --- Step 2: Forecast-based adjustments ---
	forecastAvailable := len(doForecasts) > 0 || len(tempForecasts) > 0 || len(nh3Forecasts) > 0
	if forecastAvailable {
		rec.Confidence += 0.25
		e.applyForecastAdjustments(rec, state, doForecasts, tempForecasts, nh3Forecasts)
	}

	// --- Step 3: Growth-based adjustments ---
	if growthResult != nil {
		rec.Confidence += 0.25
		e.applyGrowthAdjustments(rec, state, growthResult)
	}

	// --- Step 4: Anomaly detection on current state ---
	e.applyCurrentStateAnomalies(rec, state)

	// --- Step 5: Risk assessment ---
	rec.RiskLevel = assessRisk(rec, state, doForecasts, nh3Forecasts)

	// --- Step 6: Clamp and finalize ---
	rec.FeedingRate = clampTo(rec.FeedingRate, 0, 1)
	rec.Confidence = clampTo(rec.Confidence, 0, 1)
	rec.RequiresManualReview = rec.Confidence < 0.7

	if rec.RequiresManualReview {
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionManualReview,
			Description: "confidence below 0.7; verify recommendation before applying",
			Priority:    1,
		})
	}

	// Ensure at least one action.
	if len(rec.Actions) == 0 {
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionHold,
			Description: "maintain current feeding strategy",
			Priority:    5,
		})
	}

	// Sort actions by priority.
	sortActions(rec.Actions)

	return rec
}

// ============================================================================
// PredictGrowth is a helper that runs the growth model for the recommend engine.
// Returns nil if the growth model is not configured or the species is unknown.
// ============================================================================

// PredictGrowth runs the growth model for a given state and duration.
// Returns nil if the growth model is nil, the species is unknown, or
// the prediction fails.
func (e *RecommendEngine) PredictGrowth(state StateInput, env growth.Environment, duration time.Duration) *growth.GrowthResult {
	if e.growthModel == nil {
		return nil
	}
	gs := &growth.GrowthState{
		Species:         state.Species,
		WeightG:         state.FishWeight,
		StockingDensity: state.StockingDensity,
	}
	result, err := e.growthModel.Predict(gs, &env, duration)
	if err != nil {
		return nil
	}
	return result
}

// ============================================================================
// RecommendDaily
// ============================================================================

// RecommendDaily generates a daily feeding plan for a pond. It returns a
// summary recommendation rather than per-hour predictions since the engine
// does not have access to time-series history for multi-point forecasting.
func (e *RecommendEngine) RecommendDaily(state StateInput, doForecasts, tempForecasts, nh3Forecasts []forecast.Forecast, growthResult *growth.GrowthResult) *DailyRecommendation {
	single := e.RecommendFeeding(state, doForecasts, tempForecasts, nh3Forecasts, growthResult)

	daily := &DailyRecommendation{
		Date:     time.Now().Format("2006-01-02"),
		PondID:   state.PondID,
		Feedings: []FeedingRecommendation{*single},
		Summary:  buildDailySummary(single),
	}
	return daily
}

// ============================================================================
// Model readiness
// ============================================================================

// ModelReadiness describes which sub-models are available.
type ModelReadiness struct {
	RLReady       bool
	ForecastReady bool
	GrowthReady   bool
}

// Readiness returns the current model readiness snapshot.
func (e *RecommendEngine) Readiness() ModelReadiness {
	return ModelReadiness{
		RLReady:       e.rlEngine != nil,
		ForecastReady: e.forecastEngine != nil,
		GrowthReady:   e.growthModel != nil,
	}
}

// ============================================================================
// Private: rule-based fallback
// ============================================================================

// ruleBasedFeedingRate computes a conservative feeding rate from current state
// thresholds without any AI model. Used when RL is unavailable.
func ruleBasedFeedingRate(state StateInput) float64 {
	rate := 0.5 // neutral baseline

	// DO: reduce if low.
	if state.DO < 4.5 {
		rate -= 0.3
	} else if state.DO < 5.5 {
		rate -= 0.1
	} else if state.DO > 8.0 {
		rate += 0.05
	}

	// Temperature: optimal range 22-28°C; reduce outside.
	if state.Temp < 18 {
		rate -= 0.2
	} else if state.Temp < 22 {
		rate -= 0.05
	} else if state.Temp > 32 {
		rate -= 0.3
	} else if state.Temp > 30 {
		rate -= 0.1
	}

	// NH3: reduce if high.
	if state.NH3 > 1.0 {
		rate -= 0.3
	} else if state.NH3 > 0.5 {
		rate -= 0.15
	} else if state.NH3 > 0.2 {
		rate -= 0.05
	}

	// FCR: reduce if inefficient.
	if state.FCR > 3.0 {
		rate -= 0.15
	} else if state.FCR > 2.0 {
		rate -= 0.05
	}

	return clampTo(rate, 0, 1)
}

// ============================================================================
// Private: forecast adjustments
// ============================================================================

// applyForecastAdjustments modifies the recommendation based on forecast trends.
func (e *RecommendEngine) applyForecastAdjustments(
	rec *FeedingRecommendation,
	state StateInput,
	doForecasts, tempForecasts, nh3Forecasts []forecast.Forecast,
) {
	// --- DO forecast ---
	if len(doForecasts) > 0 {
		minDO, trendDO := forecastSummary(doForecasts)
		if minDO < 4.0 {
			rec.RiskLevel = RiskHigh
			rec.FeedingRate *= 0.5
			rec.Actions = append(rec.Actions, RecommendationAction{
				Type:        ActionAerate,
				Description: fmt.Sprintf("DO forecast drops to %.1f mg/L; urgent aeration required", minDO),
				Priority:    1,
			})
			rec.Reason += fmt.Sprintf("DO critical (min %.1f). ", minDO)
		} else if minDO < 4.5 {
			rec.FeedingRate *= 0.7
			rec.Actions = append(rec.Actions, RecommendationAction{
				Type:        ActionAerate,
				Description: fmt.Sprintf("DO forecast trending low (min %.1f mg/L); start early aeration", minDO),
				Priority:    2,
			})
			rec.Reason += fmt.Sprintf("DO declining (min %.1f). ", minDO)
		}
		if trendDO < -0.5 {
			rec.FeedingRate *= 0.85
			if rec.RiskLevel == RiskLow {
				rec.RiskLevel = RiskMedium
			}
		}
	}

	// --- Temperature forecast ---
	if len(tempForecasts) > 0 {
		maxTemp, _ := forecastSummary(tempForecasts)
		if maxTemp > 35.0 {
			rec.RiskLevel = RiskHigh
			rec.FeedingRate *= 0.4
			rec.Actions = append(rec.Actions, RecommendationAction{
				Type:        ActionReduceFeeding,
				Description: fmt.Sprintf("temperature forecast exceeds 35°C (max %.1f); stop feeding to reduce stress", maxTemp),
				Priority:    1,
			})
			rec.Reason += fmt.Sprintf("Temp critical (max %.1f°C). ", maxTemp)
		} else if maxTemp > 32.0 {
			rec.FeedingRate *= 0.7
			rec.Actions = append(rec.Actions, RecommendationAction{
				Type:        ActionReduceFeeding,
				Description: fmt.Sprintf("temperature forecast high (max %.1f°C); reduce feeding", maxTemp),
				Priority:    2,
			})
			rec.Reason += fmt.Sprintf("Temp high (max %.1f°C). ", maxTemp)
		}
	}

	// --- NH3 forecast ---
	if len(nh3Forecasts) > 0 {
		maxNH3, _ := forecastSummary(nh3Forecasts)
		if maxNH3 > 1.0 {
			rec.RiskLevel = RiskHigh
			rec.FeedingRate *= 0.3
			rec.Actions = append(rec.Actions, RecommendationAction{
				Type:        ActionMonitorWater,
				Description: fmt.Sprintf("NH3 forecast spikes to %.2f mg/L; stop feeding, check water quality", maxNH3),
				Priority:    1,
			})
			rec.Reason += fmt.Sprintf("NH3 critical (max %.2f). ", maxNH3)
		} else if maxNH3 > 0.5 {
			rec.FeedingRate *= 0.8
			rec.Actions = append(rec.Actions, RecommendationAction{
				Type:        ActionMonitorWater,
				Description: fmt.Sprintf("NH3 forecast rising (max %.2f mg/L); monitor water quality", maxNH3),
				Priority:    2,
			})
			if rec.RiskLevel == RiskLow {
				rec.RiskLevel = RiskMedium
			}
			rec.Reason += fmt.Sprintf("NH3 elevated (max %.2f). ", maxNH3)
		}
	}
}

// forecastSummary returns the min (lowest predicted value) and trend
// (slope of linear fit, positive = rising).
func forecastSummary(fcs []forecast.Forecast) (minVal, trend float64) {
	if len(fcs) == 0 {
		return 0, 0
	}
	minVal = fcs[0].Value
	for _, f := range fcs {
		if f.Value < minVal {
			minVal = f.Value
		}
	}
	// Simple trend: compare last N vs first N.
	n := len(fcs)
	if n >= 4 {
		half := n / 2
		var firstHalf, secondHalf float64
		for i := 0; i < half; i++ {
			firstHalf += fcs[i].Value
			secondHalf += fcs[n-half+i].Value
		}
		firstHalf /= float64(half)
		secondHalf /= float64(half)
		trend = secondHalf - firstHalf
	}
	return
}

// ============================================================================
// Private: growth adjustments
// ============================================================================

// applyGrowthAdjustments modifies the recommendation based on growth predictions.
func (e *RecommendEngine) applyGrowthAdjustments(
	rec *FeedingRecommendation,
	state StateInput,
	result *growth.GrowthResult,
) {
	rec.ExpectedGrowthGPerDay = result.WeightGainGPerDay

	// Low growth rate: below 0.5 g/day for most species is concerning.
	if result.WeightGainGPerDay < 0.5 {
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionAdjustDensity,
			Description: fmt.Sprintf("growth lagging (%.2f g/day); consider adjusting feeding rate or stocking density", result.WeightGainGPerDay),
			Priority:    3,
		})
		if rec.RiskLevel == RiskLow {
			rec.RiskLevel = RiskMedium
		}
		rec.Reason += fmt.Sprintf("Growth slow (%.2f g/day). ", result.WeightGainGPerDay)
	}

	// High FCR: inefficient feed usage.
	if result.FeedConversionRatio > 2.5 {
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionReduceFeeding,
			Description: fmt.Sprintf("high FCR (%.2f); reduce feeding or improve feed quality", result.FeedConversionRatio),
			Priority:    3,
		})
		rec.Reason += fmt.Sprintf("FCR high (%.2f). ", result.FeedConversionRatio)
	} else if result.FeedConversionRatio > 2.0 {
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionMonitorWater,
			Description: fmt.Sprintf("FCR elevated (%.2f); monitor feed efficiency", result.FeedConversionRatio),
			Priority:    4,
		})
	}
}

// ============================================================================
// Private: current state anomalies
// ============================================================================

// applyCurrentStateAnomalies checks the current state for immediate hazards.
func (e *RecommendEngine) applyCurrentStateAnomalies(rec *FeedingRecommendation, state StateInput) {
	if state.DO < 4.0 {
		rec.RiskLevel = RiskHigh
		rec.FeedingRate *= 0.3
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionAerate,
			Description: fmt.Sprintf("DO critically low (%.1f mg/L); immediate aeration required, stop feeding", state.DO),
			Priority:    1,
		})
		rec.Reason += fmt.Sprintf("DO critical (%.1f). ", state.DO)
	} else if state.DO < 4.5 {
		rec.FeedingRate *= 0.7
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionAerate,
			Description: fmt.Sprintf("DO low (%.1f mg/L); start aeration", state.DO),
			Priority:    2,
		})
		rec.Reason += fmt.Sprintf("DO low (%.1f). ", state.DO)
	}

	if state.Temp > 35.0 {
		rec.RiskLevel = RiskHigh
		rec.FeedingRate *= 0.3
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionReduceFeeding,
			Description: fmt.Sprintf("temperature critically high (%.1f°C); stop feeding", state.Temp),
			Priority:    1,
		})
		rec.Reason += fmt.Sprintf("Temp critical (%.1f°C). ", state.Temp)
	}

	if state.NH3 > 1.0 {
		rec.RiskLevel = RiskHigh
		rec.FeedingRate *= 0.2
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionMonitorWater,
			Description: fmt.Sprintf("ammonia critically high (%.2f mg/L); stop feeding, water change required", state.NH3),
			Priority:    1,
		})
		rec.Reason += fmt.Sprintf("NH3 critical (%.2f). ", state.NH3)
	} else if state.NH3 > 0.5 {
		rec.FeedingRate *= 0.7
		if rec.RiskLevel == RiskLow {
			rec.RiskLevel = RiskMedium
		}
		rec.Actions = append(rec.Actions, RecommendationAction{
			Type:        ActionMonitorWater,
			Description: fmt.Sprintf("ammonia elevated (%.2f mg/L); monitor water quality", state.NH3),
			Priority:    2,
		})
		rec.Reason += fmt.Sprintf("NH3 elevated (%.2f). ", state.NH3)
	}

	if rec.Reason == "" {
		rec.Reason = "normal conditions; maintain current feeding strategy"
	}
}

// ============================================================================
// Risk assessment
// ============================================================================

// assessRisk determines the overall risk level from the recommendation and
// its inputs. It is exported so handlers and tests can call it directly.
func assessRisk(
	rec *FeedingRecommendation,
	state StateInput,
	doForecasts, nh3Forecasts []forecast.Forecast,
) RiskLevel {
	// Immediate critical conditions → HIGH.
	if state.DO < 4.0 || state.Temp > 35.0 || state.NH3 > 1.0 {
		return RiskHigh
	}

	// Forecast critical predictions → HIGH.
	if len(doForecasts) > 0 {
		minDO, _ := forecastSummary(doForecasts)
		if minDO < 4.0 {
			return RiskHigh
		}
	}
	if len(nh3Forecasts) > 0 {
		maxNH3, _ := forecastSummary(nh3Forecasts)
		if maxNH3 > 1.0 {
			return RiskHigh
		}
	}

	// Low confidence → HIGH (flag for manual review).
	if rec.Confidence < 0.7 {
		return RiskHigh
	}

	// Moderate anomalies → MEDIUM.
	if state.DO < 4.5 || state.NH3 > 0.5 || state.Temp > 32.0 {
		return RiskMedium
	}
	if len(doForecasts) > 0 {
		_, trendDO := forecastSummary(doForecasts)
		if trendDO < -0.5 {
			return RiskMedium
		}
	}

	return RiskLow
}

// ============================================================================
// Private: daily summary
// ============================================================================

func buildDailySummary(rec *FeedingRecommendation) string {
	switch {
	case rec.RiskLevel == RiskHigh && rec.RequiresManualReview:
		return "critical conditions with low AI confidence — manual review required before any action"
	case rec.RiskLevel == RiskHigh:
		return "high risk conditions detected; follow recommended actions immediately"
	case rec.RiskLevel == RiskMedium:
		return "moderate risk; monitor conditions and consider recommended adjustments"
	default:
		return "normal conditions; maintain current feeding strategy"
	}
}

// ============================================================================
// Private: helpers
// ============================================================================

// clampTo bounds v to [lo, hi].
func clampTo(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// sortActions orders actions by priority (lower = more urgent first).
func sortActions(actions []RecommendationAction) {
	for i := 0; i < len(actions); i++ {
		for j := i + 1; j < len(actions); j++ {
			if actions[j].Priority < actions[i].Priority {
				actions[i], actions[j] = actions[j], actions[i]
			}
		}
	}
}

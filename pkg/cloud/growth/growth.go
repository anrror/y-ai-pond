// Package growth implements aquaculture growth models for predicting fish
// weight gain, feed conversion ratio (FCR), and harvest timing. It provides
// two complementary models:
//
//   - VBGM (von Bertalanffy Growth Model): analytic length-at-age growth
//     prediction using species-specific parameters.
//   - Bioenergetic 4.0 (Deslauriers et al. 2017): energy-balance model
//     that couples temperature, consumption, respiration, and growth
//     through a system of ODEs solved via Runge-Kutta 4 integration.
//
// All models implement the GrowthModel interface and are injectable for
// testing. Species parameters come from an embedded CSV library (105+
// aquaculture species).
package growth

import (
	"errors"
	"fmt"
	"time"
)

// ============================================================================
// Domain errors
// ============================================================================

var (
	// ErrUnsupportedSpecies is returned when species parameters are not
	// found in the species library.
	ErrUnsupportedSpecies = errors.New("growth: unsupported species")

	// ErrInvalidState is returned when the growth state is invalid (e.g.,
	// negative weight or length).
	ErrInvalidState = errors.New("growth: invalid growth state")

	// ErrInvalidEnvironment is returned when the environment parameters
	// are out of valid bounds (e.g., temperature > 50°C or DO < 0).
	ErrInvalidEnvironment = errors.New("growth: invalid environment")

	// ErrInvalidDuration is returned when the prediction duration is
	// zero or negative.
	ErrInvalidDuration = errors.New("growth: invalid prediction duration")
)

// ============================================================================
// Core types
// ============================================================================

// GrowthState describes the current state of a fish cohort.
type GrowthState struct {
	// Species identifies the fish species (e.g., "tilapia", "salmon").
	Species string `json:"species"`

	// AgeDays is the age of the fish in days since hatch/stocking.
	AgeDays float64 `json:"age_days"`

	// WeightG is the average individual fish weight in grams.
	WeightG float64 `json:"weight_g"`

	// LengthCm is the average individual fish length in centimeters.
	LengthCm float64 `json:"length_cm"`

	// StockingDensity is the number of fish per cubic meter.
	StockingDensity float64 `json:"stocking_density"`
}

// Validate checks that the growth state values are within reasonable bounds.
func (s *GrowthState) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: state is nil", ErrInvalidState)
	}
	if s.WeightG < 0 {
		return fmt.Errorf("%w: weight must be non-negative, got %.2f", ErrInvalidState, s.WeightG)
	}
	if s.LengthCm < 0 {
		return fmt.Errorf("%w: length must be non-negative, got %.2f", ErrInvalidState, s.LengthCm)
	}
	if s.AgeDays < 0 {
		return fmt.Errorf("%w: age must be non-negative, got %.2f", ErrInvalidState, s.AgeDays)
	}
	return nil
}

// Environment describes the environmental conditions affecting growth.
type Environment struct {
	// Temperature is the water temperature in degrees Celsius.
	Temperature float64 `json:"temperature"`

	// DissolvedOxygen is the dissolved oxygen level in mg/L.
	DissolvedOxygen float64 `json:"dissolved_oxygen"`

	// FeedingRate is the proportion of maximum consumption (0.0-1.0).
	// A value of 1.0 means the fish are fed at satiation.
	FeedingRate float64 `json:"feeding_rate"`

	// ActivityMultiplier is an optional override for the species-specific
	// activity multiplier. If 0, the species default is used.
	ActivityMultiplier float64 `json:"activity_multiplier,omitempty"`
}

// Validate checks that the environment values are within reasonable bounds.
func (e *Environment) Validate() error {
	if e == nil {
		return fmt.Errorf("%w: environment is nil", ErrInvalidEnvironment)
	}
	if e.Temperature < -2 || e.Temperature > 45 {
		return fmt.Errorf("%w: temperature %.2f out of range [-2, 45]", ErrInvalidEnvironment, e.Temperature)
	}
	if e.DissolvedOxygen < 0 || e.DissolvedOxygen > 25 {
		return fmt.Errorf("%w: dissolved oxygen %.2f out of range [0, 25]", ErrInvalidEnvironment, e.DissolvedOxygen)
	}
	if e.FeedingRate < 0 || e.FeedingRate > 1 {
		return fmt.Errorf("%w: feeding rate %.2f out of range [0, 1]", ErrInvalidEnvironment, e.FeedingRate)
	}
	return nil
}

// GrowthResult contains the predicted growth outcomes for a given period.
type GrowthResult struct {
	// WeightGainGPerDay is the average daily weight gain in grams per day.
	WeightGainGPerDay float64 `json:"weight_gain_g_per_day"`

	// FCR is the feed conversion ratio: feed consumed / weight gained.
	// Lower values indicate better feed efficiency. A value of 0 means
	// no weight gain occurred.
	FeedConversionRatio float64 `json:"feed_conversion_ratio"`

	// HarvestDays is the estimated number of days until the target harvest
	// weight is reached. Zero if already at or above target.
	HarvestDays float64 `json:"harvest_days"`

	// LengthCm is the predicted fish length after the growth period.
	LengthCm float64 `json:"length_cm"`

	// FinalWeightG is the predicted fish weight after the growth period.
	FinalWeightG float64 `json:"final_weight_g"`

	// CumulativeConsumptionG is the total feed consumed per fish during
	// the growth period, in grams.
	CumulativeConsumptionG float64 `json:"cumulative_consumption_g"`

	// EnergyBalance is the energy conservation check value: C - (R + F + U).
	// Should be approximately equal to the energy stored as growth (G).
	// A positive value indicates net anabolism; negative indicates catabolism.
	EnergyBalanceKJ float64 `json:"energy_balance_kj"`
}

// ============================================================================
// GrowthModel interface
// ============================================================================

// GrowthModel predicts fish growth over a given duration given the current
// state and environmental conditions.
//
// Implementations:
//   - VBGMModel (von Bertalanffy): analytic length-based growth prediction.
//   - BioenergeticModel (Deslauriers 2017): energy-balance ODE model with
//     temperature coupling.
type GrowthModel interface {
	// Predict forecasts growth over the given duration.
	//
	// state is the current fish state (species, age, weight, length).
	// env specifies environmental conditions (temperature, DO, feeding rate).
	// duration is the period over which to predict growth.
	//
	// Returns ErrUnsupportedSpecies if the species is not in the library,
	// ErrInvalidState if the state is invalid, or ErrInvalidEnvironment
	// if the environment is out of bounds.
	Predict(state *GrowthState, env *Environment, duration time.Duration) (*GrowthResult, error)

	// Name returns a human-readable model identifier.
	Name() string
}

// ============================================================================
// Target weight helper
// ============================================================================

// TargetWeight defines a target harvest weight for a species.
// Used to estimate days-to-harvest.
type TargetWeight struct {
	// Species matches the species name in the library.
	Species string `json:"species"`

	// WeightG is the target harvest weight in grams.
	WeightG float64 `json:"weight_g"`
}

// CommonTargetWeights maps common aquaculture species to typical harvest
// weights. These are sensible defaults that can be overridden.
var CommonTargetWeights = map[string]float64{
	"tilapia":              500.0, // typical market size
	"nile_tilapia":         500.0,
	"common_carp":          1000.0, // 1 kg
	"grass_carp":           1500.0,
	"bighead_carp":         2000.0,
	"silver_carp":          1500.0,
	"channel_catfish":      680.0,  // ~1.5 lb
	"atlantic_salmon":      4500.0, // ~4.5 kg
	"rainbow_trout":        350.0,  // portion size
	"largemouth_bass":      450.0,
	"striped_bass":         1500.0,
	"barramundi":           500.0,
	"grouper":              1000.0,
	"red_sea_bream":        500.0,
	"olive_flounder":       800.0,
	"pacific_white_shrimp": 20.0, // 20g individual
	"giant_tiger_prawn":    30.0,
	"freshwater_prawn":     25.0,
	"abalone":              80.0,
	"green_abalone":        80.0,
}

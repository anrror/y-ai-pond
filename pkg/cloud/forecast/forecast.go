// Package forecast implements time-series forecasting for water quality
// and aquaculture growth prediction. It provides two complementary models:
//
//   - Prophet-style: trend + Fourier seasonality decomposition with
//     confidence intervals computed from residual statistics. Handles
//     daily/weekly cycles in dissolved oxygen and temperature.
//   - SARIMAX: seasonal ARIMA with exogenous variable support for
//     short-horizon predictions where external factors (e.g. temperature
//     affecting DO) improve accuracy.
//
// The package is pure Go with zero external dependencies. All models
// implement the ForecastEngine interface and are injectable for testing.
package forecast

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ============================================================================
// Domain errors
// ============================================================================

var (
	// ErrInsufficientData is returned when a training series has fewer than
	// the minimum required points (default 168 = 7 days of hourly data).
	ErrInsufficientData = errors.New("forecast: insufficient data for training")
	// ErrInvalidHorizon is returned when the prediction horizon is zero or negative.
	ErrInvalidHorizon = errors.New("forecast: invalid prediction horizon")
	// ErrInvalidSteps is returned when the number of prediction steps is <= 0.
	ErrInvalidSteps = errors.New("forecast: invalid prediction steps")
	// ErrModelExpired indicates the model needs retraining.
	ErrModelExpired = errors.New("forecast: model has expired, retrain required")
)

// ============================================================================
// Core types
// ============================================================================

// Point is a single time-series data point.
type Point struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// ExogPoint is a time-series point with exogenous (external) variables.
// Used by SARIMAX for multi-variable forecasting.
type ExogPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Exog      []float64 `json:"exog"` // external regressors, e.g. [temperature]
}

// Forecast is a single predicted value with confidence intervals.
type Forecast struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Lower80   float64   `json:"lower80"`
	Upper80   float64   `json:"upper80"`
	Lower95   float64   `json:"lower95"`
	Upper95   float64   `json:"upper95"`
}

// DriftReport describes whether a model's predictions have degraded over time.
type DriftReport struct {
	DriftDetected   bool    `json:"drift_detected"`
	BaselineRMSE    float64 `json:"baseline_rmse"`
	RecentRMSE      float64 `json:"recent_rmse"`
	Ratio           float64 `json:"ratio"`
	Recommendation  string  `json:"recommendation"`
}

// ============================================================================
// Model interface
// ============================================================================

// Model is a trained forecasting model that can predict future values.
type Model interface {
	// Predict forecasts the next `steps` data points with confidence intervals.
	// steps must be >= 1.
	Predict(steps int) ([]Forecast, error)
	// Name returns a human-readable model type identifier.
	Name() string
	// Marshal serializes the model to bytes for storage.
	Marshal() ([]byte, error)
}

// ============================================================================
// ForecastEngine interface
// ============================================================================

// ForecastEngine is the main interface for training and using forecasting models.
// Implementations include Prophet-style (trend + seasonality) and SARIMAX
// (seasonal ARIMA with exogenous variables).
type ForecastEngine interface {
	// Train fits a model to historical time-series data for the given
	// prediction horizon. Returns ErrInsufficientData if the series has
	// fewer than the minimum required points.
	Train(series []Point, horizon time.Duration) (Model, error)

	// Predict uses a trained model to forecast the next `steps` data points
	// with 80% and 95% confidence intervals.
	Predict(model Model, steps int) ([]Forecast, error)
}

// ============================================================================
// Configuration
// ============================================================================

// Config holds tunable parameters for the forecast engine.
type Config struct {
	// MinPoints is the minimum number of data points required for training.
	// Default: 168 (7 days of hourly data).
	MinPoints int

	// FourierOrder is the number of Fourier terms used for seasonality.
	// Higher values capture more complex cycles but risk overfitting.
	// Default: 3 (sufficient for daily DO/temperature cycles).
	FourierOrder int

	// MaxAROrder caps the AR(p) order to prevent overfitting. Default: 5.
	MaxAROrder int

	// MaxMAOrder caps the MA(q) order. Default: 3.
	MaxMAOrder int

	// DriftThreshold is the ratio of recent RMSE to baseline RMSE that
	// triggers drift detection. Default: 2.0.
	DriftThreshold float64

	// DriftWindow is the number of recent points used for drift detection.
	// Default: 72 (3 days of hourly data).
	DriftWindow int

	// Z80 is the z-score for 80% confidence intervals. Default: 1.282.
	Z80 float64

	// Z95 is the z-score for 95% confidence intervals. Default: 1.960.
	Z95 float64
}

// DefaultConfig returns the recommended configuration for water-quality
// forecasting. It is tuned for hourly sensor data with daily seasonality.
func DefaultConfig() Config {
	return Config{
		MinPoints:      168, // 7 days × 24 hours
		FourierOrder:   3,
		MaxAROrder:     5,
		MaxMAOrder:     3,
		DriftThreshold: 2.0,
		DriftWindow:    72, // 3 days × 24 hours
		Z80:            1.282,
		Z95:            1.960,
	}
}

// ============================================================================
// Helpers
// ============================================================================

// normalQuantile returns the z-score for the given confidence level using
// a rational approximation of the inverse normal CDF. This avoids importing
// gonum/stat for a handful of constants.
//
// The function uses the Acklam algorithm (percentile via rational approx),
// which has absolute error < 1.15e-9 for the normal distribution.
func normalQuantile(p float64) float64 {
	if p <= 0 || p >= 1 {
		return 0
	}
	// For standard confidence levels, return exact z-scores.
	switch {
	case math.Abs(p-0.80) < 1e-10:
		return 1.2815515655446004
	case math.Abs(p-0.90) < 1e-10:
		return 1.6448536269514722
	case math.Abs(p-0.95) < 1e-10:
		return 1.959963984540054
	case math.Abs(p-0.975) < 1e-10:
		return 2.241402727604945
	case math.Abs(p-0.99) < 1e-10:
		return 2.5758293035489004
	}

	// Acklam's algorithm for arbitrary p.
	// a1-a6 and b1-b5 coefficients.
	a := []float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := []float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01, 1.0}
	c := []float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := []float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00, 1.0, 0.0}

	q := p - 0.5
	var r, x float64
	if math.Abs(q) < 0.425 {
		r = 0.180625 - q*q
		x = q * (((((a[5]*r+a[4])*r+a[3])*r+a[2])*r+a[1])*r + a[0]) /
			(((((b[5]*r+b[4])*r+b[3])*r+b[2])*r+b[1])*r + 1.0)
	} else {
		if q > 0 {
			r = 1 - p
		} else {
			r = p
		}
		r = math.Sqrt(-math.Log(r))
		x = (((((c[5]*r+c[4])*r+c[3])*r+c[2])*r+c[1])*r + c[0]) /
			((((d[5]*r+d[4])*r+d[3])*r+d[2])*r+d[1])*r + 1.0
		if q < 0 {
			x = -x
		}
	}
	return x
}

// computeCI computes the confidence interval bounds around a predicted value
// given the residual standard deviation and z-scores.
func computeCI(predicted, residualStd float64, z80, z95 float64) (lo80, hi80, lo95, hi95 float64) {
	if residualStd <= 0 {
		return predicted, predicted, predicted, predicted
	}
	margin80 := z80 * residualStd
	margin95 := z95 * residualStd
	return predicted - margin80, predicted + margin80,
		predicted - margin95, predicted + margin95
}

// meanStddev computes the mean and sample standard deviation of a slice.
func meanStddev(vals []float64) (mean, stddev float64) {
	n := float64(len(vals))
	if n == 0 {
		return 0, 0
	}
	for _, v := range vals {
		mean += v
	}
	mean /= n
	if n <= 1 {
		return mean, 0
	}
	var ss float64
	for _, v := range vals {
		d := v - mean
		ss += d * d
	}
	stddev = math.Sqrt(ss / (n - 1))
	return
}

// rmse computes the root-mean-square error between two slices.
func rmse(actual, predicted []float64) float64 {
	if len(actual) != len(predicted) || len(actual) == 0 {
		return 0
	}
	var sumSq float64
	for i := range actual {
		d := actual[i] - predicted[i]
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(actual)))
}

// linearRegression performs OLS: y = β₀ + β₁·x.
// Returns intercept, slope, and R².
func linearRegression(x, y []float64) (intercept, slope, rSquared float64) {
	n := float64(len(x))
	if n < 2 {
		return 0, 0, 0
	}

	var sumX, sumY, sumXY, sumX2, sumY2 float64
	for i := range x {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
		sumY2 += y[i] * y[i]
	}

	denom := n*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-12 {
		return sumY / n, 0, 0
	}

	slope = (n*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / n

	// R²
	var ssRes, ssTot float64
	meanY := sumY / n
	for i := range x {
		pred := intercept + slope*x[i]
		ssRes += (y[i] - pred) * (y[i] - pred)
		ssTot += (y[i] - meanY) * (y[i] - meanY)
	}
	if ssTot > 1e-12 {
		rSquared = 1 - ssRes/ssTot
	}
	return
}

// fmt.Stringer-compatible helper for model names.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

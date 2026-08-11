package forecast

import (
	"fmt"
	"log/slog"
	"math"
	"time"
)

// ============================================================================
// Drift detection
//
// Model drift occurs when a trained model's prediction accuracy degrades
// over time due to changes in the underlying data distribution (seasonal
// shifts, sensor recalibration, degradation, etc.).
//
// The drift detector compares recent prediction errors against the model's
// baseline error level (derived from training residuals). When the ratio
// of recent RMSE to baseline RMSE exceeds a configurable threshold, drift
// is flagged and a retraining recommendation is issued.
//
// Algorithm: rolling window RMSE ratio
//
//	baseline_rmse = stddev(model training residuals)
//	recent_rmse   = RMSE(new_actuals, model_predictions) over drift_window
//	drift_detected = (recent_rmse / baseline_rmse) > threshold
//	             OR recent_rmse > max(error_threshold, 3 * baseline_rmse)
// ============================================================================

// DriftStatus indicates whether model drift has been detected.
type DriftStatus string

const (
	// DriftOK means the model is performing within acceptable bounds.
	DriftOK DriftStatus = "OK"
	// DriftWarning means performance is degrading but not yet critical.
	DriftWarning DriftStatus = "WARNING"
	// DriftDetected means the model should be retrained.
	DriftDetected DriftStatus = "DRIFT_DETECTED"
)

// drift detector configuration values.
const (
	// defaultDriftThreshold is the ratio of recent RMSE to baseline RMSE
	// that triggers drift detection.
	defaultDriftThreshold = 2.0
	// defaultDriftWindow is the number of recent points used for drift
	// computation (72 = 3 days of hourly data).
	defaultDriftWindow = 72
	// warningThreshold is the ratio for a warning (below detection but elevated).
	warningThreshold = 1.5
	// errorTrendThreshold is the absolute RMSE that always triggers drift,
	// regardless of the baseline ratio.
	errorTrendThreshold = 1.0
)

// DetectDrift analyzes recent prediction errors against the model's
// training baseline to determine if the model needs retraining.
//
// Parameters:
//   - model: a trained model with training residual statistics
//   - newSeries: recent actual observations (should be the same length as predictions)
//   - cfg: configuration for drift thresholds
//
// Returns a DriftReport with the status and recommendation.
func DetectDrift(model Model, newSeries []Point, predicted []Forecast, cfg Config) DriftReport {
	if len(newSeries) == 0 || len(predicted) == 0 {
		return DriftReport{
			DriftDetected:  false,
			Recommendation: "insufficient data for drift analysis",
		}
	}

	// Determine baseline RMSE from the model type.
	baselineRMSE := extractResidualStd(model)

	// Use the shorter of the two series for comparison.
	n := len(newSeries)
	if len(predicted) < n {
		n = len(predicted)
	}

	// Use the configured drift window, capped at the available data.
	window := cfg.DriftWindow
	if window <= 0 {
		window = defaultDriftWindow
	}
	if n > window {
		// Focus on the most recent window of points.
		start := n - window
		newSeries = newSeries[start:]
		predicted = predicted[start:]
		n = window
	}

	// Compute recent RMSE.
	actuals := make([]float64, n)
	forecasts := make([]float64, n)
	for i := 0; i < n; i++ {
		actuals[i] = newSeries[i].Value
		forecasts[i] = predicted[i].Value
	}
	recentRMSE := rmse(actuals, forecasts)

	// Determine drift threshold.
	threshold := cfg.DriftThreshold
	if threshold <= 0 {
		threshold = defaultDriftThreshold
	}

	var driftDetected bool
	var recommendation string

	if baselineRMSE <= 0 {
		// No baseline — can't compute ratio. Use absolute threshold.
		if recentRMSE > errorTrendThreshold {
			driftDetected = true
			recommendation = fmt.Sprintf("RETRAIN: recent RMSE %.4f exceeds absolute threshold %.2f (no baseline available)", recentRMSE, errorTrendThreshold)
		} else {
			recommendation = "monitoring (baseline unavailable)"
		}
	} else {
		ratio := recentRMSE / baselineRMSE
		if ratio > threshold || recentRMSE > math.Max(errorTrendThreshold, 3*baselineRMSE) {
			driftDetected = true
			recommendation = fmt.Sprintf("RETRAIN: recent RMSE (%.4f) is %.1fx baseline (%.4f)", recentRMSE, ratio, baselineRMSE)
		} else if ratio > warningThreshold {
			recommendation = fmt.Sprintf("WARNING: prediction error rising (%.1fx baseline), monitor closely", ratio)
		} else {
			recommendation = "model performing within acceptable bounds"
		}
	}

	ratio := 0.0
	if baselineRMSE > 0 {
		ratio = recentRMSE / baselineRMSE
	}

	return DriftReport{
		DriftDetected:  driftDetected,
		BaselineRMSE:   baselineRMSE,
		RecentRMSE:     recentRMSE,
		Ratio:          ratio,
		Recommendation: recommendation,
	}
}

// extractResidualStd attempts to extract the residual standard deviation
// from a trained model. Since Model is an interface, we type-assert to
// known concrete types to access internal statistics.
func extractResidualStd(model Model) float64 {
	switch m := model.(type) {
	case *prophetModel:
		return m.residualStd
	case *sarimaxModel:
		return m.residualStd
	default:
		// Unknown model type — return 0 to indicate no baseline.
		slog.Warn("forecast: cannot extract residual std from unknown model type", "type", fmt.Sprintf("%T", model))
		return 0
	}
}

// ============================================================================
// DriftDetector — stateful drift monitor
// ============================================================================

// DriftDetector monitors a model's prediction accuracy over time by
// tracking a rolling window of recent errors. It is designed to be
// called incrementally as new observations arrive.
type DriftDetector struct {
	cfg Config

	// Baseline RMSE extracted from training residuals.
	baselineRMSE float64

	// Rolling window of recent absolute errors.
	recentErrors []float64
	errorIdx     int // write position in circular buffer
	errorCount   int // number of valid entries
}

// NewDriftDetector creates a DriftDetector with the given config and
// training baseline RMSE.
func NewDriftDetector(cfg Config, baselineRMSE float64) *DriftDetector {
	if cfg.DriftWindow <= 0 {
		cfg.DriftWindow = defaultDriftWindow
	}
	if cfg.DriftThreshold <= 0 {
		cfg.DriftThreshold = defaultDriftThreshold
	}
	return &DriftDetector{
		cfg:          cfg,
		baselineRMSE: baselineRMSE,
		recentErrors: make([]float64, cfg.DriftWindow),
	}
}

// AddObservation records a new prediction-vs-actual pair for drift tracking.
func (d *DriftDetector) AddObservation(actual, predicted float64) {
	absErr := math.Abs(actual - predicted)
	d.recentErrors[d.errorIdx] = absErr
	d.errorIdx = (d.errorIdx + 1) % len(d.recentErrors)
	if d.errorCount < len(d.recentErrors) {
		d.errorCount++
	}
}

// Check evaluates whether model drift has been detected based on all
// accumulated observations. Returns nil if no drift, or a report if
// drift is detected.
func (d *DriftDetector) Check() DriftReport {
	if d.errorCount == 0 {
		return DriftReport{Recommendation: "no observations recorded"}
	}

	// Compute recent RMSE from rolling errors.
	n := d.errorCount
	var sumSq float64
	for i := 0; i < n; i++ {
		sumSq += d.recentErrors[i] * d.recentErrors[i]
	}
	recentRMSE := math.Sqrt(sumSq / float64(n))

	threshold := d.cfg.DriftThreshold

	var driftDetected bool
	var recommendation string

	if d.baselineRMSE <= 0 {
		if recentRMSE > errorTrendThreshold {
			driftDetected = true
			recommendation = fmt.Sprintf("RETRAIN: recent RMSE %.4f exceeds absolute threshold %.2f", recentRMSE, errorTrendThreshold)
		} else {
			recommendation = "monitoring"
		}
	} else {
		ratio := recentRMSE / d.baselineRMSE
		if ratio > threshold || recentRMSE > math.Max(errorTrendThreshold, 3*d.baselineRMSE) {
			driftDetected = true
			recommendation = fmt.Sprintf("RETRAIN: recent RMSE (%.4f) is %.1fx baseline (%.4f)", recentRMSE, ratio, d.baselineRMSE)
		} else if ratio > warningThreshold {
			recommendation = fmt.Sprintf("WARNING: prediction error rising (%.1fx baseline)", ratio)
		} else {
			recommendation = "model performing within acceptable bounds"
		}
	}

	ratio := 0.0
	if d.baselineRMSE > 0 {
		ratio = recentRMSE / d.baselineRMSE
	}

	return DriftReport{
		DriftDetected:  driftDetected,
		BaselineRMSE:   d.baselineRMSE,
		RecentRMSE:     recentRMSE,
		Ratio:          ratio,
		Recommendation: recommendation,
	}
}

// Reset clears all accumulated observations.
func (d *DriftDetector) Reset() {
	d.errorIdx = 0
	d.errorCount = 0
	for i := range d.recentErrors {
		d.recentErrors[i] = 0
	}
}

// ============================================================================
// Model serialization helpers
// ============================================================================

// SaveModel serializes a model to JSON bytes for persistent storage.
func SaveModel(model Model) ([]byte, error) {
	return model.Marshal()
}

// LoadModel deserializes a model from JSON bytes. The caller must know
// the model type to use the appropriate unmarshal function.
func LoadProphetModel(data []byte) (*prophetModel, error) {
	return unmarshalProphet(data)
}

// LoadSARIMAXModel deserializes a SARIMAX model from JSON bytes.
func LoadSARIMAXModel(data []byte) (*sarimaxModel, error) {
	return unmarshalSARIMAX(data)
}

// Ensure time is used.
var _ = time.Now

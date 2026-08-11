package forecast

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// ============================================================================
// Prophet-style model: trend + Fourier seasonality + confidence intervals
//
// This is a self-contained, pure-Go implementation inspired by Facebook's
// Prophet. It decomposes a time series into:
//
//   y(t) = g(t) + s(t) + ε(t)
//
// where g(t) is a linear trend, s(t) is modeled as a Fourier series for
// daily and weekly cycles, and ε(t) is the residual used for confidence
// intervals.
//
// The model is intentionally simpler than full Prophet (no changepoint
// detection or holiday effects by default) to keep dependencies minimal.
// For water-quality forecasting (DO, pH, temperature, NH3), the dominant
// signals are daily photosynthesis cycles and seasonal temperature trends,
// which are well-captured by this decomposition.
// ============================================================================

const (
	// prophetHoursInDay is the period of a daily cycle in hours.
	prophetHoursInDay = 24.0
	// prophetHoursInWeek is the period of a weekly cycle in hours.
	prophetHoursInWeek = 168.0
)

// prophetModel holds the trained Prophet-style decomposition.
type prophetModel struct {
	name string

	// Trend parameters: value = intercept + slope * t
	intercept float64
	slope     float64

	// Fourier coefficients for daily seasonality
	dailyCos []float64
	dailySin []float64

	// Fourier coefficients for weekly seasonality
	weeklyCos []float64
	weeklySin []float64

	// Training metadata
	firstTime   time.Time
	intervalSec float64 // seconds between consecutive points
	trainedOn   int     // number of training points

	// Residual statistics for confidence intervals
	residualStd float64
	residuals   []float64

	// Config reference for serialization
	z80 float64
	z95 float64
	fourierOrder int
}

// ensure prophetModel implements Model
var _ Model = (*prophetModel)(nil)

// Name returns "prophet_<horizon>".
func (m *prophetModel) Name() string {
	return m.name
}

// Predict forecasts the next `steps` data points using the fitted model.
func (m *prophetModel) Predict(steps int) ([]Forecast, error) {
	if steps <= 0 {
		return nil, ErrInvalidSteps
	}
	if m.residualStd < 0 {
		return nil, fmt.Errorf("forecast: prophet model has insufficient training data")
	}

	results := make([]Forecast, steps)
	for i := 0; i < steps; i++ {
		offsetSec := float64(m.trainedOn+i) * m.intervalSec
		relTime := offsetSec / 3600.0 // hours from first point
		predicted := m.predictAtHour(relTime)
		lo80, hi80, lo95, hi95 := computeCI(predicted, m.residualStd, m.z80, m.z95)
		results[i] = Forecast{
			Timestamp: m.firstTime.Add(time.Duration(offsetSec * float64(time.Second))),
			Value:     predicted,
			Lower80:   lo80,
			Upper80:   hi80,
			Lower95:   lo95,
			Upper95:   hi95,
		}
	}
	return results, nil
}

// predictAtHour computes the decomposed value at relative hour `t` from the
// first training point.
func (m *prophetModel) predictAtHour(t float64) float64 {
	// Trend
	val := m.intercept + m.slope*t

	// Daily seasonality
	for k := range m.dailyCos {
		theta := 2 * math.Pi * float64(k+1) * t / prophetHoursInDay
		val += m.dailyCos[k]*math.Cos(theta) + m.dailySin[k]*math.Sin(theta)
	}

	// Weekly seasonality
	for k := range m.weeklyCos {
		theta := 2 * math.Pi * float64(k+1) * t / prophetHoursInWeek
		val += m.weeklyCos[k]*math.Cos(theta) + m.weeklySin[k]*math.Sin(theta)
	}

	return val
}

// Marshal serializes the model to JSON for storage and later reloading.
func (m *prophetModel) Marshal() ([]byte, error) {
	type wire struct {
		Name         string    `json:"name"`
		Intercept    float64   `json:"intercept"`
		Slope        float64   `json:"slope"`
		DailyCos     []float64 `json:"daily_cos"`
		DailySin     []float64 `json:"daily_sin"`
		WeeklyCos    []float64 `json:"weekly_cos"`
		WeeklySin    []float64 `json:"weekly_sin"`
		FirstTime    time.Time `json:"first_time"`
		IntervalSec  float64   `json:"interval_sec"`
		TrainedOn    int       `json:"trained_on"`
		ResidualStd  float64   `json:"residual_std"`
		Z80          float64   `json:"z80"`
		Z95          float64   `json:"z95"`
		FourierOrder int       `json:"fourier_order"`
	}
	w := wire{
		Name:         m.name,
		Intercept:    m.intercept,
		Slope:        m.slope,
		DailyCos:     m.dailyCos,
		DailySin:     m.dailySin,
		WeeklyCos:    m.weeklyCos,
		WeeklySin:    m.weeklySin,
		FirstTime:    m.firstTime,
		IntervalSec:  m.intervalSec,
		TrainedOn:    m.trainedOn,
		ResidualStd:  m.residualStd,
		Z80:          m.z80,
		Z95:          m.z95,
		FourierOrder: m.fourierOrder,
	}
	return json.Marshal(w)
}

// unmarshalProphet deserializes a prophetModel from JSON bytes.
func unmarshalProphet(data []byte) (*prophetModel, error) {
	type wire struct {
		Name         string    `json:"name"`
		Intercept    float64   `json:"intercept"`
		Slope        float64   `json:"slope"`
		DailyCos     []float64 `json:"daily_cos"`
		DailySin     []float64 `json:"daily_sin"`
		WeeklyCos    []float64 `json:"weekly_cos"`
		WeeklySin    []float64 `json:"weekly_sin"`
		FirstTime    time.Time `json:"first_time"`
		IntervalSec  float64   `json:"interval_sec"`
		TrainedOn    int       `json:"trained_on"`
		ResidualStd  float64   `json:"residual_std"`
		Z80          float64   `json:"z80"`
		Z95          float64   `json:"z95"`
		FourierOrder int       `json:"fourier_order"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("forecast: unmarshal prophet: %w", err)
	}
	return &prophetModel{
		name:         w.Name,
		intercept:    w.Intercept,
		slope:        w.Slope,
		dailyCos:     w.DailyCos,
		dailySin:     w.DailySin,
		weeklyCos:    w.WeeklyCos,
		weeklySin:    w.WeeklySin,
		firstTime:    w.FirstTime,
		intervalSec:  w.IntervalSec,
		trainedOn:    w.TrainedOn,
		residualStd:  w.ResidualStd,
		residuals:    nil, // residuals not serialized
		z80:          w.Z80,
		z95:          w.Z95,
		fourierOrder: w.FourierOrder,
	}, nil
}

// ============================================================================
// ProphetEngine
// ============================================================================

// ProphetEngine implements ForecastEngine using the Prophet-style
// decomposition (trend + Fourier seasonality).
type ProphetEngine struct {
	cfg Config
}

// ensure ProphetEngine implements ForecastEngine.
var _ ForecastEngine = (*ProphetEngine)(nil)

// NewProphetEngine creates a Prophet-style forecasting engine. If cfg
// is zero-valued, DefaultConfig is used.
func NewProphetEngine(cfg Config) *ProphetEngine {
	if cfg.MinPoints == 0 {
		cfg = DefaultConfig()
	}
	return &ProphetEngine{cfg: cfg}
}

// Train fits a Prophet-style model to the given time series. The horizon
// determines the model name only; actual prediction steps are controlled
// at Predict time.
func (e *ProphetEngine) Train(series []Point, horizon time.Duration) (Model, error) {
	if len(series) < e.cfg.MinPoints {
		return nil, fmt.Errorf("%w: got %d points, need at least %d", ErrInsufficientData, len(series), e.cfg.MinPoints)
	}
	if horizon <= 0 {
		return nil, ErrInvalidHorizon
	}

	// Compute interval between consecutive points.
	interval := computeInterval(series)

	// Convert to relative time in hours from first point.
	n := len(series)
	x := make([]float64, n) // relative time in hours
	y := make([]float64, n) // values
	firstTime := series[0].Timestamp
	for i := range series {
		x[i] = series[i].Timestamp.Sub(firstTime).Hours()
		y[i] = series[i].Value
	}

	// 1. Fit linear trend: y = intercept + slope * x
	intercept, slope, _ := linearRegression(x, y)

	// 2. Detrend: subtract trend to isolate seasonality
	detrended := make([]float64, n)
	for i := range detrended {
		detrended[i] = y[i] - (intercept + slope*x[i])
	}

	// 3. Fit Fourier series for daily seasonality (period = 24h)
	dailyCos, dailySin := fitFourier(detrended, x, prophetHoursInDay, e.cfg.FourierOrder)

	// 4. Fit Fourier series for weekly seasonality (period = 168h) on residuals
	dailyFitted := make([]float64, n)
	copy(dailyFitted, detrended)
	for k := 0; k < e.cfg.FourierOrder; k++ {
		for i := range dailyFitted {
			theta := 2 * math.Pi * float64(k+1) * x[i] / prophetHoursInDay
			dailyFitted[i] -= dailyCos[k]*math.Cos(theta) + dailySin[k]*math.Sin(theta)
		}
	}
	weeklyCos, weeklySin := fitFourier(dailyFitted, x, prophetHoursInWeek, e.cfg.FourierOrder)

	// 5. Compute residuals and CI parameters
	residuals := make([]float64, n)
	for i := range residuals {
		pred := intercept + slope*x[i]
		for k := 0; k < e.cfg.FourierOrder; k++ {
			thetaDaily := 2 * math.Pi * float64(k+1) * x[i] / prophetHoursInDay
			pred += dailyCos[k]*math.Cos(thetaDaily) + dailySin[k]*math.Sin(thetaDaily)
			thetaWeekly := 2 * math.Pi * float64(k+1) * x[i] / prophetHoursInWeek
			pred += weeklyCos[k]*math.Cos(thetaWeekly) + weeklySin[k]*math.Sin(thetaWeekly)
		}
		residuals[i] = y[i] - pred
	}

	_, residualStd := meanStddev(residuals)

	return &prophetModel{
		name:         "prophet_" + formatDuration(horizon),
		intercept:    intercept,
		slope:        slope,
		dailyCos:     dailyCos,
		dailySin:     dailySin,
		weeklyCos:    weeklyCos,
		weeklySin:    weeklySin,
		firstTime:    firstTime,
		intervalSec:  interval,
		trainedOn:    n,
		residualStd:  residualStd,
		residuals:    residuals,
		z80:          e.cfg.Z80,
		z95:          e.cfg.Z95,
		fourierOrder: e.cfg.FourierOrder,
	}, nil
}

// Predict delegates to the model's Predict method.
func (e *ProphetEngine) Predict(model Model, steps int) ([]Forecast, error) {
	return model.Predict(steps)
}

// ============================================================================
// Fourier fitting via least squares with OLS normal equations + LU decomposition
// ============================================================================

// fitFourier estimates the coefficients a_k, b_k for the Fourier series:
//
//	f(t) = Σ [a_k · cos(2π·k·t/P) + b_k · sin(2π·k·t/P)]
//
// This is a multiple linear regression on the cosine and sine basis functions.
// The OLS normal equations (XᵀX)β = Xᵀy are solved via Cholesky decomposition
// since XᵀX is symmetric positive-definite.
func fitFourier(values, t []float64, period float64, order int) (cos, sin []float64) {
	if order <= 0 {
		return nil, nil
	}

	cos = make([]float64, order)
	sin = make([]float64, order)

	if len(values) < 2*order {
		return cos, sin // not enough data
	}

	// Build the design matrix columns: for each k=1..order, columns [cos_k, sin_k]
	numBasis := 2 * order
	n := len(values)

	// Compute XᵀX and Xᵀy incrementally to avoid materializing X.
	xtx := make([][]float64, numBasis)
	for i := range xtx {
		xtx[i] = make([]float64, numBasis)
	}
	xty := make([]float64, numBasis)

	for i := 0; i < n; i++ {
		basis := make([]float64, numBasis)
		for k := 0; k < order; k++ {
			theta := 2 * math.Pi * float64(k+1) * t[i] / period
			basis[2*k] = math.Cos(theta)
			basis[2*k+1] = math.Sin(theta)
		}
		for p := 0; p < numBasis; p++ {
			xty[p] += basis[p] * values[i]
			for q := 0; q < numBasis; q++ {
				xtx[p][q] += basis[p] * basis[q]
			}
		}
	}

	// Solve (XᵀX)β = Xᵀy via Cholesky decomposition.
	// XᵀX is symmetric and positive semi-definite.
	coeffs := solveCholesky(xtx, xty)

	// Extract cos/sin coefficients.
	for k := 0; k < order; k++ {
		cos[k] = coeffs[2*k]
		sin[k] = coeffs[2*k+1]
	}
	return
}

// solveCholesky solves Ax = b via Cholesky decomposition (A must be SPD).
// Returns the zero vector if the matrix is singular.
func solveCholesky(A [][]float64, b []float64) []float64 {
	n := len(A)
	L := make([][]float64, n)
	for i := range L {
		L[i] = make([]float64, n)
	}

	// Cholesky: A = L Lᵀ
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := A[i][j]
			for k := 0; k < j; k++ {
				sum -= L[i][k] * L[j][k]
			}
			if i == j {
				if sum <= 1e-12 {
					return make([]float64, n) // singular
				}
				L[i][j] = math.Sqrt(sum)
			} else {
				L[i][j] = sum / L[j][j]
			}
		}
	}

	// Forward substitution: L y = b
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := b[i]
		for j := 0; j < i; j++ {
			sum -= L[i][j] * y[j]
		}
		y[i] = sum / L[i][i]
	}

	// Back substitution: Lᵀ x = y
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := y[i]
		for j := i + 1; j < n; j++ {
			sum -= L[j][i] * x[j]
		}
		x[i] = sum / L[i][i]
	}

	return x
}

// computeInterval estimates the median time interval between consecutive points in seconds.
func computeInterval(series []Point) float64 {
	if len(series) < 2 {
		return 3600 // default: 1 hour
	}
	// Use the most common interval (the first gap is usually representative).
	total := series[len(series)-1].Timestamp.Sub(series[0].Timestamp).Seconds()
	return total / float64(len(series)-1)
}

// Ensure math is used.

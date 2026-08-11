package recommend

// ============================================================================
// Risk assessment thresholds
// ============================================================================

// Risk thresholds define the boundary values used for risk classification.
// These are the critical levels that trigger different risk levels and actions.
// All values are derived from aquaculture best practices and the plan spec.
const (
	// DOCritical is the DO level (mg/L) below which fish are at immediate
	// risk of hypoxia. Triggers RiskHigh + AERATE action.
	DOCritical = 4.0

	// DOLow is the DO level (mg/L) at which early aeration should start.
	// Triggers RiskMedium + AERATE action.
	DOLow = 4.5

	// TempCritical is the temperature (°C) above which feeding must stop.
	// Above this, fish experience severe heat stress.
	TempCritical = 35.0

	// TempHigh is the temperature (°C) at which feeding should be reduced.
	TempHigh = 32.0

	// TempOptimalMin is the lower bound of the optimal temperature range.
	TempOptimalMin = 22.0

	// TempOptimalMax is the upper bound of the optimal temperature range.
	TempOptimalMax = 28.0

	// NH3Critical is the ammonia level (mg/L) above which water quality
	// is severely compromised. Triggers RiskHigh.
	NH3Critical = 1.0

	// NH3Elevated is the ammonia level (mg/L) at which water quality
	// monitoring should intensify.
	NH3Elevated = 0.5

	// FCRHigh is the FCR threshold above which feed efficiency is poor.
	FCRHigh = 2.5

	// FCRElevated is the FCR threshold for moderate feed efficiency concern.
	FCRElevated = 2.0

	// ConfidenceThreshold is the value below which recommendations are
	// flagged for manual review and risk is elevated to HIGH.
	ConfidenceThreshold = 0.7

	// MinGrowthGPerDay is the minimum acceptable daily growth rate (g/day).
	// Below this, growth is considered lagging.
	MinGrowthGPerDay = 0.5
)

// ============================================================================
// Trend detection thresholds
// ============================================================================

const (
	// DOTrendDecline is the rate of DO change that indicates a meaningful
	// downward trend. Used in forecast-based adjustments.
	DOTrendDecline = -0.5 // mg/L per forecast window
)

// ============================================================================
// Risk classification helpers
// ============================================================================

// IsCriticalDO returns true if the DO value is at a critical level.
func IsCriticalDO(do float64) bool {
	return do < DOCritical
}

// IsLowDO returns true if the DO value is below the early-aeration threshold.
func IsLowDO(do float64) bool {
	return do < DOLow
}

// IsCriticalTemp returns true if the temperature exceeds the critical threshold.
func IsCriticalTemp(temp float64) bool {
	return temp > TempCritical
}

// IsHighTemp returns true if the temperature is in the high-risk zone.
func IsHighTemp(temp float64) bool {
	return temp > TempHigh
}

// IsCriticalNH3 returns true if ammonia is at a critically high level.
func IsCriticalNH3(nh3 float64) bool {
	return nh3 > NH3Critical
}

// IsElevatedNH3 returns true if ammonia is elevated and requires monitoring.
func IsElevatedNH3(nh3 float64) bool {
	return nh3 > NH3Elevated
}

// IsHighFCR returns true if the FCR indicates poor feed efficiency.
func IsHighFCR(fcr float64) bool {
	return fcr > FCRHigh
}

// ShouldManualReview returns true when the confidence is below the threshold
// for automated recommendation acceptance.
func ShouldManualReview(confidence float64) bool {
	return confidence < ConfidenceThreshold
}

// RiskLevelString returns a Chinese description of the risk level for UI display.
func RiskLevelString(level RiskLevel) string {
	switch level {
	case RiskHigh:
		return "高风险"
	case RiskMedium:
		return "中等风险"
	case RiskLow:
		return "低风险"
	default:
		return "未知"
	}
}

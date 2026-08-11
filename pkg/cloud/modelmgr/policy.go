package modelmgr

import (
	"errors"
	"fmt"
	"os"
)

// ============================================================================
// Gate errors
// ============================================================================

var (
	// errEvalMetricsFailed is returned when eval metrics do not meet thresholds.
	errEvalMetricsFailed = errors.New("eval metrics below threshold")

	// errRollbackSafetyFailed is returned when the rollback safety check fails
	// (previous active model files not accessible).
	errRollbackSafetyFailed = errors.New("rollback safety check failed")

	// errSafetyGateUnset is returned when RequireSafetyGate is true but the
	// model's RuntimeRequirements do not include the safety gate flag.
	errSafetyGateUnset = errors.New("safety gate not set in runtime requirements")
)

// ============================================================================
// Gate 1: Eval metrics threshold check
// ============================================================================

// checkEvalMetrics verifies that all configured eval metric thresholds for
// the model's type are met. If no thresholds are configured for this type,
// the gate passes automatically.
func checkEvalMetrics(entry *modelEntry, cfg Config) error {
	thresholds, ok := cfg.EvalThresholds[entry.ModelType]
	if !ok || len(thresholds) == 0 {
		// No thresholds configured for this model type — gate passes.
		return nil
	}

	var failures []string
	for metricName, threshold := range thresholds {
		actual, exists := entry.Metadata.EvalMetrics[metricName]
		if !exists {
			failures = append(failures, fmt.Sprintf("metric %q not present in eval metrics", metricName))
			continue
		}
		if actual < threshold {
			failures = append(failures, fmt.Sprintf("metric %q: got %.4f, need >= %.4f",
				metricName, actual, threshold))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%w: %v", errEvalMetricsFailed, failures)
	}
	return nil
}

// ============================================================================
// Gate 2: Rollback safety check
// ============================================================================

// checkRollbackSafety verifies that if there is a currently active model for
// this name, its files are still retrievable on disk. This ensures rollback
// is possible if the new activation causes issues.
//
// If there is no previous active model (first activation for this name),
// the gate passes vacuously.
func (r *ModelRegistry) checkRollbackSafety(entry *modelEntry) error {
	// Find the currently active model for this name (if any).
	activeID, hasActive := r.active[entry.Name]
	if !hasActive {
		// First activation for this name — nothing to roll back to, gate passes.
		return nil
	}

	// If the active model is the same as the one being activated (re-activation),
	// gate passes.
	if activeID == entry.ID {
		return nil
	}

	activeEntry, ok := r.entries[activeID]
	if !ok {
		// Active entry not in memory — unexpected, but gate fails.
		return fmt.Errorf("%w: active model %q not found in registry", errRollbackSafetyFailed, activeID)
	}

	// Verify the active model's files still exist on disk.
	modelPath := activeEntry.dirPath
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("%w: cannot stat previous active model at %q: %w",
			errRollbackSafetyFailed, modelPath, err)
	}

	// Verify model.bin exists and is readable.
	binPath := modelPath + "/model.bin"
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("%w: previous active model.bin not found at %q: %w",
			errRollbackSafetyFailed, binPath, err)
	}

	// Verify entry.json exists and is readable.
	entryPath := modelPath + "/entry.json"
	if _, err := os.Stat(entryPath); err != nil {
		return fmt.Errorf("%w: previous active entry.json not found at %q: %w",
			errRollbackSafetyFailed, entryPath, err)
	}

	return nil
}

// ============================================================================
// Gate 3: Safety gate check
// ============================================================================

// checkSafetyGate verifies that the model's RuntimeRequirements include the
// safety gate flag when RequireSafetyGate is true. When RequireSafetyGate is
// false, this gate passes automatically.
func checkSafetyGate(entry *modelEntry, cfg Config) error {
	if !cfg.RequireSafetyGate {
		return nil
	}

	// Look for the safety_gate key in RuntimeRequirements.
	val, ok := entry.Metadata.RuntimeRequirements["safety_gate"]
	if !ok || val != "true" {
		return fmt.Errorf("%w: runtime requirement 'safety_gate' must be set to 'true'", errSafetyGateUnset)
	}
	return nil
}

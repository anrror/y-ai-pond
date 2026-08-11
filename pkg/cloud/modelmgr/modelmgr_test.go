package modelmgr

import (
	"encoding/json"
	goerrors "errors"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================================
// Test helper
// ============================================================================

// newTestRegistry creates a registry backed by t.TempDir() with default config.
func newTestRegistry(t *testing.T) *ModelRegistry {
	t.Helper()
	cfg := Config{
		RegistryRoot: t.TempDir(),
	}
	r, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// testMetadata returns a valid ModelMetadata for testing.
func testMetadata() ModelMetadata {
	return ModelMetadata{
		Src:          "test-run-001",
		Name:         "test-model",
		ModelType:    ModelTypeForecast,
		Version:      "1.0.0",
		TrainingDate: "2026-08-11",
		EvalMetrics: map[string]float64{
			"rmse":      0.12,
			"r_squared": 0.94,
			"mape":      5.2,
		},
		RuntimeRequirements: map[string]string{
			"framework":   "onnxer",
			"safety_gate": "true",
		},
		Inputs:  []string{"do", "temp", "nh3"},
		Outputs: []string{"do_pred"},
	}
}

// ============================================================================
// TestUploadAndMetadata
// ============================================================================

func TestUploadAndMetadata(t *testing.T) {
	reg := newTestRegistry(t)
	meta := testMetadata()
	modelBytes := []byte("fake-model-binary-content")

	id, err := reg.Upload("do-forecast", ModelTypeForecast, "1.0.0", modelBytes, meta)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if id != "do-forecast@1.0.0" {
		t.Errorf("expected id 'do-forecast@1.0.0', got %q", id)
	}

	// Verify files exist on disk.
	versionDir := filepath.Join(reg.cfg.RegistryRoot, "do-forecast", "1.0.0")
	verifyFileExists(t, filepath.Join(versionDir, "model.bin"))
	verifyFileExists(t, filepath.Join(versionDir, "entry.json"))

	// Verify model.bin content.
	readBytes, err := os.ReadFile(filepath.Join(versionDir, "model.bin"))
	if err != nil {
		t.Fatalf("ReadFile model.bin: %v", err)
	}
	if string(readBytes) != string(modelBytes) {
		t.Errorf("model.bin content mismatch: got %q, want %q", readBytes, modelBytes)
	}

	// Verify metadata round-trips via JSON.
	entry, err := reg.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.State != StateUploaded {
		t.Errorf("expected state %s, got %s", StateUploaded, entry.State)
	}
	if entry.Metadata.Src != "test-run-001" {
		t.Errorf("expected Src 'test-run-001', got %q", entry.Metadata.Src)
	}
	if entry.Metadata.EvalMetrics["rmse"] != 0.12 {
		t.Errorf("expected rmse 0.12, got %v", entry.Metadata.EvalMetrics["rmse"])
	}
	if entry.Metadata.SHA256 == "" {
		t.Error("expected SHA256 to be computed automatically")
	}

	// Verify SHA256 round-trips.
	if len(entry.Metadata.SHA256) != 64 {
		t.Errorf("expected SHA256 hex to be 64 chars, got %d", len(entry.Metadata.SHA256))
	}

	// Verify version conflict.
	_, err = reg.Upload("do-forecast", ModelTypeForecast, "1.0.0", modelBytes, meta)
	if !goerrors.Is(err, ErrVersionConflict) {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}

	// Verify metadata JSON serialization.
	data, err := json.Marshal(entry.Metadata)
	if err != nil {
		t.Fatalf("json.Marshal metadata: %v", err)
	}
	var restored ModelMetadata
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal metadata: %v", err)
	}
	if len(restored.Inputs) != 3 || restored.Inputs[0] != "do" {
		t.Errorf("metadata inputs not round-tripped: %v", restored.Inputs)
	}
}

// ============================================================================
// TestVersionSemver
// ============================================================================

func TestVersionSemver(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int // -1: a < b, 0: a == b, 1: a > b
	}{
		{"0.1.0", "1.0.0", -1},
		{"1.0.0", "1.2.0", -1},
		{"1.0.0", "1.0.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0", "1.0.0-alpha", 1},
	}

	for _, tc := range tests {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			cmp, err := compareVersionStrings(tc.a, tc.b)
			if err != nil {
				t.Fatalf("compareVersionStrings(%q, %q): %v", tc.a, tc.b, err)
			}
			if cmp != tc.expected {
				t.Errorf("compareVersionStrings(%q, %q) = %d, want %d", tc.a, tc.b, cmp, tc.expected)
			}
		})
	}

	// Verify ParseVersion error for invalid version.
	_, err := ParseVersion("not-a-version")
	if err == nil {
		t.Error("expected error for invalid version string")
	}

	// Verify MustParseVersion.
	v := MustParseVersion("2.1.3")
	if v.String() != "2.1.3" {
		t.Errorf("MustParseVersion: got %q, want '2.1.3'", v.String())
	}
}

// ============================================================================
// TestLifecycle
// ============================================================================

func TestLifecycle(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := Config{
		RegistryRoot: reg.cfg.RegistryRoot,
	}
	r, _ := NewRegistry(cfg) // re-create to test scan
	_ = r.Close()            // keep original reg

	meta := testMetadata()
	_, err := reg.Upload("lifecycle-test", ModelTypeForecast, "1.0.0", []byte("model-data"), meta)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	id := "lifecycle-test@1.0.0"

	// State after upload: uploaded.
	entry, _ := reg.Get(id)
	if entry.State != StateUploaded {
		t.Errorf("after upload: expected %s, got %s", StateUploaded, entry.State)
	}

	// Validate.
	scope := ValidationScope{
		ModelType: ModelTypeForecast,
		Inputs:    []string{"do", "temp", "nh3"},
		Outputs:   []string{"do_pred"},
	}
	err = reg.Validate(id, scope)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	entry, _ = reg.Get(id)
	if entry.State != StateValidated {
		t.Errorf("after validate: expected %s, got %s", StateValidated, entry.State)
	}

	// Activate.
	err = reg.Activate(id)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	entry, _ = reg.Get(id)
	if entry.State != StateActive {
		t.Errorf("after activate: expected %s, got %s", StateActive, entry.State)
	}

	// Verify GetActive.
	active, err := reg.GetActive("lifecycle-test")
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if active.ID != id {
		t.Errorf("GetActive: expected id %q, got %q", id, active.ID)
	}

	// Retire.
	err = reg.Retire(id)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	entry, _ = reg.Get(id)
	if entry.State != StateRetired {
		t.Errorf("after retire: expected %s, got %s", StateRetired, entry.State)
	}

	// Verify active is gone after retire.
	_, err = reg.GetActive("lifecycle-test")
	if !goerrors.Is(err, ErrModelNotFound) {
		t.Errorf("after retire GetActive: expected ErrModelNotFound, got %v", err)
	}

	// Verify retired can be deleted.
	err = reg.Delete(id)
	if err != nil {
		t.Fatalf("Delete retired: %v", err)
	}
	_, err = reg.Get(id)
	if !goerrors.Is(err, ErrModelNotFound) {
		t.Errorf("after delete: expected ErrModelNotFound, got %v", err)
	}
}

// ============================================================================
// TestActivationGates
// ============================================================================

func TestActivationGates(t *testing.T) {
	t.Run("Gate1_EvalMetricsBelowThreshold", func(t *testing.T) {
		reg := newTestRegistry(t)
		// Set a threshold that the model can't meet (model rmse=0.12 < threshold=0.50).
		reg.cfg.EvalThresholds = map[ModelType]map[string]float64{
			ModelTypeForecast: {"rmse": 0.50},
		}

		meta := testMetadata()
		meta.EvalMetrics["rmse"] = 0.12

		id, _ := reg.Upload("gate-test", ModelTypeForecast, "1.0.0", []byte("data"), meta)
		_ = reg.Validate(id, ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		})

		err := reg.Activate(id)
		if !goerrors.Is(err, ErrActivationGateFailed) {
			t.Fatalf("expected ErrActivationGateFailed, got %v", err)
		}
		// Verify model stayed in validated state (not active).
		entry, _ := reg.Get(id)
		if entry.State != StateValidated {
			t.Errorf("expected state %s, got %s", StateValidated, entry.State)
		}
	})

	t.Run("Gate1_EvalMetricsPass", func(t *testing.T) {
		reg := newTestRegistry(t)
		reg.cfg.EvalThresholds = map[ModelType]map[string]float64{
			ModelTypeForecast: {"rmse": 0.05}, // model rmse=0.12 >= 0.05, passes
		}

		meta := testMetadata()
		meta.EvalMetrics["rmse"] = 0.12

		id, _ := reg.Upload("gate-pass", ModelTypeForecast, "1.0.0", []byte("data"), meta)
		_ = reg.Validate(id, ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		})

		if err := reg.Activate(id); err != nil {
			t.Fatalf("expected activation to pass, got %v", err)
		}
	})

	t.Run("Gate2_RollbackSafety", func(t *testing.T) {
		reg := newTestRegistry(t)
		meta := testMetadata()

		// Upload and activate v1.
		id1, _ := reg.Upload("rollback-test", ModelTypeForecast, "1.0.0", []byte("v1"), meta)
		_ = reg.Validate(id1, ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		})
		if err := reg.Activate(id1); err != nil {
			t.Fatalf("activate v1: %v", err)
		}

		// Upload v2.
		id2, _ := reg.Upload("rollback-test", ModelTypeForecast, "2.0.0", []byte("v2"), meta)
		_ = reg.Validate(id2, ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		})

		// Manually delete v1 files to break rollback safety.
		v1Dir := filepath.Join(reg.cfg.RegistryRoot, "rollback-test", "1.0.0")
		if err := os.RemoveAll(v1Dir); err != nil {
			t.Fatalf("remove v1 dir: %v", err)
		}

		err := reg.Activate(id2)
		if !goerrors.Is(err, ErrActivationGateFailed) {
			t.Fatalf("expected ErrActivationGateFailed for broken rollback, got %v", err)
		}

		// Verify v2 not activated.
		entry, _ := reg.Get(id2)
		if entry.State == StateActive {
			t.Error("v2 should not be active after failed activation")
		}
	})

	t.Run("Gate3_SafetyGateUnset", func(t *testing.T) {
		reg := newTestRegistry(t)
		reg.cfg.RequireSafetyGate = true

		meta := testMetadata()
		// Remove safety_gate from runtime requirements.
		meta.RuntimeRequirements = map[string]string{"framework": "onnxer"}
		// Note: safety_gate key is now absent.

		id, _ := reg.Upload("safety-test", ModelTypeForecast, "1.0.0", []byte("data"), meta)
		_ = reg.Validate(id, ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		})

		err := reg.Activate(id)
		if !goerrors.Is(err, ErrActivationGateFailed) {
			t.Fatalf("expected ErrActivationGateFailed for missing safety gate, got %v", err)
		}
	})

	t.Run("Gate3_SafetyGateSet", func(t *testing.T) {
		reg := newTestRegistry(t)
		reg.cfg.RequireSafetyGate = true

		meta := testMetadata() // has safety_gate: true
		id, _ := reg.Upload("safety-ok", ModelTypeForecast, "1.0.0", []byte("data"), meta)
		_ = reg.Validate(id, ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		})

		if err := reg.Activate(id); err != nil {
			t.Fatalf("expected activation to pass with safety gate set, got %v", err)
		}
	})
}

// ============================================================================
// TestRollback
// ============================================================================

func TestRollback(t *testing.T) {
	reg := newTestRegistry(t)
	meta := testMetadata()

	// Upload and activate v1.
	id1, _ := reg.Upload("rollback", ModelTypeForecast, "1.0.0", []byte("v1"), meta)
	_ = reg.Validate(id1, ValidationScope{
		ModelType: ModelTypeForecast, Inputs: []string{"do", "temp", "nh3"}, Outputs: []string{"do_pred"},
	})
	if err := reg.Activate(id1); err != nil {
		t.Fatalf("activate v1: %v", err)
	}

	// Upload and activate v2 (replaces v1).
	id2, _ := reg.Upload("rollback", ModelTypeForecast, "2.0.0", []byte("v2"), meta)
	_ = reg.Validate(id2, ValidationScope{
		ModelType: ModelTypeForecast, Inputs: []string{"do", "temp", "nh3"}, Outputs: []string{"do_pred"},
	})
	if err := reg.Activate(id2); err != nil {
		t.Fatalf("activate v2: %v", err)
	}

	// v1 should now be retired.
	entry1, _ := reg.Get(id1)
	if entry1.State != StateRetired {
		t.Errorf("v1 should be retired after v2 activation, got %s", entry1.State)
	}

	// v2 should be active.
	active, _ := reg.GetActive("rollback")
	if active.ID != id2 {
		t.Errorf("expected active id %q, got %q", id2, active.ID)
	}

	// Rollback to v1.
	if err := reg.Rollback("rollback", "1.0.0"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// v1 should now be active.
	entry1, _ = reg.Get(id1)
	if entry1.State != StateActive {
		t.Errorf("v1 should be active after rollback, got %s", entry1.State)
	}

	// v2 should now be retired.
	entry2, _ := reg.Get(id2)
	if entry2.State != StateRetired {
		t.Errorf("v2 should be retired after rollback, got %s", entry2.State)
	}

	// v2 should now be deletable (retired).
	if err := reg.Delete(id2); err != nil {
		t.Errorf("v2 should be deletable after rollback, got %v", err)
	}
}

// ============================================================================
// TestDeletePolicy
// ============================================================================

func TestDeletePolicy(t *testing.T) {
	reg := newTestRegistry(t)
	meta := testMetadata()

	// Upload and activate a model.
	id, _ := reg.Upload("delete-test", ModelTypeForecast, "1.0.0", []byte("data"), meta)
	_ = reg.Validate(id, ValidationScope{
		ModelType: ModelTypeForecast, Inputs: []string{"do", "temp", "nh3"}, Outputs: []string{"do_pred"},
	})
	if err := reg.Activate(id); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// Try to delete active model — should fail.
	err := reg.Delete(id)
	if !goerrors.Is(err, ErrActiveModelCannotDelete) {
		t.Fatalf("expected ErrActiveModelCannotDelete, got %v", err)
	}

	// Verify model still exists.
	entry, getErr := reg.Get(id)
	if getErr != nil {
		t.Fatalf("model should still exist after failed delete: %v", getErr)
	}
	if entry.State != StateActive {
		t.Errorf("expected state %s, got %s", StateActive, entry.State)
	}

	// Retire the model.
	err = reg.Retire(id)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}

	// Now delete should succeed.
	err = reg.Delete(id)
	if err != nil {
		t.Fatalf("Delete retired: %v", err)
	}

	// Model should be gone.
	_, err = reg.Get(id)
	if !goerrors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound after delete, got %v", err)
	}
}

// ============================================================================
// TestScopeCheck
// ============================================================================

func TestScopeCheck(t *testing.T) {
	reg := newTestRegistry(t)
	meta := testMetadata()

	t.Run("MismatchedModelType", func(t *testing.T) {
		id, _ := reg.Upload("scope-type", ModelTypeRL, "1.0.0", []byte("data"), meta)

		scope := ValidationScope{
			ModelType: ModelTypeForecast, // different type
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		}
		err := reg.Validate(id, scope)
		if !goerrors.Is(err, ErrScopeMismatch) {
			t.Fatalf("expected ErrScopeMismatch for model type mismatch, got %v", err)
		}
	})

	t.Run("MismatchedInputs", func(t *testing.T) {
		id, _ := reg.Upload("scope-inputs", ModelTypeForecast, "1.0.0", []byte("data"), meta)

		scope := ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"wrong_input"}, // different inputs
			Outputs:   []string{"do_pred"},
		}
		err := reg.Validate(id, scope)
		if !goerrors.Is(err, ErrScopeMismatch) {
			t.Fatalf("expected ErrScopeMismatch for input mismatch, got %v", err)
		}
	})

	t.Run("MismatchedOutputs", func(t *testing.T) {
		id, _ := reg.Upload("scope-outputs", ModelTypeForecast, "1.0.0", []byte("data"), meta)

		scope := ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"wrong_output"}, // different outputs
		}
		err := reg.Validate(id, scope)
		if !goerrors.Is(err, ErrScopeMismatch) {
			t.Fatalf("expected ErrScopeMismatch for output mismatch, got %v", err)
		}
	})

	t.Run("PassingScope", func(t *testing.T) {
		id, _ := reg.Upload("scope-ok", ModelTypeForecast, "1.0.0", []byte("data"), meta)

		scope := ValidationScope{
			ModelType: ModelTypeForecast,
			Inputs:    []string{"do", "temp", "nh3"},
			Outputs:   []string{"do_pred"},
		}
		if err := reg.Validate(id, scope); err != nil {
			t.Fatalf("expected Validate to pass, got %v", err)
		}

		entry, _ := reg.Get(id)
		if entry.State != StateValidated {
			t.Errorf("expected state %s, got %s", StateValidated, entry.State)
		}
	})
}

// ============================================================================
// TestPersistence
// ============================================================================

func TestPersistence(t *testing.T) {
	root := t.TempDir()

	// Create and populate a registry.
	cfg := Config{RegistryRoot: root}
	reg1, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry 1: %v", err)
	}

	meta := testMetadata()
	id, _ := reg1.Upload("persist", ModelTypeForecast, "1.0.0", []byte("model-data"), meta)
	_ = reg1.Validate(id, ValidationScope{
		ModelType: ModelTypeForecast, Inputs: []string{"do", "temp", "nh3"}, Outputs: []string{"do_pred"},
	})
	err = reg1.Activate(id)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	_ = reg1.Close()

	// Create a new registry over the same root — it should see the existing models.
	reg2, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry 2: %v", err)
	}
	defer func() { _ = reg2.Close() }()

	entry, err := reg2.Get(id)
	if err != nil {
		t.Fatalf("reg2.Get: %v", err)
	}
	if entry.State != StateActive {
		t.Errorf("expected state %s, got %s", StateActive, entry.State)
	}
	if entry.Metadata.Src != "test-run-001" {
		t.Errorf("expected Src 'test-run-001', got %q", entry.Metadata.Src)
	}
	if entry.Metadata.EvalMetrics["r_squared"] != 0.94 {
		t.Errorf("expected r_squared 0.94, got %v", entry.Metadata.EvalMetrics["r_squared"])
	}

	// Verify GetActive works after reload.
	active, err := reg2.GetActive("persist")
	if err != nil {
		t.Fatalf("reg2.GetActive: %v", err)
	}
	if active.ID != id {
		t.Errorf("expected active id %q, got %q", id, active.ID)
	}

	// Verify List works after reload.
	entries, err := reg2.List("persist")
	if err != nil {
		t.Fatalf("reg2.List: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}

	// Verify ListAll works.
	all := reg2.ListAll()
	if len(all) != 1 {
		t.Errorf("expected 1 total entry, got %d", len(all))
	}
}

// ============================================================================
// TestInvalidName
// ============================================================================

func TestInvalidName(t *testing.T) {
	reg := newTestRegistry(t)
	meta := testMetadata()

	invalidNames := []string{
		"",
		"UPPERCASE",
		"has spaces",
		"special!char",
		"中文名",
		"path/traversal",
		"../../etc/passwd",
	}

	for _, name := range invalidNames {
		t.Run("name="+name, func(t *testing.T) {
			_, err := reg.Upload(name, ModelTypeForecast, "1.0.0", []byte("data"), meta)
			if !goerrors.Is(err, ErrInvalidName) {
				t.Errorf("expected ErrInvalidName for %q, got %v", name, err)
			}
		})
	}
}

// ============================================================================
// TestNonExistentModel
// ============================================================================

func TestNonExistentModel(t *testing.T) {
	reg := newTestRegistry(t)

	// Get non-existent model.
	_, err := reg.Get("nonexistent@1.0.0")
	if !goerrors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound for Get, got %v", err)
	}

	// GetActive for non-existent name.
	_, err = reg.GetActive("nonexistent")
	if !goerrors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound for GetActive, got %v", err)
	}

	// Delete non-existent model.
	err = reg.Delete("nonexistent@1.0.0")
	if !goerrors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound for Delete, got %v", err)
	}

	// Activate non-existent model.
	err = reg.Activate("nonexistent@1.0.0")
	if !goerrors.Is(err, ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound for Activate, got %v", err)
	}
}

// ============================================================================
// TestList
// ============================================================================

func TestList(t *testing.T) {
	reg := newTestRegistry(t)
	meta := testMetadata()

	// Upload multiple versions of the same model.
	if _, err := reg.Upload("mymodel", ModelTypeForecast, "1.0.0", []byte("v1"), meta); err != nil {
		t.Fatalf("Upload v1: %v", err)
	}
	if _, err := reg.Upload("mymodel", ModelTypeForecast, "1.1.0", []byte("v1.1"), meta); err != nil {
		t.Fatalf("Upload v1.1: %v", err)
	}
	if _, err := reg.Upload("mymodel", ModelTypeForecast, "2.0.0", []byte("v2"), meta); err != nil {
		t.Fatalf("Upload v2: %v", err)
	}

	// Upload a different model.
	if _, err := reg.Upload("othermodel", ModelTypeGrowth, "0.1.0", []byte("other"), meta); err != nil {
		t.Fatalf("Upload other: %v", err)
	}

	entries, err := reg.List("mymodel")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 entries for mymodel, got %d", len(entries))
	}

	// Verify ListAll.
	all := reg.ListAll()
	if len(all) != 4 {
		t.Errorf("expected 4 total entries, got %d", len(all))
	}
}

// ============================================================================
// TestRetireNonActive
// ============================================================================

func TestRetireNonActive(t *testing.T) {
	reg := newTestRegistry(t)
	meta := testMetadata()

	id, _ := reg.Upload("retire-test", ModelTypeForecast, "1.0.0", []byte("data"), meta)
	_ = reg.Validate(id, ValidationScope{
		ModelType: ModelTypeForecast, Inputs: []string{"do", "temp", "nh3"}, Outputs: []string{"do_pred"},
	})

	// Try to retire a validated (not active) model.
	err := reg.Retire(id)
	if !goerrors.Is(err, ErrInvalidState) {
		t.Errorf("expected ErrInvalidState for retiring non-active model, got %v", err)
	}
}

// ============================================================================
// TestRollbackErrors
// ============================================================================

func TestRollbackErrors(t *testing.T) {
	reg := newTestRegistry(t)

	// Rollback when nothing is active.
	err := reg.Rollback("no-model", "1.0.0")
	if !goerrors.Is(err, ErrNoActiveModel) {
		t.Errorf("expected ErrNoActiveModel, got %v", err)
	}

	// Upload and activate a model, then rollback to a non-existent target.
	meta := testMetadata()
	id, _ := reg.Upload("re", ModelTypeForecast, "1.0.0", []byte("data"), meta)
	_ = reg.Validate(id, ValidationScope{
		ModelType: ModelTypeForecast, Inputs: []string{"do", "temp", "nh3"}, Outputs: []string{"do_pred"},
	})
	_ = reg.Activate(id)

	err = reg.Rollback("re", "9.9.9")
	if !goerrors.Is(err, ErrTargetVersionNotFound) {
		t.Errorf("expected ErrTargetVersionNotFound, got %v", err)
	}
}

// ============================================================================
// helpers
// ============================================================================

func verifyFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file %q does not exist: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("expected file, got directory: %q", path)
	}
}

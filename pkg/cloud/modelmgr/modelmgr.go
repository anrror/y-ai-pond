package modelmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// ============================================================================
// Domain errors
// ============================================================================

var (
	// ErrModelNotFound is returned when a model ID or name/version is not
	// found in the registry.
	ErrModelNotFound = errors.New("modelmgr: model not found")

	// ErrVersionConflict is returned when uploading a model version that
	// already exists in the registry.
	ErrVersionConflict = errors.New("modelmgr: version already exists")

	// ErrActiveModelCannotDelete is returned when attempting to delete an
	// active model. Active models must be retired or rolled back first.
	ErrActiveModelCannotDelete = errors.New("modelmgr: cannot delete active model")

	// ErrActivationGateFailed is returned when one or more activation gates
	// fail during the Activate operation. The underlying error describes
	// which gate(s) failed.
	ErrActivationGateFailed = errors.New("modelmgr: activation gate(s) failed")

	// ErrInvalidState is returned when an operation is attempted on a model
	// in an incompatible state (e.g. activating a non-validated model).
	ErrInvalidState = errors.New("modelmgr: invalid model state for operation")

	// ErrInvalidName is returned when a model name contains invalid
	// characters or exceeds the length limit.
	ErrInvalidName = errors.New("modelmgr: invalid model name")

	// ErrScopeMismatch is returned by Validate when the model's declared
	// type, inputs, or outputs do not match the expected scope.
	ErrScopeMismatch = errors.New("modelmgr: scope mismatch")

	// ErrNoActiveModel is returned when a rollback is attempted but there
	// is no active model to roll back from.
	ErrNoActiveModel = errors.New("modelmgr: no active model for rollback")

	// ErrTargetVersionNotFound is returned when a rollback target version
	// is not found in the registry.
	ErrTargetVersionNotFound = errors.New("modelmgr: target rollback version not found")
)

// ============================================================================
// Config
// ============================================================================

// Config holds the configuration for a ModelRegistry.
type Config struct {
	// RegistryRoot is the filesystem path where model files and metadata
	// are stored. The directory is created on NewRegistry if it does not
	// exist.
	RegistryRoot string `json:"registry_root"`

	// EvalThresholds maps model types to their evaluation metric thresholds.
	// During activation, each metric in the model's EvalMetrics is compared
	// against the threshold for its type. If any metric falls below its
	// threshold, the activation gate fails.
	//
	// Example:
	//
	//	Config{
	//	  EvalThresholds: map[ModelType]map[string]float64{
	//	    ModelTypeForecast: {"rmse": 0.5, "r_squared": 0.7},
	//	    ModelTypeRL:       {"reward": 0.0},
	//	  },
	//	}
	//
	// If a model type has no configured thresholds, the eval metrics gate
	// passes automatically (no thresholds to check).
	EvalThresholds map[ModelType]map[string]float64 `json:"eval_thresholds"`

	// RequireSafetyGate controls whether the safety gate check is enforced
	// during activation. When true, the model's RuntimeRequirements must
	// include a "safety_gate" entry set to "true".
	// Default: false.
	RequireSafetyGate bool `json:"require_safety_gate"`
}

// DefaultConfig returns a Config with sensible defaults suitable for
// local development and testing.
func DefaultConfig() Config {
	return Config{
		RegistryRoot:      "models",
		EvalThresholds:    nil,
		RequireSafetyGate: false,
	}
}

// ============================================================================
// modelEntry (in-memory)
// ============================================================================

// modelEntry is the in-memory representation of a registered model.
type modelEntry struct {
	ID        string
	Name      string
	ModelType ModelType
	Version   string
	State     ModelState
	Metadata  ModelMetadata
	// dirPath is the absolute path to the version directory on disk.
	dirPath string
}

// toPersisted converts the in-memory entry to the persisted form.
func (e *modelEntry) toPersisted() persistedEntry {
	return persistedEntry{
		ID:        e.ID,
		Name:      e.Name,
		ModelType: e.ModelType,
		Version:   e.Version,
		State:     e.State,
		Metadata:  e.Metadata,
	}
}

// ============================================================================
// ModelRegistry
// ============================================================================

// ModelRegistry is a filesystem-backed model registry with versioning,
// lifecycle management, and activation policy enforcement.
//
// The registry is generic — it does not couple to any specific model
// implementation (ONNX, RKNN, etc.) and accepts arbitrary ModelType strings.
// Model binaries are stored as opaque bytes on the filesystem.
//
// Concurrency: safe for concurrent use via sync.RWMutex.
type ModelRegistry struct {
	mu      sync.RWMutex
	cfg     Config
	entries map[string]*modelEntry // keyed by ID
	active  map[string]string      // name → active ID
}

// Compile-time interface assertion.
var _ interface{ Close() error } = (*ModelRegistry)(nil)

// NewRegistry creates a ModelRegistry backed by the given Config.
// It creates the registry root directory if it does not exist, then
// scans the filesystem to rebuild the in-memory index from persisted
// entry.json files. Returns an error if the root cannot be created
// or if existing entries cannot be read.
func NewRegistry(cfg Config) (*ModelRegistry, error) {
	if err := os.MkdirAll(cfg.RegistryRoot, 0750); err != nil {
		return nil, fmt.Errorf("modelmgr: create registry root %q: %w", cfg.RegistryRoot, err)
	}
	r := &ModelRegistry{
		cfg:     cfg,
		entries: make(map[string]*modelEntry),
		active:  make(map[string]string),
	}
	if err := r.scanFilesystem(); err != nil {
		return nil, fmt.Errorf("modelmgr: scan registry: %w", err)
	}
	return r, nil
}

// ============================================================================
// Upload
// ============================================================================

// Upload stores a model binary and metadata in the registry. The model enters
// the StateUploaded state. If a model with the same name and version already
// exists, ErrVersionConflict is returned.
//
// Parameters:
//   - name: model name (must be a safe, sanitized identifier)
//   - modelType: model category (forecast, growth, rl, dt, or arbitrary)
//   - version: semantic version string (e.g. "1.2.0")
//   - file: opaque model bytes
//   - metadata: model metadata (Src, TrainingDate, EvalMetrics, etc.)
//
// Returns the model ID (format: "name@version") and nil error on success.
func (r *ModelRegistry) Upload(name string, modelType ModelType, version string, file []byte, metadata ModelMetadata) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	sv, err := ParseVersion(version)
	if err != nil {
		return "", fmt.Errorf("modelmgr: %w", err)
	}

	id := fmt.Sprintf("%s@%s", name, version)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[id]; exists {
		return "", fmt.Errorf("modelmgr: %s: %w", id, ErrVersionConflict)
	}

	dirPath := filepath.Join(r.cfg.RegistryRoot, name, sv.String())
	if err := os.MkdirAll(dirPath, 0750); err != nil {
		return "", fmt.Errorf("modelmgr: create version dir %q: %w", dirPath, err)
	}

	// Write model binary.
	modelPath := filepath.Join(dirPath, "model.bin")
	if err := os.WriteFile(modelPath, file, 0600); err != nil {
		return "", fmt.Errorf("modelmgr: write model.bin: %w", err)
	}

	// Compute SHA256 if not provided.
	if metadata.SHA256 == "" {
		h := sha256.Sum256(file)
		metadata.SHA256 = hex.EncodeToString(h[:])
	}
	// Sync metadata name/type/version from arguments.
	metadata.Name = name
	metadata.ModelType = modelType
	metadata.Version = sv.String()

	entry := &modelEntry{
		ID:        id,
		Name:      name,
		ModelType: modelType,
		Version:   sv.String(),
		State:     StateUploaded,
		Metadata:  metadata,
		dirPath:   dirPath,
	}

	if err := r.persistEntry(entry); err != nil {
		return "", err
	}

	r.entries[id] = entry
	return id, nil
}

// ============================================================================
// Validate
// ============================================================================

// Validate performs a build scope check: it compares the model's declared
// model_type, inputs, and outputs against the expected scope. On success,
// the model transitions to StateValidated.
//
// A model in any state can be validated (re-validation is allowed). This
// resets the model back to StateValidated.
func (r *ModelRegistry) Validate(id string, scope ValidationScope) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("modelmgr: %s: %w", id, ErrModelNotFound)
	}

	if err := checkScope(entry, scope); err != nil {
		return err
	}

	entry.State = StateValidated
	return r.persistEntry(entry)
}

// ============================================================================
// Activate
// ============================================================================

// Activate makes a validated model the active version for its name. It
// enforces the 3-gate activation policy:
//
//  1. Eval metrics gate: all metrics in the model's EvalMetrics must meet
//     or exceed the configured thresholds for its ModelType.
//  2. Rollback safety gate: if there is a previously active model for this
//     name, its files must still be retrievable on disk (i.e., the rollback
//     path is tested and passes).
//  3. Safety gate: if RequireSafetyGate is true, the model's
//     RuntimeRequirements must include "safety_gate" = "true".
//
// On success, the previously active model (if any) is retired, and this
// model becomes StateActive. Returns ErrActivationGateFailed if any gate
// fails, with a descriptive cause.
func (r *ModelRegistry) Activate(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("modelmgr: %s: %w", id, ErrModelNotFound)
	}

	// --- Gate 1: eval metrics ---
	if err := checkEvalMetrics(entry, r.cfg); err != nil {
		return fmt.Errorf("%w: metrics gate: %w", ErrActivationGateFailed, err)
	}

	// --- Gate 2: rollback safety ---
	if err := r.checkRollbackSafety(entry); err != nil {
		return fmt.Errorf("%w: rollback gate: %w", ErrActivationGateFailed, err)
	}

	// --- Gate 3: safety gate ---
	if err := checkSafetyGate(entry, r.cfg); err != nil {
		return fmt.Errorf("%w: safety gate: %w", ErrActivationGateFailed, err)
	}

	// Retire the current active version for this name (if any).
	r.retireActiveForName(entry.Name)

	entry.State = StateActive
	r.active[entry.Name] = entry.ID
	return r.persistEntry(entry)
}

// ============================================================================
// Retire
// ============================================================================

// Retire transitions an active model to StateRetired. After retirement,
// the model becomes deletable. Returns ErrInvalidState if the model is
// not active.
func (r *ModelRegistry) Retire(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("modelmgr: %s: %w", id, ErrModelNotFound)
	}
	if entry.State != StateActive {
		return fmt.Errorf("modelmgr: %s in state %s: %w", id, entry.State, ErrInvalidState)
	}

	entry.State = StateRetired
	if r.active[entry.Name] == id {
		delete(r.active, entry.Name)
	}
	return r.persistEntry(entry)
}

// ============================================================================
// Rollback
// ============================================================================

// Rollback activates a target version and retires the current active version
// for the same name. This bypasses the 3-gate activation policy — the target
// was previously validated and trusted. After rollback, the replaced version
// (the previously active one) becomes deletable.
//
// Returns an error if there is no active model for the name, or if the target
// version is not found.
func (r *ModelRegistry) Rollback(name string, targetVersion string) error {
	if err := validateName(name); err != nil {
		return err
	}
	sv, err := ParseVersion(targetVersion)
	if err != nil {
		return fmt.Errorf("modelmgr: target version: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check active model first — if none is active, report ErrNoActiveModel
	// regardless of whether the target version exists.
	currentActiveID, hasActive := r.active[name]
	if !hasActive {
		return fmt.Errorf("modelmgr: %s: %w", name, ErrNoActiveModel)
	}

	targetID := fmt.Sprintf("%s@%s", name, sv.String())
	target, ok := r.entries[targetID]
	if !ok {
		return fmt.Errorf("modelmgr: %s: %w", targetID, ErrTargetVersionNotFound)
	}

	// Retire the current active.
	if currentEntry, ok := r.entries[currentActiveID]; ok {
		currentEntry.State = StateRetired
		if err := r.persistEntry(currentEntry); err != nil {
			return fmt.Errorf("modelmgr: retire current active: %w", err)
		}
	}

	// Activate the target.
	target.State = StateActive
	r.active[name] = target.ID
	return r.persistEntry(target)
}

// ============================================================================
// Delete
// ============================================================================

// Delete removes a model from the registry and its files from disk.
// Active models cannot be deleted (returns ErrActiveModelCannotDelete).
// Only retired, uploaded, or validated models can be deleted.
func (r *ModelRegistry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("modelmgr: %s: %w", id, ErrModelNotFound)
	}
	if entry.State == StateActive {
		return fmt.Errorf("modelmgr: %s: %w", id, ErrActiveModelCannotDelete)
	}

	// Remove files from disk.
	if err := os.RemoveAll(entry.dirPath); err != nil {
		return fmt.Errorf("modelmgr: remove model files: %w", err)
	}

	// Clean up empty parent directory.
	parentDir := filepath.Dir(entry.dirPath)
	_ = removeDirIfEmpty(parentDir)

	delete(r.entries, id)
	if r.active[entry.Name] == id {
		delete(r.active, entry.Name)
	}
	return nil
}

// ============================================================================
// Query operations
// ============================================================================

// Get returns the model entry for the given ID, or ErrModelNotFound.
func (r *ModelRegistry) Get(id string) (*modelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[id]
	if !ok {
		return nil, fmt.Errorf("modelmgr: %s: %w", id, ErrModelNotFound)
	}
	return entry, nil
}

// GetActive returns the currently active model entry for the given name,
// or ErrModelNotFound if no active model exists.
func (r *ModelRegistry) GetActive(name string) (*modelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	activeID, ok := r.active[name]
	if !ok {
		return nil, fmt.Errorf("modelmgr: active model for %q: %w", name, ErrModelNotFound)
	}
	entry, ok := r.entries[activeID]
	if !ok {
		return nil, fmt.Errorf("modelmgr: active entry %q: %w", activeID, ErrModelNotFound)
	}
	return entry, nil
}

// List returns all model entries for the given name, sorted by upload order
// (as returned by the filesystem scan). Returns an empty slice if no models
// exist for the name.
func (r *ModelRegistry) List(name string) ([]*modelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*modelEntry
	for _, entry := range r.entries {
		if entry.Name == name {
			result = append(result, entry)
		}
	}
	return result, nil
}

// ListAll returns all model entries in the registry.
func (r *ModelRegistry) ListAll() []*modelEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*modelEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		result = append(result, entry)
	}
	return result
}

// Close is a no-op for the filesystem-backed registry. It exists to satisfy
// io.Closer in test helpers that need to defer Close().
func (r *ModelRegistry) Close() error {
	return nil
}

// ============================================================================
// Private: filesystem persistence
// ============================================================================

// persistEntry writes the entry's persistedEntry JSON to entry.json on disk.
// Caller must hold r.mu (write lock).
func (r *ModelRegistry) persistEntry(entry *modelEntry) error {
	persisted := entry.toPersisted()
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("modelmgr: marshal entry: %w", err)
	}

	entryPath := filepath.Join(entry.dirPath, "entry.json")
	if err := os.WriteFile(entryPath, data, 0600); err != nil {
		return fmt.Errorf("modelmgr: write entry.json: %w", err)
	}
	return nil
}

// scanFilesystem walks the registry root to rebuild the in-memory index
// from persisted entry.json files.
func (r *ModelRegistry) scanFilesystem() error {
	return filepath.WalkDir(r.cfg.RegistryRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %q: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "entry.json" {
			return nil
		}

		//nolint:gosec // path originates from filepath.WalkDir within registry root
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %q: %w", path, readErr)
		}

		var persisted persistedEntry
		if err := json.Unmarshal(data, &persisted); err != nil {
			return fmt.Errorf("unmarshal %q: %w", path, err)
		}

		dirPath := filepath.Dir(path)
		entry := &modelEntry{
			ID:        persisted.ID,
			Name:      persisted.Name,
			ModelType: persisted.ModelType,
			Version:   persisted.Version,
			State:     persisted.State,
			Metadata:  persisted.Metadata,
			dirPath:   dirPath,
		}
		r.entries[entry.ID] = entry
		if entry.State == StateActive {
			r.active[entry.Name] = entry.ID
		}
		return nil
	})
}

// ============================================================================
// Private: lifecycle helpers
// ============================================================================

// retireActiveForName sets the current active model for name to StateRetired
// and removes it from the active map. If no active model exists, this is a
// no-op. Caller must hold r.mu (write lock).
func (r *ModelRegistry) retireActiveForName(name string) {
	activeID, ok := r.active[name]
	if !ok {
		return
	}
	if entry, ok := r.entries[activeID]; ok {
		entry.State = StateRetired
		_ = r.persistEntry(entry) // best-effort; already locked
	}
	delete(r.active, name)
}

// ============================================================================
// Private: path safety
// ============================================================================

// validateName checks that the model name is a safe filesystem identifier.
// Allowed characters: lowercase alphanumeric, hyphens, underscores.
// Length: 1-64 characters.
func validateName(name string) error {
	if len(name) < 1 || len(name) > 64 {
		return fmt.Errorf("modelmgr: name %q length must be 1-64: %w", name, ErrInvalidName)
	}
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return fmt.Errorf("modelmgr: name %q contains invalid character %q: %w", name, string(c), ErrInvalidName)
	}
	return nil
}

// removeDirIfEmpty removes a directory if it contains no entries.
// Errors are silently ignored (best-effort cleanup).
func removeDirIfEmpty(dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(dirPath)
	}
	return nil
}

// ============================================================================
// Private: scope check
// ============================================================================

// checkScope verifies that the entry's declared model_type, inputs, and
// outputs match the expected ValidationScope.
func checkScope(entry *modelEntry, scope ValidationScope) error {
	if entry.ModelType != scope.ModelType {
		return fmt.Errorf("%w: expected model_type %q, got %q",
			ErrScopeMismatch, scope.ModelType, entry.ModelType)
	}
	if !stringSlicesEqual(entry.Metadata.Inputs, scope.Inputs) {
		return fmt.Errorf("%w: expected inputs %v, got %v",
			ErrScopeMismatch, scope.Inputs, entry.Metadata.Inputs)
	}
	if !stringSlicesEqual(entry.Metadata.Outputs, scope.Outputs) {
		return fmt.Errorf("%w: expected outputs %v, got %v",
			ErrScopeMismatch, scope.Outputs, entry.Metadata.Outputs)
	}
	return nil
}

// stringSlicesEqual compares two string slices element-by-element
// (order-sensitive). Both nil and both empty are considered equal.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

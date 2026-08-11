// Package modelmgr implements cloud AI model management with filesystem-backed
// versioning and lifecycle control. It provides a generic ModelRegistry that
// supports upload, validate, activate, retire, rollback, and delete operations
// with a 3-gate activation policy and strict delete semantics.
//
// The registry is model-type-agnostic: it accepts arbitrary ModelType strings
// and stores opaque model binaries on the filesystem. It does not parse or
// couple to specific model formats (ONNX, RKNN, etc.).
//
// Storage layout:
//
//	<registryRoot>/
//	  <model_name>/
//	    <version>/
//	      entry.json    — persisted registry entry (id, name, type, version, state, metadata)
//	      model.bin     — opaque model bytes
//
// Concurrency: sync.RWMutex protects the in-memory index; filesystem is the
// durable store. On NewRegistry, the filesystem is scanned to rebuild the index.
package modelmgr

// ============================================================================
// ModelType
// ============================================================================

// ModelType identifies the category of a model. The registry is generic and
// accepts arbitrary strings, but well-known types are defined as constants
// for documentation and configuration convenience.
type ModelType string

// Well-known model types used across the y-ai-pond platform.
const (
	ModelTypeForecast ModelType = "forecast"
	ModelTypeGrowth   ModelType = "growth"
	ModelTypeRL       ModelType = "rl"
	ModelTypeDT       ModelType = "dt"
)

// ============================================================================
// ModelState
// ============================================================================

// ModelState tracks the lifecycle phase of a registered model.
type ModelState string

const (
	// StateUploaded is the initial state after Upload. The model files
	// are on disk but not yet validated or activated.
	StateUploaded ModelState = "uploaded"

	// StateValidated is set after a successful Validate call. The model
	// has passed the build scope check (type, inputs, outputs match).
	StateValidated ModelState = "validated"

	// StateActive is set after a successful Activate call. The model is
	// the current production model for its name. Only one version per
	// name can be active.
	StateActive ModelState = "active"

	// StateRetired is set after Retire (or after being replaced by a
	// newer version via Activate or Rollback). Retired models are
	// deletable; active models are not.
	StateRetired ModelState = "retired"
)

// ============================================================================
// ModelMetadata
// ============================================================================

// ModelMetadata carries descriptive and technical metadata about a model.
// It is serialized to JSON for filesystem persistence and API responses.
type ModelMetadata struct {
	// Src is the model source identifier (e.g. training run ID, dataset
	// reference, or URL from which the model originated).
	Src string `json:"src"`

	// Name is the human-readable model name (e.g. "do-forecast-v2").
	Name string `json:"name"`

	// ModelType is the category of the model (forecast, growth, rl, dt, etc.).
	ModelType ModelType `json:"model_type"`

	// Version is the semantic version string (e.g. "1.2.0").
	Version string `json:"version"`

	// TrainingDate is an ISO 8601 date string indicating when the model
	// was trained (e.g. "2026-08-11").
	TrainingDate string `json:"training_date"`

	// EvalMetrics holds evaluation metrics as key-value pairs
	// (e.g. {"rmse": 0.12, "r_squared": 0.94, "mape": 5.2}).
	// These are compared against configured thresholds during activation.
	EvalMetrics map[string]float64 `json:"eval_metrics"`

	// RuntimeRequirements declares the runtime environment requirements
	// as key-value pairs (e.g. {"framework": "onnxer", "safety_gate": "true"}).
	// The "safety_gate" key is checked by the activation policy when
	// RequireSafetyGate is true.
	RuntimeRequirements map[string]string `json:"runtime_requirements"`

	// Inputs lists the expected input feature names or tensor names.
	// Used by Validate to verify the model matches the build scope.
	Inputs []string `json:"inputs"`

	// Outputs lists the expected output feature names or tensor names.
	// Used by Validate to verify the model matches the build scope.
	Outputs []string `json:"outputs"`

	// SHA256 is the hex-encoded SHA-256 hash of the model binary.
	// It is computed automatically on Upload if not provided.
	SHA256 string `json:"sha256"`
}

// ============================================================================
// ValidationScope
// ============================================================================

// ValidationScope defines the expected model characteristics for a build
// scope check. During Validate, the model's declared type, inputs, and
// outputs are compared against these expectations.
type ValidationScope struct {
	// ModelType is the expected model category.
	ModelType ModelType `json:"model_type"`

	// Inputs is the expected list of input identifiers. Order-insensitive
	// comparison unless the scope is configured otherwise.
	Inputs []string `json:"inputs"`

	// Outputs is the expected list of output identifiers. Order-insensitive
	// comparison.
	Outputs []string `json:"outputs"`
}

// ============================================================================
// persistedEntry
// ============================================================================

// persistedEntry is the full serializable representation of a registry entry
// written to entry.json on disk. It includes runtime state and metadata.
type persistedEntry struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	ModelType ModelType     `json:"model_type"`
	Version   string        `json:"version"`
	State     ModelState    `json:"state"`
	Metadata  ModelMetadata `json:"metadata"`
}

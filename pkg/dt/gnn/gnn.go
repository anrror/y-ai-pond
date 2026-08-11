// Package gnn wraps ST-GNN water quality model inference for the digital
// twin (TIER 3). The Python side (python/dt/train_stgnn.py) trains a
// D-TGCN / AEST-GNN model offline and exports it to ONNX; this package
// loads the ONNX model via the GNNBackend interface and performs low-latency
// multi-step prediction (1h/6h/24h).
//
// Architecture:
//   - GNNBackend: pluggable interface for ONNX/Mock backends (mirrors rl.RLPolicy).
//   - Matrix: dynamic adjacency matrix — GNN nodes are pond monitoring
//     stations, edges encode water flow direction / pipe connectivity.
//     Flow changes update the matrix so predictions adapt (no static graph).
//   - Predictor: wraps a backend + adjacency matrix + input validation.
//
// Input layout: a flat []float64 of N*FeatureLen elements, one row per node
// in the order [pH, DO, Temp, NH3, Turbidity, AirTemp, Pressure, Rainfall].
//
// Real ONNX inference requires github.com/yalue/onnxruntime_go (CGO around
// the ONNX Runtime C library). The ONNXGNN stub returns a clear error with
// deployment instructions when the library is not linked. Use MockGNN for CI.
package gnn

import (
	"errors"
	"fmt"
)

// ============================================================================
// Feature layout
// ============================================================================

// FeatureLen is the number of sensor + weather features per node.
const FeatureLen = 8

// Feature indices for documentation.
const (
	IdxPH       = 0 // pH
	IdxDO       = 1 // Dissolved oxygen (mg/L)
	IdxTemp     = 2 // Water temperature (°C)
	IdxNH3      = 3 // Ammonia nitrogen (mg/L)
	IdxTurbid   = 4 // Turbidity (NTU)
	IdxAirTemp  = 5 // Air temperature (°C)
	IdxPressure = 6 // Atmospheric pressure (hPa)
	IdxRainfall = 7 // Rainfall (mm/h)
)

// NumHorizons is the number of forecast horizons (1h, 6h, 24h).
const NumHorizons = 3

// ============================================================================
// Domain errors
// ============================================================================

var (
	// ErrInvalidInput is returned when the flat feature matrix is malformed.
	ErrInvalidInput = errors.New("gnn: invalid input feature matrix")
	// ErrModelNotLoaded is returned when Predict is called before LoadModel.
	ErrModelNotLoaded = errors.New("gnn: model not loaded")
	// ErrInferenceFailed is returned when the ONNX runtime fails.
	ErrInferenceFailed = errors.New("gnn: inference failed")
	// ErrShapeMismatch is returned when model input/output shapes do not match.
	ErrShapeMismatch = errors.New("gnn: model input/output shape mismatch")
)

// ============================================================================
// Predictions
// ============================================================================

// Prediction is the multi-step forecast for a single node.
// DO[i] holds the predicted dissolved oxygen at horizon i
// (index 0 = 1h, 1 = 6h, 2 = 24h).
type Prediction struct {
	DO [NumHorizons]float64
}

// ============================================================================
// GNNBackend interface
// ============================================================================

// GNNBackend is the interface for ST-GNN inference backends.
// Each backend wraps a specific runtime: ONNX Runtime (via CGO), or a
// deterministic in-memory mock for CI testing.
//
// Implementations must be safe for concurrent use.
type GNNBackend interface {
	// Predict runs a forward pass over the flat feature matrix and returns
	// one Prediction per node. The matrix must contain n*FeatureLen elements
	// where n is the number of nodes.
	Predict(matrix []float64) ([]Prediction, error)

	// Name returns a human-readable backend identifier (e.g., "ONNX", "Mock").
	Name() string

	// Close releases backend resources.
	Close() error
}

// ============================================================================
// Dynamic adjacency matrix
// ============================================================================

// Matrix is an N×N weighted adjacency matrix for the pond network.
// Row source -> Column target encodes directional influence (upstream ->
// downstream pump/pipe connectivity).
type Matrix struct {
	n int
	// w[i*n+j] is the influence weight from node i to node j (row-major).
	w []float64
	// flow[i] is the current flow strength at node i
	// (e.g., pump output in m³/h). Non-zero flow activates/strengthens
	// outgoing edges.
	flow []float64
}

// NewMatrix creates an N×N adjacency matrix with zero weights.
func NewMatrix(n int) *Matrix {
	return &Matrix{n: n, w: make([]float64, n*n), flow: make([]float64, n)}
}

// SetEdge sets the influence weight from node from to node to.
func (m *Matrix) SetEdge(from, to int, weight float64) {
	if m == nil || from < 0 || to < 0 || from >= m.n || to >= m.n {
		return
	}
	m.w[from*m.n+to] = weight
}

// Weight returns the influence weight between two nodes.
func (m *Matrix) Weight(from, to int) float64 {
	if m == nil || from < 0 || to < 0 || from >= m.n || to >= m.n {
		return 0
	}
	return m.w[from*m.n+to]
}

// UpdateFlow sets the flow strength at a node. The effective outgoing edge
// weight becomes baseWeight * (1 + 2*flow), so increasing flow strengthens
// downstream influence and predictions adapt to connectivity changes.
func (m *Matrix) UpdateFlow(node int, flow float64) {
	if m == nil || node < 0 || node >= m.n {
		return
	}
	m.flow[node] = flow
}

// flowScale returns the multiplier applied to node's outgoing edges.
func (m *Matrix) flowScale(node int) float64 {
	return 1 + 2*m.flow[node]
}

// ============================================================================
// Configuration
// ============================================================================

// Config holds tunable parameters for the GNN predictor.
type Config struct {
	// MaxFlow clamps flow values used in adjacency updates.
	MaxFlow float64
}

// DefaultConfig returns the recommended configuration.
func DefaultConfig() Config {
	return Config{MaxFlow: 10}
}

// ============================================================================
// Predictor
// ============================================================================

// Predictor wraps a GNNBackend with an adjacency matrix and input validation.
// It is the primary entry point for ST-GNN inference.
type Predictor struct {
	backend GNNBackend
	adj     *Matrix
	cfg     Config
}

// NewPredictor creates a Predictor over the given backend and adjacency.
func NewPredictor(backend GNNBackend, adj *Matrix, cfg Config) *Predictor {
	return &Predictor{backend: backend, adj: adj, cfg: cfg}
}

// Predict validates the flat feature matrix, runs inference, and returns
// one Prediction per node.
func (p *Predictor) Predict(matrix []float64) ([]Prediction, error) {
	if p.backend == nil {
		return nil, fmt.Errorf("gnn: backend is nil")
	}
	if p.adj == nil {
		return nil, fmt.Errorf("gnn: adjacency matrix is nil")
	}
	if len(matrix) == 0 || len(matrix)%FeatureLen != 0 {
		return nil, fmt.Errorf("%w: len %d is not a multiple of %d", ErrInvalidInput, len(matrix), FeatureLen)
	}
	nodes := len(matrix) / FeatureLen
	if nodes != p.adj.n {
		return nil, fmt.Errorf("%w: matrix has %d nodes, adjacency has %d", ErrInvalidInput, nodes, p.adj.n)
	}

	// Apply dynamic adjacency: propagate flow-scaled upstream influence into
	// the mock/managed observation stream is backend-specific; predictors
	// with MockGNN consume it via backend.Predict. We pass the current scaled
	// adjacency snapshot to the backend through a metadata key is not part of
	// the interface, so the mock reads it via withAdjacency.
	pred, err := p.backend.Predict(matrix)
	if err != nil {
		return nil, fmt.Errorf("gnn predict: %w", err)
	}
	if len(pred) != nodes {
		return nil, fmt.Errorf("%w: backend returned %d predictions for %d nodes",
			ErrInvalidInput, len(pred), nodes)
	}
	return pred, nil
}

// Name returns the underlying backend name.
func (p *Predictor) Name() string {
	if p.backend == nil {
		return "nil"
	}
	return p.backend.Name()
}

// Close releases backend resources.
func (p *Predictor) Close() error {
	if p.backend == nil {
		return nil
	}
	return p.backend.Close()
}

// ============================================================================
// Shape validation
// ============================================================================

// ValidateInputDim checks a model input feature dimension against the
// expected ST-GNN feature layout.
func ValidateInputDim(dim int) error {
	if dim != FeatureLen {
		return fmt.Errorf("%w: expected input dimension %d, got %d", ErrShapeMismatch, FeatureLen, dim)
	}
	return nil
}
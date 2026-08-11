package gnn

// MockGNN is a deterministic in-memory ST-GNN backend for CI testing.
// It simulates water-quality propagation along the dynamic adjacency matrix:
// each node's DO forecast is its measured DO, augmented by flow-scaled
// upstream influence. A forecast converges smoothly toward downstream nodes.
type MockGNN struct {
	adj *Matrix
}

// NewMockGNN creates a MockGNN that reads connectivity from adj.
func NewMockGNN(adj *Matrix) *MockGNN {
	return &MockGNN{adj: adj}
}

// Predict computes per-node DO forecasts for the 1h/6h/24h horizons.
//
//	DO_h(node) = do(node) + Σ_upstream influenceUpstream * decay(h)
//
// influence from upstream u onto downstream v equals
// adj.Weight(u,v) * adj.flowScale(u) * do(u); decay shrinks with horizon so
// farther-horizon forecasts relax toward the local baseline.
func (m *MockGNN) Predict(matrix []float64) ([]Prediction, error) {
	n := len(matrix) / FeatureLen
	out := make([]Prediction, n)
	for v := 0; v < n; v++ {
		local := matrix[v*FeatureLen+IdxDO]
		var up float64
		for u := 0; u < n; u++ {
			w := m.adj.Weight(u, v)
			if w == 0 {
				continue
			}
			up += w * m.adj.flowScale(u) * matrix[u*FeatureLen+IdxDO]
		}
		for h := 0; h < NumHorizons; h++ {
			decay := 1.0 / float64(h+1)
			out[v].DO[h] = local + up*decay
		}
	}
	return out, nil
}

// Name returns the backend identifier.
func (m *MockGNN) Name() string { return "Mock" }

// Close is a no-op for the in-memory mock.
func (m *MockGNN) Close() error { return nil }
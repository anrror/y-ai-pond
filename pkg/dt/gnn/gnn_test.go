package gnn

import (
	"errors"
	"testing"
)

// buildNodes builds a feature matrix for n nodes with distinct DO values.
func buildNodes(n int) []float64 {
	out := make([]float64, 0, n*FeatureLen)
	for i := 0; i < n; i++ {
		row := []float64{7.0 + float64(i)*0.1, 25.0, 0.1, 5.0 / (1 + float64(i)), 28.0, 30.0, 1013.0, 0.0}
		out = append(out, row...)
	}
	return out
}

func TestGNNInference(t *testing.T) {
	adj := NewMatrix(3)
	adj.SetEdge(0, 1, 0.5)
	adj.SetEdge(1, 2, 0.5)
	p := NewPredictor(NewMockGNN(adj), adj, DefaultConfig())

	out, err := p.Predict(buildNodes(3))
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 node predictions, got %d", len(out))
	}
	for i, pred := range out {
		if got := len(pred.DO); got != NumHorizons {
			t.Fatalf("node %d: expected %d horizons, got %d", i, got, len(pred.DO))
		}
		for _, v := range pred.DO {
			if v <= 0 {
				t.Fatalf("node %d: expected positive DO prediction, got %v", i, pred.DO)
			}
		}
	}
}

func TestDynamicAdjacency(t *testing.T) {
	adj := NewMatrix(2)
	adj.SetEdge(0, 1, 0.5) // base upstream->downstream connectivity
	p := NewPredictor(NewMockGNN(adj), adj, DefaultConfig())

	base, err := p.Predict(buildNodes(2))
	if err != nil {
		t.Fatalf("base predict: %v", err)
	}

	// Flow increase (pump on) strengthens upstream->downstream influence.
	adj.UpdateFlow(0, 2.0)

	after, err := p.Predict(buildNodes(2))
	if err != nil {
		t.Fatalf("after predict: %v", err)
	}

	d0 := after[1].DO[0] - base[1].DO[0]
	if d0 <= 0 {
		t.Fatalf("expected downstream prediction to adjust upward after flow change, delta=%v", d0)
	}

	// Flow stop returns to baseline.
	adj.UpdateFlow(0, 0.0)
	back, err := p.Predict(buildNodes(2))
	if err != nil {
		t.Fatalf("back predict: %v", err)
	}
	if back[1].DO[0] != base[1].DO[0] {
		t.Fatalf("expected prediction to revert after flow stop, got %v want %v", back[1].DO[0], base[1].DO[0])
	}
}

func TestONNXLoadModel(t *testing.T) {
	b := NewONNXGNN()
	defer func() { _ = b.Close() }()

	if err := b.LoadModel(""); err == nil {
		t.Fatal("expected error for empty model path")
	}

	p, err := b.Predict(make([]float64, FeatureLen))
	if err == nil {
		t.Fatal("expected error from ONNX stub without model")
	}
	_ = p
}

func TestONNXShapeValidation(t *testing.T) {
	// Valid input dimension passes.
	if err := ValidateInputDim(FeatureLen); err != nil {
		t.Fatalf("expected valid input dim, got %v", err)
	}
	// Invalid input dimension fails with ErrShapeMismatch.
	if err := ValidateInputDim(FeatureLen - 1); !errors.Is(err, ErrShapeMismatch) {
		t.Fatalf("expected ErrShapeMismatch, got %v", err)
	}
}

func TestPredictInputTooShort(t *testing.T) {
	adj := NewMatrix(2)
	p := NewPredictor(NewMockGNN(adj), adj, DefaultConfig())
	// 2 nodes need 16 features; provide 8.
	_, err := p.Predict(make([]float64, FeatureLen))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestPredictNilBackend(t *testing.T) {
	adj := NewMatrix(2)
	p := NewPredictor(nil, adj, DefaultConfig())
	_, err := p.Predict(buildNodes(2))
	if err == nil {
		t.Fatal("expected error for nil backend")
	}
}

func BenchmarkGNNInference50Nodes(b *testing.B) {
	adj := NewMatrix(50)
	for i := 0; i < 49; i++ {
		adj.SetEdge(i, i+1, 0.5)
	}
	p := NewPredictor(NewMockGNN(adj), adj, DefaultConfig())
	input := buildNodes(50)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Predict(input); err != nil {
			b.Fatalf("predict: %v", err)
		}
	}
}


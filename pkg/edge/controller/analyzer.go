package controller

import (
	"github.com/anrror/y-ai-pond/pkg/edge/texture"
)

// DefaultTextureAnalyzer fuses texture energy and active pixel ratio into
// a single feeding intensity score in [0, 1].
type DefaultTextureAnalyzer struct{}

// Intensity computes feeding intensity as a weighted combination of texture
// energy and active pixel ratio. Mismatched or empty frames return 0.
func (DefaultTextureAnalyzer) Intensity(prev, curr texture.Frame) float64 {
	e := texture.TextureEnergy(prev, curr)
	a := texture.ActiveRatio(prev, curr, 8)
	return clamp01(0.6*e + 0.4*a)
}

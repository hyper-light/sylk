package forest

import (
	"time"

	"github.com/viterin/vek/vek32"
)

type scoreInput struct {
	QueryMatch       float64
	Evidence         float64
	Canopy           float64
	Substrate        float64
	Frontier         float64
	Confidence       float64
	Recency          float64
	Warmth           float64
	Utility          float64
	Salience         float64
	ConflictSafety   float64
	ScopeSafety      float64
	InhibitionSafety float64
}

var defaultScoreWeights = []float32{
	0.16, // query match
	0.11, // evidence support
	0.07, // canopy
	0.08, // substrate
	0.05, // frontier
	0.08, // confidence
	0.07, // recency
	0.10, // warmth
	0.10, // utility
	0.07, // salience
	0.05, // conflict safety
	0.03, // scope safety
	0.03, // inhibition safety
}

func scoreBatch(inputs []scoreInput, now time.Time, branches []*Branch) []PacketScore {
	if len(inputs) == 0 {
		return nil
	}

	matrix := make([]float32, 0, len(inputs)*len(defaultScoreWeights))
	for _, input := range inputs {
		matrix = append(matrix,
			float32(clamp01(input.QueryMatch)),
			float32(clamp01(input.Evidence)),
			float32(clamp01(input.Canopy)),
			float32(clamp01(input.Substrate)),
			float32(clamp01(input.Frontier)),
			float32(clamp01(input.Confidence)),
			float32(clamp01(input.Recency)),
			float32(clamp01(input.Warmth)),
			float32(clamp01(input.Utility)),
			float32(clamp01(input.Salience)),
			float32(clamp01(input.ConflictSafety)),
			float32(clamp01(input.ScopeSafety)),
			float32(clamp01(input.InhibitionSafety)),
		)
	}

	scores := make([]PacketScore, len(inputs))
	width := len(defaultScoreWeights)
	for i := range inputs {
		row := matrix[i*width : (i+1)*width]
		base := float64(vek32.Dot(row, defaultScoreWeights))
		scores[i] = PacketScore{
			Total:            base,
			Base:             base,
			Learned:          base,
			QueryMatch:       inputs[i].QueryMatch,
			Evidence:         inputs[i].Evidence,
			Canopy:           inputs[i].Canopy,
			Substrate:        inputs[i].Substrate,
			Frontier:         inputs[i].Frontier,
			Confidence:       inputs[i].Confidence,
			Recency:          inputs[i].Recency,
			Warmth:           inputs[i].Warmth,
			Utility:          inputs[i].Utility,
			Salience:         inputs[i].Salience,
			Conflict:         inputs[i].ConflictSafety,
			ScopeSafety:      inputs[i].ScopeSafety,
			InhibitionSafety: inputs[i].InhibitionSafety,
		}
	}
	return scores
}

func branchRecency(branch *Branch, now time.Time) float64 {
	return recencyScore(branch.UpdatedAt, now, 72*time.Hour)
}

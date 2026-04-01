package forest

import (
	"context"
	"errors"
	"testing"
)

func TestTrainBoosterModel_RespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	examples := make([]labeledExample, 0, 16)
	for i := 0; i < 16; i++ {
		features := make([]float32, featureCount)
		features[featureBaseScore] = float32(i) / 16
		examples = append(examples, labeledExample{
			Features: features,
			Label:    float64(i % 2),
		})
	}

	_, err := trainBoosterModel(ctx, ObjectiveUtility, "engineer", examples, defaultBoosterConfig())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("trainBoosterModel() error = %v, want %v", err, context.Canceled)
	}
}

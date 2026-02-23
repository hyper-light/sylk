package activation

import (
	"testing"
	"time"
)

// testPredictorConfig returns the default config for test assertions.
func testPredictorConfig() PredictorConfig {
	cfg := PredictorConfig{}
	applyPredictorDefaults(&cfg)
	return cfg
}

func TestPredictor_EmptyHistory(t *testing.T) {
	p := NewActivationPredictor(PredictorConfig{})
	predictions := p.Predict("engineer")
	if len(predictions) != 0 {
		t.Fatalf("expected no predictions, got %v", predictions)
	}
}

func TestPredictor_CoActivationLearning(t *testing.T) {
	cfg := testPredictorConfig()
	p := NewActivationPredictor(cfg)

	// Simulate: architect always activates shortly after engineer.
	for range cfg.AffinityThreshold + 1 {
		p.Record("engineer")
		time.Sleep(time.Millisecond)
		p.Record("architect")
		time.Sleep(time.Millisecond)
	}

	predictions := p.Predict("engineer")
	if len(predictions) == 0 {
		t.Fatal("expected at least one prediction")
	}
	if predictions[0] != "architect" {
		t.Fatalf("expected architect, got %s", predictions[0])
	}
}

func TestPredictor_BelowThreshold(t *testing.T) {
	p := NewActivationPredictor(PredictorConfig{})

	// Single co-activation pair — well below threshold of 3.
	p.Record("engineer")
	time.Sleep(time.Millisecond)
	p.Record("architect")

	predictions := p.Predict("engineer")
	if len(predictions) != 0 {
		t.Fatalf("expected no predictions below threshold, got %v", predictions)
	}
}

func TestPredictor_BoundedToMaxPredictions(t *testing.T) {
	cfg := testPredictorConfig()
	p := NewActivationPredictor(cfg)

	// Create strong affinity with 5 different types.
	types := []string{"a", "b", "c", "d", "e"}
	for range cfg.AffinityThreshold + 1 {
		p.Record("source")
		time.Sleep(time.Millisecond)
		for _, tp := range types {
			p.Record(tp)
			time.Sleep(time.Millisecond)
		}
	}

	predictions := p.Predict("source")
	if len(predictions) > cfg.MaxPredictions {
		t.Fatalf("expected at most %d predictions, got %d", cfg.MaxPredictions, len(predictions))
	}
}

func TestPredictor_Decay(t *testing.T) {
	cfg := testPredictorConfig()
	p := NewActivationPredictor(cfg)

	// Build strong affinity.
	for range cfg.AffinityThreshold + 1 {
		p.Record("engineer")
		time.Sleep(time.Millisecond)
		p.Record("architect")
	}

	if len(p.Predict("engineer")) == 0 {
		t.Fatal("expected predictions before decay")
	}

	// Decay until count drops below threshold.
	for range 10 {
		p.Decay("engineer")
	}

	predictions := p.Predict("engineer")
	if len(predictions) != 0 {
		t.Fatalf("expected no predictions after decay, got %v", predictions)
	}
}

func TestPredictor_SelfActivationIgnored(t *testing.T) {
	p := NewActivationPredictor(PredictorConfig{})

	// Same type activated repeatedly should not predict itself.
	for range 20 {
		p.Record("engineer")
		time.Sleep(time.Millisecond)
	}

	predictions := p.Predict("engineer")
	for _, pred := range predictions {
		if pred == "engineer" {
			t.Fatal("predictor should not predict self-activation")
		}
	}
}

func TestCircularBuffer_PushAndForEachReverse(t *testing.T) {
	cb := newCircularBuffer[int](4)

	for i := range 6 {
		cb.Push(i) // 0,1,2,3,4,5 → buffer has [4,5,2,3] with head=2
	}

	if cb.Len() != 4 {
		t.Fatalf("expected len 4, got %d", cb.Len())
	}

	var result []int
	cb.ForEachReverse(func(v int) bool {
		result = append(result, v)
		return true
	})

	// Should iterate: 5, 4, 3, 2 (newest to oldest)
	expected := []int{5, 4, 3, 2}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
	for i, v := range result {
		if v != expected[i] {
			t.Fatalf("position %d: expected %d, got %d", i, expected[i], v)
		}
	}
}

func TestCircularBuffer_EarlyStop(t *testing.T) {
	cb := newCircularBuffer[int](8)
	for i := range 8 {
		cb.Push(i)
	}

	var count int
	cb.ForEachReverse(func(_ int) bool {
		count++
		return count < 3
	})

	if count != 3 {
		t.Fatalf("expected 3 iterations, got %d", count)
	}
}

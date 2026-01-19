package classifier

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

// MockEmbedder implements EmbeddingProvider for testing.
type MockEmbedder struct {
	embeddings map[string][]float32
	err        error
}

func (m *MockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	if vec, ok := m.embeddings[text]; ok {
		return vec, nil
	}
	// Return a default vector
	return []float32{0.5, 0.5, 0.5, 0.5}, nil
}

func TestNewEmbeddingClassifier(t *testing.T) {
	embedder := &MockEmbedder{}
	ec := NewEmbeddingClassifier(embedder, 0.70)

	if ec == nil {
		t.Fatal("NewEmbeddingClassifier returned nil")
	}
	if ec.threshold != 0.70 {
		t.Errorf("threshold = %f, want 0.70", ec.threshold)
	}
}

func TestNewEmbeddingClassifier_DefaultThreshold(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0)

	if ec.threshold != 0.70 {
		t.Errorf("threshold = %f, want default 0.70", ec.threshold)
	}
}

func TestEmbeddingClassifier_Name(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	if ec.Name() != "embedding" {
		t.Errorf("Name() = %s, want embedding", ec.Name())
	}
}

func TestEmbeddingClassifier_Priority(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	if ec.Priority() != 20 {
		t.Errorf("Priority() = %d, want 20", ec.Priority())
	}
}

func TestEmbeddingClassifier_SetCentroid(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	vec := []float32{1.0, 0.0, 0.0, 0.0}
	ec.SetCentroid(domain.DomainLibrarian, vec, 0.75)

	centroid, ok := ec.GetCentroid(domain.DomainLibrarian)
	if !ok {
		t.Fatal("Centroid should exist")
	}
	if centroid.Threshold != 0.75 {
		t.Errorf("Threshold = %f, want 0.75", centroid.Threshold)
	}
}

func TestEmbeddingClassifier_SetCentroid_DefaultThreshold(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	vec := []float32{1.0, 0.0, 0.0, 0.0}
	ec.SetCentroid(domain.DomainLibrarian, vec, 0)

	centroid, _ := ec.GetCentroid(domain.DomainLibrarian)
	if centroid.Threshold != 0.70 {
		t.Errorf("Threshold = %f, want default 0.70", centroid.Threshold)
	}
}

func TestEmbeddingClassifier_SetCentroids(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	centroids := map[domain.Domain][]float32{
		domain.DomainLibrarian: {1.0, 0.0, 0.0, 0.0},
		domain.DomainAcademic:  {0.0, 1.0, 0.0, 0.0},
	}
	ec.SetCentroids(centroids)

	if ec.CentroidCount() != 2 {
		t.Errorf("CentroidCount() = %d, want 2", ec.CentroidCount())
	}
}

func TestEmbeddingClassifier_Classify_EmptyQuery(t *testing.T) {
	embedder := &MockEmbedder{}
	ec := NewEmbeddingClassifier(embedder, 0.70)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Empty query should produce empty result")
	}
}

func TestEmbeddingClassifier_Classify_NilEmbedder(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Nil embedder should produce empty result")
	}
}

func TestEmbeddingClassifier_Classify_NoCentroids(t *testing.T) {
	embedder := &MockEmbedder{}
	ec := NewEmbeddingClassifier(embedder, 0.70)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("No centroids should produce empty result")
	}
}

func TestEmbeddingClassifier_Classify_SingleMatch(t *testing.T) {
	embedder := &MockEmbedder{
		embeddings: map[string][]float32{
			"code query": {1.0, 0.0, 0.0, 0.0},
		},
	}
	ec := NewEmbeddingClassifier(embedder, 0.70)
	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0, 0.0, 0.0, 0.0}, 0)
	ec.SetCentroid(domain.DomainAcademic, []float32{0.0, 1.0, 0.0, 0.0}, 0)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "code query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	libConf := result.GetConfidence(domain.DomainLibrarian)
	if libConf < 0.99 {
		t.Errorf("Librarian confidence = %f, want ~1.0", libConf)
	}

	acadConf := result.GetConfidence(domain.DomainAcademic)
	if acadConf > 0.1 {
		t.Errorf("Academic confidence = %f, want ~0", acadConf)
	}
}

func TestEmbeddingClassifier_Classify_MultipleMatches(t *testing.T) {
	// Vector between Librarian and Academic
	embedder := &MockEmbedder{
		embeddings: map[string][]float32{
			"mixed query": {0.707, 0.707, 0.0, 0.0},
		},
	}
	ec := NewEmbeddingClassifier(embedder, 0.80)
	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0, 0.0, 0.0, 0.0}, 0)
	ec.SetCentroid(domain.DomainAcademic, []float32{0.0, 1.0, 0.0, 0.0}, 0)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "mixed query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.DomainCount() < 2 {
		t.Error("Should match multiple domains")
	}

	if !result.ShouldContinue {
		t.Error("Multiple domains with similar confidence should continue")
	}
}

func TestEmbeddingClassifier_Classify_HighConfidenceTerminates(t *testing.T) {
	embedder := &MockEmbedder{
		embeddings: map[string][]float32{
			"code query": {1.0, 0.0, 0.0, 0.0},
		},
	}
	ec := NewEmbeddingClassifier(embedder, 0.70)
	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0, 0.0, 0.0, 0.0}, 0)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "code query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.ShouldContinue {
		t.Error("High confidence single domain should terminate")
	}
}

func TestEmbeddingClassifier_Classify_EmbedderError(t *testing.T) {
	embedder := &MockEmbedder{
		err: errors.New("embedding failed"),
	}
	ec := NewEmbeddingClassifier(embedder, 0.70)
	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0, 0.0, 0.0, 0.0}, 0)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Should not return error on embedder failure: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Embedder failure should produce empty result")
	}
}

func TestEmbeddingClassifier_Classify_ContextCanceled(t *testing.T) {
	embedder := &MockEmbedder{}
	ec := NewEmbeddingClassifier(embedder, 0.70)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ec.Classify(ctx, "test query", concurrency.AgentGuide)
	if err == nil {
		t.Error("Should return error for canceled context")
	}
}

func TestEmbeddingClassifier_Classify_HasSignals(t *testing.T) {
	embedder := &MockEmbedder{
		embeddings: map[string][]float32{
			"test": {1.0, 0.0, 0.0, 0.0},
		},
	}
	ec := NewEmbeddingClassifier(embedder, 0.70)
	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0, 0.0, 0.0, 0.0}, 0)

	ctx := context.Background()
	result, _ := ec.Classify(ctx, "test", concurrency.AgentGuide)

	signals := result.GetSignals(domain.DomainLibrarian)
	if len(signals) == 0 {
		t.Error("Should include embedding_similarity signal")
	}
}

func TestEmbeddingClassifier_UpdateThreshold(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	ec.UpdateThreshold(0.85)

	if ec.threshold != 0.85 {
		t.Errorf("threshold = %f, want 0.85", ec.threshold)
	}
}

func TestEmbeddingClassifier_CentroidCount(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	if ec.CentroidCount() != 0 {
		t.Error("Initial centroid count should be 0")
	}

	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0}, 0)
	ec.SetCentroid(domain.DomainAcademic, []float32{1.0}, 0)

	if ec.CentroidCount() != 2 {
		t.Errorf("CentroidCount() = %d, want 2", ec.CentroidCount())
	}
}

func TestEmbeddingClassifier_GetCentroid_NotExists(t *testing.T) {
	ec := NewEmbeddingClassifier(nil, 0.70)

	_, ok := ec.GetCentroid(domain.DomainLibrarian)
	if ok {
		t.Error("Should return false for non-existent centroid")
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name    string
		a       []float32
		b       []float32
		want    float64
		epsilon float64
	}{
		{
			name:    "identical vectors",
			a:       []float32{1.0, 0.0, 0.0, 0.0},
			b:       []float32{1.0, 0.0, 0.0, 0.0},
			want:    1.0,
			epsilon: 0.001,
		},
		{
			name:    "orthogonal vectors",
			a:       []float32{1.0, 0.0, 0.0, 0.0},
			b:       []float32{0.0, 1.0, 0.0, 0.0},
			want:    0.0,
			epsilon: 0.001,
		},
		{
			name:    "opposite vectors",
			a:       []float32{1.0, 0.0, 0.0, 0.0},
			b:       []float32{-1.0, 0.0, 0.0, 0.0},
			want:    -1.0,
			epsilon: 0.001,
		},
		{
			name:    "45 degree angle",
			a:       []float32{1.0, 0.0, 0.0, 0.0},
			b:       []float32{0.707, 0.707, 0.0, 0.0},
			want:    0.707,
			epsilon: 0.01,
		},
		{
			name:    "empty vectors",
			a:       []float32{},
			b:       []float32{},
			want:    0.0,
			epsilon: 0.001,
		},
		{
			name:    "mismatched lengths",
			a:       []float32{1.0, 0.0},
			b:       []float32{1.0, 0.0, 0.0},
			want:    0.0,
			epsilon: 0.001,
		},
		{
			name:    "zero vectors",
			a:       []float32{0.0, 0.0, 0.0, 0.0},
			b:       []float32{0.0, 0.0, 0.0, 0.0},
			want:    0.0,
			epsilon: 0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.epsilon {
				t.Errorf("cosineSimilarity() = %f, want %f (±%f)", got, tt.want, tt.epsilon)
			}
		})
	}
}

func TestEmbeddingClassifier_Classify_BelowMinSimilarity(t *testing.T) {
	// Query vector orthogonal to all centroids
	embedder := &MockEmbedder{
		embeddings: map[string][]float32{
			"unrelated": {0.0, 0.0, 0.0, 1.0},
		},
	}
	ec := NewEmbeddingClassifier(embedder, 0.70)
	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0, 0.0, 0.0, 0.0}, 0)
	ec.SetCentroid(domain.DomainAcademic, []float32{0.0, 1.0, 0.0, 0.0}, 0)

	ctx := context.Background()
	result, err := ec.Classify(ctx, "unrelated", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if !result.IsEmpty() {
		t.Error("Low similarity should produce empty result")
	}
}

func TestEmbeddingClassifier_Classify_Method(t *testing.T) {
	embedder := &MockEmbedder{
		embeddings: map[string][]float32{
			"test": {1.0, 0.0, 0.0, 0.0},
		},
	}
	ec := NewEmbeddingClassifier(embedder, 0.70)
	ec.SetCentroid(domain.DomainLibrarian, []float32{1.0, 0.0, 0.0, 0.0}, 0)

	ctx := context.Background()
	result, _ := ec.Classify(ctx, "test", concurrency.AgentGuide)

	if result.Method != "embedding" {
		t.Errorf("Method = %s, want embedding", result.Method)
	}
}

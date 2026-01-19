package classifier

import (
	"context"
	"math"
	"sync"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

const (
	embeddingPriority   = 20
	embeddingMethodName = "embedding"
	minCentroidSim      = 0.50
)

// EmbeddingProvider generates embeddings for text queries.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// DomainCentroid represents a reference vector for a domain.
type DomainCentroid struct {
	Domain    domain.Domain
	Vector    []float32
	Threshold float64
}

// EmbeddingClassifier uses vector similarity to classify domains.
type EmbeddingClassifier struct {
	embedder  EmbeddingProvider
	centroids map[domain.Domain]*DomainCentroid
	threshold float64
	mu        sync.RWMutex
}

// NewEmbeddingClassifier creates a new EmbeddingClassifier.
func NewEmbeddingClassifier(
	embedder EmbeddingProvider,
	threshold float64,
) *EmbeddingClassifier {
	if threshold <= 0 {
		threshold = 0.70
	}
	return &EmbeddingClassifier{
		embedder:  embedder,
		centroids: make(map[domain.Domain]*DomainCentroid),
		threshold: threshold,
	}
}

// SetCentroid sets the reference centroid vector for a domain.
func (e *EmbeddingClassifier) SetCentroid(d domain.Domain, vector []float32, threshold float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if threshold <= 0 {
		threshold = e.threshold
	}
	e.centroids[d] = &DomainCentroid{
		Domain:    d,
		Vector:    vector,
		Threshold: threshold,
	}
}

// SetCentroids sets multiple centroid vectors at once.
func (e *EmbeddingClassifier) SetCentroids(centroids map[domain.Domain][]float32) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for d, vec := range centroids {
		e.centroids[d] = &DomainCentroid{
			Domain:    d,
			Vector:    vec,
			Threshold: e.threshold,
		}
	}
}

func (e *EmbeddingClassifier) Classify(
	ctx context.Context,
	query string,
	_ concurrency.AgentType,
) (*StageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := NewStageResult()
	result.SetMethod(embeddingMethodName)

	if query == "" || e.embedder == nil {
		return result, nil
	}

	e.mu.RLock()
	if len(e.centroids) == 0 {
		e.mu.RUnlock()
		return result, nil
	}
	e.mu.RUnlock()

	queryVec, err := e.embedder.Embed(ctx, query)
	if err != nil {
		return result, nil // Non-fatal: return empty result
	}

	e.computeSimilarities(result, queryVec)
	e.checkTermination(result)

	return result, nil
}

func (e *EmbeddingClassifier) computeSimilarities(result *StageResult, queryVec []float32) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for d, centroid := range e.centroids {
		sim := cosineSimilarity(queryVec, centroid.Vector)
		if sim >= minCentroidSim {
			result.AddDomain(d, sim, []string{"embedding_similarity"})
		}
	}
}

func (e *EmbeddingClassifier) checkTermination(result *StageResult) {
	if result.DomainCount() != 1 {
		return
	}

	_, maxConf := result.HighestConfidence()
	if maxConf >= e.threshold {
		result.MarkComplete()
	}
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

func (e *EmbeddingClassifier) Name() string {
	return embeddingMethodName
}

func (e *EmbeddingClassifier) Priority() int {
	return embeddingPriority
}

// GetCentroid returns the centroid for a domain, if set.
func (e *EmbeddingClassifier) GetCentroid(d domain.Domain) (*DomainCentroid, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	centroid, ok := e.centroids[d]
	return centroid, ok
}

// CentroidCount returns the number of configured centroids.
func (e *EmbeddingClassifier) CentroidCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.centroids)
}

// UpdateThreshold updates the default similarity threshold.
func (e *EmbeddingClassifier) UpdateThreshold(threshold float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.threshold = threshold
}

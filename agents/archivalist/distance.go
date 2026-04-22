package archivalist

import "math"

// cosineSimilarity returns the cosine similarity of two equal-length
// float32 vectors. Result range is [-1, 1]; 1 indicates identical
// direction, 0 orthogonal, -1 opposite. Mismatched lengths or
// all-zero vectors return 0 so callers can treat "no useful signal"
// uniformly instead of propagating NaN.
//
// Used by EmbeddingStore.findCandidates and the similarity-match cache
// in query_cache.go. Kept local to the package so archivalist's
// embedding-adjacent code does not pull in vamana or any vector-index
// dependency.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

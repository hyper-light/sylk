package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/knowledge/query"
)

type ClaimsBackendConfig struct {
	Coordinator                *query.HybridQueryCoordinator
	VectorIndexRefs            []string
	ExpectedEmbeddingDimension int
}

type ClaimsBackend struct {
	coordinator                *query.HybridQueryCoordinator
	vectorIndexRefs            []string
	expectedEmbeddingDimension int
}

type claimsKnowledgeArgs struct {
	Query          string    `json:"query,omitempty"`
	TextQuery      string    `json:"text_query,omitempty"`
	SemanticVector []float32 `json:"semantic_vector,omitempty"`
	Limit          int       `json:"limit,omitempty"`
}

func NewClaimsBackend(cfg ClaimsBackendConfig) *ClaimsBackend {
	return &ClaimsBackend{
		coordinator:                cfg.Coordinator,
		vectorIndexRefs:            append([]string(nil), cfg.VectorIndexRefs...),
		expectedEmbeddingDimension: cfg.ExpectedEmbeddingDimension,
	}
}

func (b *ClaimsBackend) HandleKnowledgeOperation(ctx context.Context, req claims.KnowledgeOperationRequest) (claims.KnowledgeOperationArtifactData, error) {
	data := req.Requested
	if b == nil || b.coordinator == nil {
		data.FailureReason = "knowledge coordinator not configured"
		return data, nil
	}
	data.Ready = b.coordinator.IsReady()
	data.VectorIndexRefs = firstClaimsKnowledgeStrings(data.VectorIndexRefs, b.vectorIndexRefs)
	data.ExpectedEmbeddingDimension = firstClaimsKnowledgeInt(data.ExpectedEmbeddingDimension, b.expectedEmbeddingDimension)
	data.Metadata = claimsKnowledgeMetadata(data.Metadata, b.coordinator.ReadySearchers())
	if isClaimsKnowledgeQuery(data.Operation) {
		return b.query(ctx, req, data), nil
	}
	return data, nil
}

func (b *ClaimsBackend) query(ctx context.Context, req claims.KnowledgeOperationRequest, data claims.KnowledgeOperationArtifactData) claims.KnowledgeOperationArtifactData {
	hybrid, err := hybridQueryFromClaims(req.Call.Arguments)
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	result, err := b.coordinator.ExecuteWithMetrics(ctx, hybrid)
	if err != nil {
		data.FailureReason = err.Error()
		return data
	}
	data.ResultCount = len(result.Results)
	data.ScoreMin, data.ScoreMax = claimsKnowledgeScoreRange(result.Results)
	if result.Metrics != nil {
		data.Stale = result.Metrics.TimedOut
	}
	return data
}

func hybridQueryFromClaims(args map[string]any) (*query.HybridQuery, error) {
	var decoded claimsKnowledgeArgs
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode knowledge claims args: %w", err)
	}
	return &query.HybridQuery{
		TextQuery:      firstClaimsKnowledgeString(decoded.TextQuery, decoded.Query),
		SemanticVector: decoded.SemanticVector,
		Limit:          decoded.Limit,
	}, nil
}

func isClaimsKnowledgeQuery(operation string) bool {
	switch operation {
	case claims.KnowledgeToolQueryGraph, claims.KnowledgeToolQuerySemantic, claims.KnowledgeToolQueryHybrid:
		return true
	default:
		return false
	}
}

func claimsKnowledgeScoreRange(results []query.HybridResult) (float64, float64) {
	if len(results) == 0 {
		return 0, 0
	}
	minScore := results[0].Score
	maxScore := results[0].Score
	for _, result := range results[1:] {
		if result.Score < minScore {
			minScore = result.Score
		}
		if result.Score > maxScore {
			maxScore = result.Score
		}
	}
	return minScore, maxScore
}

func claimsKnowledgeMetadata(base map[string]any, ready []string) map[string]any {
	out := make(map[string]any, len(base)+1)
	for key, value := range base {
		out[key] = value
	}
	out["ready_searchers"] = append([]string(nil), ready...)
	return out
}

func firstClaimsKnowledgeStrings(values ...[]string) []string {
	for _, value := range values {
		if len(value) != 0 {
			return append([]string(nil), value...)
		}
	}
	return nil
}

func firstClaimsKnowledgeString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstClaimsKnowledgeInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

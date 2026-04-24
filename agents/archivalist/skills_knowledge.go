package archivalist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/knowledge/coldstart"
	"github.com/adalundhe/sylk/core/knowledge/memory"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/skills"
)

var errMemorySubsystemUnavailable = errors.New("memory subsystem not available")
var errQuerySubsystemUnavailable = errors.New("query subsystem not available")
var errCrossAgentWeightsUnavailable = errors.New("cross-agent weight manager not available")

// =============================================================================
// Skill 1: knowledge_memory — ACT-R memory operations
// =============================================================================

func knowledgeMemorySkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("knowledge_memory").
		Description("ACT-R memory operations: activation scoring, access recording, outcome feedback, spacing analysis, and review scheduling.").
		Domain("knowledge").
		Keywords("memory", "activation", "actr", "spacing", "review", "decay").
		Priority(95).
		EnumParam("action", "Operation to perform", []string{
			"activation", "record_access", "record_outcome", "review", "spacing", "stats",
		}, true).
		StringParam("node_id", "Target node ID", false).
		ArrayParam("node_ids", "Batch node IDs", "string", false).
		StringParam("access_type", "Access type: retrieval, reinforcement, creation, reference", false).
		StringParam("context", "Context description for access trace", false).
		BoolParam("was_useful", "Outcome signal", false).
		BoolParam("explore", "Use Thompson Sampling", false).
		IntParam("limit", "Result limit", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p knowledgeMemoryParams
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			result, err := a.dispatchKnowledgeMemory(ctx, &p)
			if acc := claims.AccumulatorFromContext(ctx); acc != nil {
				acc.Record("knowledge_memory_"+p.Action, p.NodeID)
			}
			return result, err
		}).
		Build()
}

type knowledgeMemoryParams struct {
	Action     string   `json:"action"`
	NodeID     string   `json:"node_id"`
	NodeIDs    []string `json:"node_ids"`
	AccessType string   `json:"access_type"`
	Context    string   `json:"context"`
	WasUseful  bool     `json:"was_useful"`
	Explore    bool     `json:"explore"`
	Limit      int      `json:"limit"`
}

func (a *Archivalist) dispatchKnowledgeMemory(ctx context.Context, p *knowledgeMemoryParams) (any, error) {
	switch p.Action {
	case "activation":
		return a.memoryActivation(ctx, p.NodeID, p.Explore)
	case "record_access":
		return a.memoryRecordAccess(ctx, p.NodeID, p.AccessType, p.Context)
	case "record_outcome":
		return a.memoryRecordOutcome(ctx, p.NodeIDs, p.WasUseful)
	case "review":
		return a.memoryReview(ctx, p.Limit)
	case "spacing":
		return a.memorySpacing(ctx, p.NodeID)
	case "stats":
		return a.memoryStats()
	default:
		return nil, fmt.Errorf("unknown knowledge_memory action: %s", p.Action)
	}
}

func (a *Archivalist) memoryActivation(ctx context.Context, nodeID string, explore bool) (any, error) {
	if a.memoryScorer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	now := time.Now()
	score, err := a.memoryScorer.ComputeMemoryScore(ctx, nodeID, now, explore)
	if err != nil {
		return nil, fmt.Errorf("compute memory score: %w", err)
	}
	mem, err := a.memoryScorer.Store().GetMemory(ctx, nodeID, domain.DomainArchivalist)
	if err != nil {
		return nil, fmt.Errorf("get memory: %w", err)
	}
	return map[string]any{
		"node_id":        nodeID,
		"activation":     score,
		"retrieval_prob": mem.RetrievalProbability(now, memory.DefaultRetrievalThreshold),
	}, nil
}

func (a *Archivalist) memoryRecordAccess(ctx context.Context, nodeID, accessTypeStr, contextStr string) (any, error) {
	if a.memoryScorer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	accessType := parseAccessType(accessTypeStr)
	if err := a.memoryScorer.Store().RecordAccess(ctx, nodeID, accessType, contextStr); err != nil {
		return nil, fmt.Errorf("record access: %w", err)
	}
	a.memoryScorer.InvalidateActivationCache(nodeID)
	return map[string]any{"recorded": true}, nil
}

func (a *Archivalist) memoryRecordOutcome(ctx context.Context, nodeIDs []string, wasUseful bool) (any, error) {
	if a.hybridMemory == nil {
		return nil, errMemorySubsystemUnavailable
	}
	if err := a.hybridMemory.RecordOutcome(ctx, nodeIDs, wasUseful); err != nil {
		return nil, fmt.Errorf("record outcome: %w", err)
	}
	stats := a.hybridMemory.GetMemoryStats()
	return map[string]any{
		"recorded": true,
		"stats":    stats,
	}, nil
}

func (a *Archivalist) memoryReview(ctx context.Context, limit int) (any, error) {
	if a.spacingAnalyzer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	if a.memoryScorer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	limit = clampLimit(limit, 10, 100)
	memories, err := a.loadRecentMemories(ctx, limit*3)
	if err != nil {
		return nil, fmt.Errorf("load memories: %w", err)
	}
	now := time.Now()
	reviewList := a.spacingAnalyzer.SuggestReviewList(ctx, memories, now, limit)
	results := make([]map[string]any, 0, len(reviewList))
	for _, m := range reviewList {
		report := a.spacingAnalyzer.AnalyzeSpacingPattern(m)
		results = append(results, map[string]any{
			"node_id":           m.NodeID,
			"optimal_review":    a.spacingAnalyzer.OptimalReviewTime(m).String(),
			"spacing_stability": report.Stability,
		})
	}
	return results, nil
}

func (a *Archivalist) memorySpacing(ctx context.Context, nodeID string) (any, error) {
	if a.spacingAnalyzer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	if a.memoryScorer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	mem, err := a.memoryScorer.Store().GetMemory(ctx, nodeID, domain.DomainArchivalist)
	if err != nil {
		return nil, fmt.Errorf("get memory: %w", err)
	}
	report := a.spacingAnalyzer.AnalyzeSpacingPattern(mem)
	return report, nil
}

func (a *Archivalist) memoryStats() (any, error) {
	if a.hybridMemory == nil {
		return nil, errMemorySubsystemUnavailable
	}
	stats := a.hybridMemory.GetMemoryStats()
	result := map[string]any{
		"memory_stats": stats,
	}
	if a.memoryScorer != nil {
		actW, recW, freqW, thrW := a.memoryScorer.GetWeights()
		actC, recC, freqC, thrC := a.memoryScorer.GetWeightConfidences()
		result["weights"] = map[string]float64{
			"activation": actW, "recency": recW,
			"frequency": freqW, "threshold": thrW,
		}
		result["confidences"] = map[string]float64{
			"activation": actC, "recency": recC,
			"frequency": freqC, "threshold": thrC,
		}
	}
	return result, nil
}

// =============================================================================
// Skill 2: knowledge_query — hybrid search with memory weighting
// =============================================================================

func knowledgeQuerySkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("knowledge_query").
		Description("Hybrid knowledge search with optional ACT-R memory weighting, readiness checks, and search feedback.").
		Domain("knowledge").
		Keywords("search", "hybrid", "knowledge", "vector", "bleve", "graph").
		Priority(100).
		EnumParam("action", "Operation to perform", []string{
			"search", "search_feedback", "readiness",
		}, true).
		StringParam("text", "Text query for search", false).
		StringParam("domain", "Domain filter", false).
		IntParam("limit", "Max results (default 10)", false).
		BoolParam("explore", "Thompson Sampling for memory weights", false).
		BoolParam("apply_memory", "Apply ACT-R memory weighting", false).
		BoolParam("filter_retrieval", "Filter by retrieval probability", false).
		StringParam("agent_type", "Source agent for feedback", false).
		ArrayParam("result_ids", "Retrieved entry IDs to attribute feedback against", "string", false).
		BoolParam("was_useful", "Feedback signal", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p struct {
				Action          string   `json:"action"`
				Text            string   `json:"text"`
				Domain          string   `json:"domain"`
				Limit           int      `json:"limit"`
				Explore         bool     `json:"explore"`
				ApplyMemory     *bool    `json:"apply_memory"`
				FilterRetrieval bool     `json:"filter_retrieval"`
				AgentType       string   `json:"agent_type"`
				ResultIDs       []string `json:"result_ids"`
				WasUseful       bool     `json:"was_useful"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}

			var result any
			var err error
			switch p.Action {
			case "search":
				result, err = a.knowledgeSearch(ctx, p.Text, p.Domain, p.Limit, p.Explore, p.ApplyMemory, p.FilterRetrieval)
			case "search_feedback":
				result, err = a.knowledgeSearchFeedback(ctx, p.AgentType, p.ResultIDs, p.WasUseful)
			case "readiness":
				result, err = a.knowledgeReadiness()
			default:
				return nil, fmt.Errorf("unknown knowledge_query action: %s", p.Action)
			}
			if acc := claims.AccumulatorFromContext(ctx); acc != nil {
				acc.Record("knowledge_query_"+p.Action, truncateArchivalist(p.Text, 60))
			}
			return result, err
		}).
		Build()
}

func (a *Archivalist) knowledgeSearch(
	ctx context.Context,
	text, domainStr string,
	limit int,
	explore bool,
	applyMemory *bool,
	filterRetrieval bool,
) (any, error) {
	snapshot := a.snapshotRecallKnowledge()
	if snapshot.coord == nil && snapshot.backend == nil {
		return nil, errQuerySubsystemUnavailable
	}
	limit = clampLimit(limit, 10, 50)

	var merged []map[string]any
	indexByID := make(map[string]int)
	metrics := map[string]any{}

	if backend := snapshot.backend; backend != nil && strings.TrimSpace(text) != "" {
		searchResult, err := backend.Search(ctx, &search.SearchRequest{
			Query: text,
			Limit: limit,
		})
		if err == nil {
			metrics["head_version"] = searchResult.HeadVersion
			metrics["total_hits"] = searchResult.TotalHits
			metrics["search_time"] = searchResult.SearchTime.String()
			for _, hit := range searchResult.Hits {
				entry := map[string]any{
					"id":              hit.ID,
					"path":            hit.Path,
					"content":         hit.Content,
					"score":           hit.Score,
					"document_type":   hit.Document.Type,
					"primary_node_id": hit.PrimaryNodeID,
					"primary_type":    hit.PrimaryNodeType,
					"domain":          hit.Domain,
					"symbols":         hit.Symbols,
					"related_paths":   hit.RelatedPaths,
					"related_symbols": hit.RelatedSymbols,
					"source":          "committed_knowledge",
				}
				indexByID[hit.ID] = len(merged)
				merged = append(merged, entry)
			}
		}
	}
	if snapshot.coord == nil {
		return map[string]any{
			"results": merged,
			"metrics": metrics,
		}, nil
	}

	hq, err := buildHybridQueryWithSemantic(ctx, snapshot.embedder, text, domainStr, limit)
	if err != nil {
		return nil, fmt.Errorf("build hybrid query: %w", err)
	}
	execResult, err := snapshot.coord.ExecuteWithMetrics(ctx, hq)
	if err != nil && len(merged) == 0 {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	if execResult == nil {
		return map[string]any{
			"results": merged,
			"metrics": metrics,
		}, nil
	}

	results := execResult.Results
	if execResult.Metrics != nil {
		metrics["hybrid_metrics"] = execResult.Metrics
	}

	shouldApplyMemory := a.hybridMemory != nil
	if applyMemory != nil {
		shouldApplyMemory = *applyMemory && a.hybridMemory != nil
	}

	if shouldApplyMemory {
		hm := a.hybridMemory.
			WithExploration(explore).
			WithRetrievalFilter(filterRetrieval)
		results, err = hm.Execute(ctx, hq, results)
		if err != nil {
			return nil, fmt.Errorf("apply memory weighting: %w", err)
		}
	}

	for _, r := range results {
		if idx, ok := indexByID[r.ID]; ok {
			entry := merged[idx]
			entry["score"] = maxFloat(anyFloat(entry["score"]), r.Score)
			entry["hybrid_score"] = r.Score
			entry["hybrid_source"] = r.Source.String()
			entry["text_score"] = r.TextScore
			entry["semantic_score"] = r.SemanticScore
			entry["graph_score"] = r.GraphScore
			entry["source"] = "committed_knowledge+hybrid"
			continue
		}
		if strings.TrimSpace(r.Content) == "" {
			continue
		}
		entry := map[string]any{
			"id":             r.ID,
			"content":        r.Content,
			"score":          r.Score,
			"source":         r.Source.String(),
			"text_score":     r.TextScore,
			"semantic_score": r.SemanticScore,
			"graph_score":    r.GraphScore,
		}
		indexByID[r.ID] = len(merged)
		merged = append(merged, entry)
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return anyFloat(merged[i]["score"]) > anyFloat(merged[j]["score"])
	})

	return map[string]any{
		"results": merged,
		"metrics": metrics,
	}, nil
}

func (a *Archivalist) knowledgeSearchFeedback(ctx context.Context, agentType string, resultIDs []string, wasUseful bool) (any, error) {
	if a.crossAgentWeights == nil {
		return nil, errCrossAgentWeightsUnavailable
	}

	recordedFor := make([]string, 0, len(resultIDs)+1)
	for _, resultID := range resultIDs {
		entry, ok := a.GetEntry(ctx, resultID)
		if !ok || entry == nil {
			continue
		}
		origin := originAgentType(entry)
		if origin == "" {
			continue
		}
		a.crossAgentWeights.RecordOutcome(origin, wasUseful)
		recordedFor = append(recordedFor, origin)
	}

	if strings.TrimSpace(agentType) != "" {
		a.crossAgentWeights.RecordOutcome(agentType, wasUseful)
		recordedFor = append(recordedFor, agentType)
	}

	recordedFor = uniqueSearchTerms(recordedFor)
	return map[string]any{
		"recorded":        len(recordedFor) > 0,
		"recorded_for":    recordedFor,
		"used_result_ids": len(resultIDs) > 0,
	}, nil
}

func (a *Archivalist) knowledgeReadiness() (any, error) {
	if a.queryCoordinator == nil {
		return nil, errQuerySubsystemUnavailable
	}
	return map[string]any{
		"ready":     a.queryCoordinator.IsReady(),
		"searchers": a.queryCoordinator.ReadySearchers(),
	}, nil
}

// =============================================================================
// Skill 3: knowledge_insights — diagnostics and introspection
// =============================================================================

func knowledgeInsightsSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("knowledge_insights").
		Description("Diagnostic introspection: agent trust weights, query weight stats, decay parameters, and cold/warm blend phase.").
		Domain("knowledge").
		Keywords("insights", "diagnostics", "trust", "weights", "decay", "blend").
		Priority(75).
		EnumParam("action", "Operation to perform", []string{
			"agent_trust", "query_weights", "decay_params", "blend_phase",
		}, true).
		StringParam("agent_type", "Agent type for trust lookup", false).
		StringParam("domain", "Domain for scoped queries", false).
		StringParam("node_id", "Node ID for blend phase", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p struct {
				Action    string `json:"action"`
				AgentType string `json:"agent_type"`
				Domain    string `json:"domain"`
				NodeID    string `json:"node_id"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}

			switch p.Action {
			case "agent_trust":
				return a.insightAgentTrust(p.AgentType)
			case "query_weights":
				return a.insightQueryWeights(p.Domain)
			case "decay_params":
				return a.insightDecayParams(p.Domain)
			case "blend_phase":
				return a.insightBlendPhase(ctx, p.NodeID)
			default:
				return nil, fmt.Errorf("unknown knowledge_insights action: %s", p.Action)
			}
		}).
		Build()
}

func (a *Archivalist) insightAgentTrust(agentType string) (any, error) {
	if a.crossAgentWeights == nil {
		return nil, errCrossAgentWeightsUnavailable
	}
	if agentType != "" {
		return map[string]any{
			"agent_type": agentType,
			"weight":     a.crossAgentWeights.WeighResult(agentType),
		}, nil
	}
	return a.crossAgentWeights.Snapshot(), nil
}

func (a *Archivalist) insightQueryWeights(domainStr string) (any, error) {
	if a.queryCoordinator == nil {
		return nil, errQuerySubsystemUnavailable
	}
	lw := a.queryCoordinator.LearnedWeights()
	if lw == nil {
		return nil, errQuerySubsystemUnavailable
	}
	if domainStr != "" {
		d := parseDomainOrDefault(domainStr)
		return lw.GetDomainStats(d), nil
	}
	return lw.GetStats(), nil
}

func (a *Archivalist) insightDecayParams(domainStr string) (any, error) {
	if a.memoryScorer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	d := parseDomainOrDefault(domainStr)
	params := a.memoryScorer.Store().GetDomainDecayParams(d)
	return map[string]any{
		"alpha":       params.DecayAlpha,
		"beta":        params.DecayBeta,
		"mean":        params.BaseOffsetMean,
		"offset_mean": params.BaseOffsetMean,
		"offset_var":  params.BaseOffsetVar,
	}, nil
}

func (a *Archivalist) insightBlendPhase(ctx context.Context, nodeID string) (any, error) {
	if a.memoryScorer == nil {
		return nil, errMemorySubsystemUnavailable
	}
	cfg := coldstart.DefaultBlendConfig()
	mem, err := a.memoryScorer.Store().GetMemory(ctx, nodeID, domain.DomainArchivalist)
	if err != nil {
		return nil, fmt.Errorf("get memory: %w", err)
	}
	traceCount := len(mem.Traces)

	var phase string
	var ratio float64
	switch {
	case traceCount < cfg.MinTracesForWarm:
		phase = "cold"
		ratio = 0.0
	case traceCount >= cfg.FullWarmTraces:
		phase = "warm"
		ratio = 1.0
	default:
		phase = "blending"
		span := cfg.FullWarmTraces - cfg.MinTracesForWarm
		if span > 0 {
			ratio = float64(traceCount-cfg.MinTracesForWarm) / float64(span)
		}
	}

	return map[string]any{
		"phase":       phase,
		"trace_count": traceCount,
		"ratio":       ratio,
	}, nil
}

// =============================================================================
// Shared Helpers
// =============================================================================

func parseDomainOrDefault(s string) domain.Domain {
	if s == "" {
		return domain.DomainArchivalist
	}
	d, ok := domain.ParseDomain(s)
	if !ok {
		return domain.DomainArchivalist
	}
	return d
}

func parseAccessType(s string) memory.AccessType {
	at, ok := memory.ParseAccessType(s)
	if ok {
		return at
	}
	return memory.AccessRetrieval
}

func buildHybridQuery(text, domainStr string, limit int) *query.HybridQuery {
	limit = clampLimit(limit, 10, 100)
	hq := &query.HybridQuery{
		TextQuery: text,
		Limit:     limit,
	}
	if domainStr != "" {
		d := parseDomainOrDefault(domainStr)
		hq.Filters = []query.QueryFilter{
			{Type: query.FilterDomain, Value: int(d)},
		}
	}
	return hq
}

func (a *Archivalist) loadRecentMemories(ctx context.Context, limit int) ([]*memory.ACTRMemory, error) {
	limit = clampLimit(limit, 30, 300)
	db := a.memoryScorer.Store().DB()
	rows, err := db.QueryContext(ctx, `
		SELECT id FROM nodes
		WHERE memory_activation IS NOT NULL
		ORDER BY last_accessed_at DESC, access_count DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent memories: %w", err)
	}
	defer rows.Close()

	var memories []*memory.ACTRMemory
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, fmt.Errorf("scan node id: %w", err)
		}
		mem, err := a.memoryScorer.Store().GetMemory(ctx, nodeID, domain.DomainArchivalist)
		if err != nil {
			continue
		}
		memories = append(memories, mem)
	}
	return memories, rows.Err()
}

func clampLimit(v, defaultVal, maxVal int) int {
	if v <= 0 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func anyFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

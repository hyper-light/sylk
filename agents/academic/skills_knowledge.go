package academic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	knowledgedomain "github.com/adalundhe/sylk/core/domain"
	knowledgequery "github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/skills"
)

var errKnowledgeQueryUnavailable = errors.New("academic knowledge query subsystem is unavailable")

type academicKnowledgeSnapshot struct {
	backend committedKnowledgeSearcher
	coord   *knowledgequery.HybridQueryCoordinator
}

func (a *Academic) snapshotKnowledgeQuery() academicKnowledgeSnapshot {
	a.knowledgeMu.RLock()
	defer a.knowledgeMu.RUnlock()
	return academicKnowledgeSnapshot{
		backend: a.knowledgeBackend,
		coord:   a.queryCoordinator,
	}
}

func knowledgeQuerySkill(a *Academic) *skills.Skill {
	return skills.NewSkill("knowledge_query").
		Description("Search Sylk's knowledge graph and grounded document index for previously ingested evidence, fetched sources, stored research, and architectural context. Use this early when prior grounded material may already answer part of the question.").
		Domain("knowledge").
		Keywords("knowledge", "graph", "grounded", "documents", "search", "internal evidence", "retrieval").
		Priority(98).
		EnumParam("action", "Operation to perform", []string{"search", "readiness"}, true).
		StringParam("text", "Text query for search", false).
		StringParam("domain", "Optional domain filter", false).
		IntParam("limit", "Max results (default 10)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p struct {
				Action string `json:"action"`
				Text   string `json:"text"`
				Domain string `json:"domain"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch p.Action {
			case "search":
				return a.knowledgeSearch(ctx, p.Text, p.Domain, p.Limit)
			case "readiness":
				return a.knowledgeReadiness()
			default:
				return nil, fmt.Errorf("unknown knowledge_query action: %s", p.Action)
			}
		}).
		Build()
}

func (a *Academic) knowledgeSearch(ctx context.Context, text, domainStr string, limit int) (any, error) {
	snapshot := a.snapshotKnowledgeQuery()
	if snapshot.backend == nil && snapshot.coord == nil {
		return nil, errKnowledgeQueryUnavailable
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("text is required for knowledge_query search")
	}

	limit = academicKnowledgeClampLimit(limit, 10, 50)
	merged := make([]map[string]any, 0, limit)
	indexByID := make(map[string]int)
	metrics := map[string]any{}
	var backendErr error

	if snapshot.backend != nil {
		searchResult, err := snapshot.backend.Search(ctx, &search.SearchRequest{
			Query: text,
			Limit: limit,
		})
		if err != nil {
			backendErr = err
		} else if searchResult != nil {
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
		if backendErr != nil {
			return nil, fmt.Errorf("committed knowledge search: %w", backendErr)
		}
		return map[string]any{
			"results": merged,
			"metrics": metrics,
		}, nil
	}

	hq := academicBuildHybridQuery(text, domainStr, limit)
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
	if execResult.Metrics != nil {
		metrics["hybrid_metrics"] = execResult.Metrics
	}

	for _, r := range execResult.Results {
		if idx, ok := indexByID[r.ID]; ok {
			entry := merged[idx]
			score := academicKnowledgeAnyFloat(entry["score"])
			if r.Score > score {
				score = r.Score
			}
			entry["score"] = score
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
		return academicKnowledgeAnyFloat(merged[i]["score"]) > academicKnowledgeAnyFloat(merged[j]["score"])
	})

	// Knowledge query testament.
	a.academicSubmitTestament(ctx, a.academicTestament(
		fmt.Sprintf("Knowledge query: %d results for %q", len(merged), truncateAcademic(text, 60)),
		"committed",
		[]*claims.Artifact{
			a.academicArtifact("query", truncateAcademic(text, 200)),
			a.academicArtifact("result_count", fmt.Sprintf("%d", len(merged))),
			a.academicJSONArtifact("metrics", metrics),
		},
	))

	return map[string]any{
		"results": merged,
		"metrics": metrics,
	}, nil
}

func (a *Academic) knowledgeReadiness() (any, error) {
	snapshot := a.snapshotKnowledgeQuery()
	if snapshot.backend == nil && snapshot.coord == nil {
		return nil, errKnowledgeQueryUnavailable
	}

	readySearchers := make([]string, 0, 4)
	ready := false
	if snapshot.backend != nil {
		ready = true
		readySearchers = append(readySearchers, "committed_backend")
	}
	if snapshot.coord != nil {
		if snapshot.coord.IsReady() {
			ready = true
		}
		readySearchers = append(readySearchers, snapshot.coord.ReadySearchers()...)
	}
	return map[string]any{
		"ready":     ready,
		"searchers": academicUniqueStrings(readySearchers),
	}, nil
}

func academicBuildHybridQuery(text, domainStr string, limit int) *knowledgequery.HybridQuery {
	limit = academicKnowledgeClampLimit(limit, 10, 100)
	hq := &knowledgequery.HybridQuery{
		TextQuery: strings.TrimSpace(text),
		Limit:     limit,
	}
	domainStr = strings.TrimSpace(domainStr)
	if domainStr == "" {
		return hq
	}
	d, ok := knowledgedomain.ParseDomain(domainStr)
	if !ok {
		d = knowledgedomain.DomainAcademic
	}
	hq.Filters = []knowledgequery.QueryFilter{
		{Type: knowledgequery.FilterDomain, Value: int(d)},
	}
	return hq
}

func academicKnowledgeClampLimit(v, defaultVal, maxVal int) int {
	if v <= 0 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func academicKnowledgeAnyFloat(value any) float64 {
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

func academicUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

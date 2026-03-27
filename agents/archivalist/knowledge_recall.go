package archivalist

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	knowledgequery "github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/search"
)

type recallKnowledgeSnapshot struct {
	backend   committedKnowledgeSearcher
	coord     *knowledgequery.HybridQueryCoordinator
	embedder  Embedder
	sessionID string
}

func (a *Archivalist) snapshotRecallKnowledge() recallKnowledgeSnapshot {
	a.runMu.RLock()
	defer a.runMu.RUnlock()

	snapshot := recallKnowledgeSnapshot{
		backend: a.knowledgeBackend,
		coord:   a.queryCoordinator,
	}
	if a.retriever != nil {
		snapshot.embedder = a.retriever.embedder
	}
	snapshot.sessionID = a.GetDefaultSession()
	return snapshot
}

func (a *Archivalist) collectKnowledgeRecallEntries(ctx context.Context, query ArchiveQuery) []*Entry {
	searchText := strings.TrimSpace(query.SearchText)
	if searchText == "" {
		return nil
	}

	snapshot := a.snapshotRecallKnowledge()
	if snapshot.backend == nil && snapshot.coord == nil {
		return nil
	}

	limit := recallKnowledgeCandidateLimit(query)
	var (
		backendResult *knowledgeruntime.CommittedSearchResult
		hybridResult  *knowledgequery.ExecutionResult
	)

	var wg sync.WaitGroup
	if snapshot.backend != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := snapshot.backend.Search(ctx, &search.SearchRequest{
				Query: searchText,
				Limit: limit,
			})
			if err == nil {
				backendResult = result
			}
		}()
	}
	if snapshot.coord != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hq, err := buildHybridQueryWithSemantic(ctx, snapshot.embedder, searchText, "", limit)
			if err != nil || hq == nil {
				return
			}
			result, execErr := snapshot.coord.ExecuteWithMetrics(ctx, hq)
			if execErr == nil {
				hybridResult = result
			}
		}()
	}
	wg.Wait()

	return a.fuseKnowledgeRecallEntries(query, snapshot.sessionID, backendResult, hybridResult)
}

func recallKnowledgeCandidateLimit(query ArchiveQuery) int {
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit < 10 {
		limit = 10
	}
	if limit > 40 {
		return 40
	}
	return limit * 2
}

func buildHybridQueryWithSemantic(ctx context.Context, embedder Embedder, text, domainStr string, limit int) (*knowledgequery.HybridQuery, error) {
	if strings.TrimSpace(domainStr) == "" {
		domainStr = "archivalist"
	}
	hq := buildHybridQuery(text, domainStr, limit)
	if hq == nil {
		return nil, nil
	}
	if embedder == nil || strings.TrimSpace(text) == "" {
		return hq, nil
	}
	vector, err := embedder.Embed(ctx, text)
	if err != nil || len(vector) == 0 {
		return hq, err
	}
	hq.SemanticVector = vector
	return hq, nil
}

func (a *Archivalist) fuseKnowledgeRecallEntries(
	query ArchiveQuery,
	sessionID string,
	backendResult *knowledgeruntime.CommittedSearchResult,
	hybridResult *knowledgequery.ExecutionResult,
) []*Entry {
	if (backendResult == nil || len(backendResult.Hits) == 0) && (hybridResult == nil || len(hybridResult.Results) == 0) {
		return nil
	}

	entriesByID := make(map[string]*Entry)
	rankByID := make(map[string]float64)

	if backendResult != nil {
		for _, hit := range backendResult.Hits {
			entry := committedKnowledgeHitEntry(query, sessionID, backendResult.HeadVersion, hit)
			entriesByID[hit.ID] = entry
			rankByID[hit.ID] = committedRecallScore(hit.Score, 0, false)
		}
	}

	if hybridResult != nil {
		for _, result := range hybridResult.Results {
			entry, ok := entriesByID[result.ID]
			if ok {
				augmentCommittedKnowledgeEntryWithHybrid(entry, result)
				baseScore := rankByID[result.ID]
				rankByID[result.ID] = maxFloat(baseScore, committedRecallScore(0, result.Score, true))
				continue
			}
			if strings.TrimSpace(result.Content) == "" {
				continue
			}
			entry = hybridKnowledgeResultEntry(query, sessionID, result)
			entriesByID[result.ID] = entry
			rankByID[result.ID] = committedRecallScore(0, result.Score, false)
		}
	}

	entries := make([]*Entry, 0, len(entriesByID))
	for id, entry := range entriesByID {
		if entry.Metadata == nil {
			entry.Metadata = make(map[string]any)
		}
		entry.Metadata["combined_score"] = rankByID[id]
		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		iScore := metadataFloat(entries[i].Metadata, "combined_score")
		jScore := metadataFloat(entries[j].Metadata, "combined_score")
		if iScore == jScore {
			return entries[i].ID < entries[j].ID
		}
		return iScore > jScore
	})

	limit := query.Limit
	if limit <= 0 {
		limit = len(entries)
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func committedKnowledgeHitEntry(query ArchiveQuery, sessionID, headVersion string, hit knowledgeruntime.CommittedSearchHit) *Entry {
	return &Entry{
		ID:        "knowledge:" + hit.ID,
		Category:  knowledgeEntryCategory(hit.Document.Type),
		Content:   committedKnowledgeExcerpt(query.SearchText, hit.Content, 900),
		Source:    SourceModelArchivalist,
		SessionID: sessionID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata: map[string]any{
			"path":            hit.Path,
			"score":           hit.Score,
			"document_type":   hit.Document.Type,
			"primary_node_id": hit.PrimaryNodeID,
			"primary_type":    hit.PrimaryNodeType,
			"domain":          hit.Domain,
			"symbols":         hit.Symbols,
			"related_paths":   hit.RelatedPaths,
			"related_symbols": hit.RelatedSymbols,
			"head_version":    headVersion,
			"search_source":   "committed_knowledge",
		},
	}
}

func hybridKnowledgeResultEntry(query ArchiveQuery, sessionID string, result knowledgequery.HybridResult) *Entry {
	return &Entry{
		ID:        "knowledge:" + result.ID,
		Category:  CategoryInsight,
		Content:   committedKnowledgeExcerpt(query.SearchText, result.Content, 900),
		Source:    SourceModelArchivalist,
		SessionID: sessionID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata: map[string]any{
			"score":          result.Score,
			"text_score":     result.TextScore,
			"semantic_score": result.SemanticScore,
			"graph_score":    result.GraphScore,
			"source":         result.Source.String(),
			"search_source":  "hybrid_knowledge",
		},
	}
}

func augmentCommittedKnowledgeEntryWithHybrid(entry *Entry, result knowledgequery.HybridResult) {
	if entry == nil {
		return
	}
	if entry.Metadata == nil {
		entry.Metadata = make(map[string]any)
	}
	entry.Metadata["hybrid_score"] = result.Score
	entry.Metadata["hybrid_text_score"] = result.TextScore
	entry.Metadata["hybrid_semantic_score"] = result.SemanticScore
	entry.Metadata["hybrid_graph_score"] = result.GraphScore
	entry.Metadata["hybrid_source"] = result.Source.String()
	entry.Metadata["search_source"] = "committed_knowledge+hybrid"
}

func committedRecallScore(committedScore, hybridScore float64, corroborated bool) float64 {
	score := maxFloat(committedScore, hybridScore)
	if corroborated {
		score += 0.15
	}
	return score
}

func metadataFloat(meta map[string]any, key string) float64 {
	if meta == nil {
		return 0
	}
	switch v := meta[key].(type) {
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

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

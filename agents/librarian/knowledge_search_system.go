package librarian

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/search"
)

type committedKnowledgeSearcher interface {
	Search(ctx context.Context, req *search.SearchRequest) (*knowledgeruntime.CommittedSearchResult, error)
}

type committedKnowledgeSearchSystem struct {
	backend committedKnowledgeSearcher
}

// NewCommittedKnowledgeSearchSystem adapts the shared committed-global
// knowledge backend to the librarian's SearchSystem contract.
func NewCommittedKnowledgeSearchSystem(backend committedKnowledgeSearcher) SearchSystem {
	return &committedKnowledgeSearchSystem{backend: backend}
}

func (s *committedKnowledgeSearchSystem) Search(ctx context.Context, queryText string, opts SearchOptions) (*CodeSearchResult, error) {
	if s == nil || s.backend == nil {
		return nil, knowledgeruntime.ErrCommittedBackendUnavailable
	}
	if err := waitForCommittedKnowledgeBackend(ctx, opts.SessionID); err != nil {
		return nil, err
	}

	req := &search.SearchRequest{
		Query:      queryText,
		PathFilter: opts.PathPrefix,
		Limit:      opts.Limit,
	}
	if opts.Fuzzy {
		req.FuzzyLevel = 1
	}

	result, err := s.backend.Search(ctx, req)
	if err != nil {
		return nil, err
	}

	docs := make([]ScoredDocument, 0, len(result.Hits))
	for _, hit := range result.Hits {
		if !matchesRequestedNodeTypes(hit, opts.Types) {
			continue
		}
		docs = append(docs, ScoredDocument{
			ID:      hit.ID,
			Path:    hit.Path,
			Type:    firstNonEmpty(hit.PrimaryNodeType, string(hit.Document.Type)),
			Content: hit.Content,
			Score:   hit.Score,
			Line:    inferQueryLine(hit.Content, queryText),
		})
	}

	return &CodeSearchResult{
		Documents: docs,
		TotalHits: len(docs),
		Took:      result.SearchTime,
	}, nil
}

func waitForCommittedKnowledgeBackend(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	board := claims.DefaultSessionBoardRegistry().Lookup(sessionID)
	if board == nil {
		return fmt.Errorf("knowledge backend readiness: session %q has no claims board", sessionID)
	}
	_, err := claims.WaitForKnowledgeBackendReady(ctx, board)
	return err
}

func matchesRequestedNodeTypes(hit knowledgeruntime.CommittedSearchHit, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	if len(hit.NodeKinds) == 0 {
		return false
	}
	for _, requestedType := range requested {
		for _, kind := range hit.NodeKinds {
			if strings.EqualFold(kind, requestedType) {
				return true
			}
		}
	}
	return false
}

func inferQueryLine(content, queryText string) int {
	if strings.TrimSpace(content) == "" || strings.TrimSpace(queryText) == "" {
		return 0
	}
	contentLower := strings.ToLower(content)
	queryLower := strings.ToLower(queryText)
	idx := strings.Index(contentLower, queryLower)
	if idx < 0 {
		return 0
	}
	return 1 + strings.Count(content[:idx], "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

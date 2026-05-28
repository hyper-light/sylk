package librarian

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/search"
)

const (
	knowledgeSearchWaitDeadline = time.Second
	knowledgeSearchSettleWindow = 20 * time.Millisecond
)

type fakeCommittedKnowledgeBackend struct {
	mu      sync.Mutex
	result  *knowledgeruntime.CommittedSearchResult
	err     error
	lastReq *search.SearchRequest
	calls   int
}

func (f *fakeCommittedKnowledgeBackend) Search(_ context.Context, req *search.SearchRequest) (*knowledgeruntime.CommittedSearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if req != nil {
		copied := *req
		f.lastReq = &copied
	}
	if f.result == nil {
		return &knowledgeruntime.CommittedSearchResult{}, f.err
	}
	return f.result, f.err
}

func (f *fakeCommittedKnowledgeBackend) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeCommittedKnowledgeBackend) lastRequest() *search.SearchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastReq == nil {
		return nil
	}
	copied := *f.lastReq
	return &copied
}

func TestCommittedKnowledgeSearchSystem_SearchUsesCommittedBackend(t *testing.T) {
	backend := &fakeCommittedKnowledgeBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			Query:      "needle",
			SearchTime: 25 * time.Millisecond,
			Hits: []knowledgeruntime.CommittedSearchHit{
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-1",
							Path:    "ui/theme/palette.go",
							Type:    search.DocTypeSourceCode,
							Content: "package theme\n\nfunc palette() {\n\tneedle()\n}\n",
						},
						Score: 0.91,
					},
					NodeKinds: []string{"file", "function"},
				},
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{
							ID:      "doc-2",
							Path:    "docs/theme.md",
							Type:    search.DocTypeMarkdown,
							Content: "needle",
						},
						Score: 0.75,
					},
					NodeKinds: []string{"document"},
				},
			},
		},
	}

	system := NewCommittedKnowledgeSearchSystem(backend)
	result, err := system.Search(context.Background(), "needle", SearchOptions{
		Limit:      7,
		PathPrefix: "ui/theme",
		Fuzzy:      true,
		Types:      []string{"function"},
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	lastReq := backend.lastRequest()
	if lastReq == nil {
		t.Fatal("expected committed backend request to be recorded")
	}
	if lastReq.Query != "needle" {
		t.Fatalf("backend query = %q, want needle", lastReq.Query)
	}
	if lastReq.PathFilter != "ui/theme" {
		t.Fatalf("backend path filter = %q, want ui/theme", lastReq.PathFilter)
	}
	if lastReq.Limit != 7 {
		t.Fatalf("backend limit = %d, want 7", lastReq.Limit)
	}
	if lastReq.FuzzyLevel != 1 {
		t.Fatalf("backend fuzzy level = %d, want 1", lastReq.FuzzyLevel)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("document count = %d, want 1", len(result.Documents))
	}
	if result.Documents[0].Path != "ui/theme/palette.go" {
		t.Fatalf("document path = %q, want ui/theme/palette.go", result.Documents[0].Path)
	}
	if result.Documents[0].Line != 4 {
		t.Fatalf("document line = %d, want 4", result.Documents[0].Line)
	}
	if result.Took != 25*time.Millisecond {
		t.Fatalf("search took = %v, want 25ms", result.Took)
	}
}

func TestCommittedKnowledgeSearchSystem_SearchRequiresBackend(t *testing.T) {
	system := NewCommittedKnowledgeSearchSystem(nil)
	_, err := system.Search(context.Background(), "needle", SearchOptions{})
	if err != knowledgeruntime.ErrCommittedBackendUnavailable {
		t.Fatalf("Search error = %v, want %v", err, knowledgeruntime.ErrCommittedBackendUnavailable)
	}
}

func TestCommittedKnowledgeSearchSystem_WaitsForReadinessTestament(t *testing.T) {
	sessionID := "session-knowledge-wait"
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{SessionID: sessionID, BoardID: "board-knowledge-wait"})
	claims.DefaultSessionBoardRegistry().ReplaceForReason(sessionID, board, "knowledge search wait test")
	t.Cleanup(func() { claims.DefaultSessionBoardRegistry().Remove(sessionID) })

	backend := &fakeCommittedKnowledgeBackend{
		result: &knowledgeruntime.CommittedSearchResult{
			Query: "needle",
			Hits: []knowledgeruntime.CommittedSearchHit{
				{
					ScoredDocument: search.ScoredDocument{
						Document: search.Document{ID: "doc-1", Path: "README.md", Type: search.DocTypeMarkdown, Content: "needle"},
						Score:    0.5,
					},
					NodeKinds: []string{"document"},
				},
			},
		},
	}
	system := NewCommittedKnowledgeSearchSystem(backend)

	var once sync.Once
	claimPosted := make(chan struct{})
	unsubscribe := board.SubscribeDelta(func(delta claims.BoardMutationDelta) error {
		if delta.Kind == "claim_posted" && delta.ClaimID == claims.KnowledgeBackendReadyClaimID {
			once.Do(func() { close(claimPosted) })
		}
		return nil
	})
	defer unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), knowledgeSearchWaitDeadline)
	defer cancel()

	type searchResult struct {
		result *CodeSearchResult
		err    error
	}
	done := make(chan searchResult, 1)
	go func() {
		result, err := system.Search(ctx, "needle", SearchOptions{SessionID: sessionID, Limit: 1})
		done <- searchResult{result: result, err: err}
	}()

	select {
	case <-claimPosted:
	case result := <-done:
		t.Fatalf("search returned before posting readiness claim: result=%v err=%v", result.result, result.err)
	case <-ctx.Done():
		t.Fatalf("readiness claim was not posted: %v", ctx.Err())
	}

	select {
	case result := <-done:
		t.Fatalf("search returned before readiness testament: result=%v err=%v", result.result, result.err)
	case <-time.After(knowledgeSearchSettleWindow):
	}
	if calls := backend.callCount(); calls != 0 {
		t.Fatalf("backend calls before readiness = %d, want 0", calls)
	}

	if _, err := claims.SubmitKnowledgeBackendReadyTestament(ctx, board, map[string]any{"level": "partial"}); err != nil {
		t.Fatalf("SubmitKnowledgeBackendReadyTestament returned error: %v", err)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("search returned error: %v", result.err)
		}
		if result.result.TotalHits != 1 {
			t.Fatalf("total hits = %d, want 1", result.result.TotalHits)
		}
	case <-ctx.Done():
		t.Fatalf("search did not resume after readiness testament: %v", ctx.Err())
	}
}

func TestCommittedKnowledgeSearchSystem_ReadinessWaitHonorsContext(t *testing.T) {
	sessionID := "session-knowledge-timeout"
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{SessionID: sessionID, BoardID: "board-knowledge-timeout"})
	claims.DefaultSessionBoardRegistry().ReplaceForReason(sessionID, board, "knowledge search timeout test")
	t.Cleanup(func() { claims.DefaultSessionBoardRegistry().Remove(sessionID) })

	backend := &fakeCommittedKnowledgeBackend{result: &knowledgeruntime.CommittedSearchResult{}}
	system := NewCommittedKnowledgeSearchSystem(backend)
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeSearchSettleWindow)
	defer cancel()

	_, err := system.Search(ctx, "needle", SearchOptions{SessionID: sessionID})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("search error = %v, want context deadline", err)
	}
	if calls := backend.callCount(); calls != 0 {
		t.Fatalf("backend calls after readiness wait timeout = %d, want 0", calls)
	}
}

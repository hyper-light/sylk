package archivalist

import (
	"context"
	"testing"
)

func TestStoreResearchPaperQueryAndPlanningBrief(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := context.Background()

	storeResult := a.StoreResearchPaper(ctx, &storeResearchPaperParams{
		ID:               "paper-1",
		SessionID:        a.defaultSessionID,
		ResearchSlug:     "distributed-cache",
		Version:          2,
		Title:            "Research Proposal: Distributed Cache",
		Abstract:         "Use cache-aside with explicit invalidation ownership.",
		ProblemFraming:   "Reduce repeated database load without serving stale writes.",
		PaperPath:        "/tmp/distributed-cache_v2.md",
		TopicsResearched: []string{"redis", "cache invalidation"},
		Recommendations:  []string{"Adopt cache-aside", "Centralize invalidation"},
		OpenQuestions:    []string{"Should invalidation be synchronous?"},
		ArchitectSummary: "Plan around explicit cache ownership and invalidation boundaries.",
	})
	if !storeResult.Success {
		t.Fatalf("StoreResearchPaper() failed: %v", storeResult.Error)
	}

	if result := a.StoreDecision(ctx, "distributed-cache", "Adopt a repository-scoped cache boundary", SourceModelArchivalist); !result.Success {
		t.Fatalf("StoreDecision() failed: %v", result.Error)
	}
	if result := a.StoreIssue(ctx, "distributed-cache incidents were caused by stale invalidation ownership", SourceModelArchivalist); !result.Success {
		t.Fatalf("StoreIssue() failed: %v", result.Error)
	}

	records, err := a.QueryResearchPapers(ctx, queryResearchPaperParams{
		ResearchSlug: "distributed-cache",
	})
	if err != nil {
		t.Fatalf("QueryResearchPapers() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(QueryResearchPapers()) = %d, want 1", len(records))
	}
	if records[0].Version != 2 {
		t.Fatalf("Version = %d, want 2", records[0].Version)
	}
	if records[0].ArchitectSummary == "" {
		t.Fatal("expected architect summary to be preserved")
	}

	brief, err := a.BuildResearchPlanningBrief(ctx, planningBriefParams{
		ResearchSlug: "distributed-cache",
	})
	if err != nil {
		t.Fatalf("BuildResearchPlanningBrief() error = %v", err)
	}
	if brief.Paper == nil {
		t.Fatal("expected planning brief paper payload")
	}
	if len(brief.RelatedDecisions) == 0 {
		t.Fatal("expected related decisions in planning brief")
	}
	if len(brief.RelatedIssues) == 0 {
		t.Fatal("expected related issues in planning brief")
	}
}

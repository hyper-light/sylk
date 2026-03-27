package academic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
)

func TestBuildResearchPaperAndArtifact(t *testing.T) {
	a, err := New(Config{SessionID: "sess-paper"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sessionDir := filepath.Join(t.TempDir(), "session")
	if err := a.steering.BindSession(sessionDir, "sess-paper"); err != nil {
		t.Fatalf("BindSession() error = %v", err)
	}
	researchDir := filepath.Join(sessionDir, "research")
	if err := os.MkdirAll(researchDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(researchDir, "distributed-cache_v1.md"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("WriteFile(v1) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(researchDir, "distributed-cache_v2.md"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("WriteFile(v2) error = %v", err)
	}

	result := &ResearchResult{
		Confidence: ConfidenceLevelHigh,
		Findings: []Finding{
			{
				ID:         "f1",
				Topic:      "distributed cache",
				Summary:    "Use cache-aside with explicit invalidation boundaries.",
				Confidence: ConfidenceLevelHigh,
				SourceIDs:  []string{"src-1"},
			},
		},
		Recommendations: []Recommendation{
			{
				ID:            "rec-1",
				Title:         "Adopt cache-aside",
				Description:   "Place Redis behind a repository-facing abstraction.",
				Rationale:     "This keeps invalidation explicit and local to write paths.",
				Applicability: "Fits service-oriented backends with clear mutation boundaries.",
				Confidence:    ConfidenceLevelHigh,
				Alternatives:  []string{"write-through"},
				SourceIDs:     []string{"src-1"},
			},
		},
		SourcesConsulted: []string{"src-1"},
	}

	a.sourceIndex["src-1"] = &Source{
		ID:         "src-1",
		Type:       SourceTypeDocumentation,
		URL:        "https://example.com/cache",
		Title:      "Caching Guide",
		TokenCount: 100,
		Quality:    0.9,
	}

	paper, err := a.buildResearchPaper(&authorResearchPaperParams{
		Topic:         "distributed cache",
		Context:       "The system needs predictable invalidation and minimal stale reads.",
		ResearchSlug:  "distributed-cache",
		Constraints:   []string{"Preserve write-path correctness"},
		Invariants:    []string{"Do not serve stale data after confirmed writes"},
		OpenQuestions: []string{"Should invalidation be event-driven or synchronous?"},
		RelatedTopics: []string{"redis", "cache invalidation"},
	}, result,
		&shared.ConsultationEvidence{Success: true, Data: map[string]any{"summary": "Existing services already centralize data access behind repository abstractions."}},
		&shared.ConsultationEvidence{Success: true, Data: map[string]any{"summary": "Previous incidents were caused by ad hoc invalidation and missing ownership."}},
		"sess-paper",
	)
	if err != nil {
		t.Fatalf("buildResearchPaper() error = %v", err)
	}

	if paper.ResearchSlug != "distributed-cache" {
		t.Fatalf("ResearchSlug = %q, want distributed-cache", paper.ResearchSlug)
	}
	if paper.Version != 3 {
		t.Fatalf("Version = %d, want 3", paper.Version)
	}
	if paper.ArchitectHandoff == nil || strings.TrimSpace(paper.ArchitectHandoff.PlanningSummary) == "" {
		t.Fatal("expected architect handoff summary to be populated")
	}
	if len(paper.PrototypeExamples) == 0 {
		t.Fatal("expected prototype examples to be populated")
	}
	if len(paper.SystemDesignImplications) == 0 {
		t.Fatal("expected system design implications to be populated")
	}
	if strings.TrimSpace(paper.DecisionRationale) == "" {
		t.Fatal("expected decision rationale to be populated")
	}
	if strings.TrimSpace(paper.ArchitectHandoff.PrototypeSketch) == "" {
		t.Fatal("expected architect handoff prototype sketch to be populated")
	}
	if paper.CodebaseApplicability == nil || strings.TrimSpace(paper.CodebaseApplicability.Summary) == "" {
		t.Fatal("expected codebase applicability summary to be populated")
	}
	if len(paper.SourcesCited) != 1 {
		t.Fatalf("len(SourcesCited) = %d, want 1", len(paper.SourcesCited))
	}

	path, err := a.writeResearchPaperArtifact(paper)
	if err != nil {
		t.Fatalf("writeResearchPaperArtifact() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	for _, needle := range []string{
		"# Research Proposal: Distributed Cache",
		"## Key Findings",
		"## Decision Rationale",
		"## Prototype / Proof Of Concept",
		"## Architecture / System Design Implications",
		"## Architect Handoff Summary",
		"## Prototype Sketch",
		"## System Design Notes",
		"cache-aside",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("artifact missing %q\n%s", needle, text)
		}
	}
	if !strings.Contains(paper.ArchitectHandoff.PlanningSummary, "Adopt cache-aside") {
		t.Fatalf("expected architect handoff summary to mention recommended option, got %q", paper.ArchitectHandoff.PlanningSummary)
	}
}

func TestAuthorResearchPaperStoresByDefaultWithoutBus(t *testing.T) {
	a, err := New(Config{SessionID: "sess-author"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sessionDir := filepath.Join(t.TempDir(), "session")
	if err := a.steering.BindSession(sessionDir, "sess-author"); err != nil {
		t.Fatalf("BindSession() error = %v", err)
	}

	result := &ResearchResult{
		Confidence: ConfidenceLevelMedium,
		Findings: []Finding{{
			ID:         "f1",
			Topic:      "oauth",
			Summary:    "Centralize token verification behind a dedicated adapter.",
			Confidence: ConfidenceLevelMedium,
		}},
	}
	now := time.Now()
	result.GeneratedAt = now
	result.CachedAt = &now
	a.researchCache[a.cacheKey(&ResearchQuery{Query: "oauth\n\nContext:\nInternal SSO", SessionID: "sess-author"})] = result

	out, err := a.authorResearchPaper(context.Background(), &authorResearchPaperParams{
		Topic:   "oauth",
		Context: "Internal SSO",
	})
	if err != nil {
		t.Fatalf("authorResearchPaper() error = %v", err)
	}
	if stored, _ := out["stored_in_archivalist"].(bool); stored {
		t.Fatal("expected storage to be skipped without bus availability")
	}
	warnings, _ := out["warnings"].([]string)
	if len(warnings) == 0 {
		t.Fatal("expected warnings when archivalist bus is unavailable")
	}
}

func TestAuthorResearchPaper_UsesExecuteResearchStateWithoutRerunningResearch(t *testing.T) {
	a, err := New(Config{SessionID: "sess-exec-paper"}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sessionDir := filepath.Join(t.TempDir(), "session")
	if err := a.steering.BindSession(sessionDir, "sess-exec-paper"); err != nil {
		t.Fatalf("BindSession() error = %v", err)
	}

	a.upsertResearchSource(&Source{
		ID:          "src-exec-1",
		Type:        SourceTypeDocumentation,
		URL:         "https://example.com/oauth",
		Title:       "OAuth Deployment Guide",
		Description: "Use a dedicated token validation boundary and rotate keys safely.",
		IngestedAt:  time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		TokenCount:  3200,
		Quality:     0.9,
	})

	state := newAcademicResearchExecutionState("sess-exec-paper")
	state.sources = append(state.sources, researchExecutionSource{
		ID:       "src-exec-1",
		URL:      "https://example.com/oauth",
		Title:    "OAuth Deployment Guide",
		Summary:  "Use a dedicated token validation boundary and rotate keys safely.",
		Ingested: true,
		Type:     SourceTypeDocumentation,
		Quality:  0.9,
	})
	state.sourceIDsByURL["https://example.com/oauth"] = "src-exec-1"
	state.librarianEvidence = &shared.ConsultationEvidence{
		Success: true,
		Data:    map[string]any{"summary": "The repo already centralizes auth decisions in one boundary."},
	}
	state.archivalEvidence = &shared.ConsultationEvidence{
		Success: true,
		Data:    map[string]any{"summary": "Prior outages came from token parsing scattered across services."},
	}

	ctx := WithAcademicResearchExecutionState(context.Background(), state)
	out, err := a.authorResearchPaper(ctx, &authorResearchPaperParams{
		Topic:           "oauth token validation",
		Context:         "Architect needs a planning-ready recommendation.",
		ResearchSummary: "Centralize token validation behind one boundary and prove the migration with a narrow prototype.",
		KeyFindings: []string{
			"Primary guidance favors a dedicated token-validation boundary with clear ownership.",
			"Repository fit is strongest where auth policy stays behind one reusable interface.",
		},
		Recommendations: []string{
			"Adopt a dedicated auth boundary and migrate callers behind it incrementally.",
		},
		StoreInArchivalist: false,
	})
	if err != nil {
		t.Fatalf("authorResearchPaper() error = %v", err)
	}
	if strings.TrimSpace(out["paper_path"].(string)) == "" {
		t.Fatalf("paper_path missing from output: %#v", out)
	}
	if strings.TrimSpace(out["summary"].(string)) == "" {
		t.Fatalf("summary missing from output: %#v", out)
	}
}

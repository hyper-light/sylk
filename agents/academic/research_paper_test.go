package academic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

type countingAcademicProvider struct {
	calls int
}

func (p *countingAcademicProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	return nil, fmt.Errorf("provider should not be called")
}

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
		Grounded: true,
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

func TestResolveResearchPaperInputs_TerminalConsultationAutoGroundsDiscoveredURLs(t *testing.T) {
	provider := &countingAcademicProvider{}
	a, err := New(Config{SessionID: "sess-terminal-paper"}, provider)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	policyCfg := fetch.DefaultPolicyConfig()
	policyCfg.RequireTLS = false
	pipeline := fetch.NewPipeline(fetch.PipelineConfig{
		Policy:     fetch.NewFetchPolicy(policyCfg),
		Consent:    fetch.NewConsentGate(fetch.ConsentGateConfig{AutoApproveDomains: []string{"*"}}),
		Quarantine: fetch.NewQuarantineBuffer(8, 1024*1024),
		Client: fetch.NewClient(fetch.ClientConfig{
			MaxBytes:       1024 * 1024,
			RequestTimeout: 5 * time.Second,
			UserAgent:      "Sylk-Academic/1.0",
			Transport: crawlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "http://docs.safe.test/oauth" {
					return &http.Response{
						StatusCode: 404,
						Status:     "404 Not Found",
						Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
						Body:       io.NopCloser(strings.NewReader("missing")),
						Request:    req,
					}, nil
				}
				return &http.Response{
					StatusCode: 200,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body: io.NopCloser(strings.NewReader(
						"<html><body><h1>OAuth deployment guidance</h1><p>Use a dedicated token validation boundary with clear ownership.</p></body></html>",
					)),
					Request: req,
				}, nil
			}),
		}),
		Extractor: fetch.NewContentExtractor(),
		Inspect: func(context.Context, *fetch.QuarantineEntry) (fetch.QuarantineVerdict, []fetch.InspectionFinding, error) {
			return fetch.VerdictClean, nil, nil
		},
	})
	a.SetFetchPipeline(pipeline)

	execState := newAcademicResearchExecutionState("sess-terminal-paper")
	execState.observeNativeSearchCall(context.Background(), a, providers.NativeWebSearchCall{
		ID:     "search-open-1",
		Action: "open_page",
		Query:  "oauth token validation boundary",
		URL:    "http://docs.safe.test/oauth",
	})

	ctx := WithAcademicTurnState(context.Background(), newAcademicTurnState(
		academicTurnActionResearchPaper,
		"Architect consultation must end at paper authoring.",
	))
	ctx = WithAcademicResearchExecutionState(ctx, execState)

	resolution, err := a.resolveResearchPaperInputs(ctx, &authorResearchPaperParams{
		Topic:           "oauth token validation",
		Context:         "Architect needs a planning-ready recommendation.",
		ResearchSummary: "Centralize token validation behind one boundary.",
	}, "sess-terminal-paper")
	if err != nil {
		t.Fatalf("resolveResearchPaperInputs() error = %v", err)
	}
	if resolution == nil {
		t.Fatal("expected resolution")
	}
	result := resolution.result
	librarianEvidence := resolution.librarianEvidence
	archivalEvidence := resolution.archivalEvidence
	if result == nil {
		t.Fatal("expected research result")
	}
	if len(result.SourcesConsulted) == 0 {
		t.Fatalf("SourcesConsulted = %#v, want grounded source IDs", result.SourcesConsulted)
	}
	if librarianEvidence != nil || archivalEvidence != nil {
		t.Fatalf("unexpected consultation evidence: librarian=%#v archivalist=%#v", librarianEvidence, archivalEvidence)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
	if !execState.hasGroundedSources() {
		t.Fatal("expected execution state to record grounded sources after auto-grounding")
	}
}

func TestResolveResearchPaperInputs_TerminalConsultationAutoGroundsProviderSearchResults(t *testing.T) {
	provider := &countingAcademicProvider{}
	a, err := New(Config{SessionID: "sess-terminal-paper"}, provider)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	policyCfg := fetch.DefaultPolicyConfig()
	policyCfg.RequireTLS = false
	pipeline := fetch.NewPipeline(fetch.PipelineConfig{
		Policy:     fetch.NewFetchPolicy(policyCfg),
		Consent:    fetch.NewConsentGate(fetch.ConsentGateConfig{AutoApproveDomains: []string{"*"}}),
		Quarantine: fetch.NewQuarantineBuffer(8, 1024*1024),
		Client: fetch.NewClient(fetch.ClientConfig{
			MaxBytes:       1024 * 1024,
			RequestTimeout: 5 * time.Second,
			UserAgent:      "Sylk-Academic/1.0",
			Transport: crawlRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "http://docs.safe.test/oauth" {
					return &http.Response{
						StatusCode: 404,
						Status:     "404 Not Found",
						Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
						Body:       io.NopCloser(strings.NewReader("missing")),
						Request:    req,
					}, nil
				}
				return &http.Response{
					StatusCode: 200,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
					Body: io.NopCloser(strings.NewReader(
						"<html><body><h1>OAuth deployment guidance</h1><p>Use a dedicated token validation boundary with clear ownership.</p></body></html>",
					)),
					Request: req,
				}, nil
			}),
		}),
		Extractor: fetch.NewContentExtractor(),
		Inspect: func(context.Context, *fetch.QuarantineEntry) (fetch.QuarantineVerdict, []fetch.InspectionFinding, error) {
			return fetch.VerdictClean, nil, nil
		},
	})
	a.SetFetchPipeline(pipeline)

	execState := newAcademicResearchExecutionState("sess-terminal-paper")
	execState.observeProviderResponse(context.Background(), a, &providers.Response{
		ProviderMetadata: map[string]any{
			providers.ProviderMetadataNativeWebSearchCallsKey: []providers.NativeWebSearchCall{
				{
					ID:       "search-1",
					Provider: "anthropic",
					Action:   "search",
					Query:    "oauth token validation boundary",
				},
			},
			providers.ProviderMetadataNativeWebSearchResultsKey: []providers.NativeWebSearchResult{
				{
					SearchID: "search-1",
					Provider: "anthropic",
					Source:   "search_result",
					Query:    "oauth token validation boundary",
					URL:      "http://docs.safe.test/oauth",
					Title:    "OAuth deployment guidance",
					PageAge:  "3d",
				},
			},
		},
	})

	ctx := WithAcademicTurnState(context.Background(), newAcademicTurnState(
		academicTurnActionResearchPaper,
		"Architect consultation must end at paper authoring.",
	))
	ctx = WithAcademicResearchExecutionState(ctx, execState)

	resolution, err := a.resolveResearchPaperInputs(ctx, &authorResearchPaperParams{
		Topic:           "oauth token validation",
		Context:         "Architect needs a planning-ready recommendation.",
		ResearchSummary: "Centralize token validation behind one boundary.",
	}, "sess-terminal-paper")
	if err != nil {
		t.Fatalf("resolveResearchPaperInputs() error = %v", err)
	}
	if resolution == nil {
		t.Fatal("expected resolution")
	}
	if resolution.result == nil {
		t.Fatal("expected research result")
	}
	if len(resolution.result.SourcesConsulted) == 0 {
		t.Fatalf("SourcesConsulted = %#v, want grounded source IDs", resolution.result.SourcesConsulted)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
	if !execState.hasGroundedSources() {
		t.Fatal("expected execution state to record grounded sources after auto-grounding")
	}
}

func TestResolveResearchPaperInputs_TerminalConsultationApprovalDeniedAutoGroundReturnsDelegatedError(t *testing.T) {
	provider := &countingAcademicProvider{}
	a, err := New(Config{SessionID: "sess-terminal-paper"}, provider)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	policyCfg := fetch.DefaultPolicyConfig()
	policyCfg.RequireTLS = false
	pipeline := fetch.NewPipeline(fetch.PipelineConfig{
		Policy: fetch.NewFetchPolicy(policyCfg),
		Consent: fetch.NewConsentGate(fetch.ConsentGateConfig{
			Callback: func(context.Context, *fetch.FetchProposal) (*fetch.ConsentResult, error) {
				return &fetch.ConsentResult{Granted: false, Reason: "not now"}, nil
			},
		}),
	})
	a.SetFetchPipeline(pipeline)

	execState := newAcademicResearchExecutionState("sess-terminal-paper")
	execState.observeProviderResponse(context.Background(), a, &providers.Response{
		ProviderMetadata: map[string]any{
			providers.ProviderMetadataNativeWebSearchResultsKey: []providers.NativeWebSearchResult{
				{
					Provider: "anthropic",
					Source:   "search_result",
					Query:    "oauth token validation boundary",
					URL:      "http://docs.safe.test/oauth",
					Title:    "OAuth deployment guidance",
				},
			},
		},
	})
	ctx := WithAcademicTurnState(context.Background(), newAcademicTurnState(
		academicTurnActionResearchPaper,
		"Architect consultation must end at paper authoring.",
	))
	ctx = WithAcademicResearchExecutionState(ctx, execState)

	_, err = a.resolveResearchPaperInputs(ctx, &authorResearchPaperParams{
		Topic:           "oauth token validation",
		Context:         "Architect needs a planning-ready recommendation.",
		ResearchSummary: "Centralize token validation behind one boundary.",
	}, "sess-terminal-paper")
	if !errors.Is(err, skills.ErrDelegatedRequested) {
		t.Fatalf("resolveResearchPaperInputs() error = %v, want delegated fetch approval error", err)
	}
}

func TestResolveResearchPaperInputs_TerminalConsultationSynthesizesAsIsWhenAutoGroundFails(t *testing.T) {
	provider := &countingAcademicProvider{}
	a, err := New(Config{SessionID: "sess-terminal-paper"}, provider)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ctx := WithAcademicTurnState(context.Background(), newAcademicTurnState(
		academicTurnActionResearchPaper,
		"Architect consultation must end at paper authoring.",
	))
	ctx = WithAcademicResearchExecutionState(ctx, newAcademicResearchExecutionState("sess-terminal-paper"))

	resolution, err := a.resolveResearchPaperInputs(ctx, &authorResearchPaperParams{
		Topic:           "oauth token validation",
		Context:         "Architect needs a planning-ready recommendation.",
		ResearchSummary: "Centralize token validation behind one boundary.",
		Recommendations: []string{"Introduce a dedicated auth boundary and migrate callers incrementally."},
	}, "sess-terminal-paper")
	if err != nil {
		t.Fatalf("resolveResearchPaperInputs() error = %v", err)
	}
	if resolution == nil || resolution.result == nil {
		t.Fatalf("expected synthesized resolution, got %#v", resolution)
	}
	if strings.TrimSpace(resolution.warning) == "" {
		t.Fatalf("expected ungrounded synthesis warning, got %#v", resolution)
	}
	if len(resolution.result.SourcesConsulted) != 0 {
		t.Fatalf("SourcesConsulted = %#v, want none", resolution.result.SourcesConsulted)
	}
	if resolution.result.Confidence != ConfidenceLevelLow {
		t.Fatalf("confidence = %s, want %s", resolution.result.Confidence, ConfidenceLevelLow)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

package cmd

import (
	"context"
	"fmt"
	"testing"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/versioning"
)

type stubApprovalGate struct {
	eval  commandapproval.Evaluation
	err   error
	delay time.Duration
	req   commandapproval.Request
}

func (s *stubApprovalGate) Authorize(ctx context.Context, req commandapproval.Request) (commandapproval.Evaluation, error) {
	s.req = req
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return commandapproval.Evaluation{}, ctx.Err()
		}
	}
	return s.eval, s.err
}

func TestRequestAcademicFetchConsent_UsesGuardianApprovalFlow(t *testing.T) {
	gate := &stubApprovalGate{
		eval: commandapproval.Evaluation{
			Decision:     commandapproval.DecisionAllow,
			Source:       commandapproval.MatchSourceInteractive,
			UserDecision: "allow_always",
			Reason:       "approved by user",
		},
	}

	ctx := versioning.WithSessionID(context.Background(), "session-1")
	ctx = commandapproval.WithGate(ctx, gate)
	ctx = agentshared.WithStreamContext(ctx, "corr-fetch", "tui")
	ctx = agentshared.WithStreamContextMetadata(ctx, map[string]any{
		"dag_id":      "dag-1",
		"task_id":     "task-1",
		"pipeline_id": "pipe-1",
	})

	result, err := requestAcademicFetchConsent(ctx, &fetch.FetchProposal{
		URL:      "https://example.com/docs",
		Domain:   "example.com",
		ToolName: "fetch_document",
		Reason:   "verify the official API reference",
	})
	if err != nil {
		t.Fatalf("requestAcademicFetchConsent() error = %v", err)
	}
	if !result.Granted {
		t.Fatal("expected granted fetch consent")
	}
	if result.GrantScope != "domain" {
		t.Fatalf("grant scope = %q, want domain", result.GrantScope)
	}
	if gate.req.Command != "https://example.com/docs" {
		t.Fatalf("approval command = %q, want fetch URL", gate.req.Command)
	}
	if gate.req.ToolName != "fetch_document" {
		t.Fatalf("tool name = %q, want fetch_document", gate.req.ToolName)
	}
	if gate.req.Domain != "example.com" {
		t.Fatalf("domain = %q, want example.com", gate.req.Domain)
	}
	if gate.req.SessionID != "session-1" {
		t.Fatalf("session = %q, want session-1", gate.req.SessionID)
	}
	if gate.req.DAGID != "dag-1" || gate.req.TaskID != "task-1" || gate.req.PipelineID != "pipe-1" {
		t.Fatalf("scope not populated: %#v", gate.req)
	}
}

func TestNewAcademicFetchConsentGate_DoesNotAutoApproveDomains(t *testing.T) {
	gate := newAcademicFetchConsentGate()

	if got := gate.Check("github.com"); got != fetch.ConsentRequired {
		t.Fatalf("github.com consent decision = %v, want %v", got, fetch.ConsentRequired)
	}
	if got := gate.Check("web.dev"); got != fetch.ConsentRequired {
		t.Fatalf("web.dev consent decision = %v, want %v", got, fetch.ConsentRequired)
	}
}

func TestRequestAcademicFetchConsent_DenialReturnsRejectedConsent(t *testing.T) {
	gate := &stubApprovalGate{
		eval: commandapproval.Evaluation{
			Decision: commandapproval.DecisionDeny,
			Source:   commandapproval.MatchSourceInteractive,
			Reason:   "user denied fetch approval",
		},
		err: fmt.Errorf("%w: %s", commandapproval.ErrApprovalDenied, "user denied fetch approval"),
	}

	ctx := versioning.WithSessionID(context.Background(), "session-1")
	ctx = commandapproval.WithGate(ctx, gate)
	ctx = agentshared.WithStreamContext(ctx, "corr-fetch-deny", "tui")

	result, err := requestAcademicFetchConsent(ctx, &fetch.FetchProposal{
		URL:      "https://example.com/docs",
		Domain:   "example.com",
		ToolName: "web_fetch",
		Reason:   "verify the official API reference",
	})
	if err != nil {
		t.Fatalf("requestAcademicFetchConsent() error = %v", err)
	}
	if result.Granted {
		t.Fatal("expected denied fetch consent")
	}
	if result.Reason != "user denied fetch approval" {
		t.Fatalf("reason = %q, want %q", result.Reason, "user denied fetch approval")
	}
}

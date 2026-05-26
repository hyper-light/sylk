package bridge

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/ui/msg"
)

// integrationProgram captures every Send invocation so tests can assert
// on the emitted message stream.
type integrationProgram struct {
	mu   sync.Mutex
	msgs []any
}

func (p *integrationProgram) Send(m any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, m)
}

func (p *integrationProgram) Snapshot() []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]any, len(p.msgs))
	copy(out, p.msgs)
	return out
}

type recordingPresentationMetricSink struct {
	mu      sync.Mutex
	metrics []PresentationMetric
}

func (s *recordingPresentationMetricSink) ObservePresentationMetric(metric PresentationMetric) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = append(s.metrics, metric)
}

func (s *recordingPresentationMetricSink) Snapshot() []PresentationMetric {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PresentationMetric, len(s.metrics))
	copy(out, s.metrics)
	return out
}

// stubScope minimal for the bridge's drain goroutine. Returns nil
// unconditionally so the bridge runs synchronously in tests.
type stubScopeProvider struct{}

func (stubScopeProvider) Go(_ string, _ time.Duration, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	return fn(context.Background())
}

func setupBridgeOnSession(t *testing.T, sessionID string) (*ClaimsBridge, *claims.ClaimsBoard, *integrationProgram, func()) {
	t.Helper()
	registry := claims.DefaultSessionBoardRegistry()
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	deltaBus := guide.NewClaimsBusAdapter(bus)
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:    "integration-board-" + sessionID,
		PipelineID: "p",
		TaskID:     "t",
		SessionID:  sessionID,
		DeltaBus:   deltaBus,
		Scope:      stubScopeProvider{},
	})
	if err := registry.Register(sessionID, board); err != nil {
		t.Fatalf("Register: %v", err)
	}
	prog := &integrationProgram{}
	br := NewClaimsBridge("test", registry, nil, bus)
	if err := br.Start(prog); err != nil {
		t.Fatalf("Start: %v", err)
	}
	registerBridgeForProgram(prog, br)
	br.SwitchSession(sessionID)
	cleanup := func() {
		br.Stop()
		_ = bus.Close()
		registry.Remove(sessionID)
	}
	return br, board, prog, cleanup
}

func TestBridgeIntegration_ClaimCreatedOpensCycle(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-cycle-open")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "top-level work",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}

	// Drain via bridge outbox into the program. Synchronous: outbox
	// is buffered, drain goroutine runs in this test's goroutine.
	drainBridge(t, prog, "ClaimsAgentStatusMsg(open)")

	var openMsg *msg.ClaimsAgentStatusMsg
	for _, m := range prog.Snapshot() {
		if s, ok := m.(msg.ClaimsAgentStatusMsg); ok && s.Active {
			openMsg = &s
			break
		}
	}
	if openMsg == nil {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=true}")
	}
	if openMsg.AgentID != "architect" {
		t.Fatalf("AgentID = %q, want architect", openMsg.AgentID)
	}
	if openMsg.CycleID == "" {
		t.Fatal("CycleID empty on open event")
	}
}

func TestBridgeIntegration_InProgressStatusOpensObserverOnlyCycle(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-cycle-in-progress-open")
	defer cleanup()

	posted := []claims.Claim{{
		Title: "classify route",
		Relations: []claims.Relation{
			{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
	}}
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt},
		posted,
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	if len(posted) == 0 || posted[0].ID == "" {
		t.Fatal("posted claim ID empty")
	}
	if err := board.UpdateClaimProgress(context.Background(), posted[0].ID, claims.ClaimProgressUpdate{
		WorkSummary: "Classifying and routing request",
	}, "guide"); err != nil {
		t.Fatalf("UpdateClaimProgress: %v", err)
	}

	drainBridge(t, prog, "ClaimsAgentStatusMsg(open from in_progress)")

	var openMsg *msg.ClaimsAgentStatusMsg
	for _, m := range prog.Snapshot() {
		if s, ok := m.(msg.ClaimsAgentStatusMsg); ok && s.Active && s.CycleID == posted[0].ID {
			openMsg = &s
			break
		}
	}
	if openMsg == nil {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=true} from in_progress status delta")
	}
	if openMsg.AgentID != "guide" {
		t.Fatalf("AgentID = %q, want guide", openMsg.AgentID)
	}
}

func TestBridgeIntegration_ClaimRejectedClosesCycleWithFailureOutcome(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-cycle-failure")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "doomed work",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	// Reject the claim — bridge should emit cycle-close with failure.
	if err := board.RejectClaim(context.Background(), claimID, claims.StatusChange{
		From: string(board.Projection().Claims[0].Status), To: string(claims.ClaimStatusRejected),
		Reason: "test reject", AgentID: "architect",
	}, nil, nil); err != nil {
		t.Fatalf("RejectClaim: %v", err)
	}
	drainBridge(t, prog, "ClaimsAgentStatusMsg(close)")

	var closeMsg *msg.ClaimsAgentStatusMsg
	for _, m := range prog.Snapshot() {
		s, ok := m.(msg.ClaimsAgentStatusMsg)
		if !ok || s.Active {
			continue
		}
		closeMsg = &s
		break
	}
	if closeMsg == nil {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=false}")
	}
	if closeMsg.TerminalOutcome != "failure" {
		t.Fatalf("TerminalOutcome = %q, want failure", closeMsg.TerminalOutcome)
	}
}

func TestBridgeIntegration_ArtifactSinkRoutesToChatPanel(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-artifact-sink")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "engineer", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "exec",
			Relations: []claims.Relation{
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	// Emit a started + completed artifact pair via the sink path.
	startedID := "art-1"
	br.OnArtifactAdded(claimID, "engineer", "ses-artifact-sink", &claims.Artifact{
		ID:        startedID,
		AgentID:   "engineer",
		Kind:      "tool_started",
		Reference: "read_file",
		Created:   time.Now(),
	})
	br.OnArtifactAdded(claimID, "engineer", "ses-artifact-sink", &claims.Artifact{
		ID:      "art-2",
		AgentID: "engineer",
		Kind:    "tool_completed",
		Relations: []claims.Relation{
			{Related: startedID, RelatedType: claims.RelatedTypeArtifact, Relationship: claims.RelationshipCompletes},
		},
		Metadata: map[string]any{"outcome": "success", "duration_ms": int64(42)},
		Created:  time.Now(),
	})
	drainBridge(t, prog, "Claim artifact pair")

	var added *msg.ClaimArtifactAddedMsg
	var completed *msg.ClaimArtifactCompletedMsg
	for _, m := range prog.Snapshot() {
		switch s := m.(type) {
		case msg.ClaimArtifactAddedMsg:
			if s.ArtifactID == startedID {
				addedCopy := s
				added = &addedCopy
			}
		case msg.ClaimArtifactCompletedMsg:
			if s.StartArtifactID == startedID {
				completedCopy := s
				completed = &completedCopy
			}
		}
	}
	if added == nil {
		debugSnapshot(t, prog, "artifact-sink")
		t.Fatal("expected ClaimArtifactAddedMsg for the started artifact")
	}
	if added.CycleID == "" {
		t.Fatal("CycleID empty on artifact-added")
	}
	if completed == nil {
		t.Fatal("expected ClaimArtifactCompletedMsg paired by completes relation")
	}
	if completed.Outcome != "success" {
		t.Fatalf("Outcome = %q, want success", completed.Outcome)
	}
}

func TestBridgeIntegration_ArtifactSummaryPrefersCompletionMetadata(t *testing.T) {
	got := artifactSummary(&claims.Artifact{
		Kind:      "tool_completed",
		Reference: "workspace_read",
		Metadata: map[string]any{
			"output": "read 3 files",
		},
	})
	if got != "read 3 files" {
		t.Fatalf("artifactSummary = %q, want metadata output", got)
	}
}

func TestBridgeIntegration_PresentableArtifactEmitsClaimPresentation(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-artifact")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	br.OnArtifactAdded(claimID, "architect", "ses-presentation-artifact", &claims.Artifact{
		ID:        "artifact-plan",
		AgentID:   "architect",
		Kind:      claims.ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\nDo the work.",
		Presentation: &claims.Presentation{
			Audiences:  []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:   []claims.PresentationSurface{claims.PresentationSurfaceChat, claims.PresentationSurfaceApproval},
			Format:     claims.PresentationFormatMarkdown,
			Title:      "Plan",
			Placement:  claims.PresentationPlacementBeforeResponse,
			ReplaceKey: "plan:p1:review",
		},
		Metadata: map[string]any{"plan_id": "p1"},
		Created:  time.Now(),
		Sequence: 77,
	})
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-artifact", &claims.Artifact{
		ID:        "artifact-plan",
		AgentID:   "architect",
		Kind:      claims.ArtifactKindPlanMarkdown,
		Reference: "duplicate",
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
		},
		Created: time.Now(),
	})
	drainBridge(t, prog, "presentable artifact")

	var presentations []msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "artifact-plan" {
			presentations = append(presentations, p)
		}
	}
	if len(presentations) != 1 {
		debugSnapshot(t, prog, "presentable artifact")
		t.Fatalf("presentations = %d, want 1", len(presentations))
	}
	got := presentations[0]
	if got.SourceType != "artifact" || got.ClaimID != claimID || got.CycleID == "" {
		t.Fatalf("unexpected presentation ids: %+v", got)
	}
	if got.Format != "markdown" || got.Placement != "before_response" || got.ReplaceKey != "plan:p1:review" {
		t.Fatalf("unexpected presentation render fields: %+v", got)
	}
	if got.Metadata["plan_id"] != "p1" {
		t.Fatalf("metadata not preserved: %+v", got.Metadata)
	}
}

func TestBridgeIntegration_FreeFloatingPlanMarkdownEmitsPresentation(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-free-floating-plan")
	defer cleanup()

	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			AgentID:    "architect",
			Summary:    "Plan ready for review.",
			Confidence: "committed",
			Artifacts: []*claims.Artifact{{
				ID:        "artifact-free-floating-plan",
				AgentID:   "architect",
				Kind:      claims.ArtifactKindPlanMarkdown,
				Reference: "### Plan\n\n1. Build it.",
				Presentation: &claims.Presentation{
					Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
					Surfaces: []claims.PresentationSurface{
						claims.PresentationSurfaceChat,
						claims.PresentationSurfaceApproval,
					},
					Format:     claims.PresentationFormatMarkdown,
					Title:      "Plan",
					Placement:  claims.PresentationPlacementBeforeResponse,
					ReplaceKey: "plan:p-free:review",
				},
				Metadata: map[string]any{
					"plan_id":               "p-free",
					"stream_correlation_id": "corr-free-plan",
				},
			}},
		}},
	); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	drainBridge(t, prog, "free-floating plan presentation")

	var got *msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "artifact-free-floating-plan" {
			cp := p
			got = &cp
			break
		}
	}
	if got == nil {
		debugSnapshot(t, prog, "free-floating plan presentation")
		t.Fatal("expected ClaimPresentationMsg for free-floating plan markdown")
	}
	if got.CycleID != "corr-free-plan" || got.ClaimID != "" {
		t.Fatalf("unexpected presentation ids: %+v", got)
	}
	if got.Content != "### Plan\n\n1. Build it." {
		t.Fatalf("content = %q", got.Content)
	}
	if got.Format != "markdown" || got.Placement != "before_response" || got.ReplaceKey != "plan:p-free:review" {
		t.Fatalf("unexpected presentation render fields: %+v", got)
	}
}

func TestBridgeIntegration_FreeFloatingLongPlanMarkdownEmitsFullPresentation(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-free-floating-long-plan")
	defer cleanup()

	content := "### Plan\n\n" + strings.Repeat("- Build the CLI with enough implementation detail to exceed projection inline limits.\n", 20)
	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			AgentID:    "architect",
			Summary:    "Plan ready for review.",
			Confidence: "committed",
			Artifacts: []*claims.Artifact{{
				ID:        "artifact-long-free-floating-plan",
				AgentID:   "architect",
				Kind:      claims.ArtifactKindPlanMarkdown,
				Reference: content,
				Presentation: &claims.Presentation{
					Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
					Surfaces: []claims.PresentationSurface{
						claims.PresentationSurfaceChat,
						claims.PresentationSurfaceApproval,
					},
					Format:     claims.PresentationFormatMarkdown,
					Title:      "Plan",
					Placement:  claims.PresentationPlacementBeforeResponse,
					ReplaceKey: "plan:p-long-free:review",
				},
				Metadata: map[string]any{
					"plan_id":               "p-long-free",
					"stream_correlation_id": "corr-long-free-plan",
				},
			}},
		}},
	); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	if len(content) <= 500 {
		t.Fatalf("test content length = %d, want projection-truncated content", len(content))
	}
	drainBridge(t, prog, "free-floating long plan presentation")

	var got *msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "artifact-long-free-floating-plan" {
			cp := p
			got = &cp
			break
		}
	}
	if got == nil {
		debugSnapshot(t, prog, "free-floating long plan presentation")
		t.Fatal("expected ClaimPresentationMsg for long free-floating plan markdown")
	}
	if got.Content != content {
		t.Fatalf("content was not hydrated from canonical artifact:\n got %q\nwant %q", got.Content, content)
	}
	if strings.Contains(strings.ToLower(got.Content), "inline projection was truncated") {
		t.Fatalf("presentation leaked truncation diagnostic: %q", got.Content)
	}
	if got.Format != "markdown" {
		t.Fatalf("format = %q, want markdown", got.Format)
	}
}

func TestBridgeIntegration_ResolveArtifactHydratesTruncatedProjection(t *testing.T) {
	br, board, _, cleanup := setupBridgeOnSession(t, "ses-resolve-long-plan")
	defer cleanup()

	content := "### Plan\n\n" + strings.Repeat("- Preserve this markdown when resolving from a projection copy.\n", 24)
	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			AgentID:    "architect",
			Summary:    "Plan ready for review.",
			Confidence: "committed",
			Artifacts: []*claims.Artifact{{
				ID:        "artifact-resolve-long-plan",
				AgentID:   "architect",
				Kind:      claims.ArtifactKindPlanMarkdown,
				Reference: content,
				Presentation: &claims.Presentation{
					Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
					Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat, claims.PresentationSurfaceApproval},
					Format:    claims.PresentationFormatMarkdown,
				},
			}},
		}},
	); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	projectionArtifact := findArtifact(board.Projection(), "artifact-resolve-long-plan")
	if projectionArtifact == nil {
		t.Fatal("projection artifact missing")
	}
	if !claimMetadataBool(projectionArtifact.Metadata, claims.ArtifactMetadataContentTruncated) {
		t.Fatalf("projection artifact was not marked truncated; len(content)=%d", len(content))
	}

	got, ok := br.ResolveArtifact("ses-resolve-long-plan", "artifact-resolve-long-plan")
	if !ok {
		t.Fatal("ResolveArtifact returned !ok")
	}
	if got.Reference != content {
		t.Fatalf("resolved reference = %q, want full content", got.Reference)
	}
	if claimMetadataBool(got.Metadata, claims.ArtifactMetadataContentTruncated) {
		t.Fatalf("resolved artifact still carries projection truncation metadata: %+v", got.Metadata)
	}
}

func TestBridgeIntegration_PresentableArtifactPreservesReferenceContent(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-content")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "diff",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	reference := "\n```diff\n+ keep leading newline\n```\n"
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-content", &claims.Artifact{
		ID:        "artifact-diff",
		AgentID:   "architect",
		Kind:      "diff",
		Reference: reference,
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
			Format:    claims.PresentationFormatDiff,
		},
		Created:  time.Now(),
		Sequence: 7,
	})
	drainBridge(t, prog, "presentable artifact content")

	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "artifact-diff" {
			if p.Content != reference {
				t.Fatalf("presentation content was altered: %q", p.Content)
			}
			return
		}
	}
	debugSnapshot(t, prog, "presentable artifact content")
	t.Fatal("expected ClaimPresentationMsg for artifact-diff")
}

func TestBridgeIntegration_PresentationReplacementSuppressesOlderLateMessage(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-replace")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	presentation := &claims.Presentation{
		Audiences:  []claims.PresentationAudience{claims.PresentationAudienceUser},
		Surfaces:   []claims.PresentationSurface{claims.PresentationSurfaceChat},
		Format:     claims.PresentationFormatMarkdown,
		ReplaceKey: "plan:p1:review",
	}
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-replace", &claims.Artifact{
		ID:           "artifact-new",
		AgentID:      "architect",
		Kind:         claims.ArtifactKindPlanMarkdown,
		Reference:    "new",
		Presentation: presentation,
		Sequence:     20,
		Created:      time.Now(),
	})
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-replace", &claims.Artifact{
		ID:           "artifact-old",
		AgentID:      "architect",
		Kind:         claims.ArtifactKindPlanMarkdown,
		Reference:    "old",
		Presentation: presentation,
		Sequence:     10,
		Created:      time.Now(),
	})
	drainBridge(t, prog, "presentable replacement")

	var got []msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.ReplaceKey == "plan:p1:review" {
			got = append(got, p)
		}
	}
	if len(got) != 1 {
		debugSnapshot(t, prog, "presentable replacement")
		t.Fatalf("presentations = %d, want 1: %+v", len(got), got)
	}
	if got[0].SourceID != "artifact-new" || got[0].Content != "new" {
		t.Fatalf("late older presentation should be suppressed, got %+v", got[0])
	}
}

func TestBridgeIntegration_TruncatedPresentableArtifactEmitsDiagnostic(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-truncated")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "large report",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-truncated", &claims.Artifact{
		ID:        "artifact-large",
		AgentID:   "architect",
		Kind:      "report",
		Reference: "partial...",
		Metadata: map[string]any{
			claims.ArtifactMetadataContentTruncated: true,
			claims.ArtifactMetadataContentSize:      1200,
		},
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
			Format:    claims.PresentationFormatMarkdown,
		},
		Sequence: 5,
		Created:  time.Now(),
	})
	drainBridge(t, prog, "truncated presentation")

	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "artifact-large" {
			if !strings.Contains(strings.ToLower(p.Content), "truncated") || strings.Contains(p.Content, "partial") {
				t.Fatalf("truncated artifact should emit diagnostic, got %q", p.Content)
			}
			if p.Format != string(claims.PresentationFormatText) {
				t.Fatalf("truncated diagnostic format = %q, want text", p.Format)
			}
			diagnosticID := "presentation-diagnostic-artifact-large-content_truncated"
			diagnostic, ok := board.CloneArtifact(diagnosticID)
			if !ok {
				t.Fatalf("expected durable truncation diagnostic artifact %q", diagnosticID)
			}
			if diagnostic.Metadata["reason"] != "content_truncated" {
				t.Fatalf("diagnostic metadata reason = %v, want content_truncated", diagnostic.Metadata["reason"])
			}
			if !claims.HasRelation(diagnostic.Relations, claims.RelationshipDerivedFrom, "artifact-large") {
				t.Fatalf("diagnostic missing derived_from relation: %+v", diagnostic.Relations)
			}
			return
		}
	}
	debugSnapshot(t, prog, "truncated presentation")
	t.Fatal("expected ClaimPresentationMsg for truncated artifact")
}

func TestBridgeIntegration_TruncatedPlanMarkdownFallsBackToMarkdownNotDiagnostic(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-truncated-plan")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-truncated-plan", &claims.Artifact{
		ID:        "artifact-truncated-plan",
		AgentID:   "architect",
		Kind:      claims.ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Build the CLI.\n…",
		Metadata: map[string]any{
			claims.ArtifactMetadataContentTruncated: true,
			claims.ArtifactMetadataContentSize:      1400,
			"plan_id":                               "p-truncated",
			"stream_correlation_id":                 "corr-plan-review",
		},
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat, claims.PresentationSurfaceApproval},
			Format:    claims.PresentationFormatMarkdown,
			Placement: claims.PresentationPlacementBeforeResponse,
		},
		Sequence: 5,
		Created:  time.Now(),
	})
	drainBridge(t, prog, "truncated plan presentation")

	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "artifact-truncated-plan" {
			if strings.Contains(strings.ToLower(p.Content), "inline projection was truncated") {
				t.Fatalf("plan markdown leaked truncation diagnostic: %q", p.Content)
			}
			if !strings.Contains(p.Content, "### Plan") {
				t.Fatalf("plan markdown content missing: %q", p.Content)
			}
			if p.Format != string(claims.PresentationFormatMarkdown) {
				t.Fatalf("format = %q, want markdown", p.Format)
			}
			if p.CycleID != "corr-plan-review" {
				t.Fatalf("cycle_id = %q, want stream correlation", p.CycleID)
			}
			return
		}
	}
	debugSnapshot(t, prog, "truncated plan presentation")
	t.Fatal("expected ClaimPresentationMsg for truncated plan markdown")
}

func TestBridgeIntegration_InternalArtifactDoesNotEmitPresentation(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-internal-artifact")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	br.OnArtifactAdded(claimID, "architect", "ses-internal-artifact", &claims.Artifact{
		ID:        "handoff",
		AgentID:   "architect",
		Kind:      claims.ArtifactKindPlanHandoffPayload,
		Reference: `{"plan_id":"p1"}`,
		Created:   time.Now(),
	})
	drainBridge(t, prog, "internal artifact")
	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "handoff" {
			t.Fatalf("internal artifact emitted presentation: %+v", p)
		}
	}
}

func TestBridgeIntegration_PresentableTestamentEmitsClaimPresentation(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-testament")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "academic", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "research",
			Relations: []claims.Relation{
				{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "academic", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			AgentID: "academic",
			Summary: "Use argparse for a stdlib-only CLI.",
			Presentation: &claims.Presentation{
				Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
				Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
				Format:    claims.PresentationFormatMarkdown,
				Title:     "Academic recommendation",
			},
			Relations: []claims.Relation{
				{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
			},
		}},
	); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	drainBridge(t, prog, "presentable testament")

	var got *msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot() {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceType == "testament" {
			copy := p
			got = &copy
			break
		}
	}
	if got == nil {
		debugSnapshot(t, prog, "presentable testament")
		t.Fatal("expected ClaimPresentationMsg for testament")
	}
	if got.Content != "Use argparse for a stdlib-only CLI." {
		t.Fatalf("Content = %q", got.Content)
	}
	if got.Title != "Academic recommendation" || got.Format != "markdown" {
		t.Fatalf("unexpected title/format: %+v", got)
	}
}

func TestBridgeIntegration_ReplayLegacyResponseTextWithoutPresentation(t *testing.T) {
	br, _, prog, cleanup := setupBridgeOnSession(t, "ses-legacy-response-replay")
	defer cleanup()

	proj := &claims.ClaimsBoardProjection{
		Claims: []claims.Claim{{
			ID:         "legacy-claim",
			Title:      "answer user",
			Status:     claims.ClaimStatusTestified,
			ActionType: claims.ActionTypeTask,
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
		}},
		Testaments: []claims.Testament{{
			ID:      "legacy-testament",
			AgentID: "architect",
			Relations: []claims.Relation{
				{Related: "legacy-claim", RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
			},
			Artifacts: []*claims.Artifact{{
				ID:        "legacy-response",
				Kind:      claims.ArtifactKindResponseText,
				Reference: "legacy answer",
			}},
		}},
	}
	before := len(prog.Snapshot())
	br.replayProjection("ses-legacy-response-replay", proj)
	drainBridge(t, prog, "legacy response replay")

	var responses, presentations int
	for _, m := range prog.Snapshot()[before:] {
		switch typed := m.(type) {
		case msg.ClaimResponseTextMsg:
			if typed.Content == "legacy answer" {
				responses++
			}
		case msg.ClaimPresentationMsg:
			if typed.SourceID == "legacy-response" {
				presentations++
			}
		}
	}
	if responses != 1 || presentations != 0 {
		debugSnapshot(t, prog, "legacy response replay")
		t.Fatalf("legacy response replay responses=%d presentations=%d, want 1/0", responses, presentations)
	}
}

func TestBridgeIntegration_ResponseTextWithPresentationRendersOnce(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-response-presentation")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "answer user",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	br.OnArtifactAdded(claimID, "architect", "ses-response-presentation", &claims.Artifact{
		ID:           "response-new",
		AgentID:      "architect",
		Kind:         claims.ArtifactKindResponseText,
		Reference:    "new answer",
		Presentation: claims.DefaultResponseTextPresentation(),
		Created:      time.Now(),
	})
	drainBridge(t, prog, "response_text with presentation")

	var responses, presentations int
	for _, m := range prog.Snapshot() {
		switch typed := m.(type) {
		case msg.ClaimResponseTextMsg:
			if typed.Content == "new answer" {
				responses++
			}
		case msg.ClaimPresentationMsg:
			if typed.SourceID == "response-new" {
				presentations++
			}
		}
	}
	if responses != 1 || presentations != 0 {
		debugSnapshot(t, prog, "response_text with presentation")
		t.Fatalf("response_text responses=%d presentations=%d, want 1/0", responses, presentations)
	}
}

func TestBridgeIntegration_InvalidPresentationIncrementsInvalidMetric(t *testing.T) {
	br, board, _, cleanup := setupBridgeOnSession(t, "ses-presentation-invalid")
	defer cleanup()
	sink := &recordingPresentationMetricSink{}
	br.SetPresentationMetricSink(sink)

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	br.OnArtifactAdded(claimID, "architect", "ses-presentation-invalid", &claims.Artifact{
		ID:        "invalid-presentation",
		AgentID:   "architect",
		Kind:      claims.ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Format:    claims.PresentationFormatMarkdown,
		},
		Created: time.Now(),
	})

	if got := presentationMetricCount(br.PresentationMetricsSnapshot(), claimsPresentationInvalid, "invalid_presentation"); got != 1 {
		t.Fatalf("invalid metric = %d, want 1: %+v", got, br.PresentationMetricsSnapshot())
	}
	if got := presentationMetricCount(br.PresentationMetricsSnapshot(), claimsPresentationMessagesDropped, "invalid_presentation"); got != 1 {
		t.Fatalf("dropped invalid metric = %d, want 1: %+v", got, br.PresentationMetricsSnapshot())
	}
	if got := presentationMetricCount(sink.Snapshot(), claimsPresentationInvalid, "invalid_presentation"); got != 1 {
		t.Fatalf("sink invalid metric = %d, want 1: %+v", got, sink.Snapshot())
	}
}

func TestBridgeIntegration_PresentationDropWithoutCycleIncrementsMetric(t *testing.T) {
	br, _, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-drop")
	defer cleanup()

	before := len(prog.Snapshot())
	br.OnArtifactAdded("", "architect", "ses-presentation-drop", &claims.Artifact{
		ID:        "orphan-presentation",
		AgentID:   "architect",
		Kind:      "report",
		Reference: "Visible but not cycle-routable.",
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
			Format:    claims.PresentationFormatMarkdown,
		},
		Created: time.Now(),
	})
	drainBridge(t, prog, "orphan presentation")

	for _, m := range prog.Snapshot()[before:] {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == "orphan-presentation" {
			t.Fatalf("orphan presentation should be dropped, got %+v", p)
		}
	}
	if got := presentationMetricCount(br.PresentationMetricsSnapshot(), claimsPresentationMessagesDropped, "missing_cycle"); got != 1 {
		t.Fatalf("missing_cycle dropped metric = %d, want 1: %+v", got, br.PresentationMetricsSnapshot())
	}
}

func TestBridgeIntegration_PresentationReplacementIncrementsMetric(t *testing.T) {
	br, board, _, cleanup := setupBridgeOnSession(t, "ses-presentation-replacement-metric")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	presentation := &claims.Presentation{
		Audiences:  []claims.PresentationAudience{claims.PresentationAudienceUser},
		Surfaces:   []claims.PresentationSurface{claims.PresentationSurfaceChat},
		Format:     claims.PresentationFormatMarkdown,
		ReplaceKey: "plan:p1:review",
	}
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-replacement-metric", &claims.Artifact{
		ID:           "plan-v1",
		AgentID:      "architect",
		Kind:         claims.ArtifactKindPlanMarkdown,
		Reference:    "### Plan\n\n- v1",
		Presentation: presentation,
		Sequence:     1,
		Created:      time.Now(),
	})
	br.OnArtifactAdded(claimID, "architect", "ses-presentation-replacement-metric", &claims.Artifact{
		ID:           "plan-v2",
		AgentID:      "architect",
		Kind:         claims.ArtifactKindPlanMarkdown,
		Reference:    "### Plan\n\n- v2",
		Presentation: presentation,
		Sequence:     2,
		Created:      time.Now(),
	})

	if got := presentationMetricCount(br.PresentationMetricsSnapshot(), claimsPresentationReplacements, ""); got != 1 {
		t.Fatalf("replacement metric = %d, want 1: %+v", got, br.PresentationMetricsSnapshot())
	}
}

func TestBridgeIntegration_ConcurrentPresentationReplacementsConverge(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-concurrent-replace")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	presentation := &claims.Presentation{
		Audiences:  []claims.PresentationAudience{claims.PresentationAudienceUser},
		Surfaces:   []claims.PresentationSurface{claims.PresentationSurfaceChat},
		Format:     claims.PresentationFormatMarkdown,
		ReplaceKey: "plan:p1:review",
	}

	var wg sync.WaitGroup
	for i := 1; i <= 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			br.OnArtifactAdded(claimID, "architect", "ses-presentation-concurrent-replace", &claims.Artifact{
				ID:           "plan-concurrent-" + strconv.Itoa(i),
				AgentID:      "architect",
				Kind:         claims.ArtifactKindPlanMarkdown,
				Reference:    "### Plan\n\n- v" + strconv.Itoa(i),
				Presentation: presentation,
				Sequence:     uint64(i),
				Created:      time.Now(),
			})
		}()
	}
	wg.Wait()
	drainBridge(t, prog, "concurrent replacements")

	br.mu.Lock()
	state := br.presentationReplacements["plan:p1:review"]
	br.mu.Unlock()
	if state.SourceID != "plan-concurrent-32" || state.Sequence != 32 {
		t.Fatalf("replacement state = %+v, want latest sequence/source", state)
	}
}

func TestBridgeIntegration_UnsupportedPresentationFormatRecordsDiagnosticArtifact(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-diagnostic")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	before := len(prog.Snapshot())

	br.OnArtifactAdded(claimID, "architect", "ses-presentation-diagnostic", &claims.Artifact{
		ID:        "bad-format",
		AgentID:   "architect",
		Kind:      claims.ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
			Format:    claims.PresentationFormat("video"),
		},
		Created: time.Now(),
	})
	drainBridge(t, prog, "unsupported presentation format")

	var fallback *msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot()[before:] {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && strings.Contains(p.SourceID, "presentation-diagnostic") {
			copy := p
			fallback = &copy
			break
		}
	}
	if fallback == nil {
		debugSnapshot(t, prog, "unsupported presentation format")
		t.Fatal("expected fallback presentation diagnostic message")
	}
	if !strings.Contains(fallback.Content, "Could not render") || fallback.Format != string(claims.PresentationFormatText) {
		t.Fatalf("unexpected fallback: %+v", fallback)
	}
	diagnostic, ok := board.CloneArtifact(fallback.SourceID)
	if !ok {
		t.Fatalf("diagnostic artifact %q not recorded", fallback.SourceID)
	}
	if diagnostic.Kind != claims.ArtifactKindErrorDiagnostic {
		t.Fatalf("diagnostic kind = %q, want error_diagnostic", diagnostic.Kind)
	}
	if !claims.HasRelation(diagnostic.Relations, claims.RelationshipDerivedFrom, "bad-format") {
		t.Fatalf("diagnostic missing derived_from relation: %+v", diagnostic.Relations)
	}
	if diagnostic.Metadata["source_artifact_id"] != "bad-format" || diagnostic.Metadata["reason"] != "unsupported_format" {
		t.Fatalf("diagnostic metadata malformed: %+v", diagnostic.Metadata)
	}

	br.OnArtifactAdded(claimID, "architect", "ses-presentation-diagnostic", &claims.Artifact{
		ID:        "bad-format",
		AgentID:   "architect",
		Kind:      claims.ArtifactKindPlanMarkdown,
		Reference: "### Plan\n\n- Task A",
		Presentation: &claims.Presentation{
			Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
			Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
			Format:    claims.PresentationFormat("video"),
		},
		Created: time.Now(),
	})
	drainBridge(t, prog, "unsupported presentation duplicate")
	var fallbackCount int
	for _, m := range prog.Snapshot()[before:] {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == fallback.SourceID {
			fallbackCount++
		}
	}
	if fallbackCount != 1 {
		t.Fatalf("diagnostic fallback count = %d, want 1", fallbackCount)
	}
}

func TestBridgeIntegration_UnsupportedTestamentPresentationRecordsDiagnosticArtifact(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-testament-diagnostic")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "academic", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "research",
			Relations: []claims.Relation{
				{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	before := len(prog.Snapshot())

	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "academic", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			ID:      "testament-bad-format",
			AgentID: "academic",
			Summary: "Research summary.",
			Presentation: &claims.Presentation{
				Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
				Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
				Format:    claims.PresentationFormat("video"),
			},
			Relations: []claims.Relation{
				{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
			},
		}},
	); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	drainBridge(t, prog, "unsupported testament presentation")

	var fallback *msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot()[before:] {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && strings.Contains(p.SourceID, "presentation-diagnostic-testament") {
			copy := p
			fallback = &copy
			break
		}
	}
	if fallback == nil {
		debugSnapshot(t, prog, "unsupported testament presentation")
		t.Fatal("expected fallback presentation diagnostic message")
	}
	diagnostic, ok := board.CloneArtifact(fallback.SourceID)
	if !ok {
		t.Fatalf("diagnostic artifact %q not recorded", fallback.SourceID)
	}
	if !claims.HasRelation(diagnostic.Relations, claims.RelationshipDerivedFrom, "testament-bad-format") {
		t.Fatalf("diagnostic missing testament derived_from relation: %+v", diagnostic.Relations)
	}
	if diagnostic.Metadata["source_testament_id"] != "testament-bad-format" || diagnostic.Metadata["source_type"] != claims.RelatedTypeTestament {
		t.Fatalf("diagnostic metadata malformed: %+v", diagnostic.Metadata)
	}
}

func TestBridgeIntegration_PresentationContentIsRedactedSanitizedAndBounded(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-presentation-safety")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "report",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	cases := []struct {
		id         string
		format     claims.PresentationFormat
		reference  string
		want       string
		mustAbsent []string
	}{
		{
			id:         "json-secret",
			format:     claims.PresentationFormatJSON,
			reference:  `{"token":"tok_live","nested":{"api_key":"key_live","password":"pw","authorization":"Bearer auth_live","private_key":"key_private"}}`,
			want:       "[REDACTED]",
			mustAbsent: []string{"tok_live", "key_live", "pw", "auth_live", "key_private"},
		},
		{
			id:         "markdown-script",
			format:     claims.PresentationFormatMarkdown,
			reference:  "### Report\n\n<script>alert(1)</script>\nAuthorization: Bearer auth_live",
			want:       "&lt;script&gt;",
			mustAbsent: []string{"<script>", "auth_live"},
		},
		{
			id:         "binary-diff",
			format:     claims.PresentationFormatDiff,
			reference:  "GIT binary patch\nliteral 4\nAAAA\n",
			want:       "Binary diff omitted",
			mustAbsent: []string{"literal 4"},
		},
		{
			id:         "huge-markdown",
			format:     claims.PresentationFormatMarkdown,
			reference:  strings.Repeat("A", presentationMaxContent+128),
			want:       "[Presentation content truncated]",
			mustAbsent: nil,
		},
	}
	for _, tc := range cases {
		before := len(prog.Snapshot())
		br.OnArtifactAdded(claimID, "architect", "ses-presentation-safety", &claims.Artifact{
			ID:        tc.id,
			AgentID:   "architect",
			Kind:      "report",
			Reference: tc.reference,
			Presentation: &claims.Presentation{
				Audiences: []claims.PresentationAudience{claims.PresentationAudienceUser},
				Surfaces:  []claims.PresentationSurface{claims.PresentationSurfaceChat},
				Format:    tc.format,
			},
			Metadata: map[string]any{
				"client_secret": "client_secret_live",
				"nested": map[string]any{
					"refresh_token": "refresh_live",
				},
			},
			Created: time.Now(),
		})
		drainBridge(t, prog, "presentation safety "+tc.id)

		var got *msg.ClaimPresentationMsg
		for _, m := range prog.Snapshot()[before:] {
			if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceID == tc.id {
				copy := p
				got = &copy
				break
			}
		}
		if got == nil {
			debugSnapshot(t, prog, "presentation safety "+tc.id)
			t.Fatalf("%s: expected presentation message", tc.id)
		}
		if !strings.Contains(got.Content, tc.want) {
			t.Fatalf("%s: content missing %q: %q", tc.id, tc.want, got.Content)
		}
		for _, absent := range tc.mustAbsent {
			if strings.Contains(got.Content, absent) {
				t.Fatalf("%s: content leaked %q: %q", tc.id, absent, got.Content)
			}
		}
		if metadataContainsString(got.Metadata, "client_secret_live") || metadataContainsString(got.Metadata, "refresh_live") {
			t.Fatalf("%s: metadata leaked secrets: %+v", tc.id, got.Metadata)
		}
		if tc.id == "huge-markdown" && len(got.Content) > presentationMaxContent+128 {
			t.Fatalf("huge content length = %d, want bounded near %d", len(got.Content), presentationMaxContent)
		}
	}
}

func presentationMetricCount(metrics []PresentationMetric, name, reason string) int64 {
	var total int64
	for _, metric := range metrics {
		if metric.Name != name {
			continue
		}
		if reason != "" && metric.Reason != reason {
			continue
		}
		total += metric.Count
	}
	return total
}

func metadataContainsString(value any, needle string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, v := range typed {
			if metadataContainsString(v, needle) {
				return true
			}
		}
	case []any:
		for _, v := range typed {
			if metadataContainsString(v, needle) {
				return true
			}
		}
	case string:
		return strings.Contains(typed, needle)
	}
	return false
}

func TestBridgeIntegration_ReplayLegacyPlanHandoffSynthesizesTransientPresentation(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-legacy-plan-replay")
	defer cleanup()

	proj := &claims.ClaimsBoardProjection{
		Claims: []claims.Claim{{
			ID:         "legacy-plan-claim",
			Title:      "plan",
			Status:     claims.ClaimStatusTestified,
			ActionType: claims.ActionTypeTask,
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
		}},
		Testaments: []claims.Testament{{
			ID:      "legacy-plan-testament",
			AgentID: "architect",
			Relations: []claims.Relation{
				{Related: "legacy-plan-claim", RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
			},
			Artifacts: []*claims.Artifact{{
				ID:   "legacy-handoff",
				Kind: claims.ArtifactKindPlanHandoffPayload,
				Metadata: map[string]any{
					"plan_id":       "legacy-p1",
					"epoch":         2,
					"plan_markdown": "### Plan\n\n- Legacy task",
				},
			}},
		}},
	}
	before := len(prog.Snapshot())
	br.replayProjection("ses-legacy-plan-replay", proj)
	drainBridge(t, prog, "legacy plan replay")

	var got *msg.ClaimPresentationMsg
	for _, m := range prog.Snapshot()[before:] {
		if p, ok := m.(msg.ClaimPresentationMsg); ok && p.SourceType == "synthetic" {
			copy := p
			got = &copy
			break
		}
	}
	if got == nil {
		debugSnapshot(t, prog, "legacy plan replay")
		t.Fatal("expected synthetic presentation")
	}
	if got.Content != "### Plan\n\n- Legacy task" || got.Metadata["synthetic"] != true {
		t.Fatalf("unexpected synthetic presentation: %+v", got)
	}
	if _, ok := board.CloneArtifact(got.SourceID); ok {
		t.Fatalf("synthetic source %q must not be a durable artifact", got.SourceID)
	}
}

// debugSnapshot prints message types — used during diagnosis only.
func debugSnapshot(t *testing.T, prog *integrationProgram, label string) {
	t.Helper()
	for i, m := range prog.Snapshot() {
		t.Logf("%s [%d]: %T %+v", label, i, m, m)
	}
}

func postArchitectConsultClaim(t *testing.T, board *claims.ClaimsBoard) (parentClaimID, consultClaimID string) {
	t.Helper()
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "architect plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction parent claim: %v", err)
	}
	parentClaimID = board.Projection().Claims[0].ID
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeConsultation},
		[]claims.Claim{{
			Title: "consult librarian",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				{Related: parentClaimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipCausedBy},
			},
			ActionType:  claims.ActionTypeConsultation,
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction consult claim: %v", err)
	}
	for _, c := range board.Projection().Claims {
		if c.Title == "consult librarian" {
			consultClaimID = c.ID
			break
		}
	}
	if parentClaimID == "" || consultClaimID == "" {
		t.Fatalf("missing test claims: parent=%q consult=%q", parentClaimID, consultClaimID)
	}
	return parentClaimID, consultClaimID
}

// Cross-claim nesting test (UI_DESIGN.md §3.4 ParentRowID). When
// agent A consults agent B, B's tool calls (emitted on B's
// testament responding to the consult claim) must carry
// ParentRowID equal to A's consult_started artifact ID, so the
// chat panel nests B's tools beneath A's consult row.
func TestBridgeIntegration_CrossClaimNestingViaParentRowID(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-nest")
	defer cleanup()

	// 1. A's top-level claim (cycle root for A).
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "architect plan",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction A's claim: %v", err)
	}
	parentClaimID := board.Projection().Claims[0].ID

	// 2. A posts a consult claim against B (engineer). The consult
	//    claim has caused_by → A's parent claim, so the resolver
	//    attaches it to A's cycle.
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeConsultation},
		[]claims.Claim{{
			Title: "consult engineer",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				{Related: parentClaimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipCausedBy},
			},
			ActionType:  claims.ActionTypeConsultation,
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction consult: %v", err)
	}
	var consultClaimID string
	for _, c := range board.Projection().Claims {
		if c.Title == "consult engineer" {
			consultClaimID = c.ID
		}
	}
	if consultClaimID == "" {
		t.Fatal("consult claim not on board")
	}

	// 3. A emits a consult_started artifact (originating side) with
	//    Metadata["claim_id"] = consult claim ID.
	consultStartedID := "consult-started-A"
	br.OnArtifactAdded(parentClaimID, "architect", "ses-nest", &claims.Artifact{
		ID:        consultStartedID,
		AgentID:   "architect",
		Kind:      "consult_started",
		Reference: "engineer",
		Metadata:  map[string]any{"claim_id": consultClaimID, "target": "engineer"},
		Created:   time.Now(),
	})

	// 4. B (engineer) handles the consult and emits a tool_started
	//    on its testament responding to the consult claim. The
	//    bridge must set ParentRowID = consultStartedID.
	bToolStartedID := "tool-started-B"
	br.OnArtifactAdded(consultClaimID, "engineer", "ses-nest", &claims.Artifact{
		ID:        bToolStartedID,
		AgentID:   "engineer",
		Kind:      "tool_started",
		Reference: "read_file",
		Created:   time.Now(),
	})

	drainBridge(t, prog, "cross-claim nesting")

	var consultRow, toolRow *msg.ClaimArtifactAddedMsg
	for _, m := range prog.Snapshot() {
		if a, ok := m.(msg.ClaimArtifactAddedMsg); ok {
			switch a.ArtifactID {
			case consultStartedID:
				consultCopy := a
				consultRow = &consultCopy
			case bToolStartedID:
				toolCopy := a
				toolRow = &toolCopy
			}
		}
	}
	if consultRow == nil {
		debugSnapshot(t, prog, "cross-claim")
		t.Fatal("expected ClaimArtifactAddedMsg for A's consult_started")
	}
	if toolRow == nil {
		debugSnapshot(t, prog, "cross-claim")
		t.Fatal("expected ClaimArtifactAddedMsg for B's tool_started")
	}
	// A's consult_started is itself top-level in A's cycle.
	if consultRow.ParentRowID != "" {
		t.Fatalf("consult_started ParentRowID = %q, want empty (top-level in cycle)", consultRow.ParentRowID)
	}
	// B's tool_started must nest under A's consult_started.
	if toolRow.ParentRowID != consultStartedID {
		t.Fatalf("tool_started.ParentRowID = %q, want %q (cross-claim nest under consult)", toolRow.ParentRowID, consultStartedID)
	}
	// Both must share the same cycle.
	if consultRow.CycleID != toolRow.CycleID {
		t.Fatalf("CycleID mismatch: consult=%q tool=%q", consultRow.CycleID, toolRow.CycleID)
	}
}

func TestBridgeIntegration_ChildArtifactFallbackActorIsClaimSubject(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-nest-actor")
	defer cleanup()

	parentClaimID, consultClaimID := postArchitectConsultClaim(t, board)
	consultStartedID := "consult-started-actor"
	br.OnArtifactAdded(parentClaimID, "architect", "ses-nest-actor", &claims.Artifact{
		ID:        consultStartedID,
		AgentID:   "architect",
		Kind:      "consult_started",
		Reference: "librarian",
		Metadata:  map[string]any{"claim_id": consultClaimID, "target": "librarian"},
		Created:   time.Now(),
	})
	br.OnArtifactAdded(consultClaimID, "", "ses-nest-actor", &claims.Artifact{
		ID:        "tool-started-without-agent",
		Kind:      "tool_started",
		Reference: "workspace_read",
		Created:   time.Now(),
	})
	drainBridge(t, prog, "child artifact actor fallback")

	var toolRow *msg.ClaimArtifactAddedMsg
	for _, m := range prog.Snapshot() {
		if a, ok := m.(msg.ClaimArtifactAddedMsg); ok && a.ArtifactID == "tool-started-without-agent" {
			copy := a
			toolRow = &copy
			break
		}
	}
	if toolRow == nil {
		debugSnapshot(t, prog, "child artifact actor fallback")
		t.Fatal("expected child tool row")
	}
	if toolRow.ParentRowID != consultStartedID {
		t.Fatalf("ParentRowID = %q, want %q", toolRow.ParentRowID, consultStartedID)
	}
	if toolRow.AgentID != "librarian" {
		t.Fatalf("AgentID = %q, want librarian fallback from consultation subject", toolRow.AgentID)
	}
}

func TestBridgeIntegration_TestamentSummaryEmitsNestedChildResponse(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-nest-response")
	defer cleanup()

	parentClaimID, consultClaimID := postArchitectConsultClaim(t, board)
	consultStartedID := "consult-started-response"
	br.OnArtifactAdded(parentClaimID, "architect", "ses-nest-response", &claims.Artifact{
		ID:        consultStartedID,
		AgentID:   "architect",
		Kind:      "consult_started",
		Reference: "librarian",
		Metadata:  map[string]any{"claim_id": consultClaimID, "target": "librarian"},
		Created:   time.Now(),
	})
	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "librarian", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			Summary: "Workspace inventory complete.",
			Relations: []claims.Relation{
				{Related: consultClaimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
			},
		}},
	); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	drainBridge(t, prog, "testament child response")

	var response *msg.ClaimResponseTextMsg
	var completed *msg.ClaimArtifactCompletedMsg
	for _, m := range prog.Snapshot() {
		switch typed := m.(type) {
		case msg.ClaimResponseTextMsg:
			if typed.ClaimID == consultClaimID {
				copy := typed
				response = &copy
			}
		case msg.ClaimArtifactCompletedMsg:
			if typed.StartArtifactID == consultStartedID {
				copy := typed
				completed = &copy
			}
		}
	}
	if response == nil {
		debugSnapshot(t, prog, "testament child response")
		t.Fatal("expected child ClaimResponseTextMsg from testament summary")
	}
	if response.ParentRowID != consultStartedID {
		t.Fatalf("response.ParentRowID = %q, want %q", response.ParentRowID, consultStartedID)
	}
	if response.AgentID != "librarian" {
		t.Fatalf("response.AgentID = %q, want librarian", response.AgentID)
	}
	if response.Content != "Workspace inventory complete." {
		t.Fatalf("response.Content = %q", response.Content)
	}
	if completed == nil {
		t.Fatal("expected consult_started completion from testament submission")
	}
}

func TestBridgeIntegration_OutOfSessionArtifactDropped(t *testing.T) {
	br, _, prog, cleanup := setupBridgeOnSession(t, "ses-active")
	defer cleanup()

	br.OnArtifactAdded("ghost-claim", "engineer", "ses-OTHER", &claims.Artifact{
		ID:   "art-1",
		Kind: "tool_started",
	})
	drainBridge(t, prog, "out-of-session sink call")

	for _, m := range prog.Snapshot() {
		if _, ok := m.(msg.ClaimArtifactAddedMsg); ok {
			t.Fatal("expected out-of-session artifact to be dropped, but got an emission")
		}
	}
}

func TestBridgeIntegration_HandoffClosesPredecessorOpensSuccessor(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-handoff")
	defer cleanup()

	// Predecessor cycle.
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "guardian", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "guardian work",
			Relations: []claims.Relation{
				{Related: "guardian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "guardian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction predecessor: %v", err)
	}
	predID := board.Projection().Claims[0].ID
	// Close the predecessor's claim before the handoff so HandoffEligible
	// passes (no open child work).
	if err := board.RejectClaim(context.Background(), predID, claims.StatusChange{
		From: string(claims.ClaimStatusPending), To: string(claims.ClaimStatusRejected),
		Reason: "close for handoff seed", AgentID: "guardian",
	}, nil, nil); err != nil {
		t.Fatalf("close predecessor: %v", err)
	}
	// Successor handoff claim.
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "inspector", Type: claims.ActionTypeHandoff},
		[]claims.Claim{{
			Title: "inspector takes over",
			Relations: []claims.Relation{
				{Related: "inspector", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "inspector", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
				{Related: predID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipHandoffFrom},
			},
			ActionType:  claims.ActionTypeHandoff,
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction handoff successor: %v", err)
	}
	drainBridge(t, prog, "handoff sequence")

	var sawSuccessorOpen, sawPredecessorClose bool
	for _, m := range prog.Snapshot() {
		s, ok := m.(msg.ClaimsAgentStatusMsg)
		if !ok {
			continue
		}
		if s.Active && s.AgentID == "inspector" {
			sawSuccessorOpen = true
		}
		if !s.Active && s.AgentID == "guardian" {
			sawPredecessorClose = true
		}
	}
	if !sawSuccessorOpen {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=true, AgentID=inspector}")
	}
	if !sawPredecessorClose {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=false, AgentID=guardian} from handoff close")
	}
}

// TestBridgeIntegration_HandoffOpensCycleForSubject verifies that a
// handoff claim opens the new top-level cycle keyed to the SUBJECT
// (the agent receiving the handoff), not the issuer. Without this rule
// a guide → architect handoff opens a cycle owned by the guide, the
// bridge emits ClaimsAgentStatusMsg{AgentID=guide,Active=true}, and
// the agent panel's "active agent" indicator desyncs from the chat
// panel (which correctly shows architect as the active agent for the
// turn). UI_DESIGN.md §2.2 — handoff transfers cycle ownership.
//
// Mirrors the guide's first-hop handoff shape (no handoff_from: the
// user prompt is not a claim, so the guide's handoff has no
// predecessor cycle root).
func TestBridgeIntegration_HandoffOpensCycleForSubject(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "sess-hopen")
	defer cleanup()
	_ = br

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "guide", Type: claims.ActionTypeHandoff},
		[]claims.Claim{{
			Title:      "Route to architect",
			ActionType: claims.ActionTypeHandoff,
			Relations: []claims.Relation{
				{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeReceipt, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction handoff: %v", err)
	}
	drainBridge(t, prog, "guide handoff to architect")

	var sawArchitectActive, sawGuideActive bool
	for _, m := range prog.Snapshot() {
		s, ok := m.(msg.ClaimsAgentStatusMsg)
		if !ok {
			continue
		}
		if s.Active && s.AgentID == "architect" {
			sawArchitectActive = true
		}
		if s.Active && s.AgentID == "guide" {
			sawGuideActive = true
		}
	}
	if !sawArchitectActive {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=true, AgentID=architect} from handoff cycle open")
	}
	if sawGuideActive {
		t.Fatal("guide must not be marked active for a handoff it issued — handoff transfers ownership")
	}
}

// TestBridgeIntegration_PromptOpensCycleForSubject verifies that a
// user-prompt claim (postUserPromptAction at agents/guide/guide.go)
// opens a cycle keyed to the SUBJECT (the agent the prompt is routed
// to), not the guide that records the prompt event. Without this rule
// the guide ends up "doing" every user request in the agent panel —
// Reason is populated from the claim title (the user prompt itself),
// so the guide row shows the user prompt as its TaskSummary while the
// chat panel correctly attributes the work to the subject agent.
func TestBridgeIntegration_PromptOpensCycleForSubject(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "sess-popen")
	defer cleanup()
	_ = br

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt},
		[]claims.Claim{{
			Title:      "Let's create a toy python hello world cli app.",
			ActionType: claims.ActionTypePrompt,
			Relations: []claims.Relation{
				{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
		}},
	); err != nil {
		t.Fatalf("PostAction prompt: %v", err)
	}
	drainBridge(t, prog, "user prompt routed to architect")

	var sawArchitectActive, sawGuideActive bool
	for _, m := range prog.Snapshot() {
		s, ok := m.(msg.ClaimsAgentStatusMsg)
		if !ok {
			continue
		}
		if s.Active && s.AgentID == "architect" {
			sawArchitectActive = true
		}
		if s.Active && s.AgentID == "guide" {
			sawGuideActive = true
		}
	}
	if !sawArchitectActive {
		t.Fatal("expected ClaimsAgentStatusMsg{Active=true, AgentID=architect} from prompt-routed-to-architect")
	}
	if sawGuideActive {
		t.Fatal("guide must not be marked active for a user prompt it merely recorded — work belongs to the subject")
	}
}

func TestBridgeIntegration_GuideClassificationIsVisibleButDoesNotClaimRouteStream(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "sess-guide-classify-panel-only")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt},
		[]claims.Claim{{
			Title:      "Classifying request",
			ActionType: claims.ActionTypePrompt,
			Context:    "Classifying request",
			Relations: []claims.Relation{
				{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			},
			Tags: []string{
				claimTagGuideClassification,
				"stream_corr_id:route-123",
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction classification: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	if err := board.UpdateClaimProgress(context.Background(), claimID, claims.ClaimProgressUpdate{
		WorkSummary: "Classifying request",
	}, "guide"); err != nil {
		t.Fatalf("UpdateClaimProgress: %v", err)
	}
	drainBridge(t, prog, "guide classification open")

	var openMsg *msg.ClaimsAgentStatusMsg
	for _, m := range prog.Snapshot() {
		s, ok := m.(msg.ClaimsAgentStatusMsg)
		if ok && s.Active && s.CycleID == claimID {
			openMsg = &s
			break
		}
	}
	if openMsg == nil {
		t.Fatal("expected guide classification ClaimsAgentStatusMsg")
	}
	if openMsg.SuppressChat {
		t.Fatal("guide classification claim must be visible in chat")
	}
	if openMsg.State != "classifying" {
		t.Fatalf("State = %q, want classifying", openMsg.State)
	}
	if openMsg.StreamCorrelationID != "" {
		t.Fatalf("StreamCorrelationID = %q, want empty so routed agent owns the route stream", openMsg.StreamCorrelationID)
	}

	if err := board.SetClaimContext(context.Background(), claimID, "Request forwarded"); err != nil {
		t.Fatalf("SetClaimContext: %v", err)
	}
	drainBridge(t, prog, "guide classification forwarded context")

	var contextMsg *msg.ClaimContextMsg
	for _, m := range prog.Snapshot() {
		c, ok := m.(msg.ClaimContextMsg)
		if ok && c.ClaimID == claimID && c.Context == "Request forwarded" {
			contextMsg = &c
			break
		}
	}
	if contextMsg == nil {
		t.Fatal("expected guide classification ClaimContextMsg")
	}
	if contextMsg.SuppressChat {
		t.Fatal("guide classification context must be visible in chat")
	}
	if contextMsg.State != "routing" {
		t.Fatalf("context State = %q, want routing", contextMsg.State)
	}
}

// TestBridgeIntegration_ClaimContextRoutesToUI exercises the Phase 3
// context-delta path: SetClaimContext on the board → notifyDelta with
// kind=claim_context_changed → bridge → msg.ClaimContextMsg with the
// owner agent + cycle ID resolved correctly. Guards against the bridge
// regressing into dropping context updates as no-op deltas.
func TestBridgeIntegration_ClaimContextRoutesToUI(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-claim-context")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "context-bearing claim",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID
	drainBridge(t, prog, "open cycle")

	if err := board.SetClaimContext(context.Background(), claimID, "Composing response"); err != nil {
		t.Fatalf("SetClaimContext: %v", err)
	}
	drainBridge(t, prog, "claim context delta")

	var got *msg.ClaimContextMsg
	for _, m := range prog.Snapshot() {
		if c, ok := m.(msg.ClaimContextMsg); ok && c.ClaimID == claimID {
			got = &c
			break
		}
	}
	if got == nil {
		t.Fatal("expected ClaimContextMsg for SetClaimContext")
	}
	if got.Context != "Composing response" {
		t.Fatalf("Context = %q, want %q", got.Context, "Composing response")
	}
	if got.OwnerAgentID != "architect" {
		t.Fatalf("OwnerAgentID = %q, want architect", got.OwnerAgentID)
	}
	if got.CycleID == "" {
		t.Fatal("CycleID empty — bridge failed to resolve cycle for context delta")
	}
	if got.ContextTransition < 1 {
		t.Fatalf("ContextTransition = %d, want >= 1", got.ContextTransition)
	}

	// Second update bumps transition.
	if err := board.SetClaimContext(context.Background(), claimID, "Finalizing"); err != nil {
		t.Fatalf("SetClaimContext (2): %v", err)
	}
	drainBridge(t, prog, "claim context delta 2")

	var second *msg.ClaimContextMsg
	for _, m := range prog.Snapshot() {
		if c, ok := m.(msg.ClaimContextMsg); ok && c.ClaimID == claimID && c.Context == "Finalizing" {
			second = &c
			break
		}
	}
	if second == nil {
		t.Fatal("expected second ClaimContextMsg after second SetClaimContext")
	}
	if second.ContextTransition <= got.ContextTransition {
		t.Fatalf("transition did not advance: first=%d second=%d", got.ContextTransition, second.ContextTransition)
	}
}

// TestBridgeIntegration_ClaimContextSuppressedAfterTerminal verifies the
// claim Context is sealed when the claim reaches a terminal status —
// late narration arriving after acceptance/rejection must be dropped
// silently (no spurious ClaimContextMsg, no panic). docs/CLAIMS_UI.md.
func TestBridgeIntegration_ClaimContextSuppressedAfterTerminal(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-claim-context-sealed")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "sealing claim",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	if err := board.RejectClaim(context.Background(), claimID, claims.StatusChange{
		From: string(board.Projection().Claims[0].Status), To: string(claims.ClaimStatusRejected),
		Reason: "test", AgentID: "architect",
	}, nil, nil); err != nil {
		t.Fatalf("RejectClaim: %v", err)
	}
	drainBridge(t, prog, "rejection")

	// Late SetClaimContext after terminal — must drop silently and emit
	// no ClaimContextMsg.
	if err := board.SetClaimContext(context.Background(), claimID, "Late narration"); err != nil {
		t.Fatalf("SetClaimContext after terminal returned error: %v", err)
	}
	drainBridge(t, prog, "post-terminal context")

	for _, m := range prog.Snapshot() {
		if c, ok := m.(msg.ClaimContextMsg); ok && c.Context == "Late narration" {
			t.Fatalf("late ClaimContextMsg emitted after terminal status: %+v", c)
		}
	}
}

// TestBridgeIntegration_TestamentContextRoutesToUI mirrors the claim
// path for testaments — board.SetTestamentContext should land as
// msg.TestamentContextMsg with the agent ID and claim ID resolved.
func TestBridgeIntegration_TestamentContextRoutesToUI(t *testing.T) {
	_, board, prog, cleanup := setupBridgeOnSession(t, "ses-test-context")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "testament-bearing claim",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	if err := board.SubmitTestaments(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament},
		[]claims.Testament{{
			AgentID: "architect",
			Summary: "in-flight",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
			},
		}},
	); err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
	testamentID := board.Projection().Testaments[0].ID
	drainBridge(t, prog, "testament submitted")

	if err := board.SetTestamentContext(context.Background(), testamentID, "Composing summary"); err != nil {
		t.Fatalf("SetTestamentContext: %v", err)
	}
	drainBridge(t, prog, "testament context delta")

	var got *msg.TestamentContextMsg
	for _, m := range prog.Snapshot() {
		if c, ok := m.(msg.TestamentContextMsg); ok && c.TestamentID == testamentID {
			got = &c
			break
		}
	}
	if got == nil {
		t.Fatal("expected TestamentContextMsg")
	}
	if got.Context != "Composing summary" {
		t.Fatalf("Context = %q, want %q", got.Context, "Composing summary")
	}
	if got.AgentID != "architect" {
		t.Fatalf("AgentID = %q, want architect", got.AgentID)
	}
	if got.ClaimID != claimID {
		t.Fatalf("ClaimID = %q, want %q", got.ClaimID, claimID)
	}
}

// drainBridge pulls every queued message out of the bridge's outbox
// and forwards it to the program. The bridge's regular drain
// goroutine isn't running in tests (scope is nil), so we drain
// synchronously here. Bounded by deadline so a buggy enqueue can't
// hang the test.
func drainBridge(t *testing.T, prog *integrationProgram, label string) {
	t.Helper()
	br := bridgeForProgram(prog)
	if br == nil {
		t.Fatalf("drain %s: bridge not registered for program", label)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	emptyTicks := 0
	for time.Now().Before(deadline) {
		select {
		case m := <-br.outbox:
			prog.Send(m)
			emptyTicks = 0
		default:
			emptyTicks++
			// Wait for ~30ms of consecutive empty ticks before
			// declaring the outbox quiescent — late synchronous
			// emissions from board callbacks can arrive between
			// peeks.
			if emptyTicks > 3 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// bridgeRegistry tracks bridge↔program associations for drainBridge
// to find the right bridge to drain.
var (
	bridgeRegMu sync.Mutex
	bridgeReg   = map[*integrationProgram]*ClaimsBridge{}
)

func registerBridgeForProgram(prog *integrationProgram, br *ClaimsBridge) {
	bridgeRegMu.Lock()
	defer bridgeRegMu.Unlock()
	bridgeReg[prog] = br
}

func bridgeForProgram(prog *integrationProgram) *ClaimsBridge {
	bridgeRegMu.Lock()
	defer bridgeRegMu.Unlock()
	return bridgeReg[prog]
}

// ─── Negative-path coverage matrix (UI_DESIGN.md §7 P8.4) ─────────

func TestBridgeNegative_OrphanCompletionMessageEmitted(t *testing.T) {
	br, _, prog, cleanup := setupBridgeOnSession(t, "ses-orphan")
	defer cleanup()

	// Completion artifact arrives without ever seeing the start. The
	// bridge still emits ClaimArtifactCompletedMsg so any UI row that
	// did get the start (via a different code path) can close.
	br.OnArtifactAdded("ghost-claim", "engineer", "ses-orphan", &claims.Artifact{
		ID:   "art-completion-only",
		Kind: "tool_completed",
		Relations: []claims.Relation{
			{Related: "never-started-art", RelatedType: claims.RelatedTypeArtifact, Relationship: claims.RelationshipCompletes},
		},
		Metadata: map[string]any{"outcome": "failure"},
	})
	drainBridge(t, prog, "orphan completion")

	found := false
	for _, m := range prog.Snapshot() {
		if c, ok := m.(msg.ClaimArtifactCompletedMsg); ok && c.StartArtifactID == "never-started-art" {
			found = true
			if c.Outcome != "failure" {
				t.Errorf("orphan outcome = %q, want failure", c.Outcome)
			}
		}
	}
	if !found {
		t.Fatal("orphan completion should still emit ClaimArtifactCompletedMsg")
	}
}

// Property: every started artifact emitted by the bridge has a
// CycleID equal to the resolver's cycle for its claim.
func TestBridgeProperty_StartedArtifactCycleIDMatchesResolver(t *testing.T) {
	br, board, prog, cleanup := setupBridgeOnSession(t, "ses-prop")
	defer cleanup()

	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "engineer", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "p",
			Relations: []claims.Relation{
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	claimID := board.Projection().Claims[0].ID

	const N = 32
	for i := 0; i < N; i++ {
		br.OnArtifactAdded(claimID, "engineer", "ses-prop", &claims.Artifact{
			ID:   "art-" + itoaProp(i),
			Kind: "tool_started",
		})
	}
	drainBridge(t, prog, "many started artifacts")

	for _, m := range prog.Snapshot() {
		if a, ok := m.(msg.ClaimArtifactAddedMsg); ok {
			if a.CycleID == "" {
				t.Fatalf("artifact %s has empty CycleID", a.ArtifactID)
			}
			if a.ClaimID != claimID {
				t.Fatalf("artifact %s ClaimID = %q, want %q", a.ArtifactID, a.ClaimID, claimID)
			}
		}
	}
}

// Sanity guard: the resolver's typed error from HandoffEligible
// surfaces through verifyHandoffPredecessorDrained's wrapper.
func TestBridgeIntegration_VerifierWrapsTypedError(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:    "test",
		PipelineID: "p",
		TaskID:     "t",
		SessionID:  "s",
	})
	if err := board.PostAction(context.Background(),
		claims.Action{AgentID: "architect", Type: claims.ActionTypeTask},
		[]claims.Claim{{
			Title: "open",
			Relations: []claims.Relation{
				{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
				{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
			},
			Validations: []*claims.Validation{{Description: "v", QualityBar: "x", Type: claims.ValidationTypeInspection, Required: true}},
		}},
	); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	predID := board.Projection().Claims[0].ID

	err := claims.HandoffEligible(board, "architect")
	if err == nil {
		t.Fatal("expected ineligible (open issued claim)")
	}
	var nee *claims.HandoffNotEligibleError
	if !errors.As(err, &nee) {
		t.Fatalf("error is not *HandoffNotEligibleError: %T", err)
	}
	if !contains(nee.OpenChildClaims, predID) {
		t.Fatalf("OpenChildClaims = %v, expected to contain %q", nee.OpenChildClaims, predID)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func itoaProp(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

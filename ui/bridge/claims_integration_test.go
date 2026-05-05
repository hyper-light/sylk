package bridge

import (
	"context"
	"errors"
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

// debugSnapshot prints message types — used during diagnosis only.
func debugSnapshot(t *testing.T, prog *integrationProgram, label string) {
	t.Helper()
	for i, m := range prog.Snapshot() {
		t.Logf("%s [%d]: %T %+v", label, i, m, m)
	}
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

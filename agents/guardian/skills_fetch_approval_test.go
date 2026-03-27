package guardian

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/events"
)

func TestEvaluateCommandApproval_FetchExactPolicyUsesStoredExactAllowRule(t *testing.T) {
	g := newTestGuardian(t, &stubGuardianProvider{})
	store := commandapproval.NewRuleStore("")
	if err := store.Record(commandapproval.Rule{
		MatchKey:  "fetch:url:https://example.com/docs",
		Action:    commandapproval.RuleActionAllow,
		RuleLabel: "fetch https://example.com/docs",
		Summary:   "fetch https://example.com/docs",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record fetch allow rule: %v", err)
	}
	g.SetCommandApprovalStore(store)

	eval, err := g.evaluateCommandApproval(context.Background(), &commandapproval.Request{
		Command:        "https://example.com/docs",
		ToolName:       "web_fetch",
		Domain:         "example.com",
		ApprovalPolicy: commandapproval.ApprovalPolicyExact,
	})
	if err != nil {
		t.Fatalf("evaluateCommandApproval() error = %v", err)
	}
	if eval.Decision != commandapproval.DecisionAllow {
		t.Fatalf("decision = %s, want %s", eval.Decision, commandapproval.DecisionAllow)
	}
	if eval.Source != commandapproval.MatchSourceStoredAllow {
		t.Fatalf("source = %s, want %s", eval.Source, commandapproval.MatchSourceStoredAllow)
	}
	if eval.Analysis.PersistKey != "fetch:url:https://example.com/docs" {
		t.Fatalf("persist key = %q, want exact fetch URL rule", eval.Analysis.PersistKey)
	}
}

func TestEvaluateCommandApproval_FetchExactStoredAllowDoesNotEmitValidationOrProposal(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	g := newTestGuardian(t, &stubGuardianProvider{})
	store := commandapproval.NewRuleStore("")
	if err := store.Record(commandapproval.Rule{
		MatchKey:  "fetch:url:https://example.com/docs",
		Action:    commandapproval.RuleActionAllow,
		RuleLabel: "fetch https://example.com/docs",
		Summary:   "fetch https://example.com/docs",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record fetch allow rule: %v", err)
	}
	g.SetCommandApprovalStore(store)
	if err := g.Start(bus); err != nil {
		t.Fatalf("guardian start: %v", err)
	}
	defer func() {
		if err := g.Stop(); err != nil {
			t.Fatalf("guardian stop: %v", err)
		}
	}()

	proposalCh := make(chan *commandapproval.Proposal, 1)
	sub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if msg == nil || msg.Type != guide.MessageTypeProposal {
			return nil
		}
		proposal, ok := msg.Payload.(*commandapproval.Proposal)
		if !ok || proposal == nil {
			return nil
		}
		select {
		case proposalCh <- proposal:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe proposal topic: %v", err)
	}
	defer sub.Unsubscribe()

	eval, err := g.evaluateCommandApproval(context.Background(), &commandapproval.Request{
		Command:        "https://example.com/docs",
		ToolName:       "web_fetch",
		Domain:         "example.com",
		ApprovalPolicy: commandapproval.ApprovalPolicyExact,
		AgentType:      "academic",
		AgentID:        "academic",
		SessionID:      "session-1",
	})
	if err != nil {
		t.Fatalf("evaluateCommandApproval() error = %v", err)
	}
	if eval.Decision != commandapproval.DecisionAllow {
		t.Fatalf("decision = %s, want %s", eval.Decision, commandapproval.DecisionAllow)
	}
	if eval.Source != commandapproval.MatchSourceStoredAllow {
		t.Fatalf("source = %s, want %s", eval.Source, commandapproval.MatchSourceStoredAllow)
	}

	select {
	case proposal := <-proposalCh:
		t.Fatalf("unexpected fetch approval proposal: %#v", proposal)
	case <-time.After(150 * time.Millisecond):
	}

	collector, ok := g.activityPub.(*events.TestActivityCollector)
	if !ok {
		t.Fatalf("activity publisher = %T, want *events.TestActivityCollector", g.activityPub)
	}
	var sawSuccess bool
	for _, event := range collector.Events() {
		if event == nil {
			continue
		}
		if event.EventType == events.EventTypeAgentAction && event.Content == "Validating fetch approval request" {
			t.Fatal("unexpected validating activity for stored exact fetch allow")
		}
		if event.EventType == events.EventTypeSuccess && event.Content == "Fetch approval allowed" {
			sawSuccess = true
		}
	}
	if !sawSuccess {
		t.Fatal("expected success activity for stored exact fetch allow")
	}
}

func TestEvaluateCommandApproval_FetchExactPolicyIgnoresStoredDomainAllowRule(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	g := newTestGuardian(t, &stubGuardianProvider{})
	store := commandapproval.NewRuleStore("")
	if err := store.Record(commandapproval.Rule{
		MatchKey:  "fetch:domain:example.com",
		Action:    commandapproval.RuleActionAllow,
		RuleLabel: "fetch from example.com",
		Summary:   "fetch from example.com",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record fetch allow rule: %v", err)
	}
	g.SetCommandApprovalStore(store)
	if err := g.Start(bus); err != nil {
		t.Fatalf("guardian start: %v", err)
	}
	defer func() {
		if err := g.Stop(); err != nil {
			t.Fatalf("guardian stop: %v", err)
		}
	}()

	proposalCh := make(chan *commandapproval.Proposal, 1)
	sub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if msg == nil || msg.Type != guide.MessageTypeProposal {
			return nil
		}
		proposal, ok := msg.Payload.(*commandapproval.Proposal)
		if !ok || proposal == nil {
			return nil
		}
		select {
		case proposalCh <- proposal:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe proposal topic: %v", err)
	}
	defer sub.Unsubscribe()

	go func() {
		_, _ = g.evaluateCommandApproval(context.Background(), &commandapproval.Request{
			Command:        "https://example.com/spec",
			ToolName:       "web_fetch",
			Domain:         "example.com",
			ApprovalPolicy: commandapproval.ApprovalPolicyExact,
			AgentType:      "academic",
			AgentID:        "academic",
			SessionID:      "session-1",
		})
	}()

	select {
	case proposal := <-proposalCh:
		if proposal.Command != "https://example.com/spec" {
			t.Fatalf("proposal command = %q, want fetch URL", proposal.Command)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fetch approval proposal")
	}
}

func TestEvaluateCommandApproval_FetchAllowAlwaysPersistsExactURLRule(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	g := newTestGuardian(t, &stubGuardianProvider{})
	g.SetCommandApprovalStore(commandapproval.NewRuleStore(""))
	if err := g.Start(bus); err != nil {
		t.Fatalf("guardian start: %v", err)
	}
	defer func() {
		if err := g.Stop(); err != nil {
			t.Fatalf("guardian stop: %v", err)
		}
	}()

	proposalCh := make(chan *commandapproval.Proposal, 1)
	sub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if msg == nil || msg.Type != guide.MessageTypeProposal {
			return nil
		}
		proposal, ok := msg.Payload.(*commandapproval.Proposal)
		if !ok || proposal == nil {
			return nil
		}
		select {
		case proposalCh <- proposal:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe proposal topic: %v", err)
	}
	defer sub.Unsubscribe()

	type result struct {
		eval commandapproval.Evaluation
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		eval, err := g.evaluateCommandApproval(context.Background(), &commandapproval.Request{
			Command:        "https://example.com/spec",
			ToolName:       "web_fetch",
			Domain:         "example.com",
			Justification:  "confirm the published specification",
			AgentType:      "academic",
			AgentID:        "academic",
			SessionID:      "session-1",
			ApprovalPolicy: commandapproval.ApprovalPolicyExact,
		})
		resultCh <- result{eval: eval, err: err}
	}()

	var proposal *commandapproval.Proposal
	select {
	case proposal = <-proposalCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fetch approval proposal")
	}
	if !proposal.IsFetchApproval() {
		t.Fatalf("expected fetch approval proposal, got %#v", proposal)
	}
	if proposal.Domain != "example.com" {
		t.Fatalf("proposal domain = %q, want example.com", proposal.Domain)
	}
	if proposal.Command != "https://example.com/spec" {
		t.Fatalf("proposal command = %q, want fetch URL", proposal.Command)
	}

	if err := bus.Publish(guide.TopicResponses("guardian", g.id), &guide.Message{
		ID:            "approval-response",
		CorrelationID: proposal.CorrelationID,
		Type:          guide.MessageTypeResponse,
		SourceAgentID: "tui",
		Payload: map[string]any{
			"approved": true,
			"decision": "allow_always",
			"reason":   "approved by user",
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("evaluateCommandApproval() error = %v", res.err)
		}
		if res.eval.Decision != commandapproval.DecisionAllow {
			t.Fatalf("decision = %s, want %s", res.eval.Decision, commandapproval.DecisionAllow)
		}
		if res.eval.UserDecision != "allow_always" {
			t.Fatalf("user decision = %q, want allow_always", res.eval.UserDecision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fetch approval result")
	}

	rule, ok := g.commandRules.Lookup("fetch:url:https://example.com/spec")
	if !ok {
		t.Fatal("expected persisted fetch approval rule")
	}
	if rule.Action != commandapproval.RuleActionAllow {
		t.Fatalf("rule action = %s, want %s", rule.Action, commandapproval.RuleActionAllow)
	}
}

func TestEvaluateCommandApproval_FetchInteractiveActivitiesCarryRequestCorrelation(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	g := newTestGuardian(t, &stubGuardianProvider{})
	g.SetCommandApprovalStore(commandapproval.NewRuleStore(""))
	if err := g.Start(bus); err != nil {
		t.Fatalf("guardian start: %v", err)
	}
	defer func() {
		if err := g.Stop(); err != nil {
			t.Fatalf("guardian stop: %v", err)
		}
	}()

	proposalCh := make(chan *commandapproval.Proposal, 1)
	sub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if msg == nil || msg.Type != guide.MessageTypeProposal {
			return nil
		}
		proposal, ok := msg.Payload.(*commandapproval.Proposal)
		if !ok || proposal == nil {
			return nil
		}
		select {
		case proposalCh <- proposal:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe proposal topic: %v", err)
	}
	defer sub.Unsubscribe()

	type result struct {
		eval commandapproval.Evaluation
		err  error
	}
	resultCh := make(chan result, 1)
	const corrID = "corr_guardian_fetch_interactive"
	ctx := agentshared.WithLogMeta(context.Background(), agentshared.LogMeta{
		CorrID:    corrID,
		AgentID:   "guardian",
		SessionID: "session-1",
	})
	go func() {
		eval, err := g.evaluateCommandApproval(ctx, &commandapproval.Request{
			Command:        "https://example.com/spec",
			ToolName:       "web_fetch",
			Domain:         "example.com",
			Justification:  "confirm the published specification",
			AgentType:      "academic",
			AgentID:        "academic",
			SessionID:      "session-1",
			ApprovalPolicy: commandapproval.ApprovalPolicyExact,
		})
		resultCh <- result{eval: eval, err: err}
	}()

	var proposal *commandapproval.Proposal
	select {
	case proposal = <-proposalCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fetch approval proposal")
	}
	if proposal == nil {
		t.Fatal("expected fetch approval proposal")
	}

	if err := bus.Publish(guide.TopicResponses("guardian", g.id), &guide.Message{
		ID:            "approval-response-correlation",
		CorrelationID: proposal.CorrelationID,
		Type:          guide.MessageTypeResponse,
		SourceAgentID: "tui",
		Payload: map[string]any{
			"approved": true,
			"decision": "allow_once",
			"reason":   "approved by user",
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("evaluateCommandApproval() error = %v", res.err)
		}
		if res.eval.Decision != commandapproval.DecisionAllow {
			t.Fatalf("decision = %s, want %s", res.eval.Decision, commandapproval.DecisionAllow)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fetch approval result")
	}

	collector, ok := g.activityPub.(*events.TestActivityCollector)
	if !ok {
		t.Fatalf("activity publisher = %T, want *events.TestActivityCollector", g.activityPub)
	}
	var sawValidating, sawAllowed bool
	for _, event := range collector.Events() {
		if event == nil {
			continue
		}
		switch event.Content {
		case "Validating fetch approval request":
			sawValidating = true
			if event.CorrelationID != corrID {
				t.Fatalf("validating activity correlation = %q, want %q", event.CorrelationID, corrID)
			}
		case "Fetch approval allowed":
			sawAllowed = true
			if event.CorrelationID != corrID {
				t.Fatalf("success activity correlation = %q, want %q", event.CorrelationID, corrID)
			}
		}
	}
	if !sawValidating {
		t.Fatal("expected validating fetch approval activity")
	}
	if !sawAllowed {
		t.Fatal("expected allowed fetch approval activity")
	}
}

func TestEvaluateCommandApproval_FetchPublishesProposalBeforeHoldCompletes(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	g := newTestGuardian(t, &stubGuardianProvider{})
	g.SetCommandApprovalStore(commandapproval.NewRuleStore(""))
	if err := g.Start(bus); err != nil {
		t.Fatalf("guardian start: %v", err)
	}
	defer func() {
		if err := g.Stop(); err != nil {
			t.Fatalf("guardian stop: %v", err)
		}
	}()

	proposalCh := make(chan *commandapproval.Proposal, 1)
	proposalSub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		if msg == nil || msg.Type != guide.MessageTypeProposal {
			return nil
		}
		proposal, ok := msg.Payload.(*commandapproval.Proposal)
		if !ok || proposal == nil {
			return nil
		}
		select {
		case proposalCh <- proposal:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe proposal topic: %v", err)
	}
	defer proposalSub.Unsubscribe()

	holdSeen := make(chan struct{}, 1)
	holdSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.TargetAgentID != "orchestrator" {
			return nil
		}
		if kind, _ := req.Metadata["control_plane_kind"].(string); kind != agentshared.ControlPlaneKindCommandApprovalHold {
			return nil
		}
		select {
		case holdSeen <- struct{}{}:
		default:
		}
		go func(correlationID string) {
			time.Sleep(300 * time.Millisecond)
			_ = bus.Publish(guide.TopicResponses("guardian", g.id), guide.NewResponseMessage("", &guide.RouteResponse{
				CorrelationID:       correlationID,
				Success:             true,
				RespondingAgentID:   "orchestrator",
				RespondingAgentName: "orchestrator",
				Data: &agentshared.CommandApprovalHoldResult{
					Accepted:  true,
					HoldID:    "approval_hold_1",
					SessionID: "session-1",
					DAGID:     "dag-1",
					State:     "active",
					UpdatedAt: time.Now().UTC(),
				},
			}))
		}(req.CorrelationID)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe hold requests: %v", err)
	}
	defer holdSub.Unsubscribe()

	type result struct {
		eval commandapproval.Evaluation
		err  error
	}
	resultCh := make(chan result, 1)
	start := time.Now()
	go func() {
		eval, err := g.evaluateCommandApproval(context.Background(), &commandapproval.Request{
			Command:       "https://example.com/spec",
			ToolName:      "web_fetch",
			Domain:        "example.com",
			Justification: "confirm the published specification",
			AgentType:     "academic",
			AgentID:       "academic",
			SessionID:     "session-1",
			DAGID:         "dag-1",
			TaskID:        "task-1",
			PipelineID:    "task-1",
		})
		resultCh <- result{eval: eval, err: err}
	}()

	select {
	case <-holdSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hold request")
	}

	var proposal *commandapproval.Proposal
	select {
	case proposal = <-proposalCh:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("timed out waiting for fetch approval proposal before hold completion")
	}
	if proposal == nil {
		t.Fatal("expected fetch approval proposal")
	}
	if elapsed := time.Since(start); elapsed >= 250*time.Millisecond {
		t.Fatalf("proposal published after %v, want before delayed hold completion", elapsed)
	}

	if err := bus.Publish(guide.TopicResponses("guardian", g.id), &guide.Message{
		ID:            "approval-response",
		CorrelationID: proposal.CorrelationID,
		Type:          guide.MessageTypeResponse,
		SourceAgentID: "tui",
		Payload: map[string]any{
			"approved": true,
			"decision": "allow_once",
			"reason":   "approved by user",
		},
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("publish approval response: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("evaluateCommandApproval() error = %v", res.err)
		}
		if res.eval.Decision != commandapproval.DecisionAllow {
			t.Fatalf("decision = %s, want %s", res.eval.Decision, commandapproval.DecisionAllow)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fetch approval result")
	}
}

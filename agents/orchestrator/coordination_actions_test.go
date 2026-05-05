package orchestrator

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

type coordinationActionTestBus struct {
	topics []string
	msgs   []*guide.Message
}

func (b *coordinationActionTestBus) Publish(topic string, msg *guide.Message) error {
	b.topics = append(b.topics, topic)
	b.msgs = append(b.msgs, msg)
	return nil
}

func (b *coordinationActionTestBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *coordinationActionTestBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *coordinationActionTestBus) Close() error                         { return nil }
func (b *coordinationActionTestBus) CloseWithContext(_ context.Context) error { return nil }

func TestPublishCoordinationSuccess_PublishesToSourceAgentResponseTopic(t *testing.T) {
	bus := &coordinationActionTestBus{}
	o := &Orchestrator{
		bus:         bus,
		knownAgents: map[string]*guide.AgentAnnouncement{},
		config:      Config{AgentID: "orchestrator"},
	}
	req := &guide.ActionRequest{
		CorrelationID:   "corr-1",
		SourceAgentID:   "worker-1234",
		SourceAgentName: "inspector-pipeline",
	}

	if err := o.publishCoordinationSuccess(req, map[string]any{"ok": true}); err != nil {
		t.Fatalf("publishCoordinationSuccess() error = %v", err)
	}

	wantTopic := guide.TopicResponses("inspector-pipeline", "worker-1234")
	if len(bus.topics) != 1 || bus.topics[0] != wantTopic {
		t.Fatalf("published topics = %v, want [%q]", bus.topics, wantTopic)
	}
	if len(bus.msgs) != 1 || bus.msgs[0] == nil {
		t.Fatal("expected response message to be published")
	}
}

func TestHandleCoordinationAction_RequestReviewSkipsRedundantPipelineWake(t *testing.T) {
	ctx := context.Background()
	svc := newCoordinationTestService(t)
	bus := &coordinationActionTestBus{}
	o := &Orchestrator{
		bus:          bus,
		coordination: svc,
		knownAgents:  map[string]*guide.AgentAnnouncement{},
		config:       Config{AgentID: "orchestrator", SessionID: "sess-1"},
	}

	artifact, err := svc.PublishArtifact(ctx, coordination.Actor{AgentID: "tester-1", AgentType: "tester-pipeline"}, coordination.PublishArtifactInput{
		TaskID:    "task-7",
		TaskName:  "CLI failure follow-up",
		Kind:      "verification_result",
		Summary:   "CLI test failure details",
		ScopeKind: coordination.ScopeKindTestSurface,
		ScopeKey:  "tests/test_cli.py",
		Payload: map[string]any{
			"file": "src/hello_cli/cli.py",
		},
		Evidence: []coordination.EvidenceRef{{Kind: "file", Value: "src/hello_cli/cli.py"}},
	})
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}

	handled, err := o.handleCoordinationAction(ctx, &guide.ActionRequest{
		CorrelationID:   "coord-1",
		SourceAgentID:   "tester-task-7",
		SourceAgentName: "tester-pipeline",
		Action:          coordination.ActionRequestReview,
		Data: map[string]any{
			"task_id":       "task-7",
			"artifact_id":   artifact.ID,
			"reviewer_type": "engineer",
			"summary":       "Address the failing CLI test",
		},
	})
	if err != nil {
		t.Fatalf("handleCoordinationAction() error = %v", err)
	}
	if !handled {
		t.Fatal("expected action to be handled")
	}
	if len(bus.msgs) != 1 {
		t.Fatalf("published messages = %d, want 1 (response only)", len(bus.msgs))
	}

	response, ok := bus.msgs[0].GetRouteResponse()
	if !ok || response == nil || !response.Success {
		t.Fatalf("expected successful coordination response, got %#v", bus.msgs[0])
	}
}

func TestHandleCoordinationAction_RequestReviewPublishesGlobalReviewerWakeActivity(t *testing.T) {
	ctx := context.Background()
	svc := newCoordinationTestService(t)
	bus := &coordinationActionTestBus{}
	pub := &trackingActivityPub{}
	o := &Orchestrator{
		bus:          bus,
		coordination: svc,
		activityPub:  pub,
		knownAgents:  map[string]*guide.AgentAnnouncement{},
		config:       Config{AgentID: "orchestrator", SessionID: "sess-1"},
	}

	artifact, err := svc.PublishArtifact(ctx, coordination.Actor{AgentID: "eng-1", AgentType: "engineer"}, coordination.PublishArtifactInput{
		TaskID:    "task-11",
		TaskName:  "Global review follow-up",
		Kind:      "implementation_summary",
		Summary:   "Ready for global inspector audit",
		ScopeKind: coordination.ScopeKindImplementation,
		ScopeKey:  "src/service.go",
		Payload: map[string]any{
			"file": "src/service.go",
		},
		Evidence: []coordination.EvidenceRef{{Kind: "file", Value: "src/service.go"}},
	})
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}

	handled, err := o.handleCoordinationAction(ctx, &guide.ActionRequest{
		CorrelationID:   "coord-2",
		SourceAgentID:   "engineer-task-11",
		SourceAgentName: "engineer",
		Action:          coordination.ActionRequestReview,
		Data: map[string]any{
			"task_id":       "task-11",
			"artifact_id":   artifact.ID,
			"reviewer_type": "inspector",
			"summary":       "Audit the merged implementation",
		},
	})
	if err != nil {
		t.Fatalf("handleCoordinationAction() error = %v", err)
	}
	if !handled {
		t.Fatal("expected action to be handled")
	}

	collected := pub.collected()
	if len(collected) != 1 {
		t.Fatalf("published %d activity events, want 1", len(collected))
	}
	if collected[0].AgentID != "inspector" {
		t.Fatalf("activity agent_id = %q, want inspector", collected[0].AgentID)
	}
	if collected[0].Data["source"] != "coordination_review_wakeup" {
		t.Fatalf("activity source = %v, want coordination_review_wakeup", collected[0].Data["source"])
	}
}

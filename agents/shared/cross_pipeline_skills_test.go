package shared

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/skills"
)

// findSkill returns the first skill in skills with the given name.
func findSkill(t *testing.T, ss []*skills.Skill, name string) *skills.Skill {
	t.Helper()
	for _, s := range ss {
		if s != nil && s.Name == name {
			return s
		}
	}
	t.Fatalf("skill %q not found", name)
	return nil
}

func TestConsultPeerSkill_NilRouteSyncReturnsConsultID(t *testing.T) {
	// Fire-and-forget fallback: when no RouteSync transport is wired,
	// the skill emits the consult_emitted activity and returns the
	// consult_id + in_flight status without blocking on a response.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-1" },
		RouteSync:  nil,
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Any prior art?"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("handler result type = %T", result)
	}
	if got["status"] != "in_flight" {
		t.Fatalf("status = %v, want in_flight", got["status"])
	}
	if _, ok := got["consult_id"].(string); !ok {
		t.Fatalf("consult_id missing or wrong type: %#v", got)
	}
}

func TestConsultPeerSkill_RouteSyncSuccessReturnsResponse(t *testing.T) {
	// When RouteSync returns a successful RouteResponse, the handler
	// surfaces the payload as the consult response and emits completed
	// status. The target's stream events are expected to stitch as
	// children upstream via the branch metadata the handler stamps on
	// the outgoing RouteRequest — we verify the request shape here,
	// since the stitching is observed in UI integration tests.
	var capturedReq *guide.RouteRequest
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
		RouteSync: func(_ context.Context, req *guide.RouteRequest) (*guide.Message, error) {
			capturedReq = req
			return guide.NewResponseMessage("resp-1", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       true,
				Data:          map[string]any{"user_message": "Found two related modules."},
			}), nil
		},
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Any prior art?"}`)
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("handler result type = %T", result)
	}
	if got["status"] != "completed" {
		t.Fatalf("status = %v, want completed", got["status"])
	}
	resp, ok := got["response"].(map[string]any)
	if !ok {
		t.Fatalf("response missing or wrong type: %#v", got)
	}
	if resp["user_message"] != "Found two related modules." {
		t.Fatalf("response.user_message = %v", resp["user_message"])
	}
	if capturedReq == nil {
		t.Fatal("route sync never called")
	}
	if capturedReq.TargetAgentID != "librarian" {
		t.Fatalf("target agent = %q", capturedReq.TargetAgentID)
	}
	if capturedReq.Input != "Any prior art?" {
		t.Fatalf("input = %q", capturedReq.Input)
	}
	if !capturedReq.ExplicitTarget {
		t.Fatal("expected ExplicitTarget=true")
	}
}

func TestConsultPeerSkill_RouteSyncCrossPipelineMetadata(t *testing.T) {
	// Cross-pipeline consults carry target_pipeline_id; the handler
	// stamps it into request metadata so the routing layer can resolve
	// a specific pipeline's peer rather than the nearest same-pipeline
	// or knowledge peer.
	var capturedReq *guide.RouteRequest
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-origin" },
		RouteSync: func(_ context.Context, req *guide.RouteRequest) (*guide.Message, error) {
			capturedReq = req
			return guide.NewResponseMessage("resp-1", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       true,
				Data:          map[string]any{"note": "ok"},
			}), nil
		},
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	// Cross-pipeline consult: engineer in pipe-origin asking the
	// tester-pipeline in pipe-42 about shared fixture state. Per the
	// authority matrix (see docs/COMMS_MATRIX.md), engineer may
	// consult tester-pipeline and holds AllowsCrossPipelineConsult.
	input := json.RawMessage(`{"target_agent_type":"tester-pipeline","target_pipeline_id":"pipe-42","query":"How are you handling retries?"}`)
	if _, err := skill.Handler(context.Background(), input); err != nil {
		t.Fatalf("handler err = %v", err)
	}
	if capturedReq == nil {
		t.Fatal("route sync never called")
	}
	if got, _ := capturedReq.Metadata["target_pipeline_id"].(string); got != "pipe-42" {
		t.Fatalf("metadata.target_pipeline_id = %v", capturedReq.Metadata["target_pipeline_id"])
	}
}

func TestConsultPeerSkill_RouteSyncFailureReturnsError(t *testing.T) {
	// A failed RouteResponse.Success=false is propagated as a handler
	// error so the tool-loop's Phase 1 event marks the consult_peer row
	// as Failed. This avoids the silent-success case where a peer
	// rejected the consult but the UI still showed a green checkmark.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
		RouteSync: func(_ context.Context, req *guide.RouteRequest) (*guide.Message, error) {
			return guide.NewResponseMessage("resp-1", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       false,
				Error:         "peer refused consultation: out of scope",
			}), nil
		},
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Q"}`)
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected handler error on failed route response")
	}
	if !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("error does not mention peer reason: %v", err)
	}
}

func TestConsultPeerSkill_RouteSyncTransportErrorReturnsError(t *testing.T) {
	// Transport-level failures (bus unavailable, timeout, cancellation)
	// propagate as handler errors so the tool row renders as Failed.
	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "" },
		RouteSync: func(_ context.Context, _ *guide.RouteRequest) (*guide.Message, error) {
			return nil, errors.New("bus unavailable")
		},
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"Q"}`)
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected handler error on transport failure")
	}
	if !strings.Contains(err.Error(), "bus unavailable") {
		t.Fatalf("error does not mention transport failure: %v", err)
	}
}

func TestConsultPeerSkill_EmitsConsultEmittedActivity(t *testing.T) {
	// Audit-trail invariant: the consult_emitted activity is written
	// regardless of which mode (sync vs. fire-and-forget) the caller
	// is in, so post-hoc causal_trace and ambient recall still work.
	collector := &activity.TestCollector{}
	prev := activity.SetDefaultSource(nil)           // sink-only, no source
	prevSink := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSource(prev)
	defer activity.SetDefaultSink(prevSink)

	cfg := CrossPipelineSkillConfig{
		SessionID:  func() string { return "sess-1" },
		AgentID:    func() string { return "agent-1" },
		AgentType:  func() string { return "engineer" },
		PipelineID: func() string { return "pipe-1" },
		RouteSync:  nil, // fire-and-forget exercises just the activity write
	}
	skill := findSkill(t, CrossPipelineSkills(cfg), "consult_peer")

	input := json.RawMessage(`{"target_agent_type":"librarian","query":"X","scope":"svc/auth/"}`)
	if _, err := skill.Handler(context.Background(), input); err != nil {
		t.Fatalf("handler err = %v", err)
	}

	acts := collector.Snapshot()
	if len(acts) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(acts))
	}
	act := acts[0]
	if act.Action != activity.ActionConsultEmitted {
		t.Fatalf("action = %q", act.Action)
	}
	if act.Actor.AgentID != "agent-1" || act.Actor.AgentType != "engineer" || act.Actor.PipelineID != "pipe-1" {
		t.Fatalf("actor = %#v", act.Actor)
	}
	if act.Subject.TargetAgent != "librarian" {
		t.Fatalf("subject.TargetAgent = %q", act.Subject.TargetAgent)
	}
	if act.Subject.PathPrefix != "svc/auth/" {
		t.Fatalf("subject.PathPrefix = %q", act.Subject.PathPrefix)
	}
}

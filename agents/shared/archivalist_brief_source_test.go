package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestArchivalistBriefSource_RequestBriefUsesGuideRouteSync(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	source := NewArchivalistBriefSource(bus)

	reqCh := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}

		payload := `{"task_summary":"summarized","key_decisions":"decisions","active_state":"active","next_steps":"next","blockers":"none"}`
		resp := guide.NewResponseMessage("brief-resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			Data:              payload,
			RespondingAgentID: "archivalist",
		})
		return bus.Publish(guide.TopicResponses("brief-requester", "brief-requester"), resp)
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	directCh := make(chan *guide.ForwardedRequest, 1)
	directSub, err := bus.SubscribeAsync(guide.TopicRequests("archivalist", "archivalist"), func(msg *guide.Message) error {
		fwd, ok := msg.GetForwardedRequest()
		if !ok || fwd == nil {
			return nil
		}
		select {
		case directCh <- fwd:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe archivalist requests: %v", err)
	}
	defer directSub.Unsubscribe()

	ctx, cancel := context.WithTimeout(versioning.WithSessionID(context.Background(), "sess-brief"), 2*time.Second)
	defer cancel()

	brief, err := source.RequestBrief(ctx, "engineer", 5000, 3)
	if err != nil {
		t.Fatalf("RequestBrief returned error: %v", err)
	}
	if brief == nil {
		t.Fatal("expected brief")
	}
	if brief.TaskSummary != "summarized" {
		t.Fatalf("task_summary = %q, want summarized", brief.TaskSummary)
	}

	select {
	case req := <-reqCh:
		if req.SourceAgentID != "brief-requester" {
			t.Fatalf("source_agent_id = %q, want brief-requester", req.SourceAgentID)
		}
		if req.TargetAgentID != "archivalist" {
			t.Fatalf("target_agent_id = %q, want archivalist", req.TargetAgentID)
		}
		if !req.ExplicitTarget {
			t.Fatal("expected explicit archivalist target")
		}
		if req.SessionID != "sess-brief" {
			t.Fatalf("session_id = %q, want sess-brief", req.SessionID)
		}
		if req.Input != `archivalist_get_briefing {"tier":"standard"}` {
			t.Fatalf("input = %q, want canonical briefing request", req.Input)
		}
		if got := req.Metadata["tool_name"]; got != "archivalist_get_briefing" {
			t.Fatalf("tool_name = %#v, want archivalist_get_briefing", got)
		}
		if got := req.Metadata["brief_format"]; got != "context_brief" {
			t.Fatalf("brief_format = %#v, want context_brief", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for guide route request")
	}

	select {
	case fwd := <-directCh:
		t.Fatalf("unexpected direct archivalist forwarded request: %+v", fwd)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestArchivalistBriefSource_ParseBriefResponseFallback(t *testing.T) {
	source := NewArchivalistBriefSource(nil)
	msg := guide.NewResponseMessage("brief-fallback", &guide.RouteResponse{
		CorrelationID:     "corr-brief",
		Success:           true,
		Data:              "plain summary",
		RespondingAgentID: "archivalist",
	})

	brief := source.parseBriefResponse(msg)
	if brief == nil {
		t.Fatal("expected brief")
	}
	if brief.TaskSummary != "plain summary" {
		t.Fatalf("task_summary = %q, want plain summary", brief.TaskSummary)
	}
	if brief.GeneratedAt.IsZero() {
		t.Fatal("expected GeneratedAt to be set")
	}
}

func TestArchivalistBriefSource_ParseBriefResponseTypedBrief(t *testing.T) {
	source := NewArchivalistBriefSource(nil)
	expected := &handoff.ContextBrief{
		TaskSummary: "typed summary",
		GeneratedAt: time.Now(),
		ContextSize: 2048,
		TurnNumber:  4,
	}
	msg := guide.NewResponseMessage("brief-typed", &guide.RouteResponse{
		CorrelationID:     "corr-typed",
		Success:           true,
		Data:              expected,
		RespondingAgentID: "archivalist",
	})

	brief := source.parseBriefResponse(msg)
	if brief != expected {
		t.Fatalf("brief = %#v, want original typed brief", brief)
	}
}

var _ handoff.BriefSource = (*ArchivalistBriefSource)(nil)

package guide

import (
	"strings"
	"testing"
	"time"

	corecontext "github.com/adalundhe/sylk/core/context"
)

func TestConsultationObserver_LogToArchivalistPublishesGuideRouteRequest(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	observer := NewConsultationObserver(bus, "sess-1")

	reqCh := make(chan *RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(TopicGuideRequests, func(msg *Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	fwdCh := make(chan *ForwardedRequest, 1)
	fwdSub, err := bus.SubscribeAsync(TopicRequests("archivalist", "archivalist"), func(msg *Message) error {
		fwd, ok := msg.GetForwardedRequest()
		if !ok || fwd == nil {
			return nil
		}
		select {
		case fwdCh <- fwd:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe archivalist requests: %v", err)
	}
	defer fwdSub.Unsubscribe()

	observer.logToArchivalist(&Message{
		Type:          MessageTypeDirectConsultation,
		CorrelationID: "consult-root",
		Metadata: map[string]any{
			"chat_nested_branch":    true,
			"chat_inter_agent_kind": "consult",
		},
		Payload: corecontext.ConsultationRequest{
			FromAgent: "architect",
			ToAgent:   "librarian",
			SessionID: "sess-1",
			Query:     "what patterns exist?",
		},
	})

	select {
	case req := <-reqCh:
		if req.SourceAgentID != "architect" {
			t.Fatalf("source_agent_id = %q, want architect", req.SourceAgentID)
		}
		if req.ParentCorrelationID != "consult-root" {
			t.Fatalf("parent_correlation_id = %q, want consult-root", req.ParentCorrelationID)
		}
		if !strings.HasPrefix(req.Input, "@archivalist:store:history") {
			t.Fatalf("input = %q, want archivalist store DSL", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for guide route request")
	}

	select {
	case fwd := <-fwdCh:
		t.Fatalf("unexpected direct archivalist forwarded request: %+v", fwd)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestGuidePersistCorrectionToArchivalistPublishesGuideRouteRequest(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g := &Guide{
		bus:       bus,
		running:   true,
		agentID:   "guide",
		sessionID: "sess-1",
	}

	reqCh := make(chan *RouteRequest, 1)
	sub, err := bus.SubscribeAsync(TopicGuideRequests, func(msg *Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case reqCh <- req:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	g.persistCorrectionToArchivalist(CorrectionRecord{
		Input:         "old request",
		WrongIntent:   IntentHelp,
		CorrectIntent: IntentRecall,
		CorrectDomain: DomainHistory,
		CorrectTarget: TargetArchivalist,
		CorrectedAt:   time.Now(),
	})

	select {
	case req := <-reqCh:
		if !req.FireAndForget {
			t.Fatal("expected fire-and-forget correction persistence route")
		}
		if !strings.HasPrefix(req.Input, "@archivalist:store:history") {
			t.Fatalf("input = %q, want archivalist store DSL", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for correction archivalist route")
	}
}

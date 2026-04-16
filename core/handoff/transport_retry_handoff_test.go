package handoff

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

type recordingScribeFeedSink struct {
	feeds []scribeFeedRecord
}

type scribeFeedRecord struct {
	agentType     string
	userRequest   string
	agentResponse string
	correlationID string
}

func (r *recordingScribeFeedSink) FeedScribe(parentAgentType, userRequest, agentResponse, parentCorrelationID string) {
	r.feeds = append(r.feeds, scribeFeedRecord{
		agentType:     parentAgentType,
		userRequest:   userRequest,
		agentResponse: agentResponse,
		correlationID: parentCorrelationID,
	})
}

type fakeTransportRetryBridge struct {
	forceCalls int
	reason     string
	record     TurnRecord
	messages   []Message
}

func (f *fakeTransportRetryBridge) PreserveInFlightWork(rec TurnRecord, messages ...Message) {
	f.record = rec
	f.messages = append([]Message(nil), messages...)
}

func (f *fakeTransportRetryBridge) ForceHandoff(_ context.Context, reason string) error {
	f.forceCalls++
	f.reason = reason
	return nil
}

func TestWithTransportRetryHandoff_PreservesWorkAndForcesHandoff(t *testing.T) {
	bridge := &fakeTransportRetryBridge{}
	sink := &recordingScribeFeedSink{}
	ctx := WithTransportRetryHandoff(context.Background(), TransportRetryHandoffConfig{
		Enabled:       true,
		Bridge:        bridge,
		AgentID:       "engineer-1",
		AgentType:     "engineer",
		UserRequest:   "Implement retry recovery for OpenAI transport failures.",
		CorrelationID: "corr-transport",
		SessionID:     "sess-1",
		Scribe:        sink,
	})

	observer := providers.RetryExhaustedObserverFromContext(ctx)
	if observer == nil {
		t.Fatal("expected retry exhausted observer")
	}
	observer(providers.RetryExhaustedEvent{
		RetriesUsed:   2,
		TotalAttempts: 3,
		Err:           &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")},
		Transport:     true,
	})

	if bridge.forceCalls != 1 {
		t.Fatalf("force handoff calls = %d, want 1", bridge.forceCalls)
	}
	if !strings.Contains(bridge.reason, "connection reset by peer") {
		t.Fatalf("handoff reason missing transport error: %q", bridge.reason)
	}
	if bridge.record.CorrelationID != "corr-transport" {
		t.Fatalf("handoff correlation = %q, want corr-transport", bridge.record.CorrelationID)
	}
	if bridge.record.SessionID != "sess-1" {
		t.Fatalf("handoff session = %q, want sess-1", bridge.record.SessionID)
	}
	if len(bridge.messages) != 2 {
		t.Fatalf("preserved messages = %d, want 2", len(bridge.messages))
	}
	if !strings.Contains(bridge.messages[0].Content, "Implement retry recovery for OpenAI transport failures.") {
		t.Fatalf("first preserved message missing request summary: %q", bridge.messages[0].Content)
	}
	if !strings.Contains(bridge.messages[1].Content, "connection reset by peer") {
		t.Fatalf("second preserved message missing transport error: %q", bridge.messages[1].Content)
	}

	if len(sink.feeds) != 1 {
		t.Fatalf("scribe feeds = %d, want 1", len(sink.feeds))
	}
	if sink.feeds[0].agentType != "engineer" {
		t.Fatalf("scribe agent type = %q, want engineer", sink.feeds[0].agentType)
	}
	if sink.feeds[0].correlationID != "corr-transport" {
		t.Fatalf("scribe correlation = %q, want corr-transport", sink.feeds[0].correlationID)
	}
	if !strings.Contains(sink.feeds[0].agentResponse, "connection reset by peer") {
		t.Fatalf("scribe response missing transport error: %q", sink.feeds[0].agentResponse)
	}

	observer(providers.RetryExhaustedEvent{
		RetriesUsed:   3,
		TotalAttempts: 4,
		Err:           &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")},
		Transport:     true,
	})
	if bridge.forceCalls != 1 {
		t.Fatalf("force handoff calls after duplicate event = %d, want 1", bridge.forceCalls)
	}
}

func TestWithTransportRetryHandoff_IgnoresNonTransportExhaustion(t *testing.T) {
	bridge := &fakeTransportRetryBridge{}
	sink := &recordingScribeFeedSink{}
	ctx := WithTransportRetryHandoff(context.Background(), TransportRetryHandoffConfig{
		Enabled:       true,
		Bridge:        bridge,
		AgentID:       "engineer-1",
		AgentType:     "engineer",
		UserRequest:   "Ignore non-transport exhaustion.",
		CorrelationID: "corr-non-transport",
		Scribe:        sink,
	})

	observer := providers.RetryExhaustedObserverFromContext(ctx)
	if observer == nil {
		t.Fatal("expected retry exhausted observer")
	}
	observer(providers.RetryExhaustedEvent{
		RetriesUsed:   3,
		TotalAttempts: 4,
		Err:           errors.New("rate limited"),
		Transport:     false,
	})

	if len(sink.feeds) != 0 {
		t.Fatalf("scribe feeds = %d, want 0", len(sink.feeds))
	}
	if bridge.forceCalls != 0 {
		t.Fatalf("force handoff calls = %d, want 0", bridge.forceCalls)
	}
	if len(bridge.messages) != 0 {
		t.Fatalf("preserved messages = %d, want 0", len(bridge.messages))
	}
}

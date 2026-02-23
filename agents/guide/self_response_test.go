package guide

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

type stubGuideResponder struct {
	reply string
	err   error
	calls int
}

func (r *stubGuideResponder) Respond(_ context.Context, _ GuideSelfResponseRequest) (string, error) {
	r.calls++
	if r.err != nil {
		return "", r.err
	}
	return r.reply, nil
}

type stubStreamGuideResponder struct {
	reply       string
	chunks      []string
	err         error
	responds    int
	streamCalls int
}

func (r *stubStreamGuideResponder) Respond(_ context.Context, _ GuideSelfResponseRequest) (string, error) {
	r.responds++
	if r.err != nil {
		return "", r.err
	}
	return r.reply, nil
}

func (r *stubStreamGuideResponder) RespondStream(
	_ context.Context,
	_ GuideSelfResponseRequest,
	onChunk func(string) error,
) (string, *providers.Usage, error) {
	r.streamCalls++
	if r.err != nil {
		return "", nil, r.err
	}
	for _, chunk := range r.chunks {
		if onChunk != nil {
			if err := onChunk(chunk); err != nil {
				return "", nil, err
			}
		}
	}
	return r.reply, nil, nil
}

func TestStaticGuideResponder_Status(t *testing.T) {
	responder := NewStaticGuideResponder()
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input:              "status please",
		PendingRequests:    3,
		RegisteredAgentIDs: []string{"guide", "architect"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Guide is running. Pending requests: 3. Registered agents: 2."
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestStaticGuideResponder_StatusIncludesActiveConversation(t *testing.T) {
	responder := NewStaticGuideResponder()
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input:                   "status please",
		PendingRequests:         1,
		RegisteredAgentIDs:      []string{"guide", "architect"},
		ActiveConversationAgent: "architect",
		ActiveConversationTurns: 2,
		ActiveConversationAge:   12,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Guide is running. Pending requests: 1. Registered agents: 2. Active conversation is with architect (2 turns, updated 12s ago)."
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestStaticGuideResponder_AgentsSorted(t *testing.T) {
	responder := NewStaticGuideResponder()
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input:              "agents",
		RegisteredAgentIDs: []string{"tester", " architect", "guide"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Registered agents (3): architect, guide, tester"
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestStaticGuideResponder_AgentCount(t *testing.T) {
	responder := NewStaticGuideResponder()
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input:              "how many agents are registered?",
		RegisteredAgentIDs: []string{"guide", "architect", "tester"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "There are 3 registered agents right now."
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestStaticGuideResponder_Greeting(t *testing.T) {
	responder := NewStaticGuideResponder()
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input: "Hello there!",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Hi. I can help with general questions and route work across Sylk agents."
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestStaticGuideResponder_GreetingWordBoundary(t *testing.T) {
	responder := NewStaticGuideResponder()
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input: "This keeps failing after OAuth login",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply == guideChatReply {
		t.Fatalf("reply matched greeting fallback unexpectedly: %q", reply)
	}
}

func TestIsGuideGreetingQuery_StandaloneTokensOnly(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "hi", input: "hi there", want: true},
		{name: "hello", input: "hello!", want: true},
		{name: "good morning", input: "good morning team", want: true},
		{name: "this", input: "this is not a greeting", want: false},
		{name: "which", input: "which agent should I use", want: false},
		{name: "high", input: "high availability design", want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := isGuideGreetingQuery(normalizeGuideQuery(tt.input))
			if got != tt.want {
				t.Fatalf("isGuideGreetingQuery(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFallbackGuideResponder_UsesPrimary(t *testing.T) {
	responder := NewFallbackGuideResponder(
		&stubGuideResponder{reply: "primary"},
		&stubGuideResponder{reply: "fallback"},
	)
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "primary" {
		t.Fatalf("reply = %q, want %q", reply, "primary")
	}
}

func TestFallbackGuideResponder_UsesFallbackOnPrimaryError(t *testing.T) {
	responder := NewFallbackGuideResponder(
		&stubGuideResponder{err: errors.New("primary failed")},
		&stubGuideResponder{reply: "fallback"},
	)
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "fallback" {
		t.Fatalf("reply = %q, want %q", reply, "fallback")
	}
}

func TestFallbackGuideResponder_SkipsFallbackForNonMetaInput(t *testing.T) {
	responder := NewFallbackGuideResponder(
		&stubGuideResponder{err: errors.New("primary failed")},
		&stubGuideResponder{reply: "fallback"},
	)
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input: "plan an oauth architecture",
	})
	if err == nil {
		t.Fatal("expected primary error when fallback is skipped for non-meta input")
	}
	if !strings.Contains(err.Error(), "primary failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "" {
		t.Fatalf("reply = %q, want empty", reply)
	}
}

func TestRespondGuideSelf_UsesStreamingResponder(t *testing.T) {
	responder := &stubStreamGuideResponder{
		reply:  "hello world",
		chunks: []string{"hello ", "world"},
	}
	var chunks []string
	reply, _, err := RespondGuideSelf(context.Background(), responder, GuideSelfResponseRequest{}, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "hello world" {
		t.Fatalf("reply = %q, want %q", reply, "hello world")
	}
	if responder.streamCalls != 1 {
		t.Fatalf("expected one stream call, got %d", responder.streamCalls)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
}

func TestRespondGuideSelf_FallsBackToRespond(t *testing.T) {
	responder := &stubGuideResponder{reply: "fallback response"}
	var chunks []string
	reply, _, err := RespondGuideSelf(context.Background(), responder, GuideSelfResponseRequest{}, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "fallback response" {
		t.Fatalf("reply = %q, want %q", reply, "fallback response")
	}
	if len(chunks) != 1 || chunks[0] != "fallback response" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

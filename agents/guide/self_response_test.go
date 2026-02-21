package guide

import (
	"context"
	"errors"
	"testing"
)

type stubGuideResponder struct {
	reply string
	err   error
}

func (r stubGuideResponder) Respond(_ context.Context, _ GuideSelfResponseRequest) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.reply, nil
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

func TestStaticGuideResponder_AgentsSorted(t *testing.T) {
	responder := NewStaticGuideResponder()
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{
		Input:              "agents",
		RegisteredAgentIDs: []string{"tester", " architect", "guide"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Registered agents: architect, guide, tester"
	if reply != want {
		t.Fatalf("reply = %q, want %q", reply, want)
	}
}

func TestFallbackGuideResponder_UsesPrimary(t *testing.T) {
	responder := NewFallbackGuideResponder(
		stubGuideResponder{reply: "primary"},
		stubGuideResponder{reply: "fallback"},
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
		stubGuideResponder{err: errors.New("primary failed")},
		stubGuideResponder{reply: "fallback"},
	)
	reply, err := responder.Respond(context.Background(), GuideSelfResponseRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "fallback" {
		t.Fatalf("reply = %q, want %q", reply, "fallback")
	}
}

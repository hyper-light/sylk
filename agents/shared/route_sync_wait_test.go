package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestPendingSyncActivityCorrelations_IncludesNestedParent(t *testing.T) {
	msg := &guide.Message{
		CorrelationID: "child-corr",
		Type:          guide.MessageTypeStream,
		Payload: &guide.StreamResponse{
			CorrelationID: "child-corr",
			Metadata: map[string]any{
				streamMetadataNestedBranch:      true,
				streamMetadataParentCorrelation: "parent-corr",
				streamMetadataParentToolCallKey: "consult-1",
				streamMetadataInterAgentKind:    InterAgentToolEventKindConsult,
			},
			Event: &guide.StreamEvent{Type: guide.StreamEventProgress},
		},
	}

	got := PendingSyncActivityCorrelations(msg)
	if len(got) != 2 {
		t.Fatalf("PendingSyncActivityCorrelations() len = %d, want 2", len(got))
	}
	if got[0] != "child-corr" || got[1] != "parent-corr" {
		t.Fatalf("PendingSyncActivityCorrelations() = %#v, want child and parent correlations", got)
	}
}

func TestWaitForPendingSyncResponse_UsesInactivityTimeout(t *testing.T) {
	wait := NewPendingSyncWait()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := WaitForPendingSyncResponse(ctx, `consultation to "academic"`, 40*time.Millisecond, wait)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	wait.Activity <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	wait.Activity <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	wait.Response <- &guide.Message{Type: guide.MessageTypeResponse, CorrelationID: "corr-1"}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForPendingSyncResponse() error = %v, want nil", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for response")
	}
}

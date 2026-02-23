package architect

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestHandleCancelAction_CancelsInFlightCorrelation(t *testing.T) {
	a := &Architect{inFlight: make(map[string]context.CancelFunc)}

	ctx, cancel := context.WithCancel(context.Background())
	a.registerInFlight("corr-1", cancel)

	err := a.handleCancelAction(&guide.ActionRequest{
		CorrelationID: "corr-1",
		Action:        "cancel",
		FireAndForget: true,
	})
	if err != nil {
		t.Fatalf("handleCancelAction returned error: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected in-flight request context to be canceled")
	}
}

func TestHandleCancelAction_UsesDataCorrelationID(t *testing.T) {
	a := &Architect{inFlight: make(map[string]context.CancelFunc)}

	ctx, cancel := context.WithCancel(context.Background())
	a.registerInFlight("corr-data", cancel)

	err := a.handleCancelAction(&guide.ActionRequest{
		CorrelationID: "action-corr",
		Action:        "cancel",
		FireAndForget: true,
		Data: map[string]any{
			"correlation_id": "corr-data",
		},
	})
	if err != nil {
		t.Fatalf("handleCancelAction returned error: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected in-flight request context to be canceled")
	}
}

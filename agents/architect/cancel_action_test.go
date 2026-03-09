package architect

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
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

func TestHandleCancelAction_CancelsSession(t *testing.T) {
	a := &Architect{
		inFlight:  make(map[string]context.CancelFunc),
		steering:  shared.NewSteeringManager(),
		planStore: NewPlanStore(t.TempDir(), NewPlanLeaseManager(time.Second, time.Second), slog.Default()),
	}
	defer a.planStore.Close()

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	a.steering.RegisterCancel("corr-1", "session-1", cancel1)
	a.steering.RegisterCancel("corr-2", "session-1", cancel2)

	err := a.handleCancelAction(&guide.ActionRequest{
		Action:        "cancel",
		FireAndForget: true,
		Data: map[string]any{
			"session_id": "session-1",
		},
	})
	if err != nil {
		t.Fatalf("handleCancelAction returned error: %v", err)
	}

	select {
	case <-ctx1.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first session request context to be canceled")
	}
	select {
	case <-ctx2.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected second session request context to be canceled")
	}
}

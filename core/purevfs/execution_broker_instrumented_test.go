package purevfs

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
)

type fakeBroker struct {
	caps   ExecutionCapabilities
	result *BrokerRunResult
	err    error
	calls  int
}

func (b *fakeBroker) Capabilities(_ context.Context) (ExecutionCapabilities, error) {
	return b.caps, nil
}

func (b *fakeBroker) Run(_ context.Context, _ BrokerRunRequest) (*BrokerRunResult, error) {
	b.calls++
	return b.result, b.err
}

func TestInstrumentBroker_NilWrapsToNil(t *testing.T) {
	if got := InstrumentBroker(nil); got != nil {
		t.Fatalf("InstrumentBroker(nil) = %v, want nil", got)
	}
}

func TestInstrumentBroker_IdempotentDoubleWrap(t *testing.T) {
	inner := &fakeBroker{}
	a := InstrumentBroker(inner)
	b := InstrumentBroker(a)
	if a != b {
		t.Fatal("InstrumentBroker double-wrap must be idempotent")
	}
}

func TestUnderlyingBroker_StripsWrapper(t *testing.T) {
	inner := &fakeBroker{}
	wrapped := InstrumentBroker(inner)
	if got := UnderlyingBroker(wrapped); got != ExecutionBroker(inner) {
		t.Fatalf("UnderlyingBroker did not unwrap; got %T", got)
	}
}

func TestInstrumentBroker_EmitsCommandExecutedOnRun(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	inner := &fakeBroker{result: &BrokerRunResult{ExitCode: 0, Stdout: []byte("ok")}}
	wrapped := InstrumentBroker(inner)

	ctx := activity.EnsureFabricContext(context.Background())
	_, err := wrapped.Run(ctx, BrokerRunRequest{
		Plan: ExecutionPlan{Language: "go", FrameworkID: "go-test"},
		Argv: []string{"go", "test", "./..."},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if col.CountByKind(activity.ActionCommandExecuted) == 0 {
		t.Fatal("Run through wrapper must emit ActionCommandExecuted")
	}
}

func TestInstrumentBroker_TagsErrorOnRunFailure(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	inner := &fakeBroker{err: errors.New("broker boom")}
	wrapped := InstrumentBroker(inner)

	ctx := activity.EnsureFabricContext(context.Background())
	_, err := wrapped.Run(ctx, BrokerRunRequest{
		Plan: ExecutionPlan{Language: "go"},
		Argv: []string{"true"},
	})
	if err == nil {
		t.Fatal("expected Run to surface the broker error")
	}

	emitted := col.FilterByKind(activity.ActionCommandExecuted)
	if len(emitted) == 0 {
		t.Fatal("failed Run must still emit an activity")
	}
	last := emitted[len(emitted)-1]
	if string(last.Payload) == "" {
		t.Fatal("error path must populate payload with error metadata")
	}
}

// TestInstrumentBroker_NoEmissionsFromCapabilities documents the design
// choice that capability probes are NOT instrumented — they happen
// frequently and don't carry causal weight; spanning them would dilute
// the broker activity stream. If a future change starts spanning
// Capabilities, this test fails loudly so the trade-off is reconsidered
// rather than silently changed.
func TestInstrumentBroker_NoEmissionsFromCapabilities(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	inner := &fakeBroker{}
	wrapped := InstrumentBroker(inner)

	ctx := activity.EnsureFabricContext(context.Background())
	if _, err := wrapped.Capabilities(ctx); err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if got := col.Count(); got != 0 {
		t.Fatalf("Capabilities must NOT emit an activity by design; got %d", got)
	}
}

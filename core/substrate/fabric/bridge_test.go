package fabric

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

func TestNewActivityBridge_Defaults(t *testing.T) {
	b := NewActivityBridge(BridgeConfig{})
	if b.agentType != "substrate" {
		t.Errorf("default agentType = %q, want substrate", b.agentType)
	}
	if b.agentIDResolver() != "substrate" {
		t.Errorf("default agentID = %q, want substrate", b.agentIDResolver())
	}
}

func TestActivityBridge_Translate_TimestampDefault(t *testing.T) {
	b := NewActivityBridge(BridgeConfig{})
	act := b.translate(context.Background(), Activity{Kind: ToolPinned})
	if act.Timestamp.IsZero() {
		t.Error("translated activity has zero timestamp; should default to now")
	}
}

func TestActivityBridge_Translate_PreservesProvidedTimestamp(t *testing.T) {
	b := NewActivityBridge(BridgeConfig{})
	stamp := time.Date(2026, 4, 19, 16, 23, 18, 0, time.UTC)
	act := b.translate(context.Background(), Activity{Kind: ToolPinned, Timestamp: stamp})
	if !act.Timestamp.Equal(stamp) {
		t.Errorf("translated timestamp = %v, want %v", act.Timestamp, stamp)
	}
}

func TestActivityBridge_Translate_SubjectScope(t *testing.T) {
	b := NewActivityBridge(BridgeConfig{})
	act := b.translate(context.Background(), Activity{
		Kind:  ToolPinned,
		Scope: "tooling/python/pytest",
	})
	if act.Subject.PathPrefix != "tooling/python/pytest" {
		t.Errorf("Subject.PathPrefix = %q", act.Subject.PathPrefix)
	}
	if act.Subject.Domain != "substrate" {
		t.Errorf("Subject.Domain = %q, want substrate", act.Subject.Domain)
	}
}

func TestActivityBridge_Translate_ActorAgentType(t *testing.T) {
	b := NewActivityBridge(BridgeConfig{AgentType: "substrate-test"})
	act := b.translate(context.Background(), Activity{Kind: ToolPinned})
	if act.Actor.AgentType != "substrate-test" {
		t.Errorf("AgentType = %q", act.Actor.AgentType)
	}
}

func TestActivityBridge_Translate_PayloadMarshaled(t *testing.T) {
	b := NewActivityBridge(BridgeConfig{})
	act := b.translate(context.Background(), Activity{
		Kind: ToolPinned,
		Payload: map[string]any{
			"coordinate": "python/pytest",
			"version":    "8.3.2",
		},
	})
	var decoded map[string]any
	if err := json.Unmarshal(act.Payload, &decoded); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if decoded["coordinate"] != "python/pytest" {
		t.Errorf("decoded coordinate = %v", decoded["coordinate"])
	}
}

func TestMapToActionKind_PinningKindsBecomeDecisions(t *testing.T) {
	pinning := []ActivityKind{
		ToolPinned, ToolUpgraded, ToolWitnessAdded,
		ToolAttached, ToolDetached, DiskFallbackUsed,
	}
	for _, k := range pinning {
		got := mapToActionKind(k)
		if got != activity.ActionDecisionDeclared {
			t.Errorf("mapToActionKind(%s) = %s, want decision_declared", k, got)
		}
	}
}

func TestMapToActionKind_FailureKindsBecomeErrors(t *testing.T) {
	failures := []ActivityKind{
		ProvisioningFailed, BuildFailed, SupplyChainViolation,
	}
	for _, k := range failures {
		got := mapToActionKind(k)
		if got != activity.ActionErrorEmitted {
			t.Errorf("mapToActionKind(%s) = %s, want error_emitted", k, got)
		}
	}
}

func TestMapToActionKind_AdvisoryKindsBecomeConsulted(t *testing.T) {
	advisory := []ActivityKind{ProvisioningStarted, BuildStarted, SubstrateEvictionPressure}
	for _, k := range advisory {
		got := mapToActionKind(k)
		if got != activity.ActionConsulted {
			t.Errorf("mapToActionKind(%s) = %s, want consulted", k, got)
		}
	}
}

func TestActivityBridge_Emit_RoutesThroughActivityAppend(t *testing.T) {
	collector := activity.NewTestCollector()
	prev := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSink(prev)

	bridge := NewActivityBridge(BridgeConfig{
		AgentType: "substrate-test",
		SessionIDResolver: func(_ context.Context, _ string) string { return "test-session" },
	})

	bridge.Emit(Activity{
		Kind:  ToolPinned,
		Scope: "tooling/python/pytest",
		Payload: map[string]any{"version": "8.3.2"},
	})

	got := collector.Snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 activity routed through Append, got %d", len(got))
	}
	if got[0].Action != activity.ActionDecisionDeclared {
		t.Errorf("Action = %s, want decision_declared", got[0].Action)
	}
	if string(got[0].SessionID) != "test-session" {
		t.Errorf("SessionID = %q, want test-session", got[0].SessionID)
	}
	if got[0].Subject.PathPrefix != "tooling/python/pytest" {
		t.Errorf("PathPrefix = %q", got[0].Subject.PathPrefix)
	}
}

func TestActivityBridge_AgentIDResolverDynamic(t *testing.T) {
	collector := activity.NewTestCollector()
	prev := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSink(prev)

	bridge := NewActivityBridge(BridgeConfig{
		AgentType:         "substrate",
		AgentIDResolver:   func() string { return "substrate-instance-7" },
		SessionIDResolver: func(_ context.Context, _ string) string { return "s" },
	})
	bridge.Emit(Activity{Kind: ToolPinned})

	got := collector.Snapshot()
	if got[0].Actor.AgentID != "substrate-instance-7" {
		t.Errorf("Actor.AgentID = %q", got[0].Actor.AgentID)
	}
}

func TestActivityBridge_PayloadMarshalFailureFallsBack(t *testing.T) {
	collector := activity.NewTestCollector()
	prev := activity.SetDefaultSink(collector)
	defer activity.SetDefaultSink(prev)

	bridge := NewActivityBridge(BridgeConfig{
		SessionIDResolver: func(_ context.Context, _ string) string { return "s" },
	})
	// chan values can't be marshaled to JSON.
	bridge.Emit(Activity{Kind: ToolPinned, Payload: make(chan int)})

	got := collector.Snapshot()
	if len(got) != 1 {
		t.Fatal("emit dropped activity on payload marshal failure")
	}
	if !json.Valid(got[0].Payload) {
		t.Errorf("fallback payload is not valid JSON: %s", got[0].Payload)
	}
}

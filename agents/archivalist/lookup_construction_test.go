package archivalist

import (
	"context"
	"errors"
	"testing"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// TestNewSynthesizer_PanicsWithoutProviderLookup pins the strict
// construction discipline: a synthesizer constructed without a
// ProviderLookup is a half-constructed object that advertises RAG
// capability but cannot synthesize. Strictly refusing construction makes
// the misconfiguration visible at startup rather than at first tool call.
// See agents/shared/PROVIDER_LOOKUP.md for the rule.
func TestNewSynthesizer_PanicsWithoutProviderLookup(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when ProviderLookup is nil")
		}
	}()
	_ = NewSynthesizer(SynthesizerConfig{ProviderLookup: nil})
}

// TestNewClient_PanicsWithoutProviderLookup pins the strict construction
// discipline for the archivalist's summary client.
func TestNewClient_PanicsWithoutProviderLookup(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when ProviderLookup is nil")
		}
	}()
	_ = NewClient(ClientConfig{
		ProviderLookup: nil,
		ModelLookup:    func() string { return "test" },
	})
}

// TestNewClient_PanicsWithoutModelLookup completes the constructor's
// required-dependency contract.
func TestNewClient_PanicsWithoutModelLookup(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when ModelLookup is nil")
		}
	}()
	_ = NewClient(ClientConfig{
		ProviderLookup: func() archivalistProvider { return nil },
		ModelLookup:    nil,
	})
}

// TestSynthesizer_LookupReturnsErrAgentNotReadyWhenProviderNil pins the
// transient-vs-permanent error discipline: a synthesizer whose
// ProviderLookup returns nil at call time MUST surface ErrAgentNotReady
// (transient, retry-after-readiness) rather than a generic error string
// that callers cannot distinguish from a permanent misconfiguration.
// This is the bug class that produced the historical "synthesis: no LLM
// provider configured" report.
func TestSynthesizer_LookupReturnsErrAgentNotReadyWhenProviderNil(t *testing.T) {
	s := NewSynthesizer(SynthesizerConfig{
		ProviderLookup: func() archivalistProvider { return nil },
	})
	_, err := s.synthesize(context.Background(), "any prompt")
	if err == nil {
		t.Fatal("expected error when provider lookup returns nil")
	}
	if !errors.Is(err, agentshared.ErrAgentNotReady) {
		t.Fatalf("error = %v, want it to wrap ErrAgentNotReady (so callers can distinguish transient startup state from permanent misconfiguration)", err)
	}
}

// TestSynthesizer_LookupObservesLiveProviderAcrossSwap is the canonical
// regression test for the synthesizer-snapshot bug. The lookup is called
// per-synthesize, so a swap on the underlying provider is observed without
// any explicit re-binding. Pre-refactor, the synthesizer captured the
// provider as a value at construction and never refreshed.
func TestSynthesizer_LookupObservesLiveProviderAcrossSwap(t *testing.T) {
	var live archivalistProvider
	s := NewSynthesizer(SynthesizerConfig{
		ProviderLookup: func() archivalistProvider { return live },
	})

	// Before swap: lookup returns nil → ErrAgentNotReady.
	if _, err := s.synthesize(context.Background(), "warmup"); !errors.Is(err, agentshared.ErrAgentNotReady) {
		t.Fatalf("pre-swap error = %v, want ErrAgentNotReady", err)
	}

	// Simulate post-init wiring (the post-auth SetProvider path).
	live = stubArchivalistProvider{}

	// After swap: same synthesizer instance, different provider — the
	// lookup observes the live value without the synthesizer being
	// rebuilt. This is the property the snapshot pattern broke.
	resp, err := s.synthesize(context.Background(), "real call")
	if err != nil {
		t.Fatalf("post-swap synthesize error = %v, want success", err)
	}
	if resp == nil {
		t.Fatal("expected response from stub provider")
	}
}

type stubArchivalistProvider struct{}

func (stubArchivalistProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{Content: "stub"}, nil
}

// TestArchivalist_ReadyReportsProviderState pins the readiness contract:
// the archivalist's Ready() method returns (false, reason) when the
// provider is not yet wired and (true, "") once it is. The dispatcher
// uses this signal to refuse routing pre-auth instead of letting the
// call reach the deepest tool layer.
func TestArchivalist_ReadyReportsProviderState(t *testing.T) {
	a := &Archivalist{}
	ready, reason := a.Ready()
	if ready {
		t.Fatal("expected Ready=false when provider is nil")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason when not ready")
	}

	a.provider = stubArchivalistProvider{}
	ready, reason = a.Ready()
	if !ready {
		t.Fatal("expected Ready=true once provider is wired")
	}
	if reason != "" {
		t.Fatalf("expected empty reason when ready, got %q", reason)
	}
}

// TestReadyOrNotReady_AbsentReporterTreatedReady confirms the sentinel
// behavior of the dispatcher gate: agents that don't implement
// ReadinessReporter are treated as always-ready (matching pre-refactor
// behavior so legacy agents aren't accidentally locked out).
func TestReadyOrNotReady_AbsentReporterTreatedReady(t *testing.T) {
	type plainAgent struct{}
	ready, reason := agentshared.ReadyOrNotReady(plainAgent{})
	if !ready {
		t.Fatal("agents without ReadinessReporter must default to ready")
	}
	if reason != "" {
		t.Fatalf("expected empty reason for default-ready agents, got %q", reason)
	}
}

// TestReadyOrNotReady_LiveAgentReportsState exercises the ReadyOrNotReady
// helper against a real Archivalist (which implements ReadinessReporter).
// Pin both states: nil provider → not ready; live provider → ready.
func TestReadyOrNotReady_LiveAgentReportsState(t *testing.T) {
	a := &Archivalist{}
	if ready, _ := agentshared.ReadyOrNotReady(a); ready {
		t.Fatal("Archivalist with nil provider must report not ready via the dispatcher helper")
	}
	a.provider = stubArchivalistProvider{}
	if ready, _ := agentshared.ReadyOrNotReady(a); !ready {
		t.Fatal("Archivalist with live provider must report ready via the dispatcher helper")
	}
}

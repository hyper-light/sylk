package quality

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
)

// capturingWAL implements WALWriter and records every append for
// inspection by tests. Thread-safe for concurrent observers.
type capturingWAL struct {
	mu      sync.Mutex
	entries []capturedEntry
}

type capturedEntry struct {
	Type    concurrency.EntryType
	Payload []byte
}

func (w *capturingWAL) Append(entryType concurrency.EntryType, payload []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, capturedEntry{Type: entryType, Payload: payload})
	return uint64(len(w.entries)), nil
}

func (w *capturingWAL) snapshot() []capturedEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]capturedEntry, len(w.entries))
	copy(out, w.entries)
	return out
}

// TestWALSink_WritesObservationToWAL is the SCORE-04 core invariant: every
// Observe call produces exactly one EntryWeightObservation write.
func TestWALSink_WritesObservationToWAL(t *testing.T) {
	wal := &capturingWAL{}
	sink := NewWALSink(wal)

	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)
	m.SetObservationHook(sink.ObservationHook())

	m.Observe(SignalObservation{Kind: SignalStaleness, Success: true, EffectiveWeight: 1}, DomainCode)
	m.Observe(SignalObservation{Kind: SignalBleve, Success: false, EffectiveWeight: 2}, DomainCode)

	got := wal.snapshot()
	if len(got) != 2 {
		t.Fatalf("WAL entries = %d, want 2", len(got))
	}
	for i, entry := range got {
		if entry.Type != concurrency.EntryWeightObservation {
			t.Errorf("entry %d type = %v, want EntryWeightObservation", i, entry.Type)
		}
		var payload WeightObservationPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Errorf("entry %d decode: %v", i, err)
		}
		if payload.SessionID != "session-1" {
			t.Errorf("entry %d SessionID = %q, want session-1", i, payload.SessionID)
		}
	}
}

// TestWALSink_RoundTripViaReplayer exercises the full write → replay cycle:
// a session observes a signal, the WAL entry is written, a fresh session
// is created, the replayer reapplies the entry, and the fresh session's
// posterior matches the original's.
func TestWALSink_RoundTripViaReplayer(t *testing.T) {
	wal := &capturingWAL{}
	sink := NewWALSink(wal)

	base := PriorQualityWeights(DomainCode)
	original, _ := NewSessionWeightManager("session-1", base)
	original.SetObservationHook(sink.ObservationHook())

	for i := 0; i < 5; i++ {
		original.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)
	}
	want := original.WeightFor(DomainCode, SignalVector)

	// Fresh session, no observations yet — replayer should reconstruct.
	restored, _ := NewSessionWeightManager("session-1", base)
	replayer := NewReplayer()
	replayer.RegisterSession("session-1", restored)

	for _, captured := range wal.snapshot() {
		entry := &concurrency.WALEntry{Type: captured.Type, Payload: captured.Payload}
		if err := replayer.ReplayEntry(entry); err != nil {
			t.Fatalf("ReplayEntry: %v", err)
		}
	}

	got := restored.WeightFor(DomainCode, SignalVector)
	if got.Alpha != want.Alpha || got.Beta != want.Beta {
		t.Fatalf("restored posterior (α=%v β=%v) does not match original (α=%v β=%v)",
			got.Alpha, got.Beta, want.Alpha, want.Beta)
	}
	if got.EffectiveSamples != want.EffectiveSamples {
		t.Fatalf("restored EffectiveSamples = %v, want %v", got.EffectiveSamples, want.EffectiveSamples)
	}
}

// TestReplayer_SkipsUnknownSession verifies that a WAL entry for a
// session not registered on the replayer is skipped silently, not an
// error — a crashed peer session should not block the current session's
// replay.
func TestReplayer_SkipsUnknownSession(t *testing.T) {
	replayer := NewReplayer()
	payload, _ := json.Marshal(WeightObservationPayload{
		SessionID:       "other-session",
		Domain:          "code",
		Signal:          "vector",
		Success:         true,
		EffectiveWeight: 1,
	})
	entry := &concurrency.WALEntry{Type: concurrency.EntryWeightObservation, Payload: payload}
	if err := replayer.ReplayEntry(entry); err != nil {
		t.Fatalf("replay of unknown session should not error: %v", err)
	}
}

// TestReplayer_RejectsUnknownDomain verifies that a WAL entry referring
// to an unparseable Domain fails loudly — silent drop would let schema
// drift corrupt posteriors undetectably.
func TestReplayer_RejectsUnknownDomain(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)
	replayer := NewReplayer()
	replayer.RegisterSession("session-1", m)

	payload, _ := json.Marshal(WeightObservationPayload{
		SessionID: "session-1",
		Domain:    "quantum", // not a known domain
		Signal:    "vector",
		Success:   true,
	})
	entry := &concurrency.WALEntry{Type: concurrency.EntryWeightObservation, Payload: payload}
	if err := replayer.ReplayEntry(entry); err == nil {
		t.Fatal("expected error for unknown domain")
	}
}

// TestReplayer_RegisterDispatchesAllFourEntryTypes verifies Register()
// hooks the replayer to all four Bayesian entry types in the
// core/concurrency dispatch registry. Using Unregister after guards
// against state leaks across tests.
func TestReplayer_RegisterDispatchesAllFourEntryTypes(t *testing.T) {
	replayer := NewReplayer()
	replayer.Register()
	defer replayer.Unregister()

	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)
	replayer.RegisterSession("session-1", m)

	// Flag the handlers so we can assert they fired.
	var gotGP, gotThreshold, gotDecay bool
	replayer.SetGPHandler(func(GPObservationPayload) error { gotGP = true; return nil })
	replayer.SetThresholdHandler(func(ThresholdFeedbackPayload) error { gotThreshold = true; return nil })
	replayer.SetDecayHandler(func(DecayFeedbackPayload) error { gotDecay = true; return nil })

	// Drive each entry type through the recovery dispatcher by hand.
	entries := []*concurrency.WALEntry{
		{Type: concurrency.EntryWeightObservation, Payload: mustEncode(t, WeightObservationPayload{SessionID: "session-1", Domain: "code", Signal: "vector", Success: true})},
		{Type: concurrency.EntryGPObservation, Payload: mustEncode(t, GPObservationPayload{SessionID: "session-1"})},
		{Type: concurrency.EntryThresholdFeedback, Payload: mustEncode(t, ThresholdFeedbackPayload{SessionID: "session-1"})},
		{Type: concurrency.EntryDecayFeedback, Payload: mustEncode(t, DecayFeedbackPayload{SessionID: "session-1", Domain: "code"})},
	}
	for _, entry := range entries {
		if err := replayer.ReplayEntry(entry); err != nil {
			t.Fatalf("ReplayEntry(%v): %v", entry.Type, err)
		}
	}
	if !gotGP || !gotThreshold || !gotDecay {
		t.Errorf("handlers fired: gp=%v threshold=%v decay=%v — all must be true", gotGP, gotThreshold, gotDecay)
	}
	if m.WeightFor(DomainCode, SignalVector).EffectiveSamples == 0 {
		t.Error("WeightObservation did not reach the session manager")
	}
}

// TestWALSink_ErrorHookFiresOnAppendFailure locks the durability contract:
// if the WAL append fails, the caller is notified (via the error hook)
// but the observation still reaches memory. Availability over durability
// fallback behavior.
func TestWALSink_ErrorHookFiresOnAppendFailure(t *testing.T) {
	failing := &failingWAL{}
	sink := NewWALSink(failing)

	var observedErr error
	sink.SetErrorHook(func(err error) { observedErr = err })

	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)
	m.SetObservationHook(sink.ObservationHook())

	m.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)

	if observedErr == nil {
		t.Fatal("expected error hook to fire when WAL append fails")
	}
	// The in-memory observation still applies.
	if m.WeightFor(DomainCode, SignalVector).EffectiveSamples != 1 {
		t.Error("observation should still apply in-memory when WAL append fails")
	}
}

type failingWAL struct{}

func (failingWAL) Append(concurrency.EntryType, []byte) (uint64, error) {
	return 0, assertingError("wal full")
}

type assertingError string

func (e assertingError) Error() string { return string(e) }

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return data
}

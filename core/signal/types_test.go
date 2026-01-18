package signal

import (
	"encoding/json"
	"testing"
	"time"
)

func TestValidSignals_IncludesMemoryPressureSignals(t *testing.T) {
	signals := ValidSignals()

	memorySignals := []Signal{EvictCaches, CompactContexts, MemoryPressureChanged}

	for _, expected := range memorySignals {
		found := false
		for _, s := range signals {
			if s == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ValidSignals() missing memory pressure signal: %s", expected)
		}
	}
}

func TestValidSignals_IncludesAllSignals(t *testing.T) {
	signals := ValidSignals()

	expected := []Signal{
		PauseAll,
		ResumeAll,
		PausePipeline,
		ResumePipeline,
		CancelTask,
		AbortSession,
		QuotaWarning,
		StateChanged,
		EvictCaches,
		CompactContexts,
		MemoryPressureChanged,
	}

	if len(signals) != len(expected) {
		t.Errorf("ValidSignals() returned %d signals, expected %d", len(signals), len(expected))
	}
}

func TestEvictCachesPayload_Serialization(t *testing.T) {
	payload := EvictCachesPayload{
		Percent:     0.25,
		TargetBytes: 1024 * 1024,
		Reason:      "memory pressure",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal EvictCachesPayload: %v", err)
	}

	var decoded EvictCachesPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal EvictCachesPayload: %v", err)
	}

	if decoded.Percent != payload.Percent {
		t.Errorf("Percent mismatch: got %v, want %v", decoded.Percent, payload.Percent)
	}
	if decoded.TargetBytes != payload.TargetBytes {
		t.Errorf("TargetBytes mismatch: got %v, want %v", decoded.TargetBytes, payload.TargetBytes)
	}
	if decoded.Reason != payload.Reason {
		t.Errorf("Reason mismatch: got %q, want %q", decoded.Reason, payload.Reason)
	}
}

func TestCompactContextsPayload_Serialization(t *testing.T) {
	payload := CompactContextsPayload{
		TargetID: "agent-123",
		All:      false,
		Reason:   "high memory",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal CompactContextsPayload: %v", err)
	}

	var decoded CompactContextsPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CompactContextsPayload: %v", err)
	}

	if decoded.TargetID != payload.TargetID {
		t.Errorf("TargetID mismatch: got %q, want %q", decoded.TargetID, payload.TargetID)
	}
	if decoded.All != payload.All {
		t.Errorf("All mismatch: got %v, want %v", decoded.All, payload.All)
	}
	if decoded.Reason != payload.Reason {
		t.Errorf("Reason mismatch: got %q, want %q", decoded.Reason, payload.Reason)
	}
}

func TestCompactContextsPayload_AllTrue(t *testing.T) {
	payload := CompactContextsPayload{
		TargetID: "",
		All:      true,
		Reason:   "critical pressure",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal CompactContextsPayload: %v", err)
	}

	var decoded CompactContextsPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CompactContextsPayload: %v", err)
	}

	if !decoded.All {
		t.Error("All should be true")
	}
	if decoded.TargetID != "" {
		t.Errorf("TargetID should be empty when All is true, got %q", decoded.TargetID)
	}
}

func TestMemoryPressurePayload_Serialization(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	payload := MemoryPressurePayload{
		From:      "NORMAL",
		To:        "ELEVATED",
		Usage:     0.55,
		Timestamp: now,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal MemoryPressurePayload: %v", err)
	}

	var decoded MemoryPressurePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal MemoryPressurePayload: %v", err)
	}

	if decoded.From != payload.From {
		t.Errorf("From mismatch: got %q, want %q", decoded.From, payload.From)
	}
	if decoded.To != payload.To {
		t.Errorf("To mismatch: got %q, want %q", decoded.To, payload.To)
	}
	if decoded.Usage != payload.Usage {
		t.Errorf("Usage mismatch: got %v, want %v", decoded.Usage, payload.Usage)
	}
}

func TestMemoryPressurePayload_AllPressureLevels(t *testing.T) {
	levels := []string{"NORMAL", "ELEVATED", "HIGH", "CRITICAL"}

	for i := 0; i < len(levels)-1; i++ {
		payload := MemoryPressurePayload{
			From:      levels[i],
			To:        levels[i+1],
			Usage:     float64(i+1) * 0.25,
			Timestamp: time.Now(),
		}

		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("failed to marshal transition %s->%s: %v", levels[i], levels[i+1], err)
		}

		var decoded MemoryPressurePayload
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("failed to unmarshal transition %s->%s: %v", levels[i], levels[i+1], err)
		}

		if decoded.From != levels[i] || decoded.To != levels[i+1] {
			t.Errorf("transition mismatch: got %s->%s, want %s->%s",
				decoded.From, decoded.To, levels[i], levels[i+1])
		}
	}
}

func TestSignalConstants_Values(t *testing.T) {
	if EvictCaches != "evict_caches" {
		t.Errorf("EvictCaches = %q, want %q", EvictCaches, "evict_caches")
	}
	if CompactContexts != "compact_contexts" {
		t.Errorf("CompactContexts = %q, want %q", CompactContexts, "compact_contexts")
	}
	if MemoryPressureChanged != "memory_pressure_changed" {
		t.Errorf("MemoryPressureChanged = %q, want %q", MemoryPressureChanged, "memory_pressure_changed")
	}
}

func TestSignalMessage_WithMemoryPayload(t *testing.T) {
	msg := NewSignalMessage(EvictCaches, "cache-manager", false)
	msg.Payload = EvictCachesPayload{
		Percent: 0.25,
		Reason:  "elevated pressure",
	}

	if msg.Signal != EvictCaches {
		t.Errorf("Signal = %q, want %q", msg.Signal, EvictCaches)
	}
	if msg.TargetID != "cache-manager" {
		t.Errorf("TargetID = %q, want %q", msg.TargetID, "cache-manager")
	}

	payload, ok := msg.Payload.(EvictCachesPayload)
	if !ok {
		t.Fatal("Payload is not EvictCachesPayload")
	}
	if payload.Percent != 0.25 {
		t.Errorf("Payload.Percent = %v, want %v", payload.Percent, 0.25)
	}
}

func TestSignalMessage_BroadcastWithCompactContexts(t *testing.T) {
	msg := NewSignalMessage(CompactContexts, "", true)
	msg.Payload = CompactContextsPayload{
		All:    true,
		Reason: "memory pressure",
	}

	if msg.Signal != CompactContexts {
		t.Errorf("Signal = %q, want %q", msg.Signal, CompactContexts)
	}
	if !msg.RequiresAck {
		t.Error("RequiresAck should be true")
	}

	payload, ok := msg.Payload.(CompactContextsPayload)
	if !ok {
		t.Fatal("Payload is not CompactContextsPayload")
	}
	if !payload.All {
		t.Error("Payload.All should be true")
	}
}

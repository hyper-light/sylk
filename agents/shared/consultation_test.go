package shared

import (
	"encoding/json"
	"testing"
	"time"
)

// --- DefaultConsultationTimeout ---

func TestDefaultConsultationTimeout_Value(t *testing.T) {
	expected := 60 * time.Second
	if DefaultConsultationTimeout != expected {
		t.Fatalf("DefaultConsultationTimeout = %v, want %v", DefaultConsultationTimeout, expected)
	}
}

// --- ConsultationEvidence JSON roundtrip ---

func TestConsultationEvidence_JSONRoundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	original := ConsultationEvidence{
		Target:      "knowledge-agent",
		Query:       "explain the auth flow",
		Scope:       "project",
		Correlation: "corr-abc-123",
		Success:     true,
		Data:        map[string]any{"summary": "OAuth2 PKCE flow"},
		Error:       "",
		RequestedAt: now,
		ReceivedAt:  now.Add(DefaultConsultationTimeout / 2),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored ConsultationEvidence
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.Target != original.Target {
		t.Fatalf("Target mismatch: got %q, want %q", restored.Target, original.Target)
	}
	if restored.Query != original.Query {
		t.Fatalf("Query mismatch: got %q, want %q", restored.Query, original.Query)
	}
	if restored.Scope != original.Scope {
		t.Fatalf("Scope mismatch: got %q, want %q", restored.Scope, original.Scope)
	}
	if restored.Correlation != original.Correlation {
		t.Fatalf("Correlation mismatch: got %q, want %q", restored.Correlation, original.Correlation)
	}
	if restored.Success != original.Success {
		t.Fatalf("Success mismatch: got %v, want %v", restored.Success, original.Success)
	}
	if restored.Error != original.Error {
		t.Fatalf("Error mismatch: got %q, want %q", restored.Error, original.Error)
	}
}

func TestConsultationEvidence_JSONRoundtrip_WithError(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	original := ConsultationEvidence{
		Target:      "tester-agent",
		Query:       "run unit tests",
		Scope:       "file",
		Correlation: "corr-def-456",
		Success:     false,
		Error:       "timeout exceeded",
		RequestedAt: now,
		ReceivedAt:  now.Add(DefaultConsultationTimeout),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored ConsultationEvidence
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.Success {
		t.Fatal("Success should be false for error case")
	}
	if restored.Error != original.Error {
		t.Fatalf("Error mismatch: got %q, want %q", restored.Error, original.Error)
	}
}

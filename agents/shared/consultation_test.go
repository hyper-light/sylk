package shared

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestConsultationInactivityTimeout_AcademicUsesResearchBudget(t *testing.T) {
	tests := []struct {
		target string
		want   time.Duration
	}{
		{target: "academic", want: DefaultResearchConsultationTimeout},
		{target: "task_1:academic", want: DefaultResearchConsultationTimeout},
		{target: "librarian", want: DefaultConsultationTimeout},
		{target: "", want: DefaultConsultationTimeout},
	}

	for _, tt := range tests {
		if got := ConsultationInactivityTimeout(tt.target); got != tt.want {
			t.Fatalf("ConsultationInactivityTimeout(%q) = %v, want %v", tt.target, got, tt.want)
		}
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

func TestRunWithContextLease_RefreshesChildDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	responseReady := make(chan struct{})
	var (
		attempts  int
		refreshes int
	)
	err := RunWithContextLease(parent, ContextLeaseConfig{
		AttemptTimeout: 20 * time.Millisecond,
		MaxRefreshes:   2,
		DeadlineGuard:  10 * time.Millisecond,
		OnRefresh: func(info ContextLeaseRefresh) {
			refreshes = info.RefreshCount
		},
	}, func(ctx context.Context) error {
		attempts++
		if attempts == 2 {
			close(responseReady)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-responseReady:
			return nil
		}
	})
	if err != nil {
		t.Fatalf("RunWithContextLease() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes)
	}
}

func TestRunWithContextLease_ReturnsLeaseTimeoutAfterRefreshBudgetExhausted(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := RunWithContextLease(parent, ContextLeaseConfig{
		AttemptTimeout: 20 * time.Millisecond,
		MaxRefreshes:   1,
		DeadlineGuard:  10 * time.Millisecond,
	}, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	var leaseErr *ContextLeaseTimeoutError
	if !errors.As(err, &leaseErr) {
		t.Fatalf("error = %v, want ContextLeaseTimeoutError", err)
	}
	if leaseErr.Refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", leaseErr.Refreshes)
	}
}

func TestWrapLeaseTimeoutError_UsesLeaseElapsed(t *testing.T) {
	err := WrapLeaseTimeoutError("consultation to \"librarian\"", time.Minute, &ContextLeaseTimeoutError{
		AttemptTimeout: time.Minute,
		Refreshes:      2,
		Elapsed:        3 * time.Minute,
		Err:            context.DeadlineExceeded,
	})
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	got := err.Error()
	want := "consultation to \"librarian\" timed out after 3m0s"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("error prefix = %q, want prefix %q", got, want)
	}
}

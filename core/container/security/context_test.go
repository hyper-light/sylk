package security

import (
	"context"
	"errors"
	"testing"
)

func TestSecurityContext_AuthorizeFileRead(t *testing.T) {
	caps := NewCapabilitySet(nil, nil, nil, []string{"/data"}, nil, false)
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID:  "c1",
		AgentID:      "a1",
		Role:         "worker",
		Capabilities: caps,
	})

	result := sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionFileRead,
		Target: "/data",
	})
	if !result.Allowed {
		t.Fatal("should allow read of /data")
	}

	result = sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionFileRead,
		Target: "/forbidden",
	})
	if result.Allowed {
		t.Fatal("should deny read of /forbidden")
	}
}

func TestSecurityContext_ReadOnlyBlocksWrite(t *testing.T) {
	caps := NewCapabilitySet(nil, nil, nil, nil, []string{"/output"}, false)
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID:  "c1",
		AgentID:      "a1",
		Role:         "worker",
		Capabilities: caps,
		ReadOnly:     true,
	})

	result := sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionFileWrite,
		Target: "/output",
	})
	if result.Allowed {
		t.Fatal("read-only should block write")
	}
	if result.Reason != "read_only_filesystem" {
		t.Fatalf("expected read_only_filesystem, got %s", result.Reason)
	}
}

func TestSecurityContext_ReadOnlyBlocksDelete(t *testing.T) {
	caps := NewCapabilitySet(nil, nil, nil, nil, []string{"/output"}, false)
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID:  "c1",
		Capabilities: caps,
		ReadOnly:     true,
	})

	result := sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionFileDelete,
		Target: "/output",
	})
	if result.Allowed {
		t.Fatal("read-only should block delete")
	}
}

func TestSecurityContext_EscalationDenied(t *testing.T) {
	caps := NewCapabilitySet(nil, nil, nil, nil, nil, false) // canEscalate=false
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID:  "c1",
		Capabilities: caps,
	})

	result := sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionEscalate,
	})
	if result.Allowed {
		t.Fatal("should deny escalation when not permitted")
	}
}

func TestSecurityContext_EscalationAllowed(t *testing.T) {
	caps := NewCapabilitySet(nil, nil, nil, nil, nil, true)
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID:  "c1",
		Capabilities: caps,
	})

	result := sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionEscalate,
	})
	if !result.Allowed {
		t.Fatal("should allow escalation when permitted")
	}
}

func TestSecurityContext_SecretAccess(t *testing.T) {
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID: "c1",
		Secrets:     map[string][]byte{"api-key": []byte("secret123")},
	})

	result := sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionSecretAccess,
		Target: "api-key",
	})
	if !result.Allowed {
		t.Fatal("should allow access to registered secret")
	}

	result = sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionSecretAccess,
		Target: "unknown",
	})
	if result.Allowed {
		t.Fatal("should deny access to unknown secret")
	}
}

func TestSecurityContext_GetSecret(t *testing.T) {
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID: "c1",
		Secrets:     map[string][]byte{"key": []byte("value")},
	})

	val, err := sc.GetSecret("key")
	if err != nil {
		t.Fatalf("GetSecret failed: %v", err)
	}
	if string(val) != "value" {
		t.Fatalf("expected 'value', got '%s'", val)
	}

	_, err = sc.GetSecret("missing")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
}

func TestSecurityContext_GetSecretReturnsCopy(t *testing.T) {
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID: "c1",
		Secrets:     map[string][]byte{"key": []byte("value")},
	})

	val, _ := sc.GetSecret("key")
	val[0] = 'X' // mutate the copy

	// Original should be unchanged
	val2, _ := sc.GetSecret("key")
	if string(val2) != "value" {
		t.Fatalf("mutation leaked: got '%s'", val2)
	}
}

func TestSecurityContext_Accessors(t *testing.T) {
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID: "c1",
		AgentID:     "a1",
		Role:        "supervisor",
		ReadOnly:    true,
	})

	if sc.ContainerID() != "c1" {
		t.Fatalf("expected c1, got %s", sc.ContainerID())
	}
	if sc.Role() != "supervisor" {
		t.Fatalf("expected supervisor, got %s", sc.Role())
	}
	if !sc.IsReadOnly() {
		t.Fatal("should be read-only")
	}
	if sc.Capabilities() == nil {
		t.Fatal("capabilities should not be nil")
	}
}

type mockAuditSink struct {
	calls int
}

func (m *mockAuditSink) LogContainerAction(_, _ string, _ ActionType, _, _ string) {
	m.calls++
}

func TestSecurityContext_AuditSink(t *testing.T) {
	sink := &mockAuditSink{}
	caps := NewCapabilitySet(nil, nil, nil, []string{"/data"}, nil, false)
	sc := NewSecurityContext(SecurityContextConfig{
		ContainerID:  "c1",
		Capabilities: caps,
		AuditSink:    sink,
	})

	sc.Authorize(context.Background(), AuthorizationRequest{
		Action: ActionFileRead,
		Target: "/data",
	})

	if sink.calls != 1 {
		t.Fatalf("expected 1 audit call, got %d", sink.calls)
	}
}

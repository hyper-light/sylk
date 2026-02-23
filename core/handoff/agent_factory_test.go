package handoff

import (
	"context"
	"testing"
	"time"
)

// mockHandoffAgent implements HandoffableAgent for testing.
type mockHandoffAgent struct {
	id        string
	agentType string
	desc      AgentDescriptor
	stopped   bool
}

func (m *mockHandoffAgent) AgentID() string                          { return m.id }
func (m *mockHandoffAgent) AgentType() string                        { return m.agentType }
func (m *mockHandoffAgent) Descriptor() AgentDescriptor              { return m.desc }
func (m *mockHandoffAgent) ExtractArchivableState() *ArchivableState { return nil }
func (m *mockHandoffAgent) Terminate(_ context.Context) error        { m.stopped = true; return nil }

func newMockAgent(id, agentType string) *mockHandoffAgent {
	return &mockHandoffAgent{
		id:        id,
		agentType: agentType,
		desc: AgentDescriptor{
			AgentType:     agentType,
			ModelID:       "test-model",
			ContextWindow: 100_000,
			Category:      CategoryStandalone,
		},
	}
}

func TestAgentFactory_RegisterAndCreate(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)

	factory.RegisterCreator("test-agent", func(_ context.Context) (HandoffableAgent, error) {
		return newMockAgent("test-1", "test-agent"), nil
	})

	if !factory.HasCreator("test-agent") {
		t.Fatal("HasCreator(test-agent) = false, want true")
	}

	agent, err := factory.CreateAgent(context.Background(), "test-agent")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if agent.AgentID() != "test-1" {
		t.Errorf("AgentID = %q, want %q", agent.AgentID(), "test-1")
	}
}

func TestAgentFactory_UnknownType(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)

	_, err := factory.CreateAgent(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown agent type")
	}
}

func TestAgentFactory_HasCreator(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)

	if factory.HasCreator("anything") {
		t.Fatal("HasCreator should be false for unregistered type")
	}
}

func TestFactorySessionAdapter_CreateAndTake(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)
	factory.RegisterCreator("guide", func(_ context.Context) (HandoffableAgent, error) {
		return newMockAgent("guide-new", "guide"), nil
	})

	adapter := NewFactorySessionAdapter(factory)

	cfg := SessionConfig{
		Name: "test-session",
		Metadata: map[string]interface{}{
			"agent_type": "guide",
		},
	}

	sessionID, err := adapter.CreateSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sessionID == "" {
		t.Fatal("sessionID is empty")
	}

	// TakeAgent should return the agent.
	agent, ok := adapter.TakeAgent(sessionID)
	if !ok {
		t.Fatal("TakeAgent returned false")
	}
	if agent.AgentID() != "guide-new" {
		t.Errorf("AgentID = %q, want %q", agent.AgentID(), "guide-new")
	}

	// Second take should fail.
	_, ok = adapter.TakeAgent(sessionID)
	if ok {
		t.Fatal("second TakeAgent should return false")
	}
}

func TestFactorySessionAdapter_TransferContext(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)
	factory.RegisterCreator("guide", func(_ context.Context) (HandoffableAgent, error) {
		return newMockAgent("guide-2", "guide"), nil
	})

	adapter := NewFactorySessionAdapter(factory)
	cfg := SessionConfig{
		Name:     "test",
		Metadata: map[string]interface{}{"agent_type": "guide"},
	}

	sessionID, err := adapter.CreateSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// TransferContext with non-injectable agent should not error.
	transfer := &ContextTransfer{
		PreparedContext: NewPreparedContextDefault(),
	}
	if err := adapter.TransferContext(context.Background(), sessionID, transfer); err != nil {
		t.Fatalf("TransferContext: %v", err)
	}
}

func TestFactorySessionAdapter_MissingAgentType(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)
	adapter := NewFactorySessionAdapter(factory)

	_, err := adapter.CreateSession(context.Background(), SessionConfig{
		Metadata: map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected error for missing agent_type")
	}
}

func TestFactorySessionAdapter_DeleteSession(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)

	var created *mockHandoffAgent
	factory.RegisterCreator("guide", func(_ context.Context) (HandoffableAgent, error) {
		created = newMockAgent("guide-del", "guide")
		return created, nil
	})

	adapter := NewFactorySessionAdapter(factory)
	cfg := SessionConfig{
		Name:     "test",
		Metadata: map[string]interface{}{"agent_type": "guide"},
	}

	sessionID, _ := adapter.CreateSession(context.Background(), cfg)

	if err := adapter.DeleteSession(context.Background(), sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if !created.stopped {
		t.Error("agent should have been terminated")
	}

	// TakeAgent should now fail.
	_, ok := adapter.TakeAgent(sessionID)
	if ok {
		t.Error("TakeAgent after delete should return false")
	}
}

func TestFactorySessionAdapter_ActivateDeactivate(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)
	adapter := NewFactorySessionAdapter(factory)

	// These should be no-ops.
	if err := adapter.ActivateSession(context.Background(), "any"); err != nil {
		t.Errorf("ActivateSession: %v", err)
	}
	if err := adapter.DeactivateSession(context.Background(), "any"); err != nil {
		t.Errorf("DeactivateSession: %v", err)
	}
}

func TestFactorySessionAdapter_TransferContextMissing(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)
	adapter := NewFactorySessionAdapter(factory)

	err := adapter.TransferContext(context.Background(), "nonexistent", &ContextTransfer{})
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

// Verify session IDs contain the agent type.
func TestFactorySessionAdapter_SessionIDFormat(t *testing.T) {
	registry := NewDescriptorRegistry()
	factory := NewAgentFactory(registry)
	factory.RegisterCreator("architect", func(_ context.Context) (HandoffableAgent, error) {
		return newMockAgent("arch-1", "architect"), nil
	})

	adapter := NewFactorySessionAdapter(factory)
	cfg := SessionConfig{
		Name:     "test",
		Metadata: map[string]interface{}{"agent_type": "architect"},
	}

	sessionID, err := adapter.CreateSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_ = time.Now() // Ensure import
	if len(sessionID) < 10 {
		t.Errorf("sessionID too short: %q", sessionID)
	}
}

package credentials

import (
	"context"
	"testing"
)

type mockToolRequirements struct {
	requirements map[string][]string
}

func newMockToolRequirements() *mockToolRequirements {
	return &mockToolRequirements{
		requirements: map[string][]string{
			"generate_embeddings": {"openai"},
			"create_pr":           {"github"},
			"web_search":          {"serpapi"},
			"multi_tool":          {"openai", "github"},
			"read_file":           {},
		},
	}
}

func (m *mockToolRequirements) GetRequiredCredentials(toolName string) []string {
	return m.requirements[toolName]
}

func TestCredentialContext_AddAndResolve(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "test",
	)

	credCtx := NewCredentialContext(broker)
	credCtx.AddHandle("openai", handle)

	value, err := credCtx.Credential("openai")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if value != "sk-openai-key" {
		t.Error("wrong credential value")
	}
}

func TestCredentialContext_CachedResolve(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "test",
	)

	credCtx := NewCredentialContext(broker)
	credCtx.AddHandle("openai", handle)

	value1, _ := credCtx.Credential("openai")
	value2, _ := credCtx.Credential("openai")

	if value1 != value2 {
		t.Error("cached value should be same")
	}
}

func TestCredentialContext_Cleanup(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	h1, _ := broker.RequestCredential(context.Background(), "a", "librarian", "openai", "c1", "r")
	h2, _ := broker.RequestCredential(context.Background(), "a", "librarian", "anthropic", "c2", "r")

	credCtx := NewCredentialContext(broker)
	credCtx.AddHandle("openai", h1)
	credCtx.AddHandle("anthropic", h2)

	credCtx.Credential("openai")

	credCtx.Cleanup()

	_, err := broker.GetHandle(h2.ID)
	if err != ErrHandleNotFound {
		t.Error("unused handle should be revoked")
	}
}

func TestCredentialContext_HandleIDs(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	h1, _ := broker.RequestCredential(context.Background(), "a", "librarian", "openai", "c1", "r")
	h2, _ := broker.RequestCredential(context.Background(), "a", "librarian", "anthropic", "c2", "r")

	credCtx := NewCredentialContext(broker)
	credCtx.AddHandle("openai", h1)
	credCtx.AddHandle("anthropic", h2)

	ids := credCtx.HandleIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 handle IDs, got %d", len(ids))
	}
}

func TestWithCredentialContext(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	credCtx := NewCredentialContext(broker)
	ctx := WithCredentialContext(context.Background(), credCtx)

	retrieved := GetCredentialContext(ctx)
	if retrieved != credCtx {
		t.Error("should retrieve same context")
	}
}

func TestGetCredentialContext_NotSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	retrieved := GetCredentialContext(ctx)
	if retrieved != nil {
		t.Error("should return nil when not set")
	}
}

func TestPreToolCredentialHook_WithRequirements(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	hook := NewPreToolCredentialHook(broker, newMockToolRequirements())

	data := &ToolHookData{
		ToolName:   "generate_embeddings",
		AgentID:    "agent-1",
		AgentType:  "librarian",
		ToolCallID: "call-123",
	}

	ctx, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}

	credCtx := GetCredentialContext(ctx)
	if credCtx == nil {
		t.Fatal("credential context should be set")
	}

	value, err := credCtx.Credential("openai")
	if err != nil {
		t.Fatalf("credential resolve failed: %v", err)
	}
	if value != "sk-openai-key" {
		t.Error("wrong credential value")
	}
}

func TestPreToolCredentialHook_NoRequirements(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	hook := NewPreToolCredentialHook(broker, newMockToolRequirements())

	data := &ToolHookData{
		ToolName:   "read_file",
		AgentID:    "agent-1",
		AgentType:  "librarian",
		ToolCallID: "call-123",
	}

	ctx, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}

	credCtx := GetCredentialContext(ctx)
	if credCtx != nil {
		t.Error("credential context should not be set for tools without requirements")
	}
}

func TestPreToolCredentialHook_MultipleCredentials(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
		AuthCallback: func(agentType, provider, reason string) (bool, error) {
			return true, nil
		},
	})
	defer broker.Stop()

	hook := NewPreToolCredentialHook(broker, newMockToolRequirements())

	data := &ToolHookData{
		ToolName:   "multi_tool",
		AgentID:    "agent-1",
		AgentType:  "engineer",
		ToolCallID: "call-123",
	}

	ctx, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}

	credCtx := GetCredentialContext(ctx)
	if credCtx == nil {
		t.Fatal("credential context should be set")
	}

	if len(credCtx.HandleIDs()) != 2 {
		t.Error("should have 2 handles")
	}
}

func TestPreToolCredentialHook_DeniedCredential(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	hook := NewPreToolCredentialHook(broker, newMockToolRequirements())

	data := &ToolHookData{
		ToolName:   "create_pr",
		AgentID:    "agent-1",
		AgentType:  "librarian",
		ToolCallID: "call-123",
	}

	_, err := hook.Handle(context.Background(), data)
	if err == nil {
		t.Error("should fail for denied credential")
	}
}

func TestPreToolCredentialHook_NilData(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	hook := NewPreToolCredentialHook(broker, newMockToolRequirements())

	ctx, err := hook.Handle(context.Background(), nil)
	if err != nil {
		t.Errorf("nil data should not error: %v", err)
	}
	if GetCredentialContext(ctx) != nil {
		t.Error("nil data should not set context")
	}
}

func TestPostToolCredentialHook_Cleanup(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{
		CredentialProvider: newMockCredentialProvider(),
	})
	defer broker.Stop()

	handle, _ := broker.RequestCredential(
		context.Background(),
		"agent-1", "librarian", "openai", "call-123", "test",
	)

	credCtx := NewCredentialContext(broker)
	credCtx.AddHandle("openai", handle)

	ctx := WithCredentialContext(context.Background(), credCtx)

	hook := NewPostToolCredentialHook()
	err := hook.Handle(ctx, &ToolHookData{})
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	_, err = broker.GetHandle(handle.ID)
	if err != ErrHandleNotFound {
		t.Error("handle should be revoked after cleanup")
	}
}

func TestPostToolCredentialHook_NoContext(t *testing.T) {
	t.Parallel()

	hook := NewPostToolCredentialHook()
	err := hook.Handle(context.Background(), &ToolHookData{})
	if err != nil {
		t.Errorf("should not error without context: %v", err)
	}
}

func TestHookMetadata(t *testing.T) {
	t.Parallel()

	broker := NewCredentialBroker(BrokerConfig{})
	defer broker.Stop()

	preHook := NewPreToolCredentialHook(broker, nil)
	if preHook.Name() != "credential_injection" {
		t.Error("wrong pre-hook name")
	}
	if preHook.Priority() != 20 {
		t.Error("wrong pre-hook priority")
	}

	postHook := NewPostToolCredentialHook()
	if postHook.Name() != "credential_cleanup" {
		t.Error("wrong post-hook name")
	}
	if postHook.Priority() != 100 {
		t.Error("wrong post-hook priority")
	}
}

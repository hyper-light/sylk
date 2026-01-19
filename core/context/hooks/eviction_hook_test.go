package hooks

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/resources"
)

// =============================================================================
// Test Helpers
// =============================================================================

// MockContextManager implements EvictableContextManager for testing.
type MockContextManager struct {
	mu             sync.Mutex
	evictionCalls  []evictionCall
	evictionErr    error
	evictedTokens  int
}

type evictionCall struct {
	agentID string
	percent float64
}

func (m *MockContextManager) ForceEvict(agentID string, percent float64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.evictionCalls = append(m.evictionCalls, evictionCall{
		agentID: agentID,
		percent: percent,
	})

	if m.evictionErr != nil {
		return 0, m.evictionErr
	}

	return m.evictedTokens, nil
}

func (m *MockContextManager) GetEvictionCalls() []evictionCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evictionCalls
}

func (m *MockContextManager) SetError(err error) {
	m.mu.Lock()
	m.evictionErr = err
	m.mu.Unlock()
}

func (m *MockContextManager) SetEvictedTokens(tokens int) {
	m.mu.Lock()
	m.evictedTokens = tokens
	m.mu.Unlock()
}

func createEvictionTestPromptData(agentType string, pressureLevel int) *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:     "test-session",
		AgentID:       "test-agent",
		AgentType:     agentType,
		TurnNumber:    1,
		Query:         "test query",
		PressureLevel: pressureLevel,
		Timestamp:     time.Now(),
	}
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewPressureEvictionHook_DefaultConfig(t *testing.T) {
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{})

	if hook.highEvictionPercent != EvictionPercentHigh {
		t.Errorf("highEvictionPercent = %v, want %v", hook.highEvictionPercent, EvictionPercentHigh)
	}

	if hook.criticalEvictionPercent != EvictionPercentCritical {
		t.Errorf("criticalEvictionPercent = %v, want %v", hook.criticalEvictionPercent, EvictionPercentCritical)
	}

	if !hook.enabled {
		t.Error("Hook should be enabled by default")
	}
}

func TestNewPressureEvictionHook_CustomConfig(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager:          cm,
		HighEvictionPercent:     0.30,
		CriticalEvictionPercent: 0.60,
	})

	if hook.contextManager != cm {
		t.Error("ContextManager not set correctly")
	}

	if hook.highEvictionPercent != 0.30 {
		t.Errorf("highEvictionPercent = %v, want 0.30", hook.highEvictionPercent)
	}

	if hook.criticalEvictionPercent != 0.60 {
		t.Errorf("criticalEvictionPercent = %v, want 0.60", hook.criticalEvictionPercent)
	}
}

// =============================================================================
// Hook Interface Tests
// =============================================================================

func TestPressureEvictionHook_Name(t *testing.T) {
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{})

	if hook.Name() != PressureEvictionHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), PressureEvictionHookName)
	}
}

func TestPressureEvictionHook_Phase(t *testing.T) {
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("Phase() = %v, want HookPhasePrePrompt", hook.Phase())
	}
}

func TestPressureEvictionHook_Priority(t *testing.T) {
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityFirst) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityFirst)
	}
}

func TestPressureEvictionHook_Enabled(t *testing.T) {
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{})

	if !hook.Enabled() {
		t.Error("Hook should be enabled by default")
	}

	hook.SetEnabled(false)

	if hook.Enabled() {
		t.Error("Hook should be disabled after SetEnabled(false)")
	}
}

// =============================================================================
// Execute Tests - Normal Pressure
// =============================================================================

func TestExecute_NoEvictionAtNormal(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureNormal))

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data at normal pressure")
	}

	calls := cm.GetEvictionCalls()
	if len(calls) != 0 {
		t.Errorf("Expected no eviction calls at normal pressure, got %d", len(calls))
	}
}

func TestExecute_NoEvictionAtElevated(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureElevated))

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data at elevated pressure")
	}

	calls := cm.GetEvictionCalls()
	if len(calls) != 0 {
		t.Errorf("Expected no eviction calls at elevated pressure, got %d", len(calls))
	}
}

// =============================================================================
// Execute Tests - High Pressure
// =============================================================================

func TestExecute_EvictionAtHigh(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureHigh))

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	calls := cm.GetEvictionCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 eviction call at high pressure, got %d", len(calls))
	}

	if calls[0].percent != EvictionPercentHigh {
		t.Errorf("Eviction percent = %v, want %v", calls[0].percent, EvictionPercentHigh)
	}

	if calls[0].agentID != data.AgentID {
		t.Errorf("AgentID = %s, want %s", calls[0].agentID, data.AgentID)
	}
}

// =============================================================================
// Execute Tests - Critical Pressure
// =============================================================================

func TestExecute_EvictionAtCritical(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureCritical))

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	calls := cm.GetEvictionCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 eviction call at critical pressure, got %d", len(calls))
	}

	if calls[0].percent != EvictionPercentCritical {
		t.Errorf("Eviction percent = %v, want %v", calls[0].percent, EvictionPercentCritical)
	}
}

// =============================================================================
// Execute Tests - Agent Types
// =============================================================================

func TestEvictionHook_KnowledgeAgents(t *testing.T) {
	agents := []string{"librarian", "archivalist", "academic", "architect", "guide"}

	for _, agent := range agents {
		t.Run(agent, func(t *testing.T) {
			cm := &MockContextManager{}
			hook := NewPressureEvictionHook(PressureEvictionHookConfig{
				ContextManager: cm,
			})

			ctx := context.Background()
			data := createEvictionTestPromptData(agent, int(resources.PressureHigh))

			_, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			calls := cm.GetEvictionCalls()
			if len(calls) != 1 {
				t.Errorf("Expected eviction for %s agent", agent)
			}
		})
	}
}

func TestEvictionHook_SkipsNonKnowledgeAgent(t *testing.T) {
	nonKnowledgeAgents := []string{"engineer", "tester", "inspector", "designer", "orchestrator"}

	for _, agent := range nonKnowledgeAgents {
		t.Run(agent, func(t *testing.T) {
			cm := &MockContextManager{}
			hook := NewPressureEvictionHook(PressureEvictionHookConfig{
				ContextManager: cm,
			})

			ctx := context.Background()
			data := createEvictionTestPromptData(agent, int(resources.PressureHigh))

			result, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			if result.Modified {
				t.Error("Should not modify data for non-knowledge agent")
			}

			calls := cm.GetEvictionCalls()
			if len(calls) != 0 {
				t.Errorf("Expected no eviction for %s agent", agent)
			}
		})
	}
}

// =============================================================================
// Execute Tests - Edge Cases
// =============================================================================

func TestEvictionHook_SkipsWhenDisabled(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureCritical))

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}

	calls := cm.GetEvictionCalls()
	if len(calls) != 0 {
		t.Error("Should not evict when disabled")
	}
}

func TestExecute_SkipsNilContextManager(t *testing.T) {
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: nil,
	})

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureCritical))

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil context manager")
	}
}

func TestExecute_HandlesEvictionError(t *testing.T) {
	cm := &MockContextManager{}
	cm.SetError(errors.New("eviction failed"))

	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureHigh))

	result, err := hook.Execute(ctx, data)

	// Hook should not propagate error
	if err != nil {
		t.Fatalf("Execute should not return error, got %v", err)
	}

	// But result should contain the error
	if result.Error == nil {
		t.Error("Result.Error should contain the eviction error")
	}
}

// =============================================================================
// ToPromptHook Tests
// =============================================================================

func TestEvictionHook_ToPromptHook_ReturnsValidHook(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != PressureEvictionHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), PressureEvictionHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePrePrompt", promptHook.Phase())
	}

	if promptHook.Priority() != int(ctxpkg.HookPriorityFirst) {
		t.Errorf("PromptHook Priority = %d, want %d", promptHook.Priority(), ctxpkg.HookPriorityFirst)
	}
}

func TestEvictionHook_ToPromptHook_ExecutesCorrectly(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager: cm,
	})

	promptHook := hook.ToPromptHook()

	ctx := context.Background()
	data := createEvictionTestPromptData("librarian", int(resources.PressureHigh))

	result, err := promptHook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("PromptHook Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	calls := cm.GetEvictionCalls()
	if len(calls) != 1 {
		t.Error("PromptHook should trigger eviction")
	}
}

// =============================================================================
// Custom Eviction Percent Tests
// =============================================================================

func TestExecute_CustomEvictionPercents(t *testing.T) {
	cm := &MockContextManager{}
	hook := NewPressureEvictionHook(PressureEvictionHookConfig{
		ContextManager:          cm,
		HighEvictionPercent:     0.35,
		CriticalEvictionPercent: 0.70,
	})

	ctx := context.Background()

	// Test high pressure with custom percent
	data := createEvictionTestPromptData("librarian", int(resources.PressureHigh))
	_, _ = hook.Execute(ctx, data)

	calls := cm.GetEvictionCalls()
	if len(calls) != 1 || calls[0].percent != 0.35 {
		t.Errorf("Expected 0.35 eviction at high, got %v", calls[0].percent)
	}

	// Test critical pressure with custom percent
	data = createEvictionTestPromptData("librarian", int(resources.PressureCritical))
	_, _ = hook.Execute(ctx, data)

	calls = cm.GetEvictionCalls()
	if len(calls) != 2 || calls[1].percent != 0.70 {
		t.Errorf("Expected 0.70 eviction at critical, got %v", calls[1].percent)
	}
}

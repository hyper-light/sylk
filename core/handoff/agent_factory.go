package handoff

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

// AgentCreator creates a new agent instance. Implementations are closures
// that capture the original configuration used to bootstrap the agent.
type AgentCreator func(ctx context.Context) (HandoffableAgent, error)

// AgentFactory manages registered creators and produces new agent instances.
// It is used by the supervisor to create replacement agents during handoff.
type AgentFactory struct {
	mu       sync.RWMutex
	creators map[string]AgentCreator
	registry *DescriptorRegistry
}

// NewAgentFactory creates a factory backed by the given descriptor registry.
func NewAgentFactory(registry *DescriptorRegistry) *AgentFactory {
	return &AgentFactory{
		creators: make(map[string]AgentCreator),
		registry: registry,
	}
}

// RegisterCreator registers a creator function for the given agent type.
func (f *AgentFactory) RegisterCreator(agentType string, creator AgentCreator) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creators[agentType] = creator
}

// CreateAgent creates a new agent of the given type using the registered creator.
func (f *AgentFactory) CreateAgent(ctx context.Context, agentType string) (HandoffableAgent, error) {
	f.mu.RLock()
	creator, ok := f.creators[agentType]
	f.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no creator registered for agent type %q", agentType)
	}
	return creator(ctx)
}

// HasCreator returns true if a creator is registered for the given agent type.
func (f *AgentFactory) HasCreator(agentType string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.creators[agentType]
	return ok
}

// Registry returns the underlying descriptor registry.
func (f *AgentFactory) Registry() *DescriptorRegistry {
	return f.registry
}

// FactorySessionAdapter adapts AgentFactory to the SessionCreator interface
// expected by the existing HandoffExecutor. It translates session lifecycle
// operations into agent creation and context injection.
type FactorySessionAdapter struct {
	factory       *AgentFactory
	pendingAgents sync.Map // sessionID → HandoffableAgent

	sessionSeq atomic.Uint64
}

// NewFactorySessionAdapter creates a new adapter wrapping the given factory.
func NewFactorySessionAdapter(factory *AgentFactory) *FactorySessionAdapter {
	return &FactorySessionAdapter{factory: factory}
}

// CreateSession creates a new agent instance and stores it as a pending session.
// The agent type is extracted from SessionConfig.Metadata["agent_type"].
func (a *FactorySessionAdapter) CreateSession(ctx context.Context, cfg SessionConfig) (string, error) {
	agentType, ok := cfg.Metadata["agent_type"]
	if !ok {
		return "", fmt.Errorf("session config missing agent_type in metadata")
	}
	agentTypeStr, ok := agentType.(string)
	if !ok {
		return "", fmt.Errorf("agent_type metadata must be a string")
	}

	agent, err := a.factory.CreateAgent(WithFactoryCreationMetadata(ctx, FactoryCreationMetadata{
		AgentType: agentTypeStr,
		AgentID:   factoryMetadataString(cfg.Metadata, "agent_id"),
		TaskID:    factoryMetadataString(cfg.Metadata, "task_id"),
		TaskSlug:  factoryMetadataString(cfg.Metadata, "task_slug"),
	}), agentTypeStr)
	if err != nil {
		return "", fmt.Errorf("create agent %q: %w", agentTypeStr, err)
	}

	sessionID := fmt.Sprintf("handoff-%s-%d-%d",
		agentTypeStr,
		a.sessionSeq.Add(1),
		time.Now().UnixMilli(),
	)

	a.pendingAgents.Store(sessionID, agent)
	return sessionID, nil
}

// TransferContext injects the prepared context into the pending agent.
// Board-first: if the agent implements HandoffBoardInjectable and the
// transfer carries session metadata, attempt injection from the claims
// board before falling through to PreparedContext.
func (a *FactorySessionAdapter) TransferContext(ctx context.Context, sessionID string, transfer *ContextTransfer) error {
	val, ok := a.pendingAgents.Load(sessionID)
	if !ok {
		return fmt.Errorf("no pending agent for session %q", sessionID)
	}
	agent := val.(HandoffableAgent)

	// Board-first injection path.
	if boardInjectable, ok := agent.(HandoffBoardInjectable); ok {
		if sid := transfer.Metadata["session_id"]; sid != "" {
			if board := claims.DefaultSessionBoardRegistry().Lookup(sid); board != nil {
				claimIDsStr := transfer.Metadata["active_claim_ids"]
				claimIDs := strings.Split(claimIDsStr, ",")
				if err := boardInjectable.InjectFromBoard(board, claimIDs); err == nil {
					return nil // Board injection succeeded.
				} else {
					slog.Warn("handoff_board_injection_failed", "error", err.Error())
				}
			}
		}
	}

	// Fall through to PreparedContext injection.
	injectable, ok := agent.(HandoffInjectable)
	if !ok {
		// Agent doesn't accept context injection — not an error.
		return nil
	}

	if transfer.PreparedContext != nil {
		return injectable.InjectPreparedContext(transfer.PreparedContext)
	}

	return nil
}

// ActivateSession is a no-op for factory-based sessions — the agent is
// activated by the supervisor after TakeAgent.
func (a *FactorySessionAdapter) ActivateSession(_ context.Context, _ string) error {
	return nil
}

// DeactivateSession is a no-op for factory-based sessions.
func (a *FactorySessionAdapter) DeactivateSession(_ context.Context, _ string) error {
	return nil
}

// DeleteSession terminates and removes a pending agent.
func (a *FactorySessionAdapter) DeleteSession(ctx context.Context, sessionID string) error {
	val, ok := a.pendingAgents.LoadAndDelete(sessionID)
	if !ok {
		return nil
	}
	agent := val.(HandoffableAgent)
	return agent.Terminate(ctx)
}

// TakeAgent removes and returns the agent created for a session.
// Used by the bridge after handoff to get the new agent reference.
func (a *FactorySessionAdapter) TakeAgent(sessionID string) (HandoffableAgent, bool) {
	val, ok := a.pendingAgents.LoadAndDelete(sessionID)
	if !ok {
		return nil, false
	}
	return val.(HandoffableAgent), true
}

func factoryMetadataString(metadata map[string]interface{}, key string) string {
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	typed, ok := value.(string)
	if !ok {
		return ""
	}
	return typed
}

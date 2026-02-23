package container

import (
	"context"
	"log/slog"
	"sync"

	"github.com/adalundhe/sylk/core/container/network"
)

// AgentRouter is the subset of guide.Guide needed for agent registration.
// Defined here to avoid importing the guide package from the container layer.
type AgentRouter interface {
	MarkAgentReady(agentID string)
	UnregisterAgent(agentID string)
}

// PostStartRegistrationHook registers an agent with the ServiceRegistry
// and marks it ready with the Guide after its container starts.
// Implements both HookHandler and ContainerAwareHook — the container
// reference is injected by the HookRunner, providing the real agent ID
// rather than the spec-time name (which may differ from the runtime ID).
type PostStartRegistrationHook struct {
	mu        sync.Mutex
	container *Container
	guide     AgentRouter
	registry  *network.ServiceRegistry
	logger    *slog.Logger
}

// PostStartRegistrationHookConfig provides construction parameters.
type PostStartRegistrationHookConfig struct {
	Guide    AgentRouter
	Registry *network.ServiceRegistry
	Logger   *slog.Logger
}

// NewPostStartRegistrationHook creates a post-start hook for agent registration.
func NewPostStartRegistrationHook(cfg PostStartRegistrationHookConfig) *PostStartRegistrationHook {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &PostStartRegistrationHook{
		guide:    cfg.Guide,
		registry: cfg.Registry,
		logger:   logger,
	}
}

// BindContainer is called by the HookRunner before Execute, providing
// the owning container so the hook can read the agent's real ID and type.
func (h *PostStartRegistrationHook) BindContainer(c *Container) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.container = c
}

// Execute registers the agent with the ServiceRegistry using the real
// agent ID from the container and marks it ready with the Guide.
func (h *PostStartRegistrationHook) Execute(_ context.Context) error {
	h.mu.Lock()
	c := h.container
	h.mu.Unlock()

	if c == nil || c.Agent() == nil {
		h.logger.Warn("post-start hook: no container or agent bound")
		return nil
	}

	agent := c.Agent()
	agentID := agent.AgentID()
	agentType := agent.AgentType()

	if h.registry != nil {
		h.registry.Register(network.ServiceEndpoint{
			AgentID:   agentID,
			AgentType: agentType,
			Healthy:   true,
			Ready:     false, // readiness probe will flip this
		})
	}

	if h.guide != nil {
		h.guide.MarkAgentReady(agentID)
	}

	return nil
}

// PreStopDeregistrationHook removes an agent from the Guide routing table
// and ServiceRegistry before its container stops. Implements both
// HookHandler and ContainerAwareHook.
type PreStopDeregistrationHook struct {
	mu        sync.Mutex
	container *Container
	guide     AgentRouter
	registry  *network.ServiceRegistry
}

// NewPreStopDeregistrationHook creates a pre-stop hook for agent deregistration.
func NewPreStopDeregistrationHook(guide AgentRouter, registry *network.ServiceRegistry) *PreStopDeregistrationHook {
	return &PreStopDeregistrationHook{
		guide:    guide,
		registry: registry,
	}
}

// BindContainer is called by the HookRunner before Execute.
func (h *PreStopDeregistrationHook) BindContainer(c *Container) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.container = c
}

// Execute removes the agent from the ServiceRegistry and Guide routing table
// using the real agent ID from the container.
func (h *PreStopDeregistrationHook) Execute(_ context.Context) error {
	h.mu.Lock()
	c := h.container
	h.mu.Unlock()

	if c == nil || c.Agent() == nil {
		return nil
	}

	agentID := c.Agent().AgentID()

	if h.guide != nil {
		h.guide.UnregisterAgent(agentID)
	}
	if h.registry != nil {
		h.registry.Deregister(agentID)
	}
	return nil
}

package credentials

import (
	"context"
	"sync"
)

type credentialContextKeyType struct{}

var credentialContextKey = credentialContextKeyType{}

type CredentialContext struct {
	mu       sync.RWMutex
	broker   *CredentialBroker
	handles  map[string]*CredentialHandle
	resolved map[string]string
}

func NewCredentialContext(broker *CredentialBroker) *CredentialContext {
	return &CredentialContext{
		broker:   broker,
		handles:  make(map[string]*CredentialHandle),
		resolved: make(map[string]string),
	}
}

func (cc *CredentialContext) AddHandle(provider string, handle *CredentialHandle) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.handles[provider] = handle
}

func (cc *CredentialContext) Credential(provider string) (string, error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	if value, ok := cc.resolved[provider]; ok {
		return value, nil
	}

	handle, ok := cc.handles[provider]
	if !ok {
		return "", ErrHandleNotFound
	}

	value, err := cc.broker.ResolveHandle(handle.ID)
	if err != nil {
		return "", err
	}

	cc.resolved[provider] = value
	return value, nil
}

func (cc *CredentialContext) Cleanup() {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	for provider, handle := range cc.handles {
		if _, resolved := cc.resolved[provider]; !resolved {
			cc.broker.RevokeHandle(handle.ID)
		}
	}

	for k := range cc.resolved {
		cc.resolved[k] = ""
		delete(cc.resolved, k)
	}
	cc.handles = make(map[string]*CredentialHandle)
}

func (cc *CredentialContext) HandleIDs() []string {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	var ids []string
	for _, h := range cc.handles {
		ids = append(ids, h.ID)
	}
	return ids
}

func WithCredentialContext(ctx context.Context, cc *CredentialContext) context.Context {
	return context.WithValue(ctx, credentialContextKey, cc)
}

func GetCredentialContext(ctx context.Context) *CredentialContext {
	if cc, ok := ctx.Value(credentialContextKey).(*CredentialContext); ok {
		return cc
	}
	return nil
}

type ToolCredentialRequirements interface {
	GetRequiredCredentials(toolName string) []string
}

type PreToolCredentialHookHandler struct {
	broker       *CredentialBroker
	requirements ToolCredentialRequirements
}

func NewPreToolCredentialHook(broker *CredentialBroker, reqs ToolCredentialRequirements) *PreToolCredentialHookHandler {
	return &PreToolCredentialHookHandler{
		broker:       broker,
		requirements: reqs,
	}
}

func (h *PreToolCredentialHookHandler) Name() string {
	return "credential_injection"
}

func (h *PreToolCredentialHookHandler) Priority() int {
	return 20
}

type ToolHookData struct {
	ToolName   string
	Parameters map[string]interface{}
	AgentID    string
	AgentType  string
	ToolCallID string
	Context    context.Context
}

func (h *PreToolCredentialHookHandler) Handle(ctx context.Context, data *ToolHookData) (context.Context, error) {
	if data == nil {
		return ctx, nil
	}

	required := h.getRequiredCredentials(data.ToolName)
	if len(required) == 0 {
		return ctx, nil
	}

	credCtx := NewCredentialContext(h.broker)

	for _, provider := range required {
		handle, err := h.broker.RequestCredential(
			ctx,
			data.AgentID,
			data.AgentType,
			provider,
			data.ToolCallID,
			"tool execution: "+data.ToolName,
		)
		if err != nil {
			credCtx.Cleanup()
			return ctx, err
		}
		credCtx.AddHandle(provider, handle)
	}

	return WithCredentialContext(ctx, credCtx), nil
}

func (h *PreToolCredentialHookHandler) getRequiredCredentials(toolName string) []string {
	if h.requirements == nil {
		return nil
	}
	return h.requirements.GetRequiredCredentials(toolName)
}

type PostToolCredentialHookHandler struct{}

func NewPostToolCredentialHook() *PostToolCredentialHookHandler {
	return &PostToolCredentialHookHandler{}
}

func (h *PostToolCredentialHookHandler) Name() string {
	return "credential_cleanup"
}

func (h *PostToolCredentialHookHandler) Priority() int {
	return 100
}

func (h *PostToolCredentialHookHandler) Handle(ctx context.Context, data *ToolHookData) error {
	credCtx := GetCredentialContext(ctx)
	if credCtx == nil {
		return nil
	}

	credCtx.Cleanup()
	return nil
}

package archivalist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

const archivalistQueueKeepaliveInterval = 10 * time.Second

type archivalistToolBundle struct {
	skills        *skills.Registry
	loader        *skills.Loader
	runtime       *toolruntime.Runtime
	toolDefsDirty bool
}

func (a *Archivalist) newForwardedToolBundle() (*archivalistToolBundle, error) {
	registry, err := skills.CloneRegistry(a.skills)
	if err != nil {
		return nil, err
	}
	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = archivalistVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil
	loader := skills.NewLoader(registry, loaderCfg)
	runtime, err := toolruntime.New(toolruntime.Config{
		Registry: registry,
		Hooks:    a.hooks,
		Manifest: archivalistToolManifest(registry),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize archivalist forwarded tool runtime: %w", err)
	}
	runtime.SyncActiveFromLoaded()
	return &archivalistToolBundle{
		skills:  registry,
		loader:  loader,
		runtime: runtime,
	}, nil
}

func (b *archivalistToolBundle) Close() {
	if b == nil || b.runtime == nil {
		return
	}
	b.runtime.Close()
}

func (b *archivalistToolBundle) prepareSkillsForInput(input string) {
	if b == nil || b.loader == nil {
		return
	}
	b.loader.LoadForInput(input)
	b.loader.OptimizeForBudget()
}

func (b *archivalistToolBundle) buildToolDefinitions() []providers.Tool {
	if b == nil || b.runtime == nil {
		return nil
	}
	b.runtime.SyncActiveFromLoaded()
	return b.runtime.BuildToolDefinitions()
}

func (b *archivalistToolBundle) executeToolCall(ctx context.Context, agentID string, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	if b == nil || b.runtime == nil {
		return toolruntime.ExecutionResult{}, fmt.Errorf("archivalist forwarded tool runtime is not configured")
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = strings.TrimSpace(agentID) + "-local"
	}
	return b.runtime.Execute(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        call.ID,
			Name:      name,
			Arguments: raw,
		},
		AgentID:         b.runtime.AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: b.runtime.CapabilityScope(),
	})
}

func (b *archivalistToolBundle) toolInvocations(ctx context.Context, agentID string, calls []providers.ToolCall) []toolruntime.Invocation {
	if b == nil || b.runtime == nil || len(calls) == 0 {
		return nil
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = strings.TrimSpace(agentID) + "-local"
	}
	scope := b.runtime.CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         b.runtime.AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

func (a *Archivalist) publishReplicaActivityForRequest(
	sessionID, correlationID string,
	eventType events.EventType,
	content string,
	snapshot shared.RequestReplicaPoolSnapshot,
) {
	if a.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, strings.TrimSpace(sessionID), content)
	evt.CorrelationID = strings.TrimSpace(correlationID)
	evt.AgentID = a.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "archivalist"
	evt.Data["agent_name"] = "Archivalist"
	evt.Data["active_replicas"] = snapshot.Active
	evt.Data["max_replicas"] = snapshot.MaxActive
	evt.Data["queued_requests"] = snapshot.Queued
	evt.Data["max_queued_requests"] = snapshot.MaxQueued
	if snapshot.Active > 0 || snapshot.Queued > 0 {
		events.SetAgentUIState(evt, events.AgentUIStateSearching)
	}
	a.activityPub.PublishActivity(evt)
}

func (a *Archivalist) startQueueKeepalive(ctx context.Context, queuePosition int) func() {
	pp := shared.ProgressPublisherFromContext(ctx)
	if pp == nil {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(archivalistQueueKeepaliveInterval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				pp.PublishState(events.AgentUIStateSearching, shared.KnowledgeQueueProgressMessage("archivalist", a.requestPool.Snapshot(), queuePosition))
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

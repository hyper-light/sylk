package librarian

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

const librarianQueueKeepaliveInterval = 10 * time.Second

type librarianToolBundle struct {
	skills        *skills.Registry
	loader        *skills.Loader
	runtime       *toolruntime.Runtime
	toolDefsDirty bool
}

func (l *Librarian) newForwardedToolBundle() (*librarianToolBundle, error) {
	registry, err := skills.CloneRegistry(l.skills)
	if err != nil {
		return nil, err
	}
	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = librarianVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil
	loader := skills.NewLoader(registry, loaderCfg)
	runtime, err := toolruntime.New(toolruntime.Config{
		Registry: registry,
		Manifest: librarianToolManifest(registry),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize librarian forwarded tool runtime: %w", err)
	}
	runtime.SyncActiveFromLoaded()
	return &librarianToolBundle{
		skills:  registry,
		loader:  loader,
		runtime: runtime,
	}, nil
}

func (b *librarianToolBundle) Close() {
	if b == nil || b.runtime == nil {
		return
	}
	b.runtime.Close()
}

func (b *librarianToolBundle) prepareSkillsForInput(input string) {
	if b == nil || b.loader == nil {
		return
	}
	b.loader.LoadForInput(input)
	b.loader.OptimizeForBudget()
}

func (b *librarianToolBundle) buildToolDefinitions() []providers.Tool {
	if b == nil || b.runtime == nil {
		return nil
	}
	b.runtime.SyncActiveFromLoaded()
	return b.runtime.BuildToolDefinitions()
}

func (b *librarianToolBundle) executeToolCall(ctx context.Context, agentID string, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	if b == nil || b.runtime == nil {
		return toolruntime.ExecutionResult{}, fmt.Errorf("librarian forwarded tool runtime is not configured")
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

func (b *librarianToolBundle) toolInvocations(ctx context.Context, agentID string, calls []providers.ToolCall) []toolruntime.Invocation {
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

func (l *Librarian) publishReplicaActivityForRequest(
	sessionID, correlationID string,
	eventType events.EventType,
	content string,
	snapshot shared.RequestReplicaPoolSnapshot,
) {
	if l.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, strings.TrimSpace(sessionID), content)
	evt.CorrelationID = strings.TrimSpace(correlationID)
	evt.AgentID = l.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "librarian"
	evt.Data["agent_name"] = "Librarian"
	evt.Data["active_replicas"] = snapshot.Active
	evt.Data["max_replicas"] = snapshot.MaxActive
	evt.Data["queued_requests"] = snapshot.Queued
	evt.Data["max_queued_requests"] = snapshot.MaxQueued
	if snapshot.Active > 0 || snapshot.Queued > 0 {
		events.SetAgentUIState(evt, events.AgentUIStateSearching)
	}
	l.activityPub.PublishActivity(evt)
}

func (l *Librarian) startQueueKeepalive(ctx context.Context, queuePosition int) func() {
	pp := shared.ProgressPublisherFromContext(ctx)
	if pp == nil {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(librarianQueueKeepaliveInterval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				pp.PublishState(events.AgentUIStateSearching, shared.KnowledgeQueueProgressMessage("librarian", l.requestPool.Snapshot(), queuePosition))
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

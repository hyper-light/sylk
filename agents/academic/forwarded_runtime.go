package academic

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

const academicQueueKeepaliveInterval = 10 * time.Second

type academicToolBundle struct {
	skills        *skills.Registry
	loader        *skills.Loader
	runtime       *toolruntime.Runtime
	toolDefsDirty bool
}

type academicToolBundleKey struct{}

func withAcademicToolBundle(ctx context.Context, bundle *academicToolBundle) context.Context {
	if bundle == nil {
		return ctx
	}
	return context.WithValue(ctx, academicToolBundleKey{}, bundle)
}

func academicToolBundleFromContext(ctx context.Context) *academicToolBundle {
	if ctx == nil {
		return nil
	}
	bundle, _ := ctx.Value(academicToolBundleKey{}).(*academicToolBundle)
	return bundle
}

func (a *Academic) newForwardedToolBundle() (*academicToolBundle, error) {
	registry, err := skills.CloneRegistry(a.skills)
	if err != nil {
		return nil, err
	}
	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = academicVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil
	loader := skills.NewLoader(registry, loaderCfg)
	runtime, err := toolruntime.New(toolruntime.Config{
		Registry: registry,
		Hooks:    a.hooks,
		Manifest: academicToolManifest(registry),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize academic forwarded tool runtime: %w", err)
	}
	runtime.SyncActiveFromLoaded()
	return &academicToolBundle{
		skills:  registry,
		loader:  loader,
		runtime: runtime,
	}, nil
}

func (b *academicToolBundle) Close() {
	if b == nil || b.runtime == nil {
		return
	}
	b.runtime.Close()
}

func (b *academicToolBundle) prepareSkillsForInput(input string) {
	if b == nil || b.loader == nil {
		return
	}
	b.loader.LoadForInput(input)
	b.loader.OptimizeForBudget()
}

func (b *academicToolBundle) requestSurface(extraTools ...string) toolruntime.Surface {
	if b == nil || b.runtime == nil {
		return nil
	}
	if len(extraTools) == 0 {
		return b.runtime
	}
	view, err := b.runtime.RequestView(extraTools...)
	if err != nil {
		return b.runtime
	}
	return view
}

func (b *academicToolBundle) buildToolDefinitionsWithSurface(surface toolruntime.Surface) []providers.Tool {
	if surface == nil {
		return nil
	}
	surface.SyncActiveFromLoaded()
	return surface.BuildToolDefinitions()
}

func (b *academicToolBundle) loadedSkills() []*skills.Skill {
	if b == nil || b.skills == nil {
		return nil
	}
	return b.skills.GetLoaded()
}

func academicPrepareSkillsForInput(a *Academic, ctx context.Context, input string) {
	if bundle := academicToolBundleFromContext(ctx); bundle != nil {
		bundle.prepareSkillsForInput(input)
		return
	}
	if a != nil {
		a.prepareSkillsForInput(input)
	}
}

func (a *Academic) publishReplicaActivityForRequest(
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
	evt.Data["agent_type"] = "academic"
	evt.Data["agent_name"] = "Academic"
	evt.Data["active_replicas"] = snapshot.Active
	evt.Data["max_replicas"] = snapshot.MaxActive
	evt.Data["queued_requests"] = snapshot.Queued
	evt.Data["max_queued_requests"] = snapshot.MaxQueued
	if snapshot.Active > 0 || snapshot.Queued > 0 {
		events.SetAgentUIState(evt, events.AgentUIStateSearching)
	}
	a.activityPub.PublishActivity(evt)
}

func (a *Academic) startQueueKeepalive(ctx context.Context, queuePosition int) func() {
	pp := shared.ProgressPublisherFromContext(ctx)
	if pp == nil {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(academicQueueKeepaliveInterval)
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				pp.PublishState(events.AgentUIStateSearching, shared.KnowledgeQueueProgressMessage("academic", a.requestPool.Snapshot(), queuePosition))
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

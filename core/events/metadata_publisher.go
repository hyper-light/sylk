package events

import (
	"strings"
	"sync"
)

// AgentIdentityMetadata holds stable identity defaults for a known agent ID.
type AgentIdentityMetadata struct {
	AgentType string
	AgentName string
}

type cachedAgentMetadata struct {
	PanelID    string
	AgentType  string
	AgentName  string
	PipelineID string
	TaskID     string
	TaskName   string
	TaskSlug   string
}

// MetadataCachingPublisher enriches activity events with cached agent metadata
// before forwarding them to the underlying publisher. This keeps follow-on LLM
// and tool telemetry attached to the correct UI row even when those events do
// not carry full agent metadata.
type MetadataCachingPublisher struct {
	base  ActivityPublisher
	known map[string]AgentIdentityMetadata

	mu    sync.RWMutex
	cache map[string]cachedAgentMetadata
}

// NewMetadataCachingPublisher wraps an ActivityPublisher with metadata caching.
func NewMetadataCachingPublisher(
	base ActivityPublisher,
	known map[string]AgentIdentityMetadata,
) *MetadataCachingPublisher {
	if base == nil {
		return nil
	}
	clonedKnown := make(map[string]AgentIdentityMetadata, len(known))
	for id, meta := range known {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			continue
		}
		clonedKnown[trimmedID] = AgentIdentityMetadata{
			AgentType: strings.TrimSpace(meta.AgentType),
			AgentName: strings.TrimSpace(meta.AgentName),
		}
	}
	return &MetadataCachingPublisher{
		base:  base,
		known: clonedKnown,
		cache: make(map[string]cachedAgentMetadata, len(clonedKnown)*2),
	}
}

// PublishActivity enriches and forwards the activity event. Errors from the
// wrapped publisher are propagated so callers can react to a closed bus or
// other transport failures.
func (p *MetadataCachingPublisher) PublishActivity(event *ActivityEvent) error {
	if p == nil || p.base == nil || event == nil {
		return nil
	}

	cloned := cloneActivityEvent(event)
	rawID := strings.TrimSpace(cloned.AgentID)
	if cloned.Data == nil {
		cloned.Data = make(map[string]any)
	}

	meta := p.resolveMetadata(rawID, cloned.Data)
	if meta.AgentType != "" && extractMetadataString(cloned.Data, "agent_type") == "" {
		cloned.Data["agent_type"] = meta.AgentType
	}
	if meta.AgentName != "" && extractMetadataString(cloned.Data, "agent_name") == "" {
		cloned.Data["agent_name"] = meta.AgentName
	}
	if meta.PipelineID != "" {
		cloned.Data["pipeline_id"] = meta.PipelineID
	}
	if meta.TaskID != "" && extractMetadataString(cloned.Data, "task_id") == "" {
		cloned.Data["task_id"] = meta.TaskID
	}
	if meta.TaskName != "" && extractMetadataString(cloned.Data, "task_name") == "" {
		cloned.Data["task_name"] = meta.TaskName
	}
	if meta.TaskSlug != "" && extractMetadataString(cloned.Data, "task_slug") == "" {
		cloned.Data["task_slug"] = meta.TaskSlug
	}

	if meta.PanelID != "" && rawID != "" && rawID != meta.PanelID {
		if extractMetadataString(cloned.Data, "runtime_agent_id") == "" {
			cloned.Data["runtime_agent_id"] = rawID
		}
		cloned.AgentID = meta.PanelID
	}

	p.rememberMetadata(rawID, cloned)
	return p.base.PublishActivity(cloned)
}

func (p *MetadataCachingPublisher) resolveMetadata(agentID string, data map[string]any) cachedAgentMetadata {
	taskID := extractMetadataString(data, "task_id")
	meta := cachedAgentMetadata{
		AgentType:  extractMetadataString(data, "agent_type"),
		AgentName:  extractMetadataString(data, "agent_name"),
		PipelineID: logicalPipelineID(extractMetadataString(data, "pipeline_id"), taskID),
		TaskID:     taskID,
		TaskName:   extractMetadataString(data, "task_name"),
		TaskSlug:   extractMetadataString(data, "task_slug"),
	}

	if known, ok := p.known[agentID]; ok {
		if meta.AgentType == "" {
			meta.AgentType = known.AgentType
		}
		if meta.AgentName == "" {
			meta.AgentName = known.AgentName
		}
	}

	if agentID != "" {
		if cached, ok := p.cached(agentID); ok {
			if meta.AgentType == "" {
				meta.AgentType = cached.AgentType
			}
			if meta.AgentName == "" {
				meta.AgentName = cached.AgentName
			}
			if meta.PipelineID == "" {
				meta.PipelineID = cached.PipelineID
			}
			if meta.TaskID == "" {
				meta.TaskID = cached.TaskID
			}
			if meta.TaskName == "" {
				meta.TaskName = cached.TaskName
			}
			if meta.TaskSlug == "" {
				meta.TaskSlug = cached.TaskSlug
			}
			if meta.PanelID == "" {
				meta.PanelID = cached.PanelID
			}
		}
	}

	if panelID := metadataPipelinePanelID(meta.AgentType, meta.PipelineID); panelID != "" {
		meta.PanelID = panelID
	} else if meta.PanelID == "" {
		meta.PanelID = agentID
	}

	return meta
}

func (p *MetadataCachingPublisher) rememberMetadata(rawID string, event *ActivityEvent) {
	if event == nil {
		return
	}
	data := event.Data
	meta := cachedAgentMetadata{
		PanelID:    strings.TrimSpace(event.AgentID),
		AgentType:  extractMetadataString(data, "agent_type"),
		AgentName:  extractMetadataString(data, "agent_name"),
		PipelineID: logicalPipelineID(extractMetadataString(data, "pipeline_id"), extractMetadataString(data, "task_id")),
		TaskID:     extractMetadataString(data, "task_id"),
		TaskName:   extractMetadataString(data, "task_name"),
		TaskSlug:   extractMetadataString(data, "task_slug"),
	}
	if meta.PanelID == "" && rawID == "" {
		return
	}
	p.storeCached(rawID, meta)
	p.storeCached(extractMetadataString(data, "runtime_agent_id"), meta)
	p.storeCached(meta.PanelID, meta)
}

func (p *MetadataCachingPublisher) cached(agentID string) (cachedAgentMetadata, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	meta, ok := p.cache[agentID]
	return meta, ok
}

func (p *MetadataCachingPublisher) storeCached(agentID string, meta cachedAgentMetadata) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[agentID] = meta
}

// metadataPipelinePanelID returns the per-pipeline panel row key for an
// agent emission. Only task-scoped pipeline workers (engineer, designer,
// inspector-pipeline, tester-pipeline) get rekeyed — each pipeline spawns
// its own replica of these, so they legitimately render as distinct rows.
//
// The orchestrator is intentionally excluded: it is a singleton control-
// plane agent that orchestrates many pipelines concurrently, and its row
// must collapse to a single "orchestrator" entry regardless of which
// task_id / pipeline_id happens to be tagged on a given event. This list
// must stay aligned with agents/orchestrator/pipeline_pod.go's
// PipelinePanelAgentTypes (source of truth for per-pipeline workers).
func metadataPipelinePanelID(agentType, pipelineID string) string {
	agentType = strings.TrimSpace(agentType)
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return ""
	}
	switch agentType {
	case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
		return pipelineID + ":" + agentType
	default:
		return ""
	}
}

func logicalPipelineID(pipelineID, taskID string) string {
	if trimmed := strings.TrimSpace(taskID); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(pipelineID)
}

func extractMetadataString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, ok := data[key]
	if !ok {
		return ""
	}
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(str)
}

func cloneActivityEvent(event *ActivityEvent) *ActivityEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	if event.Data != nil {
		cloned.Data = make(map[string]any, len(event.Data))
		for key, value := range event.Data {
			cloned.Data[key] = value
		}
	}
	if event.Keywords != nil {
		cloned.Keywords = append([]string(nil), event.Keywords...)
	}
	return &cloned
}

var _ ActivityPublisher = (*MetadataCachingPublisher)(nil)

package shared

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
)

type handoffBridgeContextKey struct{}

// WithHandoffBridge stores the effective handoff bridge for the current request.
func WithHandoffBridge(ctx context.Context, bridge *handoff.HandoffBridge) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, handoffBridgeContextKey{}, bridge)
}

// HandoffBridgeFromContext retrieves the request-scoped handoff bridge.
func HandoffBridgeFromContext(ctx context.Context) *handoff.HandoffBridge {
	if ctx == nil {
		return nil
	}
	bridge, _ := ctx.Value(handoffBridgeContextKey{}).(*handoff.HandoffBridge)
	return bridge
}

// EffectiveHandoffBridge returns the request-scoped handoff bridge when present,
// otherwise it falls back to the agent's singleton bridge.
func EffectiveHandoffBridge(ctx context.Context, fallback *handoff.HandoffBridge) *handoff.HandoffBridge {
	if bridge := HandoffBridgeFromContext(ctx); bridge != nil {
		return bridge
	}
	return fallback
}

// KnowledgeReplicaHandoffOptions configures a request-scoped handoff bridge for
// a forwarded knowledge-agent replica.
type KnowledgeReplicaHandoffOptions struct {
	SessionID         string
	CorrelationID     string
	ActivityPublisher events.ActivityPublisher

	// Factory, when supplied together with ParentIdentity, causes
	// AttachReplicaHandoffBridge to mint a typed replica identity via
	// Factory.MintReplica and overlay it on the returned ctx. The
	// accountant then attributes tokens to the replica's distinct
	// UID (with Owner back-pointing to the canonical parent) instead
	// of lumping replica usage into the parent bucket. When Factory
	// or ParentIdentity is nil the replica-identity overlay is
	// skipped and the canonical identity already on ctx (if any)
	// remains in effect.
	Factory        *identity.Factory
	ParentIdentity *identity.AgentIdentity
}

// ReplicaHandoffAgentID returns the internal runtime ID for a request-scoped
// handoff bridge backing a knowledge-agent replica.
func ReplicaHandoffAgentID(agentID, correlationID string) string {
	agentID = strings.TrimSpace(agentID)
	correlationID = strings.TrimSpace(correlationID)
	if agentID == "" {
		return ""
	}
	if correlationID == "" {
		return agentID
	}
	return fmt.Sprintf("%s#replica-%s", agentID, correlationID)
}

// AttachReplicaHandoffBridge registers a request-scoped handoff bridge for a
// forwarded knowledge-agent request and stores it on the returned context.
func AttachReplicaHandoffBridge(
	ctx context.Context,
	agent handoff.ContextEvictable,
	parent *handoff.HandoffBridge,
	opts KnowledgeReplicaHandoffOptions,
) (context.Context, func(), *handoff.HandoffBridge, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if agent == nil || parent == nil {
		return ctx, func() {}, nil, nil
	}

	supervisor := parent.Supervisor()
	if supervisor == nil {
		return ctx, func() {}, nil, fmt.Errorf("handoff bridge missing supervisor")
	}

	canonicalAgentID := strings.TrimSpace(agent.AgentID())
	if canonicalAgentID == "" {
		return ctx, func() {}, nil, fmt.Errorf("missing canonical agent ID for replica handoff bridge")
	}

	replicaID := ReplicaHandoffAgentID(canonicalAgentID, opts.CorrelationID)
	if strings.TrimSpace(replicaID) == "" || replicaID == canonicalAgentID {
		return ctx, func() {}, nil, fmt.Errorf("missing correlation ID for replica handoff bridge")
	}

	replicaAgent := &replicaHandoffAgent{
		base:             agent,
		replicaID:        replicaID,
		canonicalAgentID: canonicalAgentID,
		correlationID:    strings.TrimSpace(opts.CorrelationID),
		sessionID:        strings.TrimSpace(opts.SessionID),
	}

	bridge, err := supervisor.RegisterAgent(replicaAgent)
	if err != nil {
		return ctx, func() {}, nil, err
	}

	if opts.ActivityPublisher != nil {
		bridge.SetActivityPublisher(&replicaHandoffActivityPublisher{
			base:             opts.ActivityPublisher,
			canonicalAgentID: canonicalAgentID,
			runtimeAgentID:   replicaID,
		})
	}

	ctx = WithHandoffBridge(ctx, bridge)
	ctx = WithStreamContextMetadata(ctx, MergeStreamMetadata(StreamResponseMetadataFromContext(ctx), map[string]any{
		"runtime_agent_id":   runtimeIDForStream(replicaID),
		"handoff_replica_id": replicaID,
	}))
	if opts.Factory != nil && opts.ParentIdentity != nil {
		if replicaIdentity, mintErr := opts.Factory.MintReplica(identity.MintReplicaOptions{
			Parent: opts.ParentIdentity,
		}); mintErr == nil {
			ctx = identity.WithIdentity(ctx, replicaIdentity)
		}
	}
	cleanup := func() {
		_ = supervisor.UnregisterAgent(replicaID)
	}
	return ctx, cleanup, bridge, nil
}

func runtimeIDForStream(replicaID string) string {
	return strings.TrimSpace(replicaID)
}

type replicaHandoffAgent struct {
	base             handoff.ContextEvictable
	replicaID        string
	canonicalAgentID string
	correlationID    string
	sessionID        string
}

func (a *replicaHandoffAgent) AgentID() string {
	return a.replicaID
}

func (a *replicaHandoffAgent) AgentType() string {
	return a.base.AgentType()
}

func (a *replicaHandoffAgent) Descriptor() handoff.AgentDescriptor {
	return a.base.Descriptor()
}

func (a *replicaHandoffAgent) ExtractArchivableState() *handoff.ArchivableState {
	state := &handoff.ArchivableState{
		AgentID:   a.replicaID,
		AgentType: a.base.AgentType(),
		Timestamp: time.Now(),
		State: map[string]string{
			"canonical_agent_id": a.canonicalAgentID,
			"handoff_replica_id": a.replicaID,
		},
	}
	if a.correlationID != "" {
		state.State["correlation_id"] = a.correlationID
	}
	if a.sessionID != "" {
		state.State["session_id"] = a.sessionID
	}

	if baseState := a.base.ExtractArchivableState(); baseState != nil {
		if baseState.AgentType != "" {
			state.AgentType = baseState.AgentType
		}
		if !baseState.Timestamp.IsZero() {
			state.Timestamp = baseState.Timestamp
		}
		for key, value := range baseState.State {
			trimmedKey := strings.TrimSpace(key)
			trimmedValue := strings.TrimSpace(value)
			if trimmedKey == "" || trimmedValue == "" {
				continue
			}
			state.State[trimmedKey] = trimmedValue
		}
	}

	state.State["canonical_agent_id"] = a.canonicalAgentID
	state.State["handoff_replica_id"] = a.replicaID
	if a.correlationID != "" {
		state.State["correlation_id"] = a.correlationID
	}
	if a.sessionID != "" {
		state.State["session_id"] = a.sessionID
	}
	return state
}

func (a *replicaHandoffAgent) Terminate(ctx context.Context) error {
	return a.base.Terminate(ctx)
}

func (a *replicaHandoffAgent) EvictEntries(candidates []handoff.EvictionCandidate) (int, error) {
	return a.base.EvictEntries(candidates)
}

type replicaHandoffActivityPublisher struct {
	base             events.ActivityPublisher
	canonicalAgentID string
	runtimeAgentID   string
}

func (p *replicaHandoffActivityPublisher) PublishActivity(event *events.ActivityEvent) error {
	if p == nil || p.base == nil || event == nil {
		return nil
	}

	cloned := &events.ActivityEvent{
		ID:            event.ID,
		EventType:     event.EventType,
		Timestamp:     event.Timestamp,
		SessionID:     event.SessionID,
		CorrelationID: event.CorrelationID,
		AgentID:       p.canonicalAgentID,
		Content:       event.Content,
		Summary:       event.Summary,
		Category:      event.Category,
		FilePaths:     append([]string(nil), event.FilePaths...),
		Keywords:      append([]string(nil), event.Keywords...),
		RelatedIDs:    append([]string(nil), event.RelatedIDs...),
		Outcome:       event.Outcome,
		Importance:    event.Importance,
		Visibility:    event.Visibility,
	}
	if event.Data != nil {
		cloned.Data = make(map[string]any, len(event.Data)+3)
		for key, value := range event.Data {
			cloned.Data[key] = value
		}
	} else {
		cloned.Data = make(map[string]any, 3)
	}
	cloned.Data["runtime_agent_id"] = p.runtimeAgentID
	if _, ok := cloned.Data["handoff_replica_id"]; !ok {
		cloned.Data["handoff_replica_id"] = p.runtimeAgentID
	}
	if _, ok := cloned.Data["canonical_agent_id"]; !ok {
		cloned.Data["canonical_agent_id"] = p.canonicalAgentID
	}

	return p.base.PublishActivity(cloned)
}

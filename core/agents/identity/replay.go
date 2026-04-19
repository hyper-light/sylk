package identity

// This file exposes the narrow replay path used by WAL-restore and
// session-restore code. Replay deliberately bypasses Factory:
//
//   - UIDs already exist on disk — re-minting would assign a new one
//   - ordinal registration would collide with the recorded Name
//   - model / pod registries are recreated per session, and historical
//     entries may reference models that are no longer registered
//
// Callers that need a *fresh* identity must still use Factory.Mint /
// MintReplica / WithNewGeneration. Replay is strictly for rehydrating
// values that were durably recorded in a prior live session.

// ReplayAgentIdentity is the flat input shape for RebuildForReplay.
// Every field is required except Labels and Owner. The replay path
// trusts the caller — validation belongs at the write site, not here.
type ReplayAgentIdentity struct {
	UID        UID
	Namespace  Namespace
	Pod        PodRef
	Name       Name
	Kind       AgentType
	Category   Category
	Model      ModelID
	Generation Generation
	Window     ContextWindow
	Labels     Labels
	Owner      *OwnerRef
}

// RebuildForReplay reconstructs an AgentIdentity from persisted
// fields. It does NOT touch the factory, ordinal registry, model
// registry, or pod registry. The returned *AgentIdentity is
// structurally identical to one Mint would have produced at the time
// the record was written.
//
// Labels are deep-copied; Owner is value-copied. Mutations by the
// caller after return do not affect the identity.
func RebuildForReplay(r ReplayAgentIdentity) *AgentIdentity {
	return &AgentIdentity{
		uid:        r.UID,
		namespace:  r.Namespace,
		pod:        r.Pod,
		name:       r.Name,
		kind:       r.Kind,
		category:   r.Category,
		model:      r.Model,
		generation: r.Generation,
		window:     r.Window,
		labels:     r.Labels.Clone(),
		owner:      clonedOwner(r.Owner),
	}
}

// ReplayTaskRef is the flat input shape for RebuildTaskForReplay.
type ReplayTaskRef struct {
	UID         TaskUID
	DisplayID   string
	Correlation CorrelationID
	Namespace   Namespace
	Pipeline    *PipelineRef
	Labels      Labels
}

// RebuildTaskForReplay reconstructs a TaskRef from persisted fields.
// Mirrors RebuildForReplay: skips Factory, deep-copies Labels, value-
// copies Pipeline.
func RebuildTaskForReplay(r ReplayTaskRef) *TaskRef {
	return &TaskRef{
		uid:         r.UID,
		displayID:   r.DisplayID,
		pipeline:    clonedPipelineRef(r.Pipeline),
		correlation: r.Correlation,
		namespace:   r.Namespace,
		labels:      r.Labels.Clone(),
	}
}

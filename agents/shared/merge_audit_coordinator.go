package shared

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/versioning"
)

// AuditReplicaSpawner is the direct-dispatch interface a global
// agent (inspector or tester) implements so the audit coordinator
// can launch a per-merge replica without going through the bus.
// Implementations must:
//   - Derive the replica's deterministic identity via
//     MergeReplicaAgentID(agentType, sessionID, req.Descriptor.MergedVersion).
//   - Scope the replica's steering ledger + context to that identity
//     so resume rehydrates correctly.
//   - Return when the spawn has been *accepted* for execution — not
//     when the audit completes. The audit runs asynchronously; the
//     replica calls back via the AuditDecisionFinalizer on ctx.
type AuditReplicaSpawner interface {
	SpawnAuditReplica(ctx context.Context, req *AuditMergeRequest) error
}

// AuditDecisionFinalizer is a direct callback a replica invokes when
// its audit produces a terminal verdict. Carried on ctx by the
// coordinator so emit_audit_decision (and any other audit-scoped
// skill) can locate it without bus indirection.
type AuditDecisionFinalizer func(result *AuditMergeResult)

type auditFinalizerCtxKey struct{}

// WithAuditDecisionFinalizer attaches a finalizer to ctx. Called by
// the audit coordinator before handing ctx to the replica's
// SpawnAuditReplica. The emit_audit_decision skill reads the
// finalizer via AuditDecisionFinalizerFromContext and invokes it
// directly — no bus publish required for the dispatch-path loop.
func WithAuditDecisionFinalizer(ctx context.Context, f AuditDecisionFinalizer) context.Context {
	if f == nil {
		return ctx
	}
	return context.WithValue(ctx, auditFinalizerCtxKey{}, f)
}

// AuditDecisionFinalizerFromContext returns the finalizer attached
// by WithAuditDecisionFinalizer, or nil if none is present.
func AuditDecisionFinalizerFromContext(ctx context.Context) AuditDecisionFinalizer {
	if ctx == nil {
		return nil
	}
	f, _ := ctx.Value(auditFinalizerCtxKey{}).(AuditDecisionFinalizer)
	return f
}

// MergeAuditCoordinator is the direct-dispatch pump that ties
// SessionVFS merge completions to per-merge inspector + tester
// replicas. There is no orchestrator involvement, and no bus
// broadcast for dispatch: the coordinator registers a synchronous
// MergeCallback on SessionVFS, and on fire it invokes
// spawner.SpawnAuditReplica(ctx, req) directly. Each replica's
// terminal decision flows back through a ctx-scoped
// AuditDecisionFinalizer — again no bus.
//
// The coordinator optionally publishes an AuditMergeResult to
// bus observers (TUI, architect remediation, observability) AFTER
// the internal state machine has advanced. That's a notification,
// not part of the dispatch loop.
//
// Lifetime: created alongside SessionVFS in the session-lifecycle
// wiring. Started after SessionVFS.Open succeeds; stopped before
// SessionVFS.Close (so the callback registration is torn down
// before the session's state machine goes away).
type MergeAuditCoordinator struct {
	session   *versioning.SessionVFS
	inspector AuditReplicaSpawner
	tester    AuditReplicaSpawner
	bus       guide.EventBus // optional; only for observability publishes
	logger    *slog.Logger

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	partials map[versioning.SemanticVersion]*mergePartialVerdicts
	inFlight map[string]struct{}
	// startCtx is the context supplied at Start time; used by the
	// finalizer path to spawn re-audit replicas when supersession
	// substantively reshapes intermediate merges (§3.8). Cleared on
	// Stop.
	startCtx context.Context
}

// mergePartialVerdicts accumulates per-merge audit decisions until
// both inspector and tester have emitted. AND-semantics for accept,
// first-rejection-wins for reject (per
// docs/PARALLEL_GLOBAL_VFS.md §3.3).
type mergePartialVerdicts struct {
	Inspector *AuditMergeResult
	Tester    *AuditMergeResult
	Finalized bool
}

// MergeAuditCoordinatorConfig configures the coordinator.
type MergeAuditCoordinatorConfig struct {
	// Session is the SessionVFS whose MergePipelineIntoGreen
	// completions drive dispatch. Required.
	Session *versioning.SessionVFS
	// Inspector is the global inspector the coordinator spawns per
	// merge. Required.
	Inspector AuditReplicaSpawner
	// Tester is the global tester the coordinator spawns per merge.
	// Required.
	Tester AuditReplicaSpawner
	// Bus, if non-nil, receives AuditMergeResult observability
	// publications AFTER the coordinator finalizes a merge. Used by
	// the architect (remediation) and TUI (state view). The
	// dispatch loop itself does not depend on the bus.
	Bus guide.EventBus
	// Logger is the structured logger. Optional.
	Logger *slog.Logger
}

// NewMergeAuditCoordinator returns a coordinator bound to the given
// session. Call Start to begin observing merge completions; Stop to
// release.
func NewMergeAuditCoordinator(cfg MergeAuditCoordinatorConfig) *MergeAuditCoordinator {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &MergeAuditCoordinator{
		session:   cfg.Session,
		inspector: cfg.Inspector,
		tester:    cfg.Tester,
		bus:       cfg.Bus,
		logger:    logger,
		partials:  make(map[versioning.SemanticVersion]*mergePartialVerdicts),
		inFlight:  make(map[string]struct{}),
	}
}

// Start registers the coordinator's merge callback on the session.
// Idempotent.
func (c *MergeAuditCoordinator) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}
	if c.session == nil {
		return fmt.Errorf("merge audit coordinator: nil session")
	}
	if c.inspector == nil || c.tester == nil {
		return fmt.Errorf("merge audit coordinator: inspector and tester spawners required")
	}
	c.session.RegisterMergeCallback(func(desc versioning.MergeDescriptor) {
		c.onMerge(ctx, desc)
	})
	c.stopCh = make(chan struct{})
	c.startCtx = ctx
	c.running = true
	return nil
}

// Stop halts further dispatches. In-flight replicas continue to
// their terminal states; their finalizers still advance the queue
// and retention. Idempotent.
func (c *MergeAuditCoordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	close(c.stopCh)
	c.stopCh = nil
	c.startCtx = nil
	c.running = false
}

// onMerge is invoked synchronously from SessionVFS's merge callback.
// Acquires retention on the base Copy, records lifecycle Spawned
// for both replicas, then calls the spawners' SpawnAuditReplica
// directly. The spawners are expected to run the audit async and
// call the finalizer on ctx when done.
func (c *MergeAuditCoordinator) onMerge(ctx context.Context, desc versioning.MergeDescriptor) {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Audit-addendum merges carry the tester + inspector writes
	// that accompany an accepted pipeline merge (linter / formatter
	// output, new tests, harness adjustments). They are produced by
	// a ReplicaVFS seal and already carry the verdict of the audit
	// that generated them — docs/PARALLEL_GLOBAL_VFS.md §3.7 —
	// so the coordinator does NOT dispatch fresh replicas for them.
	//
	// The coordinator still observes + notifies + sanity-checks:
	//   - Log the addendum arrival so operator traces include it
	//     alongside the original-pipeline merge.
	//   - Verify the commit-queue state is Accepted (SubmitAudit-
	//     Addendum flips it there atomically). A non-Accepted state
	//     at this point indicates a protocol violation upstream and
	//     gets logged loudly.
	//   - Emit an observability notification so TUI + architect can
	//     distinguish "original merge accepted" from "audit-authored
	//     content landed alongside it."
	if desc.AuditAddendum {
		c.handleAuditAddendum(desc)
		return
	}

	sessionID := string(c.session.SessionID())
	specs := []struct {
		agentType string
		spawner   AuditReplicaSpawner
	}{
		{AgentTypeInspectorGlobal, c.inspector},
		{AgentTypeTesterGlobal, c.tester},
	}
	// Both-or-neither spawn: AND-accept semantics require that either
	// both replicas come up or neither does. A partial spawn strands
	// the queue entry in Auditing forever because the surviving
	// replica's verdict alone never reaches the AND threshold. If
	// either spec fails, we roll back the other by calling
	// unwindReplica (release retention + mark crashed) so the queue
	// entry can be safely marked rejected or re-attempted on session
	// resume. See docs/PARALLEL_GLOBAL_VFS.md §3.3 (AND-semantics).
	launched := make([]string, 0, len(specs))
	var spawnErr error
	var failedAgentType string
	for _, spec := range specs {
		replicaID, err := c.spawnReplica(ctx, spec.agentType, spec.spawner, sessionID, desc)
		if err != nil {
			spawnErr = err
			failedAgentType = spec.agentType
			break
		}
		launched = append(launched, replicaID)
	}
	if spawnErr == nil {
		return
	}
	// Partial-spawn recovery path.
	c.logger.Warn("merge audit coordinator: spawn failed — rolling back partial dispatch",
		"failed_agent_type", failedAgentType,
		"merged_version", desc.MergedVersion.String(),
		"launched", launched,
		"error", spawnErr.Error(),
	)
	for _, replicaID := range launched {
		c.unwindReplica(replicaID)
	}
	// With no in-flight replicas, the queue entry would sit in
	// Auditing forever. Transition it to Rejected with the spawn
	// failure as the reason so the architect can compose a
	// remediation (or the operator can intervene).
	if queue := c.session.CommitQueue(); queue != nil {
		rejectReason := fmt.Sprintf("dispatch failed for %s: %v", failedAgentType, spawnErr)
		if err := queue.MarkRejected(desc.MergedVersion, "merge-audit-coordinator", rejectReason, nil); err != nil {
			c.logger.Error("merge audit coordinator: mark rejected after dispatch failure",
				"merged_version", desc.MergedVersion.String(),
				"error", err.Error(),
			)
		}
	}
}

// unwindReplica releases retention + records a Crashed lifecycle
// transition for a replica whose sibling failed to spawn. Called
// from onMerge's partial-spawn rollback path.
func (c *MergeAuditCoordinator) unwindReplica(replicaID string) {
	if retention := c.session.CopyRetention(); retention != nil {
		if err := retention.Release(replicaID); err != nil {
			c.logger.Warn("merge audit coordinator: unwind retention release failed",
				"replica_id", replicaID,
				"error", err.Error(),
			)
		}
	}
	if lifecycle := c.session.ReplicaLifecycleLog(); lifecycle != nil {
		if err := lifecycle.RecordCrashed(replicaID, "sibling spawn failed"); err != nil {
			c.logger.Warn("merge audit coordinator: unwind record crashed failed",
				"replica_id", replicaID,
				"error", err.Error(),
			)
		}
	}
	c.mu.Lock()
	delete(c.inFlight, replicaID)
	c.mu.Unlock()
}

// spawnReplica acquires retention, records the spawned lifecycle,
// materializes the shared ReplicaVFS, and dispatches to the
// spawner. On any failure AFTER retention is acquired, the function
// rolls its own side-effects back before returning the error so the
// caller sees an all-or-nothing semantic for this single replica.
// Partial-spawn recovery ACROSS siblings (inspector + tester) is
// onMerge's responsibility — see that function.
//
// Returns the deterministic replica ID on success so the caller can
// unwind it if the sibling fails.
func (c *MergeAuditCoordinator) spawnReplica(ctx context.Context, agentType string, spawner AuditReplicaSpawner, sessionID string, desc versioning.MergeDescriptor) (string, error) {
	replicaID := MergeReplicaAgentID(agentType, sessionID, desc.MergedVersion)
	retention := c.session.CopyRetention()
	if retention != nil {
		if err := retention.Retain(desc.BaseVersion, replicaID); err != nil {
			return "", fmt.Errorf("retain base copy: %w", err)
		}
	}
	lifecycle := c.session.ReplicaLifecycleLog()
	if lifecycle != nil {
		if err := lifecycle.RecordSpawned(replicaID, agentType, desc.MergedVersion); err != nil {
			if retention != nil {
				c.releaseRetentionOrLog(retention, replicaID, "record-spawned rollback")
			}
			return "", fmt.Errorf("record spawned: %w", err)
		}
	}
	// ReplicaVFS is shared between the inspector and tester audits
	// of this merge. BeginAuditReplicaVFS is idempotent per
	// MergedVersion, so the second spawner sees the same VFS the
	// first created. Both roles write into the same overlay and
	// both see each other's writes via the chain walk.
	mergeVFSID := mergeReplicaVFSKey(sessionID, desc.MergedVersion)
	replicaVFS, err := c.session.BeginAuditReplicaVFS(desc.MergedVersion, mergeVFSID)
	if err != nil {
		if retention != nil {
			c.releaseRetentionOrLog(retention, replicaID, "begin-replica-vfs rollback")
		}
		if lifecycle != nil {
			c.recordCrashedOrLog(lifecycle, replicaID, "begin replica vfs failed: "+err.Error())
		}
		return "", fmt.Errorf("begin replica vfs: %w", err)
	}

	c.mu.Lock()
	c.inFlight[replicaID] = struct{}{}
	c.mu.Unlock()

	req := &AuditMergeRequest{
		SessionID:          sessionID,
		ReplicaID:          replicaID,
		AgentType:          agentType,
		Descriptor:         desc,
		DispatchedAt:       time.Now().UTC(),
		ReplicaVFS:         replicaVFS,
		SteeringJournalDir: c.session.SteeringJournalDir(),
	}
	replicaCtx := WithAuditDecisionFinalizer(ctx, c.finalizeDecision)
	if err := spawner.SpawnAuditReplica(replicaCtx, req); err != nil {
		// Dispatch itself failed — unwind everything this call put in
		// place before returning. The lifecycle log records Crashed
		// so resume logic doesn't attempt to restart a replica the
		// spawner already rejected.
		c.mu.Lock()
		delete(c.inFlight, replicaID)
		c.mu.Unlock()
		if retention != nil {
			c.releaseRetentionOrLog(retention, replicaID, "spawn-rejected rollback")
		}
		if lifecycle != nil {
			c.recordCrashedOrLog(lifecycle, replicaID, "spawn rejected: "+err.Error())
		}
		return "", fmt.Errorf("spawn replica: %w", err)
	}
	return replicaID, nil
}

// releaseRetentionOrLog releases retention for replicaID and logs at
// Error if the release itself fails. The outer error path is already
// committed (caller is returning an error); we still want visibility
// into secondary failures so operators can diagnose leaked holds.
func (c *MergeAuditCoordinator) releaseRetentionOrLog(retention *versioning.CopyRetention, replicaID, context string) {
	if err := retention.Release(replicaID); err != nil {
		c.logger.Error("merge audit coordinator: retention release failed during rollback",
			"replica_id", replicaID,
			"context", context,
			"error", err.Error(),
		)
	}
}

// recordCrashedOrLog writes a Crashed lifecycle record and logs at
// Error if the write itself fails. Same rationale as
// releaseRetentionOrLog — secondary failures during rollback must
// be diagnosable, not silently dropped.
func (c *MergeAuditCoordinator) recordCrashedOrLog(lifecycle *versioning.ReplicaLifecycleLog, replicaID, reason string) {
	if err := lifecycle.RecordCrashed(replicaID, reason); err != nil {
		c.logger.Error("merge audit coordinator: lifecycle crash record failed",
			"replica_id", replicaID,
			"reason", reason,
			"error", err.Error(),
		)
	}
}

// mergeReplicaVFSKey is the shared-VFS identity for a merge. The
// inspector and tester use this as the WAL attribution "holder" when
// their writes touch the shared VFS — their individual role
// replica IDs are passed separately as the writer attribution for
// each specific write.
func mergeReplicaVFSKey(sessionID string, ver versioning.SemanticVersion) string {
	return fmt.Sprintf("replica-vfs#%s:%s", sessionID, ver.String())
}

// handleAuditAddendum processes merge-callback fires for descriptors
// whose AuditAddendum flag is set. See docs/PARALLEL_GLOBAL_VFS.md
// §3.7: addenda are sealed-ReplicaVFS diffs that the session submits
// via SubmitAuditAddendum after a successful audit. They carry
// inspector + tester writes (linter / formatter output, new tests,
// harness adjustments) that belong in the final committed state, and
// by construction are already audited — the verdict that produced
// them IS the audit decision. So the coordinator must NOT spawn
// fresh replicas for them; that would be both redundant (nothing new
// to audit) and incorrect (auditing an addendum would recurse into
// yet another addendum).
//
// Observers (TUI, architect, operational dashboards) still need to
// know the addendum landed so the session's total-sum view reflects
// audit-authored content. This method handles that observation +
// notification role, plus a sanity check that the commit queue's
// state machine advanced correctly upstream.
func (c *MergeAuditCoordinator) handleAuditAddendum(desc versioning.MergeDescriptor) {
	sessionID := string(c.session.SessionID())
	pathCount := desc.PathCount
	if pathCount == 0 {
		pathCount = len(desc.Paths)
	}

	// Protocol sanity check: SubmitAuditAddendum enqueues the
	// descriptor and immediately flips it from Auditing → Accepted
	// in a single call, before firing the callback. If we see the
	// entry in any other state here, something upstream is wrong
	// (e.g. a direct enqueue path bypassed MarkAccepted).
	observedState := "unknown"
	if queue := c.session.CommitQueue(); queue != nil {
		if entry := queue.Lookup(desc.MergedVersion); entry != nil {
			observedState = string(entry.State)
			if entry.State != versioning.CommitStateAccepted {
				c.logger.Warn("merge audit coordinator: audit-addendum in unexpected queue state",
					"session_id", sessionID,
					"merged_version", desc.MergedVersion.String(),
					"base_version", desc.BaseVersion.String(),
					"pipeline_id", desc.PipelineID,
					"state", observedState,
					"expected", string(versioning.CommitStateAccepted),
				)
			}
		} else {
			c.logger.Warn("merge audit coordinator: audit-addendum missing from commit queue",
				"session_id", sessionID,
				"merged_version", desc.MergedVersion.String(),
				"base_version", desc.BaseVersion.String(),
				"pipeline_id", desc.PipelineID,
			)
		}
	}

	c.logger.Info("merge audit coordinator: audit-addendum landed",
		"session_id", sessionID,
		"merged_version", desc.MergedVersion.String(),
		"base_version", desc.BaseVersion.String(),
		"pipeline_id", desc.PipelineID,
		"path_count", pathCount,
		"state", observedState,
	)

	// Observability publication on the addendum-specific topic so
	// subscribers can distinguish "original merge accepted" from
	// "audit-authored content landed alongside it."
	if c.bus != nil {
		notif := &AuditMergeAddendumNotification{
			SessionID:       sessionID,
			AddendumVersion: desc.MergedVersion,
			BaseVersion:     desc.BaseVersion,
			PipelineID:      desc.PipelineID,
			Paths:           append([]string(nil), desc.Paths...),
			PathCount:       pathCount,
			State:           observedState,
			ObservedAt:      time.Now().UTC(),
		}
		body, err := jsonMarshalAddendum(notif)
		if err != nil {
			c.logger.Warn("merge audit coordinator: addendum marshal failed",
				"merged_version", desc.MergedVersion.String(),
				"error", err.Error(),
			)
			return
		}
		msg := guide.NewBridgeMessage(desc.PipelineID, "observers", body)
		if err := c.bus.Publish(AuditMergeAddendumTopic, msg); err != nil {
			c.logger.Warn("merge audit coordinator: addendum publish failed",
				"merged_version", desc.MergedVersion.String(),
				"error", err.Error(),
			)
		}
	}
}

// finalizeDecision is the direct callback a replica invokes when it
// emits a terminal decision. Applies lifecycle + retention
// transitions and advances the commit queue under AND-semantics
// (both-accept required; first rejection short-circuits).
func (c *MergeAuditCoordinator) finalizeDecision(result *AuditMergeResult) {
	if result == nil {
		return
	}
	lifecycle := c.session.ReplicaLifecycleLog()
	retention := c.session.CopyRetention()

	if lifecycle != nil {
		if err := lifecycle.RecordDecided(result.ReplicaID, result.Decision, result.Summary, result.Concerns); err != nil {
			c.logger.Warn("merge audit coordinator: lifecycle decide failed",
				"replica_id", result.ReplicaID,
				"error", err.Error(),
			)
		}
	}
	if retention != nil {
		if err := retention.Release(result.ReplicaID); err != nil {
			c.logger.Warn("merge audit coordinator: retention release failed",
				"replica_id", result.ReplicaID,
				"error", err.Error(),
			)
		}
	}
	c.mu.Lock()
	delete(c.inFlight, result.ReplicaID)
	c.mu.Unlock()

	// Observability publication — lets the TUI + architect subscribe
	// to a typed, result-topic stream. Does not affect the dispatch
	// loop (which is fully direct via this callback).
	if c.bus != nil {
		body, err := jsonMarshalResult(result)
		if err == nil {
			msg := guide.NewBridgeMessage(result.ReplicaID, "observers", body)
			_ = c.bus.Publish(AuditMergeResultTopic, msg)
		}
	}

	c.finalizeIfReady(result)
}

// finalizeIfReady applies verdict-combining logic and advances the
// commit queue. See the comment on mergePartialVerdicts for
// semantics.
func (c *MergeAuditCoordinator) finalizeIfReady(result *AuditMergeResult) {
	queue := c.session.CommitQueue()
	if queue == nil {
		return
	}

	c.mu.Lock()
	partial, ok := c.partials[result.MergedVersion]
	if !ok {
		partial = &mergePartialVerdicts{}
		c.partials[result.MergedVersion] = partial
	}
	if partial.Finalized {
		c.mu.Unlock()
		return
	}
	agentType, _, _, _ := ParseMergeReplicaAgentID(result.ReplicaID)
	switch agentType {
	case AgentTypeInspectorGlobal:
		partial.Inspector = result
	case AgentTypeTesterGlobal:
		partial.Tester = result
	default:
		c.logger.Warn("merge audit coordinator: unrecognized agent type",
			"replica_id", result.ReplicaID,
		)
		c.mu.Unlock()
		return
	}

	var (
		finalDecision versioning.ReplicaDecision
		finalReplica  string
		finalSummary  string
		finalConcerns []string
		ready         bool
	)
	switch {
	case result.Decision == versioning.ReplicaDecisionRejected:
		finalDecision = versioning.ReplicaDecisionRejected
		finalReplica = result.ReplicaID
		finalSummary = result.Summary
		finalConcerns = append([]string(nil), result.Concerns...)
		ready = true
	case partial.Inspector != nil && partial.Tester != nil:
		finalDecision = versioning.ReplicaDecisionAccepted
		finalReplica = partial.Inspector.ReplicaID
		finalSummary = combineSummaries(partial.Inspector.Summary, partial.Tester.Summary)
		finalConcerns = append(finalConcerns, partial.Inspector.Concerns...)
		finalConcerns = append(finalConcerns, partial.Tester.Concerns...)
		ready = true
	}
	if !ready {
		c.mu.Unlock()
		return
	}
	partial.Finalized = true
	c.mu.Unlock()

	replicaVFS := c.session.LookupAuditReplicaVFS(result.MergedVersion)
	switch finalDecision {
	case versioning.ReplicaDecisionAccepted:
		if err := queue.MarkAccepted(result.MergedVersion, finalReplica); err != nil {
			// Accept failed — the replicas recorded both verdicts but
			// the queue state machine refused the transition
			// (mis-ordering, WAL error, state drift from crash+replay).
			// Downstream commit resolver will stall unless we transition
			// to a terminal state. Escalate to Rejected so the head
			// unblocks and the architect can remediate with a fresh
			// dispatch; the original reviewer verdicts are preserved in
			// the replica lifecycle log for forensics.
			c.logger.Error("merge audit finalize: mark accepted failed — escalating to reject",
				"merged_version", result.MergedVersion.String(),
				"error", err.Error(),
			)
			reason := fmt.Sprintf("mark accepted failed: %v", err)
			if rejErr := queue.MarkRejected(result.MergedVersion, finalReplica, reason, finalConcerns); rejErr != nil {
				c.logger.Error("merge audit finalize: fallback reject also failed — queue head may stall",
					"merged_version", result.MergedVersion.String(),
					"reject_error", rejErr.Error(),
				)
			}
			return
		}
		// Supersession: when the accepted descriptor was produced by a
		// remediation pipeline (pinned to a rejected merge's Copy at
		// dispatch time), the original rejected entry's slot is
		// transitioned to Superseded so the commit resolver can flush
		// the supersedor in its place. See
		// docs/PARALLEL_GLOBAL_VFS.md §3.8.
		desc, hasDesc := c.session.FindMergeDescriptor(result.MergedVersion)
		if hasDesc && !desc.SupersedesVersion.IsZero() {
			if err := queue.MarkSuperseded(desc.SupersedesVersion, result.MergedVersion); err != nil {
				// Failure here leaves the rejected slot un-superseded
				// and the commit resolver blocked at that head. There
				// is no safe automatic recovery: re-retrying would
				// re-WAL the same event (potentially succeeding or
				// doubling-up), and skipping would silently drop the
				// supersession. Surface at Error so operators see it
				// in logs and observability. A future pass can add
				// explicit retry with idempotency keys.
				c.logger.Error("merge audit finalize: mark superseded failed — commit resolver may stall",
					"rejected_version", desc.SupersedesVersion.String(),
					"supersedor_version", result.MergedVersion.String(),
					"error", err.Error(),
				)
			} else {
				c.logger.Info("merge audit finalize: supersession landed",
					"rejected_version", desc.SupersedesVersion.String(),
					"supersedor_version", result.MergedVersion.String(),
				)
				// Re-audit scan: for every in-flight descriptor K+1..M-1
				// whose diff was substantively changed by M's OT
				// transform, spawn fresh replicas against the
				// re-transformed diff.
				c.triggerReAuditAfterSupersession(c.contextForBackgroundWork(), desc)
			}
		}
		// Seal the shared VFS; capture the audit-addendum diff.
		// Downstream replicas still chain through this VFS for
		// progressive context until the commit resolver releases
		// it at water-line advance.
		if replicaVFS != nil {
			if _, err := replicaVFS.Seal(); err != nil {
				// Seal failure means the VFS mutation guard tripped
				// (e.g. already sealed, or overlay corrupted). Abandon
				// the overlay so downstream chain walks skip it and
				// return without submitting an addendum — a corrupted
				// seal would produce a corrupted addendum.
				c.logger.Error("merge audit finalize: seal replica vfs failed — abandoning",
					"merged_version", result.MergedVersion.String(),
					"error", err.Error(),
				)
				if abandonErr := replicaVFS.Abandon(); abandonErr != nil {
					c.logger.Warn("merge audit finalize: abandon after seal failure failed",
						"merged_version", result.MergedVersion.String(),
						"error", abandonErr.Error(),
					)
				}
				return
			}
			// Audit-addendum submission to MergePipe. If this fails the
			// core merge is still Accepted (queue is fine) — we lose
			// only the audit-authored content (linter/formatter output,
			// new tests). Log at Error so operators can distinguish
			// "committed successfully" from "committed without addendum."
			if err := c.session.SubmitAuditAddendum(result.MergedVersion); err != nil {
				c.logger.Error("merge audit finalize: submit audit addendum failed — audit-authored content not committed",
					"merged_version", result.MergedVersion.String(),
					"error", err.Error(),
				)
			}
		}
	case versioning.ReplicaDecisionRejected:
		if err := queue.MarkRejected(result.MergedVersion, finalReplica, finalSummary, finalConcerns); err != nil {
			c.logger.Warn("merge audit finalize: mark rejected failed",
				"merged_version", result.MergedVersion.String(),
				"error", err.Error(),
			)
			return
		}
		// Abandon the shared VFS so downstream replicas bypass it
		// in chain walks. The descriptor stays at Rejected pending
		// architect remediation; the ReplicaVFS stays allocated
		// (with parent refs) until supersession lands or the
		// abandon path runs.
		if replicaVFS != nil {
			if err := replicaVFS.Abandon(); err != nil {
				c.logger.Warn("merge audit finalize: abandon replica vfs failed",
					"merged_version", result.MergedVersion.String(),
					"error", err.Error(),
				)
			}
		}
	}
}

// contextForBackgroundWork returns the coordinator's Start-supplied
// ctx, falling back to context.Background() when the coordinator is
// not running (e.g. a finalizer fires after Stop — we still advance
// state machines but lose Start's cancellation scope). Thread-safe:
// callers read the immutable snapshot under mu.
func (c *MergeAuditCoordinator) contextForBackgroundWork() context.Context {
	c.mu.Lock()
	ctx := c.startCtx
	c.mu.Unlock()
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

// triggerReAuditAfterSupersession walks every descriptor landing
// strictly after the rejected merge (SupersedesVersion < K ≤ M-1) and
// tests whether the supersedor's path set overlaps its path set. A
// non-empty intersection means M's OT-transformed changeset
// substantively reshaped that intermediate merge relative to what its
// original audit replicas saw — per docs/PARALLEL_GLOBAL_VFS.md §3.8,
// those merges need fresh replica pairs against the re-transformed
// context.
//
// "Substantive" heuristic (§14 open question; initial implementation):
// any path M touches that the intermediate also touches is substantive.
// Later refinements can reason about OT-level conflicts; path overlap
// is a conservative starting point that avoids missing correctness
// issues at the cost of occasional over-eager re-audits.
//
// Scope: only intermediate descriptors whose commit-queue state is
// Auditing are candidates. Already-Accepted or already-Rejected
// descriptors have a finalized verdict; Committed and Abandoned are
// terminal. We don't re-audit AuditAddendum descriptors (they already
// carry an audit decision by construction).
func (c *MergeAuditCoordinator) triggerReAuditAfterSupersession(ctx context.Context, supersedor versioning.MergeDescriptor) {
	if c.session == nil {
		return
	}
	queue := c.session.CommitQueue()
	if queue == nil {
		return
	}
	supersedorPaths := pathSet(supersedor.Paths)
	if len(supersedorPaths) == 0 {
		return
	}
	rejectedVer := supersedor.SupersedesVersion
	candidates := c.session.MergesAfter(rejectedVer)
	sessionID := string(c.session.SessionID())
	for _, desc := range candidates {
		if desc.MergedVersion.Compare(supersedor.MergedVersion) >= 0 {
			continue
		}
		if desc.AuditAddendum {
			continue
		}
		entry := queue.Lookup(desc.MergedVersion)
		if entry == nil || entry.State != versioning.CommitStateAuditing {
			continue
		}
		if !pathsOverlap(supersedorPaths, desc.Paths) {
			continue
		}
		c.logger.Info("merge audit finalize: re-audit triggered by supersession",
			"merged_version", desc.MergedVersion.String(),
			"supersedor_version", supersedor.MergedVersion.String(),
			"rejected_version", rejectedVer.String(),
		)
		c.spawnReAudit(ctx, sessionID, desc)
	}
}

// spawnReAudit dispatches fresh inspector + tester replica pairs for
// the given descriptor. Used by the supersession-re-audit path. The
// replica IDs are the same deterministic identity the original pair
// used; the spawners see IsReAudit=true on the request and are
// expected to rehydrate steering state under that ID.
func (c *MergeAuditCoordinator) spawnReAudit(ctx context.Context, sessionID string, desc versioning.MergeDescriptor) {
	for _, spec := range []struct {
		agentType string
		spawner   AuditReplicaSpawner
	}{
		{AgentTypeInspectorGlobal, c.inspector},
		{AgentTypeTesterGlobal, c.tester},
	} {
		replicaID := MergeReplicaAgentID(spec.agentType, sessionID, desc.MergedVersion)
		mergeVFSID := mergeReplicaVFSKey(sessionID, desc.MergedVersion)
		replicaVFS, err := c.session.BeginAuditReplicaVFS(desc.MergedVersion, mergeVFSID)
		if err != nil {
			c.logger.Warn("re-audit spawn: begin replica vfs failed",
				"replica_id", replicaID,
				"error", err.Error(),
			)
			continue
		}
		req := &AuditMergeRequest{
			SessionID:          sessionID,
			ReplicaID:          replicaID,
			AgentType:          spec.agentType,
			Descriptor:         desc,
			DispatchedAt:       time.Now().UTC(),
			IsReAudit:          true,
			ReplicaVFS:         replicaVFS,
			SteeringJournalDir: c.session.SteeringJournalDir(),
		}
		replicaCtx := WithAuditDecisionFinalizer(ctx, c.finalizeDecision)
		if err := spec.spawner.SpawnAuditReplica(replicaCtx, req); err != nil {
			c.logger.Warn("re-audit spawn failed",
				"replica_id", replicaID,
				"error", err.Error(),
			)
		}
	}
}

// pathSet turns a path slice into a set for O(1) overlap checks.
func pathSet(paths []string) map[string]struct{} {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

// pathsOverlap reports whether any entry in `other` is also in `set`.
func pathsOverlap(set map[string]struct{}, other []string) bool {
	if len(set) == 0 || len(other) == 0 {
		return false
	}
	for _, p := range other {
		if _, hit := set[p]; hit {
			return true
		}
	}
	return false
}

// ResumeInFlightReplicas walks the session's ReplicaLifecycleLog and
// re-spawns every in-flight replica. Called by session-lifecycle
// code after SessionVFS.Open completes WAL replay. Each re-spawn
// uses the original replica's deterministic ID so its steering
// ledger rehydrates.
func (c *MergeAuditCoordinator) ResumeInFlightReplicas(ctx context.Context) error {
	if c.session == nil {
		return nil
	}
	lifecycle := c.session.ReplicaLifecycleLog()
	queue := c.session.CommitQueue()
	if lifecycle == nil || queue == nil {
		return nil
	}
	sessionID := string(c.session.SessionID())
	for _, entry := range lifecycle.InFlight() {
		qe := queue.Lookup(entry.MergedVersion)
		if qe == nil || qe.State != versioning.CommitStateAuditing {
			continue
		}
		var spawner AuditReplicaSpawner
		switch entry.AgentType {
		case AgentTypeInspectorGlobal:
			spawner = c.inspector
		case AgentTypeTesterGlobal:
			spawner = c.tester
		default:
			continue
		}
		mergeVFSID := mergeReplicaVFSKey(sessionID, qe.Descriptor.MergedVersion)
		replicaVFS, err := c.session.BeginAuditReplicaVFS(qe.Descriptor.MergedVersion, mergeVFSID)
		if err != nil {
			c.logger.Warn("resume replica vfs rehydrate failed",
				"replica_id", entry.ReplicaID,
				"error", err.Error(),
			)
			continue
		}
		req := &AuditMergeRequest{
			SessionID:          sessionID,
			ReplicaID:          entry.ReplicaID,
			AgentType:          entry.AgentType,
			Descriptor:         qe.Descriptor,
			DispatchedAt:       time.Now().UTC(),
			IsReAudit:          true,
			ReplicaVFS:         replicaVFS,
			SteeringJournalDir: c.session.SteeringJournalDir(),
		}
		replicaCtx := WithAuditDecisionFinalizer(ctx, c.finalizeDecision)
		if err := spawner.SpawnAuditReplica(replicaCtx, req); err != nil {
			c.logger.Warn("resume replica re-spawn failed",
				"replica_id", entry.ReplicaID,
				"error", err.Error(),
			)
		}
	}
	return nil
}

// combineSummaries joins inspector + tester prose for a
// both-accepted result. Preserves both voices.
func combineSummaries(inspector, tester string) string {
	return "inspector: " + inspector + "\ntester: " + tester
}

// Agent-type constants for the two globals this coordinator
// dispatches to.
const (
	AgentTypeInspectorGlobal = "inspector-global"
	AgentTypeTesterGlobal    = "tester-global"
)

// jsonMarshalResult is a small indirection so the bus publish path
// can be mocked in tests. Kept local to this file.
var jsonMarshalResult = func(r *AuditMergeResult) ([]byte, error) {
	return marshalAuditMergeResultForBus(r)
}

// jsonMarshalAddendum is the addendum-notification counterpart of
// jsonMarshalResult.
var jsonMarshalAddendum = func(n *AuditMergeAddendumNotification) ([]byte, error) {
	return marshalAuditMergeAddendumForBus(n)
}

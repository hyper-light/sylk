package scribe

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

// inheritPriorReplicaNarrative seeds the scribe's per-workstream
// context with a synthesized "previously, on this agent's life"
// digest sourced from prior replicas' narration_emitted activities
// in the fabric. Per SCRIBE_FABRIC.md Phase 8.
//
// Called once at scribe Start (after fabric subscription is in
// place). Best-effort: when the fabric source isn't yet wired or
// no prior replica produced narrations, the scribe boots cold and
// the rest of the lifecycle proceeds normally.
//
// The inherited digest is injected as a synthetic
// providers.Message into the per-workstream history under a stable
// key (HandoffWorkstreamKey) so the next narration LLM call sees
// it as "context from your prior life" before any new batch
// arrives. The handoff bridge — when present — still seeds
// additional in-memory state via InjectPreparedContext; the
// fabric-backed inheritance is the primary mechanism, the bridge
// is the warm fast-path.
func (s *Scribe) inheritPriorReplicaNarrative(ctx context.Context) {
	if s == nil || s.replicaGeneration <= 1 {
		return
	}
	src := activity.DefaultSource()
	if src == nil {
		return
	}
	rows, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:     activity.SessionID(s.sessionID),
		ActionKinds:   []activity.ActionKind{activity.ActionNarrationEmitted},
		SubjectDomain: s.parentAgentType,
		Limit:         maxInheritanceEntries * 2, // overfetch; we filter by replica_generation client-side
	})
	if err != nil || len(rows) == 0 {
		return
	}

	digest := selectPriorReplicaEntries(rows, s.replicaGeneration)
	if len(digest) == 0 {
		return
	}

	// Post inheritance claim: this replica inherits continuity from prior ones.
	priorGens := make([]claims.Relation, 0, len(digest)+2)
	priorGens = append(priorGens,
		claims.Relation{Related: s.scribeAgentID(), RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		claims.Relation{Related: s.parentAgentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
	)
	for _, entry := range digest {
		priorGens = append(priorGens, claims.Relation{
			Related:      fmt.Sprintf("replica_gen_%d", entry.replicaGeneration),
			RelatedType:  claims.RelatedTypeAgent,
			Relationship: claims.RelationshipDerivedFrom,
		})
	}
	inheritClaim := claims.Claim{
		Title:       fmt.Sprintf("Replica %d inherited narration context from %d prior replicas", s.replicaGeneration, len(digest)),
		Description: "Cross-replica continuity established via fabric narration_emitted activity query",
		Scope:       []claims.ClaimScopeEntry{{Kind: "replica", Key: fmt.Sprintf("gen_%d", s.replicaGeneration)}},
		ActionType:  claims.ActionTypeArchival,
		Relations:   priorGens,
	}
	s.scribePostClaim(ctx, s.scribeClaimAction(claims.ActionTypeArchival), inheritClaim)

	// Submit inheritance testament with digest summary.
	entryRefs := make([]string, 0, len(digest))
	for _, e := range digest {
		entryRefs = append(entryRefs, fmt.Sprintf("gen_%d@%s", e.replicaGeneration, e.timestamp.Format(time.RFC3339)))
	}
	s.scribeSubmitTestament(ctx, s.scribeTestament(
		fmt.Sprintf("Inherited %d narrations from prior replicas", len(digest)),
		"committed",
		[]*claims.Artifact{s.scribeJSONArtifact("inherited_entries", entryRefs)},
	))

	// Inject as a synthetic seed message under the dedicated
	// workstream key so it doesn't conflict with active per-
	// correlation workstreams. Future narration triggers see this
	// as inherited context.
	seedMsg := buildInheritanceSeedMessage(s.parentAgentType, s.replicaGeneration, digest)
	s.workstreamsMu.Lock()
	defer s.workstreamsMu.Unlock()
	if s.workstreams == nil {
		return
	}
	if existing, ok := s.workstreams[crossReplicaWorkstreamKey]; ok && existing != nil {
		// Already seeded earlier this lifetime; don't duplicate.
		return
	}
	s.workstreams[crossReplicaWorkstreamKey] = &scribeWorkstream{
		messages:  []providers.Message{seedMsg},
		updatedAt: time.Now(),
	}
}

const (
	crossReplicaWorkstreamKey = "scribe:cross-replica-inheritance"
	maxInheritanceEntries     = 8
)

type inheritanceEntry struct {
	timestamp         time.Time
	replicaGeneration int
	scope             string
	commentary        json.RawMessage
}

// selectPriorReplicaEntries filters and chronologically orders
// narration_emitted rows from prior replicas, capping at the
// inheritance window. Excludes rows from the current replica
// (those are this life's, not the prior's).
func selectPriorReplicaEntries(rows []activity.AgentActivity, currentGeneration int) []inheritanceEntry {
	out := make([]inheritanceEntry, 0, maxInheritanceEntries)
	for _, r := range rows {
		gen := parseInheritanceGeneration(r.Subject.Coordinates)
		if gen <= 0 || gen >= currentGeneration {
			continue
		}
		out = append(out, inheritanceEntry{
			timestamp:         r.Timestamp,
			replicaGeneration: gen,
			scope:             r.Subject.PathPrefix,
			commentary:        r.Payload,
		})
		if len(out) >= maxInheritanceEntries {
			break
		}
	}
	return out
}

// parseInheritanceGeneration is a local copy of the shared parser to
// avoid pulling fabric.RecallSkillConfig dependencies — we just need
// the integer extraction. Returns 0 for missing/invalid coordinates.
func parseInheritanceGeneration(coords map[string]string) int {
	if coords == nil {
		return 0
	}
	raw, ok := coords["replica_generation"]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// buildInheritanceSeedMessage produces the providers.Message that
// gets injected into the cross-replica workstream as inherited
// context. Framed as a system-style "previously on this agent's
// life" preamble so the next narration LLM call recognizes it.
func buildInheritanceSeedMessage(parentAgentType string, currentGeneration int, entries []inheritanceEntry) providers.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "[cross-replica inheritance]\n")
	fmt.Fprintf(&b, "Replica %d of %s booting. Prior lives produced the following narrations (chronological):\n\n",
		currentGeneration, parentAgentType)
	for i, entry := range entries {
		fmt.Fprintf(&b, "  ↑[gen %d, %s] scope=%s\n",
			entry.replicaGeneration,
			entry.timestamp.Format(time.RFC3339),
			entry.scope)
		if len(entry.commentary) > 0 && len(entry.commentary) < 4096 {
			fmt.Fprintf(&b, "      %s\n", string(entry.commentary))
		} else if len(entry.commentary) > 0 {
			fmt.Fprintf(&b, "      (commentary omitted, %d bytes)\n", len(entry.commentary))
		}
		// Defensive bounded loop — selectPriorReplicaEntries already
		// caps at maxInheritanceEntries but a paranoid extra check
		// keeps the prompt size bounded.
		if i >= maxInheritanceEntries-1 {
			break
		}
	}
	b.WriteString("\nUse this prior context to ground narrations of this replica's work — your biographer's voice should feel continuous across the replica boundary. Do not repeat prior narrations; instead, treat them as context for what's new.\n")
	return providers.Message{
		Role:    providers.RoleUser,
		Content: b.String(),
	}
}

// Compile-time check that we still link against shared (for the
// ScribeFeed-related plumbing in the seed). Future refactors may drop
// this if shared isn't otherwise needed.
var _ shared.ScribeFeed

package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/database"
	"github.com/adalundhe/sylk/core/manifest"
	"github.com/dgraph-io/ristretto"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// tentativeTTL is the lifetime of a Tentative declaration in the live
// cache. After this window the cache evicts the entry and the next Query
// stops returning the corresponding SQLite row — equivalent to the old
// reaper's expiry behavior, achieved with no goroutine and no DELETE.
const tentativeTTL = 30 * time.Minute

// DecisionManifestStore is the orchestrator-owned cross-pipeline decision
// store. Backed by SQLite for durability + audit trail, with Ristretto as
// a TTL gate on Tentative entries. Tentative declarations live in both:
// SQLite holds the audit record; Ristretto holds the "is this still
// alive?" flag whose TTL eviction replaces a periodic reaper goroutine.
//
// Promoted entries (Committed / Consensus) bypass the cache gate — they
// are never expired and stay durable. Cache lookup short-circuits to
// "alive" for any non-Tentative confidence so the gate has zero effect
// on the hot Committed/Consensus read path.
type DecisionManifestStore struct {
	db             *database.BunSQLiteDB
	tentativeAlive *ristretto.Cache
}

// NewDecisionManifestStore wires the store onto an existing BunSQLite
// handle. Constructs the Ristretto TTL gate for Tentative entries with
// conservative sizing (10k counter slots, 1MB max cost — Tentative
// records are tiny string IDs so this comfortably holds tens of thousands
// of in-flight declarations across all sessions). Returns an error if
// the cache cannot be created.
func NewDecisionManifestStore(db *database.BunSQLiteDB) (*DecisionManifestStore, error) {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10_000,
		MaxCost:     1 << 20, // 1MB — sufficient for tens of thousands of decision IDs
		BufferItems: 64,
	})
	if err != nil {
		return nil, fmt.Errorf("decision manifest cache: %w", err)
	}
	return &DecisionManifestStore{db: db, tentativeAlive: cache}, nil
}

// Migrate creates the decision_manifest table and indices if absent.
func (s *DecisionManifestStore) Migrate() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.ExecSchema(context.Background(), decisionManifestSchema)
}

// Declare persists a new decision. If the input's scope overlaps an
// existing decision in the same session+domain, the conflict semantics
// (Equivalent/Compatible/Incompatible) are computed via the domain's
// registered Compatibility predicate; the conflict is returned alongside
// the persisted record so the caller can adopt / align / challenge.
//
// Equivalent conflicts are persisted but also promote the existing
// decision: Tentative → Committed → Consensus on the second independent
// declaration. The promoted record is what's returned in the conflict's
// Existing field so the caller sees the new effective confidence.
//
// Incompatible conflicts are persisted as Tentative regardless of the
// caller's requested confidence — explicit reconciliation must precede
// any commitment based on this value.
func (s *DecisionManifestStore) Declare(ctx context.Context, sessionID string, author manifest.AgentRef, in manifest.DeclareDecisionInput) (*manifest.DeclareDecisionResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("decision manifest store: not configured")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("decision manifest store: session_id is required")
	}
	if err := manifest.ValidateDeclareInput(&in); err != nil {
		return nil, err
	}

	scopeJSON, err := encodeScope(in.Scope)
	if err != nil {
		return nil, fmt.Errorf("encode scope: %w", err)
	}
	evidenceJSON, err := encodeEvidence(in.Evidence)
	if err != nil {
		return nil, fmt.Errorf("encode evidence: %w", err)
	}

	now := time.Now().UTC()
	id := "dm_" + uuid.NewString()[:12]
	declared := &manifest.Decision{
		ID:         id,
		SessionID:  sessionID,
		Domain:     strings.TrimSpace(in.Domain),
		Scope:      cloneScope(in.Scope),
		Value:      strings.TrimSpace(in.Value),
		Author:     author,
		DeclaredAt: now,
		Confidence: in.Confidence,
		Evidence:   append([]string(nil), in.Evidence...),
		Supersedes: strings.TrimSpace(in.Supersedes),
		History: []manifest.DecisionEvent{
			{
				Type:      manifest.DecisionEventDeclared,
				AgentID:   author.AgentID,
				AgentType: author.AgentType,
				At:        now,
			},
		},
	}

	result := &manifest.DeclareDecisionResult{Decision: declared}

	if err := s.db.RunInWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// Look up scope-overlapping decisions within the same session+domain.
		// Resolution domain is small (typically <100 decisions per session
		// per domain), so a single bounded scan is well within budget.
		existing, scanErr := scanDomainDecisionsTx(ctx, tx, sessionID, declared.Domain)
		if scanErr != nil {
			return scanErr
		}

		var conflict *manifest.DecisionConflict
		var promotion *manifest.Decision
		var promotionHistory []manifest.DecisionEvent

		for _, candidate := range existing {
			if candidate.ID == declared.Supersedes {
				continue
			}
			if !manifest.ScopesOverlap(candidate.Scope, declared.Scope) {
				continue
			}
			cmp := manifest.CompatibilityFor(declared.Domain, declared.Value, candidate.Value)
			switch cmp {
			case manifest.CompatibilityEquivalent:
				// Independent corroboration: promote the existing decision
				// one tier (Tentative → Committed → Consensus). The new
				// record is still persisted as audit/lineage so we can show
				// "two agents in two pipelines independently picked X".
				promotion, promotionHistory = promoteExisting(candidate, author, now)
				conflict = &manifest.DecisionConflict{
					Kind:          manifest.ConflictEquivalent,
					Existing:      promotion,
					Compatibility: manifest.CompatibilityEquivalent,
					Message: fmt.Sprintf(
						"a peer (%s/%s) already declared the same value %q at a scope-overlapping coordinate; treating this declaration as corroboration and promoting confidence to %s",
						candidate.Author.AgentType, candidate.Author.AgentID,
						candidate.Value, promotion.Confidence,
					),
				}
			case manifest.CompatibilityCompatible:
				conflict = &manifest.DecisionConflict{
					Kind:          manifest.ConflictCompatible,
					Existing:      candidate,
					Compatibility: manifest.CompatibilityCompatible,
					Message: fmt.Sprintf(
						"a peer (%s/%s) declared a compatible value %q at a scope-overlapping coordinate; both decisions can coexist but downstream agents should be aware",
						candidate.Author.AgentType, candidate.Author.AgentID, candidate.Value,
					),
				}
			case manifest.CompatibilityIncompatible:
				// Force the new declaration's confidence to Tentative —
				// the caller cannot commit on this value until the
				// incompatibility is resolved via challenge or adoption.
				declared.Confidence = manifest.ConfidenceTentative
				conflict = &manifest.DecisionConflict{
					Kind:          manifest.ConflictIncompatible,
					Existing:      candidate,
					Compatibility: manifest.CompatibilityIncompatible,
					Message: fmt.Sprintf(
						"a peer (%s/%s) already declared %q at a scope-overlapping coordinate; your declared %q is INCOMPATIBLE. Adopt the existing value, narrow your scope to a non-overlapping coordinate, or issue a peer challenge via challenge_agent referencing decision %s",
						candidate.Author.AgentType, candidate.Author.AgentID,
						candidate.Value, declared.Value, candidate.ID,
					),
				}
			}
			if conflict != nil {
				break
			}
		}

		if err := insertDecisionTx(ctx, tx, declared, scopeJSON, evidenceJSON); err != nil {
			return err
		}

		if promotion != nil {
			if err := updateConfidenceAndHistoryTx(ctx, tx, promotion.ID, promotion.Confidence, promotionHistory); err != nil {
				return err
			}
		}

		result.Conflict = conflict
		return nil
	}); err != nil {
		return nil, err
	}

	// Ristretto TTL gate: a Tentative entry is "alive" iff its ID is
	// present in the cache. The cache evicts after tentativeTTL with no
	// goroutine, no DELETE, and no expires_at column logic. Promotion
	// removes the cache entry so the SQLite row is no longer gated and
	// becomes durably visible to all queries.
	if declared.Confidence == manifest.ConfidenceTentative {
		s.markTentativeAlive(declared.ID)
	}
	if result.Conflict != nil && result.Conflict.Kind == manifest.ConflictEquivalent && result.Conflict.Existing != nil {
		// The promoted existing decision is no longer Tentative; its
		// gate entry can be removed so future queries see it
		// unconditionally.
		s.unmarkTentativeAlive(result.Conflict.Existing.ID)
	}

	// Activity Fabric amplifier: emit a decision_declared activity
	// (Coarse resolution — durable + Bleve + vectorgraphdb). The
	// emission is post-commit so the manifest write is the source of
	// truth; if emission fails, the manifest still works. The fabric
	// row carries SourceTable=decision_manifest + SourceID for
	// back-reference to the canonical row.
	emitDecisionActivity(ctx, declared, sessionID, "decision_manifest")
	if result.Conflict != nil && result.Conflict.Kind == manifest.ConflictEquivalent && result.Conflict.Existing != nil {
		emitDecisionPromoted(ctx, result.Conflict.Existing, sessionID, declared.ID)
	}

	return result, nil
}

// emitDecisionActivity publishes a decision_declared activity for a
// just-persisted manifest decision. Coarse resolution → durable in
// SQLite, indexed in Bleve, embedded in vectorgraphdb. The activity's
// SourceTable + SourceID provide the back-reference to the canonical
// manifest row.
func emitDecisionActivity(ctx context.Context, d *manifest.Decision, sessionID string, sourceTable string) {
	if d == nil {
		return
	}
	subject := activity.Subject{
		Domain:         d.Domain,
		Coordinates:    map[string]string{},
		PathPrefix:     d.Scope[manifest.DimensionPath],
		TargetArtifact: d.ID,
	}
	for k, v := range d.Scope {
		subject.Coordinates[string(k)] = v
	}
	confidence := mapManifestConfidence(d.Confidence)
	payload, _ := json.Marshal(map[string]any{
		"value":      d.Value,
		"author":     fmt.Sprintf("%s/%s", d.Author.AgentType, d.Author.AgentID),
		"evidence":   d.Evidence,
		"supersedes": d.Supersedes,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(sessionID),
		Timestamp:  d.DeclaredAt,
		Resolution: activity.ResolutionFor(activity.ActionDecisionDeclared),
		Action:     activity.ActionDecisionDeclared,
		Actor: activity.Actor{
			AgentID:   d.Author.AgentID,
			AgentType: d.Author.AgentType,
		},
		Subject:     subject,
		Payload:     payload,
		State:       activity.StatePoint,
		Confidence:  confidence,
		SourceTable: sourceTable,
		SourceID:    d.ID,
	}
	activity.Append(ctx, act)
}

// emitDecisionPromoted publishes a decision_promoted activity that
// resolves the prior decision_declared via Caused. The triggerActivityID
// is the just-emitted declaration whose corroboration drove the promotion.
func emitDecisionPromoted(ctx context.Context, promoted *manifest.Decision, sessionID string, _ string) {
	if promoted == nil {
		return
	}
	confidence := mapManifestConfidence(promoted.Confidence)
	payload, _ := json.Marshal(map[string]any{
		"new_confidence": string(promoted.Confidence),
		"value":          promoted.Value,
		"id":             promoted.ID,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(sessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionDecisionPromoted),
		Action:     activity.ActionDecisionPromoted,
		Actor: activity.Actor{
			AgentID:   promoted.Author.AgentID,
			AgentType: promoted.Author.AgentType,
		},
		Subject: activity.Subject{
			Domain:         promoted.Domain,
			TargetArtifact: promoted.ID,
		},
		Payload:     payload,
		State:       activity.StatePoint,
		Confidence:  confidence,
		SourceTable: "decision_manifest",
		SourceID:    promoted.ID,
	}
	activity.Append(ctx, act)
}

// mapManifestConfidence translates the manifest's confidence type to
// the fabric's. The two are intentionally aligned — the fabric
// confidence model was carried over from the manifest's design.
func mapManifestConfidence(c manifest.Confidence) activity.Confidence {
	switch c {
	case manifest.ConfidenceHint:
		return activity.ConfidenceHint
	case manifest.ConfidenceTentative:
		return activity.ConfidenceTentative
	case manifest.ConfidenceCommitted:
		return activity.ConfidenceCommitted
	case manifest.ConfidenceConsensus:
		return activity.ConfidenceConsensus
	}
	return ""
}

// markTentativeAlive records a Tentative decision ID in the live cache
// with the standard TTL. No-op when the cache is unavailable (test stubs).
//
// Deliberately does NOT call tentativeAlive.Wait(). An earlier revision
// did, and it created a synchronous-contention bug under bursty load
// — the Wait blocked the Declare path on the cache's flush worker,
// which under contention serialized cross-pipeline declares behind one
// another. The production semantics are intentionally
// eventually-consistent for cross-process visibility; agents that need
// read-your-writes within their own loop must Query against SQLite
// directly (the row is already committed) rather than relying on the
// cache gate.
func (s *DecisionManifestStore) markTentativeAlive(id string) {
	if s == nil || s.tentativeAlive == nil || strings.TrimSpace(id) == "" {
		return
	}
	s.tentativeAlive.SetWithTTL(id, struct{}{}, 1, tentativeTTL)
}

// unmarkTentativeAlive removes the cache gate for an ID — used after
// promotion so the SQLite row is queryable without the gate check.
func (s *DecisionManifestStore) unmarkTentativeAlive(id string) {
	if s == nil || s.tentativeAlive == nil || strings.TrimSpace(id) == "" {
		return
	}
	s.tentativeAlive.Del(id)
}

// isTentativeAlive reports whether a Tentative decision's gate entry is
// still present in the cache. Returns true when the cache is unavailable
// so test stubs without Ristretto wiring don't accidentally hide rows.
func (s *DecisionManifestStore) isTentativeAlive(id string) bool {
	if s == nil || s.tentativeAlive == nil {
		return true
	}
	_, ok := s.tentativeAlive.Get(id)
	return ok
}

// Query returns the resolved winner and all matching decisions for a
// (session, domain, scope) triple. Tentative entries whose Ristretto
// gate has expired are filtered out — equivalent to the old reaper but
// with no goroutine and no DELETE writes (the SQLite row stays as audit
// history; the gate decides visibility).
func (s *DecisionManifestStore) Query(ctx context.Context, sessionID string, in manifest.QueryDecisionsInput) (*manifest.QueryDecisionsResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("decision manifest store: not configured")
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("decision manifest store: session_id is required")
	}
	domain := strings.TrimSpace(in.Domain)
	if domain == "" {
		return nil, fmt.Errorf("query_decisions: domain is required")
	}

	all, err := scanDomainDecisions(ctx, s.db, sessionID, domain)
	if err != nil {
		return nil, err
	}

	// Filter out Tentative entries whose Ristretto gate has expired.
	// Promoted entries (Committed / Consensus) bypass the gate — they
	// are never expired. This is the cache-TTL equivalent of the old
	// reaper, evaluated at query time with zero per-entry overhead for
	// the hot promoted-decision read path.
	live := all[:0]
	for _, d := range all {
		if d.Confidence == manifest.ConfidenceTentative && !s.isTentativeAlive(d.ID) {
			continue
		}
		live = append(live, d)
	}

	matches := manifest.FilterMatching(live, in.Scope)
	manifest.SortBySpecificity(matches)
	winner := manifest.ResolveWinner(manifest.PolicyFor(domain), matches)

	return &manifest.QueryDecisionsResult{
		Winner:  winner,
		Matches: matches,
	}, nil
}

// promoteExisting returns a copy of candidate with its confidence
// promoted one tier and an Adopted event appended to history.
func promoteExisting(candidate *manifest.Decision, by manifest.AgentRef, at time.Time) (*manifest.Decision, []manifest.DecisionEvent) {
	out := *candidate
	out.History = append([]manifest.DecisionEvent(nil), candidate.History...)
	switch candidate.Confidence {
	case manifest.ConfidenceTentative:
		out.Confidence = manifest.ConfidenceCommitted
	case manifest.ConfidenceCommitted:
		out.Confidence = manifest.ConfidenceConsensus
	default:
		// Already Consensus or unknown — keep as-is.
	}
	out.History = append(out.History, manifest.DecisionEvent{
		Type:      manifest.DecisionEventAdopted,
		AgentID:   by.AgentID,
		AgentType: by.AgentType,
		Note:      fmt.Sprintf("independently corroborated; promoted to %s", out.Confidence),
		At:        at,
	})
	return &out, out.History
}

func insertDecisionTx(ctx context.Context, tx bun.Tx, d *manifest.Decision, scopeJSON, evidenceJSON string) error {
	historyJSON, err := encodeHistory(d.History)
	if err != nil {
		return err
	}
	const q = `INSERT INTO decision_manifest
		(id, session_id, domain, scope_json, value, author_agent_id, author_agent_type,
		 declared_at, confidence, evidence_json, supersedes, history_json, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = tx.ExecContext(ctx, q,
		d.ID, d.SessionID, d.Domain, scopeJSON, d.Value,
		d.Author.AgentID, d.Author.AgentType,
		d.DeclaredAt, string(d.Confidence),
		evidenceJSON, manifestNullableString(d.Supersedes), historyJSON,
		manifestNullableTime(d.ExpiresAt),
	)
	return err
}

func updateConfidenceAndHistoryTx(ctx context.Context, tx bun.Tx, id string, confidence manifest.Confidence, history []manifest.DecisionEvent) error {
	historyJSON, err := encodeHistory(history)
	if err != nil {
		return err
	}
	const q = `UPDATE decision_manifest
		SET confidence = ?, history_json = ?, expires_at = NULL
		WHERE id = ?`
	_, err = tx.ExecContext(ctx, q, string(confidence), historyJSON, id)
	return err
}

func scanDomainDecisions(ctx context.Context, db *database.BunSQLiteDB, sessionID, domain string) ([]*manifest.Decision, error) {
	const q = `SELECT id, session_id, domain, scope_json, value,
		       author_agent_id, author_agent_type, declared_at, confidence,
		       COALESCE(evidence_json, ''), COALESCE(supersedes, ''),
		       COALESCE(history_json, ''), expires_at
		FROM decision_manifest
		WHERE session_id = ? AND domain = ?`
	rows, err := db.QueryContext(ctx, q, sessionID, domain)
	if err != nil {
		return nil, fmt.Errorf("scan decisions: %w", err)
	}
	defer rows.Close()
	return scanDecisionRows(rows)
}

func scanDomainDecisionsTx(ctx context.Context, tx bun.Tx, sessionID, domain string) ([]*manifest.Decision, error) {
	const q = `SELECT id, session_id, domain, scope_json, value,
		       author_agent_id, author_agent_type, declared_at, confidence,
		       COALESCE(evidence_json, ''), COALESCE(supersedes, ''),
		       COALESCE(history_json, ''), expires_at
		FROM decision_manifest
		WHERE session_id = ? AND domain = ?`
	rows, err := tx.QueryContext(ctx, q, sessionID, domain)
	if err != nil {
		return nil, fmt.Errorf("scan decisions in tx: %w", err)
	}
	defer rows.Close()
	return scanDecisionRows(rows)
}

func scanDecisionRows(rows *sql.Rows) ([]*manifest.Decision, error) {
	var out []*manifest.Decision
	for rows.Next() {
		d := &manifest.Decision{}
		var scopeJSON, evidenceJSON, supersedes, historyJSON string
		var expires sql.NullTime
		var confidence string
		if err := rows.Scan(
			&d.ID, &d.SessionID, &d.Domain, &scopeJSON, &d.Value,
			&d.Author.AgentID, &d.Author.AgentType, &d.DeclaredAt, &confidence,
			&evidenceJSON, &supersedes, &historyJSON, &expires,
		); err != nil {
			return nil, fmt.Errorf("scan decision row: %w", err)
		}
		d.Confidence = manifest.Confidence(confidence)
		d.Supersedes = supersedes
		if expires.Valid {
			t := expires.Time
			d.ExpiresAt = &t
		}
		if scope, err := decodeScope(scopeJSON); err != nil {
			return nil, fmt.Errorf("decode scope for %s: %w", d.ID, err)
		} else {
			d.Scope = scope
		}
		if evidence, err := decodeEvidence(evidenceJSON); err != nil {
			return nil, fmt.Errorf("decode evidence for %s: %w", d.ID, err)
		} else {
			d.Evidence = evidence
		}
		if history, err := decodeHistory(historyJSON); err != nil {
			return nil, fmt.Errorf("decode history for %s: %w", d.ID, err)
		} else {
			d.History = history
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func encodeScope(s manifest.Scope) (string, error) {
	if len(s) == 0 {
		return "{}", nil
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeScope(raw string) (manifest.Scope, error) {
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return manifest.Scope{}, nil
	}
	var s manifest.Scope
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, err
	}
	if s == nil {
		return manifest.Scope{}, nil
	}
	return s, nil
}

func encodeEvidence(e []string) (string, error) {
	if len(e) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeEvidence(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeHistory(h []manifest.DecisionEvent) (string, error) {
	if len(h) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeHistory(raw string) ([]manifest.DecisionEvent, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []manifest.DecisionEvent
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func cloneScope(s manifest.Scope) manifest.Scope {
	if len(s) == 0 {
		return manifest.Scope{}
	}
	out := make(manifest.Scope, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

func manifestNullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func manifestNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

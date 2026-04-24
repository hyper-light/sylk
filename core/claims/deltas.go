package claims

import (
	"encoding/json"
	"fmt"
	"time"
)

// Delta is the sum type of bus-carried mutation records. Every board
// mutation emits exactly one Delta that fully describes what was
// committed. Deltas are authoritative — receivers act on them
// directly without re-reading the board. The board is consulted only
// for adjacent context (ancestry, siblings, lineage) that a delta
// does not carry.
//
// Each Delta has a stable DeltaKey + Sequence pair used for dedup at
// the inbox. Re-deliveries, WAL replays, and bus duplicates all
// collapse to a single logical entry.
type Delta interface {
	// DeltaKind returns the concrete delta type ("inbox", "testament",
	// "validation", "claim_status", "phase"). Used for routing and
	// JSON discrimination.
	DeltaKind() string

	// DeltaKey returns a stable unique key for dedup across
	// re-deliveries. For inbox deltas, this is (claim_id, agent_id);
	// for testament deltas, the testament_id; etc.
	DeltaKey() string

	// DeltaSequence returns the board sequence at which the mutation
	// committed. Sequence is strictly monotonic within a board and
	// defines delta ordering.
	DeltaSequence() uint64

	// DeltaSessionID identifies the session whose board produced this
	// delta. Inbox subscribers filter by session.
	DeltaSessionID() string
}

// DeltaKind constants. Used as the JSON discriminator and as part of
// topic key construction.
const (
	DeltaKindInbox        = "inbox"
	DeltaKindTestament    = "testament"
	DeltaKindValidation   = "validation"
	DeltaKindClaimStatus  = "claim_status"
	DeltaKindPhase        = "phase"
)

// ────────────────────────────────────────────────────────────────────
// InboxDelta
// ────────────────────────────────────────────────────────────────────

// InboxDelta is emitted for every Relation on every claim produced
// by a PostAction (or remediation). One delta per (claim, directed
// agent) pair — if an action posts three claims each directed at
// two agents, six InboxDeltas are published, each on its own topic.
type InboxDelta struct {
	SessionID string    `json:"session_id"`
	BoardID   string    `json:"board_id"`
	ActionID  string    `json:"action_id"`
	ClaimID   string    `json:"claim_id"`
	Sequence  uint64    `json:"sequence"`
	EmittedAt time.Time `json:"emitted_at"`

	// Relationship identifies why this delta is routed to AgentID.
	// Common values: RelationshipSubject, RelationshipEvaluator,
	// RelationshipConsulted (new), RelationshipDependsOn.
	Relationship string `json:"relationship"`

	// AgentID is the directed agent — the recipient this delta is
	// intended for. Multiple agents may be directed per claim (e.g.
	// subject + evaluator), each receiving their own delta.
	AgentID string `json:"agent_id"`

	ActionKind      ActionType        `json:"action_kind"`
	Priority        int               `json:"priority,omitempty"`
	Scope           []ClaimScopeEntry `json:"scope,omitempty"`
	ValidationCount int               `json:"validation_count"`
	DependsOn       []string          `json:"depends_on,omitempty"`
	IssuerAgentID   string            `json:"issuer_agent_id"`
	IssuerAgentType string            `json:"issuer_agent_type,omitempty"`
	Title           string            `json:"title,omitempty"`
	Description     string            `json:"description,omitempty"`
	Deadline        time.Time         `json:"deadline,omitempty"`
	Iteration       int               `json:"iteration,omitempty"`
}

func (d InboxDelta) DeltaKind() string      { return DeltaKindInbox }
func (d InboxDelta) DeltaKey() string       { return inboxDeltaKey(d.ClaimID, d.AgentID, d.Relationship) }
func (d InboxDelta) DeltaSequence() uint64  { return d.Sequence }
func (d InboxDelta) DeltaSessionID() string { return d.SessionID }

func inboxDeltaKey(claimID, agentID, relationship string) string {
	return DeltaKindInbox + "|" + claimID + "|" + agentID + "|" + relationship
}

// ────────────────────────────────────────────────────────────────────
// TestamentDelta
// ────────────────────────────────────────────────────────────────────

// TestamentDelta is emitted when a testament is submitted against a
// claim. Delivered to the claim's issuer and any evaluator.
type TestamentDelta struct {
	SessionID   string    `json:"session_id"`
	BoardID     string    `json:"board_id"`
	ClaimID     string    `json:"claim_id"`
	TestamentID string    `json:"testament_id"`
	Sequence    uint64    `json:"sequence"`
	EmittedAt   time.Time `json:"emitted_at"`

	// Verdict classifies the work outcome based on the artifact
	// mixture. "error" means at least one artifact is an error kind;
	// "partial" means a mix; "work_complete" means only success
	// artifacts.
	Verdict string `json:"verdict"`

	ArtifactCount   int      `json:"artifact_count"`
	ArtifactKinds   []string `json:"artifact_kinds"`
	SubjectAgentID  string   `json:"subject_agent_id"`
	IssuerAgentID   string   `json:"issuer_agent_id,omitempty"`
	EvaluatorAgent  string   `json:"evaluator_agent,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Confidence      string   `json:"confidence,omitempty"`
	AutoAccepted    bool     `json:"auto_accepted"`
}

// Verdict constants for TestamentDelta.
const (
	TestamentVerdictWorkComplete = "work_complete"
	TestamentVerdictError        = "error"
	TestamentVerdictPartial      = "partial"
)

func (d TestamentDelta) DeltaKind() string      { return DeltaKindTestament }
func (d TestamentDelta) DeltaKey() string       { return DeltaKindTestament + "|" + d.TestamentID }
func (d TestamentDelta) DeltaSequence() uint64  { return d.Sequence }
func (d TestamentDelta) DeltaSessionID() string { return d.SessionID }

// ────────────────────────────────────────────────────────────────────
// ValidationDelta
// ────────────────────────────────────────────────────────────────────

// ValidationDelta is emitted when a validation is evaluated
// (passed / failed / skipped). Delivered to the claim's subject and
// issuer.
type ValidationDelta struct {
	SessionID        string    `json:"session_id"`
	BoardID          string    `json:"board_id"`
	ClaimID          string    `json:"claim_id"`
	ValidationID     string    `json:"validation_id"`
	Sequence         uint64    `json:"sequence"`
	EmittedAt        time.Time `json:"emitted_at"`

	Verdict             string   `json:"verdict"` // "passed" | "failed" | "skipped"
	EvaluatorAgentID    string   `json:"evaluator_agent_id"`
	Reason              string   `json:"reason,omitempty"`
	RemainingOnClaim    int      `json:"remaining_on_claim"`
	FailedCountOnClaim  int      `json:"failed_count_on_claim"`
	ClaimAutoAccepted   bool     `json:"claim_auto_accepted"`
	RemediationClaimIDs []string `json:"remediation_claim_ids,omitempty"`
	SubjectAgentID      string   `json:"subject_agent_id,omitempty"`
	IssuerAgentID       string   `json:"issuer_agent_id,omitempty"`
}

func (d ValidationDelta) DeltaKind() string      { return DeltaKindValidation }
func (d ValidationDelta) DeltaKey() string       { return DeltaKindValidation + "|" + d.ValidationID }
func (d ValidationDelta) DeltaSequence() uint64  { return d.Sequence }
func (d ValidationDelta) DeltaSessionID() string { return d.SessionID }

// ────────────────────────────────────────────────────────────────────
// ClaimStatusDelta
// ────────────────────────────────────────────────────────────────────

// ClaimStatusDelta covers claim-level status transitions that are
// not produced by a testament or validation — specifically:
// pending → in_progress (progress updates), active → rejected
// (direct RejectClaim), and active → superseded (replacement chain).
// Auto-accept is NOT emitted here; it flows through
// ValidationDelta.ClaimAutoAccepted.
type ClaimStatusDelta struct {
	SessionID  string    `json:"session_id"`
	BoardID    string    `json:"board_id"`
	ClaimID    string    `json:"claim_id"`
	Sequence   uint64    `json:"sequence"`
	EmittedAt  time.Time `json:"emitted_at"`

	FromStatus     ClaimStatus `json:"from_status"`
	ToStatus       ClaimStatus `json:"to_status"`
	Reason         string      `json:"reason,omitempty"`
	AgentID        string      `json:"agent_id,omitempty"` // the agent that caused the transition
	SubjectAgentID string      `json:"subject_agent_id,omitempty"`
	IssuerAgentID  string      `json:"issuer_agent_id,omitempty"`

	// RemediationClaimIDs is populated when ToStatus == rejected and
	// replacements were posted alongside the rejection.
	RemediationClaimIDs []string `json:"remediation_claim_ids,omitempty"`

	// SupersededBy is populated when ToStatus == superseded.
	SupersededBy string `json:"superseded_by,omitempty"`
}

func (d ClaimStatusDelta) DeltaKind() string      { return DeltaKindClaimStatus }
func (d ClaimStatusDelta) DeltaKey() string {
	return DeltaKindClaimStatus + "|" + d.ClaimID + "|" + string(d.ToStatus)
}
func (d ClaimStatusDelta) DeltaSequence() uint64  { return d.Sequence }
func (d ClaimStatusDelta) DeltaSessionID() string { return d.SessionID }

// ────────────────────────────────────────────────────────────────────
// PhaseDelta
// ────────────────────────────────────────────────────────────────────

// PhaseDelta is emitted on board phase transitions and terminal
// completion.
type PhaseDelta struct {
	SessionID string    `json:"session_id"`
	BoardID   string    `json:"board_id"`
	TaskID    string    `json:"task_id"`
	Sequence  uint64    `json:"sequence"`
	EmittedAt time.Time `json:"emitted_at"`

	FromPhase BoardPhase `json:"from_phase"`
	ToPhase   BoardPhase `json:"to_phase"`
	Iteration int        `json:"iteration"`
	Reason    string     `json:"reason,omitempty"`
	AgentID   string     `json:"agent_id,omitempty"`
}

func (d PhaseDelta) DeltaKind() string      { return DeltaKindPhase }
func (d PhaseDelta) DeltaKey() string {
	return DeltaKindPhase + "|" + d.BoardID + "|" + string(d.ToPhase)
}
func (d PhaseDelta) DeltaSequence() uint64  { return d.Sequence }
func (d PhaseDelta) DeltaSessionID() string { return d.SessionID }

// ────────────────────────────────────────────────────────────────────
// JSON envelope
// ────────────────────────────────────────────────────────────────────

// DeltaEnvelope is the wire format for delta JSON encoding. Carries
// the DeltaKind discriminator + raw payload. Used by adapters that
// marshal deltas for transports that only speak bytes.
type DeltaEnvelope struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// MarshalDelta encodes a Delta into a DeltaEnvelope JSON byte slice.
// Returns an error only if the underlying JSON marshal fails (which
// should not happen for the concrete Delta types).
func MarshalDelta(d Delta) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("nil delta")
	}
	payload, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal delta payload: %w", err)
	}
	return json.Marshal(DeltaEnvelope{Kind: d.DeltaKind(), Payload: payload})
}

// UnmarshalDelta decodes a DeltaEnvelope JSON byte slice back into
// a concrete Delta based on the Kind discriminator.
func UnmarshalDelta(data []byte) (Delta, error) {
	var env DeltaEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return decodeDeltaPayload(env.Kind, env.Payload)
}

func decodeDeltaPayload(kind string, payload json.RawMessage) (Delta, error) {
	switch kind {
	case DeltaKindInbox:
		var d InboxDelta
		return d, decodeInto(payload, &d)
	case DeltaKindTestament:
		var d TestamentDelta
		return d, decodeInto(payload, &d)
	case DeltaKindValidation:
		var d ValidationDelta
		return d, decodeInto(payload, &d)
	case DeltaKindClaimStatus:
		var d ClaimStatusDelta
		return d, decodeInto(payload, &d)
	case DeltaKindPhase:
		var d PhaseDelta
		return d, decodeInto(payload, &d)
	default:
		return nil, fmt.Errorf("unknown delta kind %q", kind)
	}
}

func decodeInto(payload json.RawMessage, dst any) error {
	if err := json.Unmarshal(payload, dst); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────
// Verdict derivation
// ────────────────────────────────────────────────────────────────────

// Artifact kind prefixes classified as errors. Anchored to the "error"
// family per the errors-as-artifacts principle in docs/CLAIMS.md §2.6.
const (
	ArtifactKindError           = "error"
	ArtifactKindErrorTrace      = "error_trace"
	ArtifactKindErrorDiagnostic = "error_diagnostic"
)

// DeriveTestamentVerdict classifies a testament's artifact mixture
// into "work_complete" | "error" | "partial". Kept exported so
// adapters can classify equivalently.
func DeriveTestamentVerdict(artifacts []*Artifact) string {
	if len(artifacts) == 0 {
		return TestamentVerdictWorkComplete
	}
	errorCount := 0
	for _, a := range artifacts {
		if a == nil {
			continue
		}
		if isErrorArtifactKind(a.Kind) {
			errorCount++
		}
	}
	if errorCount == 0 {
		return TestamentVerdictWorkComplete
	}
	if errorCount == len(artifacts) {
		return TestamentVerdictError
	}
	return TestamentVerdictPartial
}

func isErrorArtifactKind(kind string) bool {
	return kind == ArtifactKindError ||
		kind == ArtifactKindErrorTrace ||
		kind == ArtifactKindErrorDiagnostic
}

// CollectArtifactKinds returns the unique artifact kinds in
// insertion order. Used to populate TestamentDelta.ArtifactKinds.
func CollectArtifactKinds(artifacts []*Artifact) []string {
	if len(artifacts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(artifacts))
	out := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		if a == nil || a.Kind == "" {
			continue
		}
		if _, ok := seen[a.Kind]; ok {
			continue
		}
		seen[a.Kind] = struct{}{}
		out = append(out, a.Kind)
	}
	return out
}

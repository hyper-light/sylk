package forest

import (
	"context"
)

// MEM-02: specialized forest projections.
//
// Retrieve returns a generic []*BranchPacket across all requested
// families. That's the right primitive but the wrong shape for
// downstream agents — Architect does not want to case-switch on
// branch.Family at call sites, and Librarian/Academic want typed,
// state-partitioned views ("candidate vs chosen decisions", "validated
// vs contradicted evidence") so their prompts do not have to
// re-discover that partition every turn.
//
// Each projection here:
//
//  1. Filters Retrieve to exactly one family.
//  2. Partitions packets by BranchState and, where useful, by conflict
//     status via BranchPacket.HasUnresolvedConflicts() (MEM-05).
//  3. Returns a typed struct with named slices, so the consumer binds
//     directly to the semantic buckets it actually cares about.
//
// Why per-family and not one mega-struct: keeping one projection per
// family lets each agent request only what it needs — the Academic
// usually wants Evidence and Outcome; the Architect wants Intent,
// Constraint, and Decision; Librarian wants Preference and Capability.
// A single Retrieve() call that returned every family would bloat the
// prompt and weaken ranking by forcing cross-family score normalization.

// ProjectionInput configures a single-family projection query.
// Shape mirrors ResolveIntentInput so agents can reuse the same input
// builder across projection types.
type ProjectionInput struct {
	Query     string        `json:"query"`
	SessionID string        `json:"session_id,omitempty"`
	TaskID    string        `json:"task_id,omitempty"`
	AgentID   string        `json:"agent_id,omitempty"`
	AgentType string        `json:"agent_type,omitempty"`
	IntentID  string        `json:"intent_id,omitempty"`
	Horizon   CanopyHorizon `json:"horizon,omitempty"`
	Limit     int           `json:"limit,omitempty"`
}

// -----------------------------------------------------------------------------
// Typed per-family projection outputs.
// -----------------------------------------------------------------------------

// IntentProjection is the family-specific view of active intent branches.
// Active = current intents the system is pursuing. Dormant = latent
// hypotheses the system has considered but deactivated; kept because
// they often resurface when a task shifts.
type IntentProjection struct {
	Query         string         `json:"query"`
	PrimaryIntent string         `json:"primary_intent,omitempty"`
	Active        []BranchPacket `json:"active,omitempty"`
	Dormant       []BranchPacket `json:"dormant,omitempty"`
	Flagged       []BranchPacket `json:"flagged,omitempty"`
}

// ConstraintProjection splits constraints into enforced (active / validated)
// and disputed (contradicted). Callers must treat disputed constraints as
// open questions rather than requirements.
type ConstraintProjection struct {
	Query    string         `json:"query"`
	Enforced []BranchPacket `json:"enforced,omitempty"`
	Disputed []BranchPacket `json:"disputed,omitempty"`
	Flagged  []BranchPacket `json:"flagged,omitempty"`
}

// EvidenceProjection partitions evidence into current (active / validated)
// and refuted (contradicted / superseded). Conflicted evidence is also
// lifted into Flagged so the consumer can decide to ignore, escalate, or
// request clarification.
type EvidenceProjection struct {
	Query   string         `json:"query"`
	Current []BranchPacket `json:"current,omitempty"`
	Refuted []BranchPacket `json:"refuted,omitempty"`
	Flagged []BranchPacket `json:"flagged,omitempty"`
}

// DecisionProjection captures decision state across the classic
// candidate → chosen → superseded lifecycle. Contradicted decisions
// are open forks that need architect review.
type DecisionProjection struct {
	Query        string         `json:"query"`
	Candidates   []BranchPacket `json:"candidates,omitempty"`
	Chosen       []BranchPacket `json:"chosen,omitempty"`
	Superseded   []BranchPacket `json:"superseded,omitempty"`
	Contradicted []BranchPacket `json:"contradicted,omitempty"`
	Flagged      []BranchPacket `json:"flagged,omitempty"`
}

// OutcomeProjection separates successes (validated) from regressions
// (contradicted). Pending covers outcomes recorded but not yet
// confirmed either way.
type OutcomeProjection struct {
	Query       string         `json:"query"`
	Successes   []BranchPacket `json:"successes,omitempty"`
	Regressions []BranchPacket `json:"regressions,omitempty"`
	Pending     []BranchPacket `json:"pending,omitempty"`
	Flagged     []BranchPacket `json:"flagged,omitempty"`
}

// PreferenceProjection isolates active preferences from dormant ones.
// Dormant preferences often encode past user corrections and should be
// surfaced to callers as weak priors rather than hard requirements.
type PreferenceProjection struct {
	Query   string         `json:"query"`
	Active  []BranchPacket `json:"active,omitempty"`
	Dormant []BranchPacket `json:"dormant,omitempty"`
	Flagged []BranchPacket `json:"flagged,omitempty"`
}

// CapabilityProjection separates proven capabilities (validated) from
// unverified claims (candidate / active) and known failure modes
// (contradicted). This lets the Orchestrator bias toward proven paths.
type CapabilityProjection struct {
	Query      string         `json:"query"`
	Proven     []BranchPacket `json:"proven,omitempty"`
	Claimed    []BranchPacket `json:"claimed,omitempty"`
	Unreliable []BranchPacket `json:"unreliable,omitempty"`
	Flagged    []BranchPacket `json:"flagged,omitempty"`
}

// OpportunityProjection exposes adjacent-value branches: proposed
// upgrades not yet accepted, accepted-in-flight ones, and rejected.
type OpportunityProjection struct {
	Query     string         `json:"query"`
	Proposed  []BranchPacket `json:"proposed,omitempty"`
	Accepted  []BranchPacket `json:"accepted,omitempty"`
	Rejected  []BranchPacket `json:"rejected,omitempty"`
	Flagged   []BranchPacket `json:"flagged,omitempty"`
}

// -----------------------------------------------------------------------------
// Entry-point methods, one per family.
// -----------------------------------------------------------------------------

// ProjectIntent returns a family-typed Intent view.
func (m *MemoryForest) ProjectIntent(ctx context.Context, input ProjectionInput) (*IntentProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyIntent, false)
	if err != nil {
		return nil, err
	}
	out := &IntentProjection{Query: input.Query}
	for i := range packets {
		packet := packets[i]
		if packet == nil || packet.Branch == nil {
			continue
		}
		if packet.HasUnresolvedConflicts() {
			out.Flagged = append(out.Flagged, *packet)
			continue
		}
		if packet.Branch.State == BranchStateDormant {
			out.Dormant = append(out.Dormant, *packet)
			continue
		}
		out.Active = append(out.Active, *packet)
		if out.PrimaryIntent == "" {
			out.PrimaryIntent = packet.Branch.Summary
		}
	}
	return out, nil
}

// ProjectConstraints returns the Constraint family view.
func (m *MemoryForest) ProjectConstraints(ctx context.Context, input ProjectionInput) (*ConstraintProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyConstraint, true)
	if err != nil {
		return nil, err
	}
	out := &ConstraintProjection{Query: input.Query}
	for i := range packets {
		placeConstraintPacket(packets[i], out)
	}
	return out, nil
}

// ProjectEvidence returns the Evidence family view.
func (m *MemoryForest) ProjectEvidence(ctx context.Context, input ProjectionInput) (*EvidenceProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyEvidence, true)
	if err != nil {
		return nil, err
	}
	out := &EvidenceProjection{Query: input.Query}
	for i := range packets {
		placeEvidencePacket(packets[i], out)
	}
	return out, nil
}

// ProjectDecisions returns the Decision projection view.
//
// Issue #11 Phase 3: Decision was merged into Intent (a decision is
// "intent + selected branch"). The DecisionProjection type is
// retained as the operator-facing surface; the underlying query
// targets TreeFamilyIntent.
func (m *MemoryForest) ProjectDecisions(ctx context.Context, input ProjectionInput) (*DecisionProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyIntent, true)
	if err != nil {
		return nil, err
	}
	out := &DecisionProjection{Query: input.Query}
	for i := range packets {
		placeDecisionPacket(packets[i], out)
	}
	return out, nil
}

// ProjectOutcomes returns the Outcome family view.
func (m *MemoryForest) ProjectOutcomes(ctx context.Context, input ProjectionInput) (*OutcomeProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyOutcome, true)
	if err != nil {
		return nil, err
	}
	out := &OutcomeProjection{Query: input.Query}
	for i := range packets {
		placeOutcomePacket(packets[i], out)
	}
	return out, nil
}

// ProjectPreferences returns the Preference projection view.
//
// Issue #11 Phase 3: Preference was subsumed by Constraint with
// ConstraintSeverity="soft". The PreferenceProjection type is
// retained as the operator-facing surface; the query targets
// TreeFamilyConstraint.
func (m *MemoryForest) ProjectPreferences(ctx context.Context, input ProjectionInput) (*PreferenceProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyConstraint, false)
	if err != nil {
		return nil, err
	}
	out := &PreferenceProjection{Query: input.Query}
	for i := range packets {
		placePreferencePacket(packets[i], out)
	}
	return out, nil
}

// ProjectCapabilities returns the Capability projection view.
//
// Issue #11 Phase 3: Capability was merged into Intent (an agent's
// affordance is "what it can pursue"). The CapabilityProjection
// type is retained as the operator-facing surface; the query
// targets TreeFamilyIntent.
func (m *MemoryForest) ProjectCapabilities(ctx context.Context, input ProjectionInput) (*CapabilityProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyIntent, true)
	if err != nil {
		return nil, err
	}
	out := &CapabilityProjection{Query: input.Query}
	for i := range packets {
		placeCapabilityPacket(packets[i], out)
	}
	return out, nil
}

// ProjectOpportunities returns the Opportunity projection view.
//
// Issue #11 Phase 3: Opportunity was merged into Intent (a time-
// bound capability/intent match becomes a time-bound Intent). The
// OpportunityProjection type is retained as the operator-facing
// surface; the query targets TreeFamilyIntent.
func (m *MemoryForest) ProjectOpportunities(ctx context.Context, input ProjectionInput) (*OpportunityProjection, error) {
	packets, err := m.retrieveFamily(ctx, input, TreeFamilyIntent, true)
	if err != nil {
		return nil, err
	}
	out := &OpportunityProjection{Query: input.Query}
	for i := range packets {
		placeOpportunityPacket(packets[i], out)
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// Internals.
// -----------------------------------------------------------------------------

// retrieveFamily issues a single-family Retrieve. The wide
// IncludeCounterEvidence toggle is passed through because some
// projections (Decision/Outcome/Evidence) need counter-evidence to
// honestly populate the "disputed" bucket, while Intent/Preference
// lean toward positive-only prompts.
func (m *MemoryForest) retrieveFamily(
	ctx context.Context,
	input ProjectionInput,
	family TreeFamily,
	includeCounter bool,
) ([]*BranchPacket, error) {
	return m.Retrieve(ctx, Query{
		Query:                  input.Query,
		SessionID:              input.SessionID,
		TaskID:                 input.TaskID,
		AgentID:                input.AgentID,
		AgentType:              input.AgentType,
		IntentID:               input.IntentID,
		Horizon:                input.Horizon,
		Limit:                  input.Limit,
		Families:               []TreeFamily{family},
		IncludeCounterEvidence: includeCounter,
	})
}

// Per-family placement helpers. Each keeps cyclomatic complexity low
// by branching on a single dimension (state or conflict) and delegates
// the generic conflict-first gating to flagIfConflicted.

func placeConstraintPacket(packet *BranchPacket, out *ConstraintProjection) {
	if flagIfNil(packet) {
		return
	}
	if packet.HasUnresolvedConflicts() {
		out.Flagged = append(out.Flagged, *packet)
		return
	}
	if packet.Branch.State == BranchStateContradicted {
		out.Disputed = append(out.Disputed, *packet)
		return
	}
	out.Enforced = append(out.Enforced, *packet)
}

func placeEvidencePacket(packet *BranchPacket, out *EvidenceProjection) {
	if flagIfNil(packet) {
		return
	}
	if packet.HasUnresolvedConflicts() {
		out.Flagged = append(out.Flagged, *packet)
		return
	}
	if isRefutedState(packet.Branch.State) {
		out.Refuted = append(out.Refuted, *packet)
		return
	}
	out.Current = append(out.Current, *packet)
}

func placeDecisionPacket(packet *BranchPacket, out *DecisionProjection) {
	if flagIfNil(packet) {
		return
	}
	if packet.HasUnresolvedConflicts() {
		out.Flagged = append(out.Flagged, *packet)
		return
	}
	switch packet.Branch.State {
	case BranchStateCandidate:
		out.Candidates = append(out.Candidates, *packet)
	case BranchStateSuperseded, BranchStateDormant:
		out.Superseded = append(out.Superseded, *packet)
	case BranchStateContradicted:
		out.Contradicted = append(out.Contradicted, *packet)
	default:
		out.Chosen = append(out.Chosen, *packet)
	}
}

func placeOutcomePacket(packet *BranchPacket, out *OutcomeProjection) {
	if flagIfNil(packet) {
		return
	}
	if packet.HasUnresolvedConflicts() {
		out.Flagged = append(out.Flagged, *packet)
		return
	}
	switch packet.Branch.State {
	case BranchStateValidated:
		out.Successes = append(out.Successes, *packet)
	case BranchStateContradicted:
		out.Regressions = append(out.Regressions, *packet)
	default:
		out.Pending = append(out.Pending, *packet)
	}
}

func placePreferencePacket(packet *BranchPacket, out *PreferenceProjection) {
	if flagIfNil(packet) {
		return
	}
	if packet.HasUnresolvedConflicts() {
		out.Flagged = append(out.Flagged, *packet)
		return
	}
	if packet.Branch.State == BranchStateDormant {
		out.Dormant = append(out.Dormant, *packet)
		return
	}
	out.Active = append(out.Active, *packet)
}

func placeCapabilityPacket(packet *BranchPacket, out *CapabilityProjection) {
	if flagIfNil(packet) {
		return
	}
	if packet.HasUnresolvedConflicts() {
		out.Flagged = append(out.Flagged, *packet)
		return
	}
	switch packet.Branch.State {
	case BranchStateValidated:
		out.Proven = append(out.Proven, *packet)
	case BranchStateContradicted:
		out.Unreliable = append(out.Unreliable, *packet)
	default:
		out.Claimed = append(out.Claimed, *packet)
	}
}

func placeOpportunityPacket(packet *BranchPacket, out *OpportunityProjection) {
	if flagIfNil(packet) {
		return
	}
	if packet.HasUnresolvedConflicts() {
		out.Flagged = append(out.Flagged, *packet)
		return
	}
	switch packet.Branch.State {
	case BranchStateCandidate:
		out.Proposed = append(out.Proposed, *packet)
	case BranchStateSuperseded, BranchStateContradicted:
		out.Rejected = append(out.Rejected, *packet)
	default:
		out.Accepted = append(out.Accepted, *packet)
	}
}

// flagIfNil is the shared nil-guard for placement helpers. Returning
// true tells the caller "skip me entirely" without forcing every place
// function to duplicate a defensive nil check.
func flagIfNil(packet *BranchPacket) bool {
	return packet == nil || packet.Branch == nil
}

// isRefutedState returns true for evidence states that represent
// contradicted or superseded evidence. Callers treat these as "keep for
// audit, do not act on". Factored out so both the Evidence and
// Capability projections can reuse the same rule if they diverge later.
func isRefutedState(state BranchState) bool {
	return state == BranchStateContradicted || state == BranchStateSuperseded
}

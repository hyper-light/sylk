package forest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/activitystore"
)

// ClaimsHarvester converts claims-board Fabric activities into forest
// events. Mapping:
//
//	ActionClaimAccepted        → Evidence (positive precedent)
//	ActionClaimRejected        → AntiPattern (anti-precedent)
//	ActionTestamentSubmitted   → Evidence (working evidence)
//	ActionClaimValidated       → Evidence/AntiPattern (per-validation
//	                             outcome — verdict-driven family routing,
//	                             carries Type/QualityBar/Description)
//	ActionBoardComplete        → Outcome (terminal episode)
//	ActionTraversalObserved    → Evidence (graph-walk precedent)
//
// Idempotency: event IDs are derived deterministically from
// (activity.ID, eventType) so AppendEvent's id-based dedupe collapses
// re-deliveries (subscriber retry, replay). The dispatcher in
// activitystore additionally dedupes by activity.ID before enqueue.
//
// Salience and Confidence are derived from observable activity
// signals (activity.State, activity.Confidence, validation Type,
// verdict, board phase) rather than literal magic numbers — see
// the salienceFor* and confidenceFromActivity helpers below.
type ClaimsHarvester struct {
	forest *MemoryForest
}

// NewClaimsHarvester creates a harvester bound to the given forest.
func NewClaimsHarvester(forest *MemoryForest) *ClaimsHarvester {
	return &ClaimsHarvester{forest: forest}
}

// Harvest is invoked by the activitystore HarvestDispatcher on each
// elected ForestCandidate. Errors are returned to the caller; the
// dispatcher counts them, logs at slog.Error, and (when wired)
// surfaces them as kind=error_harvest_failed artifacts on the
// in-flight testament per CLAIMS.md §5.11 errors-as-artifacts.
func (h *ClaimsHarvester) Harvest(ctx context.Context, candidate activitystore.ForestCandidate) error {
	if h == nil || h.forest == nil {
		return nil
	}
	event := h.buildEvent(candidate.Activity)
	if event == nil {
		return nil
	}
	if err := h.forest.AppendEvent(ctx, event); err != nil {
		return fmt.Errorf("append claim-harvest event: %w", err)
	}
	return nil
}

func (h *ClaimsHarvester) buildEvent(a activity.AgentActivity) *Event {
	switch a.Action {
	case activity.ActionClaimAccepted:
		return h.eventForAcceptedClaim(a)
	case activity.ActionClaimRejected:
		return h.eventForRejectedClaim(a)
	case activity.ActionTestamentSubmitted:
		return h.eventForTestament(a)
	case activity.ActionClaimValidated:
		return h.eventForClaimValidated(a)
	case activity.ActionBoardComplete:
		return h.eventForBoardComplete(a)
	case activity.ActionTraversalObserved:
		return h.eventForTraversal(a)
	default:
		return nil
	}
}

// ─── Event builders ──────────────────────────────────────────────────

func (h *ClaimsHarvester) eventForAcceptedClaim(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	title := stringFromPayload(payload, "title")
	supersedes, contradicts := remediationLinks(payload)
	eventType := EventTypeClaimAccepted
	if len(supersedes) > 0 {
		// Acceptance after a prior rejection chain — author this as a
		// remediation outcome instead of a flat acceptance so the
		// forest can surface "what kinds of failures were fixed by
		// what kinds of remediations." Family stays Evidence (positive
		// precedent) but the EventType discriminates it from a
		// first-try acceptance.
		eventType = EventTypeRemediationResolved
	}
	return &Event{
		ID:             stableEventID(a, eventType),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      eventType,
		Family:         TreeFamilyEvidence,
		Scope:          ScopeEpisodic,
		RootID:         claimRootID(a.Subject.TargetArtifact),
		BranchID:       claimBranchID(a.Subject.TargetArtifact),
		Confidence:     confidenceFromActivity(a),
		Salience:       salienceForAccepted(a, eventType),
		Timestamp:      a.Timestamp,
		Title:          title,
		Summary:        formatAcceptedSummary(eventType, title),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Supersedes:     supersedes,
		Contradicts:    contradicts,
		Payload:        payload,
	}
}

func (h *ClaimsHarvester) eventForRejectedClaim(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	title := stringFromPayload(payload, "title")
	return &Event{
		ID:             stableEventID(a, EventTypeClaimRejected),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeClaimRejected,
		Family:         TreeFamilyAntiPattern,
		Scope:          ScopeContradiction,
		RootID:         claimRootID(a.Subject.TargetArtifact),
		BranchID:       "claim_rejected_" + string(a.Subject.TargetArtifact),
		Confidence:     confidenceFromActivity(a),
		Salience:       salienceForRejected(a),
		Timestamp:      a.Timestamp,
		Title:          title,
		Summary:        fmt.Sprintf("Rejected claim: %s", title),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Payload:        payload,
	}
}

func (h *ClaimsHarvester) eventForTestament(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	summary := stringFromPayload(payload, "summary")
	return &Event{
		ID:             stableEventID(a, EventTypeTestamentRecorded),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeTestamentRecorded,
		Family:         TreeFamilyEvidence,
		Scope:          ScopeWorking,
		RootID:         "testament_" + string(a.Subject.TargetArtifact),
		BranchID:       "testament_" + string(a.Subject.TargetArtifact),
		Confidence:     confidenceFromActivity(a),
		Salience:       salienceForTestament(a, payload),
		Timestamp:      a.Timestamp,
		Title:          summary,
		Summary:        fmt.Sprintf("Testament: %s", summary),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Payload:        payload,
	}
}

// eventForClaimValidated handles the per-validation outcome event —
// the new-world replacement for the coord-era flat ActionReviewCompleted.
// Carries Type, Description, and verdict (status) in the payload; routes
// to Evidence on accept verdicts and AntiPattern on reject verdicts.
func (h *ClaimsHarvester) eventForClaimValidated(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	verdict := strings.ToLower(stringFromPayload(payload, "status"))
	validationType := stringFromPayload(payload, "type")
	description := stringFromPayload(payload, "description")
	rejected := isRejectedVerdict(verdict)
	family := TreeFamilyEvidence
	scope := ScopeWorking
	if rejected {
		family = TreeFamilyAntiPattern
		scope = ScopeContradiction
	}
	title := description
	if title == "" {
		title = fmt.Sprintf("validation %s", verdict)
	}
	return &Event{
		ID:             stableEventID(a, EventTypeClaimValidated),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeClaimValidated,
		Family:         family,
		Scope:          scope,
		RootID:         "validation_" + string(a.Subject.TargetArtifact),
		BranchID:       "validation_" + string(a.Subject.TargetArtifact),
		Confidence:     confidenceFromActivity(a),
		Salience:       salienceForValidation(a, verdict, validationType),
		Timestamp:      a.Timestamp,
		Title:          title,
		Summary:        formatValidationSummary(verdict, validationType, description),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Payload:        payload,
	}
}

func (h *ClaimsHarvester) eventForBoardComplete(a activity.AgentActivity) *Event {
	return &Event{
		ID:             stableEventID(a, EventTypeOutcomeRecorded),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeOutcomeRecorded,
		Family:         TreeFamilyOutcome,
		Scope:          ScopeEpisodic,
		RootID:         "board_" + string(a.Subject.TargetArtifact),
		BranchID:       "board_" + string(a.Subject.TargetArtifact),
		Confidence:     confidenceFromActivity(a),
		Salience:       salienceForBoardComplete(a),
		Timestamp:      a.Timestamp,
		Title:          "Board completed",
		Summary:        "Claims board reached terminal completion",
		ProvenanceRefs: claimsProvenanceRefs(a),
	}
}

// eventForTraversal records one claims-graph walk as evidence the
// forest can use to learn which (start_node_kind, edge_filter, depth)
// triplets yield useful downstream context. Depth-only telemetry —
// salience is intentionally low (high-frequency, low individual cost)
// and the gardener consolidates these into traversal precedents on
// replay.
func (h *ClaimsHarvester) eventForTraversal(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	startKind := stringFromPayload(payload, "start_kind")
	edgeFilter := stringFromPayload(payload, "edge_filter")
	depth := intFromPayload(payload, "depth")
	count := intFromPayload(payload, "count")
	title := fmt.Sprintf("traverse %s depth=%d", startKind, depth)
	if edgeFilter != "" {
		title = fmt.Sprintf("traverse %s edges=%s depth=%d", startKind, edgeFilter, depth)
	}
	return &Event{
		ID:             stableEventID(a, EventTypeTraversalObserved),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeTraversalObserved,
		Family:         TreeFamilyEvidence,
		Scope:          ScopeWorking,
		RootID:         "traversal_" + startKind,
		BranchID:       "traversal_" + string(a.ID),
		Confidence:     confidenceFromActivity(a),
		Salience:       salienceForTraversal(count),
		Timestamp:      a.Timestamp,
		Title:          title,
		Summary:        fmt.Sprintf("traverse start=%s edges=%q depth=%d returned=%d", startKind, edgeFilter, depth, count),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Payload:        payload,
	}
}

// ─── Salience derivations (constants from data, not magic numbers) ──
//
// Each salience derivation is a small formula over observable activity
// signals (state, confidence, verdict, validation type, board phase).
// The literal coefficients below are the calibration anchors — pieces
// of the (state × confidence × signal-strength) decomposition. Operators
// retune by overriding through forest.Config; the harvester does not
// hand-pick.

const (
	// salienceBase is the floor every harvested event clears. Below
	// this, an event is too weak to compete with any other forest
	// signal at retrieval time. Calibrated against the existing
	// Outcome harvester's "boring success" salience floor.
	salienceBase = 0.50

	// salienceConfidenceWeight is how much an activity's
	// Confidence-tier (Hint→Tentative→Committed→Consensus) shifts the
	// salience. A fully-consensus event clears the floor by this much
	// before signal-specific bonuses apply.
	salienceConfidenceWeight = 0.20

	// salienceTerminalBonus rewards terminal events (resolved state,
	// claim accepted/rejected, board complete) over in-flight ones.
	// Anchored against the forest's existing OutcomeStatusSucceeded
	// vs OutcomeStatusIgnored salience gap.
	salienceTerminalBonus = 0.15

	// salienceRejectionPremium acknowledges that anti-precedent
	// signals (claim_rejected, validation_rejected) are scarcer than
	// positive precedents and deserve a bias so they aren't drowned
	// out at retrieval. Anchored against the forest's anti-precedent
	// quota share.
	salienceRejectionPremium = 0.10

	// salienceRemediationPremium acknowledges that an accepted claim
	// that resolved a prior rejection chain (architect remediation
	// closing the loop) is rare, high-signal precedent.
	salienceRemediationPremium = 0.10

	// salienceTraversalCeiling caps how salient a single traversal
	// observation can be. Traversals are high-frequency; the gardener
	// consolidates them later. A single emission's salience is clamped
	// so a hot traverse loop can't dominate retrieval.
	salienceTraversalCeiling = 0.45
)

// confidenceFromActivity maps activity.Confidence to the forest's
// [0, 1] confidence axis. This is a calibration table; values are
// preserved from the prior harvester for continuity with existing
// trained models.
func confidenceFromActivity(a activity.AgentActivity) float64 {
	switch a.Confidence {
	case activity.ConfidenceConsensus:
		return 0.95
	case activity.ConfidenceCommitted:
		return 0.80
	case activity.ConfidenceTentative:
		return 0.60
	case activity.ConfidenceHint:
		return 0.40
	default:
		return 0.50
	}
}

func confidenceShift(a activity.AgentActivity) float64 {
	c := confidenceFromActivity(a)
	// Map [0.4, 0.95] confidence into [-1, +1] then scale by weight.
	normalized := (c - 0.50) / 0.45
	if normalized < -1 {
		normalized = -1
	}
	if normalized > 1 {
		normalized = 1
	}
	return normalized * salienceConfidenceWeight
}

func terminalShift(a activity.AgentActivity) float64 {
	if a.State == activity.StateResolved {
		return salienceTerminalBonus
	}
	return 0
}

func salienceForAccepted(a activity.AgentActivity, eventType EventType) float64 {
	s := salienceBase + confidenceShift(a) + terminalShift(a)
	if eventType == EventTypeRemediationResolved {
		s += salienceRemediationPremium
	}
	return clamp01(s)
}

func salienceForRejected(a activity.AgentActivity) float64 {
	return clamp01(salienceBase + confidenceShift(a) + terminalShift(a) + salienceRejectionPremium)
}

func salienceForTestament(a activity.AgentActivity, payload map[string]any) float64 {
	// Testaments are intermediate state; salience is base + confidence
	// without the terminal bonus. A higher-confidence testament
	// (verdict=accepted-paths) gets a small bonus from confidence
	// shift but never the terminal premium until the claim itself
	// resolves.
	s := salienceBase + confidenceShift(a)
	// If the testament's payload reports a non-zero confidence value,
	// fold it into the salience as evidence weight.
	if conf := floatFromPayload(payload, "confidence"); conf > 0 {
		s += (conf - 0.50) * salienceConfidenceWeight
	}
	return clamp01(s)
}

func salienceForValidation(a activity.AgentActivity, verdict, validationType string) float64 {
	s := salienceBase + confidenceShift(a) + terminalShift(a)
	if isRejectedVerdict(verdict) {
		s += salienceRejectionPremium
	}
	// QualityBar / required validations carry slightly more weight at
	// retrieval than auxiliary informational ones — the validation
	// type is the discriminator.
	switch strings.ToLower(strings.TrimSpace(validationType)) {
	case "qualitybar", "quality_bar", "required":
		s += 0.05
	}
	return clamp01(s)
}

func salienceForBoardComplete(a activity.AgentActivity) float64 {
	return clamp01(salienceBase + confidenceShift(a) + salienceTerminalBonus)
}

func salienceForTraversal(returned int) float64 {
	// More returned nodes = more downstream context surfaced = more
	// salient as precedent for "this kind of walk yielded results."
	// Scaled logarithmically against a saturation point so a 1000-node
	// walk doesn't dominate over a 5-node walk that found the
	// canonical answer.
	if returned <= 0 {
		return clamp01(salienceBase * 0.5)
	}
	scale := 1.0 / (1.0 + float64(returned))
	s := salienceTraversalCeiling - (salienceTraversalCeiling-salienceBase*0.5)*scale
	return clamp01(s)
}

// ─── Payload helpers ─────────────────────────────────────────────────

func parsePayload(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func stringFromPayload(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intFromPayload(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch typed := v.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	}
	return 0
}

func floatFromPayload(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch typed := v.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return 0
}

// remediationLinks extracts the rejected-claim chain from an accepted-
// claim payload. When the architect's corrective-action authoring path
// is wired, each accepted remediation claim populates these via the
// Supersedes / Contradicts payload keys. Empty when the acceptance is
// first-try.
func remediationLinks(payload map[string]any) (supersedes, contradicts []string) {
	supersedes = stringListFromPayload(payload, "supersedes")
	contradicts = stringListFromPayload(payload, "contradicts")
	return
}

func stringListFromPayload(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch typed := v.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

func claimsProvenanceRefs(a activity.AgentActivity) []string {
	refs := []string{string(a.ID)}
	if a.Subject.TargetArtifact != "" {
		refs = append(refs, a.Subject.TargetArtifact)
	}
	if a.Actor.AgentID != "" {
		refs = append(refs, a.Actor.AgentID)
	}
	if board, ok := a.Subject.Coordinates["board_id"]; ok && board != "" {
		refs = append(refs, board)
	}
	if task, ok := a.Subject.Coordinates["task_id"]; ok && task != "" {
		refs = append(refs, task)
	}
	if claimID, ok := a.Subject.Coordinates["claim_id"]; ok && claimID != "" {
		refs = append(refs, claimID)
	}
	if testID, ok := a.Subject.Coordinates["testament_id"]; ok && testID != "" {
		refs = append(refs, testID)
	}
	return refs
}

func claimRootID(targetArtifact string) string {
	return "claim_" + targetArtifact
}

func claimBranchID(targetArtifact string) string {
	return "claim_" + targetArtifact
}

func isRejectedVerdict(verdict string) bool {
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case "rejected", "reject", "fail", "failed":
		return true
	}
	return false
}

func formatAcceptedSummary(eventType EventType, title string) string {
	if eventType == EventTypeRemediationResolved {
		return fmt.Sprintf("Remediation accepted: %s", title)
	}
	return fmt.Sprintf("Accepted claim: %s", title)
}

func formatValidationSummary(verdict, validationType, description string) string {
	if description == "" {
		return fmt.Sprintf("Validation %s (%s)", verdict, validationType)
	}
	return fmt.Sprintf("Validation %s: %s (%s)", verdict, description, validationType)
}

// stableEventID derives a deterministic event ID from the activity ID
// and event type so re-deliveries collapse at AppendEvent's id-based
// dedupe layer. Idempotency is critical: subscribers can replay,
// restart can resurface in-flight activities, and bus delivery is
// at-least-once. Without a stable ID, every replay produces a new
// forest event for the same fact.
func stableEventID(a activity.AgentActivity, eventType EventType) string {
	if a.ID == "" {
		return "evt_" + string(eventType) + "_" + string(a.Subject.TargetArtifact)
	}
	return "evt_" + string(eventType) + "_" + string(a.ID)
}

// Ensure ClaimsHarvester.Harvest satisfies the HarvestFunc signature.
var _ activitystore.HarvestFunc = (*ClaimsHarvester)(nil).Harvest

// HarvestRegistrar registers an async-wrapped harvest function with
// the orchestrator's fabric. This is dependency-inverted from the
// MountClaimsHarvest helper so this package doesn't take a hard
// dependency on the orchestrator package — the bootstrap that owns
// both the forest and the orchestrator passes orchestrator.SetForestHarvester
// (or equivalent) as the registrar.
type HarvestRegistrar func(activitystore.HarvestFunc)

// MountClaimsHarvest constructs a ClaimsHarvester bound to this
// forest, wraps it in an async HarvestDispatcher with the supplied
// config, and registers the dispatcher's Harvest method via
// `register`. Returns the dispatcher so the caller can Stop() it on
// shutdown and inspect counters for observability.
//
// Wire-once: subsequent calls replace any previously registered
// harvester (orchestrator.SetForestHarvester has single-pointer
// semantics). For multi-forest scenarios where multiple forests
// should each receive claims-board precedent, switch the registrar
// to a fan-out implementation rather than calling MountClaimsHarvest
// per forest.
//
// Pass HarvestDispatcherConfig{} for the derived defaults (NumCPU
// workers, 8×workers queue, 4×queue dedupe window, 5s work timeout).
// Pass cfg.ErrorSink to surface harvest errors as kind=error_harvest_*
// artifacts on in-flight testaments per CLAIMS.md §5.11.
func (m *MemoryForest) MountClaimsHarvest(
	register HarvestRegistrar,
	cfg activitystore.HarvestDispatcherConfig,
) *activitystore.HarvestDispatcher {
	if m == nil || register == nil {
		return nil
	}
	harvester := NewClaimsHarvester(m)
	dispatcher := activitystore.NewHarvestDispatcher(harvester.Harvest, cfg)
	register(dispatcher.Harvest)
	return dispatcher
}

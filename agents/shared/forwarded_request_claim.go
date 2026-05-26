package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
)

// BeginForwardedRequestAccumulator binds an in-progress
// TestamentAccumulator to the agent's processing of a forwarded
// request, so every tool call and LLM dispatch run while the agent
// satisfies the request emits a ClaimArtifactAddedMsg into the chat
// panel as a child row under the parent claim.
//
// Why this exists. Per CLAIMS.md §5.1 every observable thing during
// an agent's processing should be a projection of board state. Today
// agents have two parallel processing paths:
//
//   - The claim path (ClaimsInbox → processClaimsEntry) creates an
//     accumulator and is fully wired for the artifact pipeline.
//   - The forwarded-request path (Guide → ForwardedRequest →
//     processForwardedRequest) runs the actual planning/handler logic
//     but creates NO accumulator, so tool calls fire silently.
//
// Until the two paths collapse (forwarded-request is retired in
// favor of the claims-inbox path), this helper bridges them: each
// agent's forwarded-request handler calls Begin* at entry to stamp
// an accumulator on ctx that inherits Guide's route-claim ID. Tool
// runtime + LLM coordinator then record artifacts naturally; the
// chat panel renders one child row per artifact under Guide's claim.
//
// Flow:
//
//  1. Guide posts a route claim against the target agent
//     (subject=agent, scope contains correlation_id).
//  2. Guide publishes the ForwardedRequest with the same correlation.
//  3. Recipient's handler calls BeginForwardedRequestAccumulator with
//     the request's correlation_id.
//  4. The helper looks up the route claim by correlation; if found,
//     stamps the claim_id on ctx via WithParentClaimID. Otherwise
//     uses the correlation_id itself as the synthetic anchor (so the
//     chat panel can still group child rows even when Guide's claim
//     wasn't posted yet — first-come-first-served).
//  5. Creates a TestamentAccumulator stamped with that anchor,
//     puts it on ctx via WithTestamentAccumulator (which inherits
//     the parent claim ID automatically per accumulator.go).
//  6. Returns the new ctx + a flush function the caller invokes
//     before publishing its final response and also defers as a
//     safety net for early exits.
//
// The flush function is a no-op when no artifacts were recorded —
// Flush already short-circuits an empty accumulator. So adding the
// helper to a handler that does nothing claim-relevant has zero board
// impact.
func BeginForwardedRequestAccumulator(
	ctx context.Context,
	agentID string,
	sessionID string,
	correlationID string,
	scope *concurrency.GoroutineScope,
) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	correlationID = strings.TrimSpace(correlationID)

	board := claims.DefaultSessionBoardRegistry().Lookup(sessionID)
	claimID := lookupRouteClaimID(board, agentID, correlationID)
	if claimID == "" {
		// Fallback: anchor on the correlation_id directly so the chat
		// panel can still group artifacts under the same row even
		// before Guide's route claim has been resolved on the board.
		claimID = correlationID
	}

	if claimID != "" {
		ctx = claims.WithParentClaimID(ctx, claimID)
	}

	acc := claims.NewTestamentAccumulator(agentID, sessionID)
	if claimID != "" {
		acc.WithClaimID(claimID)
	}
	ctx = claims.WithTestamentAccumulator(ctx, acc)

	flush := func() {
		// Flush is a no-op when nothing was recorded; safe to defer
		// unconditionally even when the handler bails early.
		_ = acc.FlushBlocking(context.Background(), board)
	}
	return ctx, flush
}

// lookupRouteClaimID searches the session board for the route claim
// Guide posted for this correlation. Returns the claim ID or empty
// when no matching claim exists yet (race or no-claim path).
//
// Match criteria (per agents/guide/claims_testimony.go::guideRouteClaim):
//   - Issuer = "guide"
//   - Subject = agentID
//   - Scope contains entry {Kind:"correlation", Key:correlationID}
func lookupRouteClaimID(board *claims.ClaimsBoard, agentID, correlationID string) string {
	if board == nil || agentID == "" || correlationID == "" {
		return ""
	}
	proj := board.Projection()
	if proj == nil {
		return ""
	}
	for i := range proj.Claims {
		c := &proj.Claims[i]
		if claims.IssuerAgentID(c.Relations) != "guide" {
			continue
		}
		if claims.SubjectAgentID(c.Relations) != agentID {
			continue
		}
		for _, s := range c.Scope {
			if s.Kind == "correlation" && s.Key == correlationID {
				return c.ID
			}
		}
	}
	return ""
}

// ─── Fabric envelope cycle-context propagation (UI_DESIGN.md §4.7.3) ──

// ForwardedRequestCycleOpts carries the cycle-context fields that
// dispatchers stamp onto Fabric envelopes. The receiver's intake
// reads these and re-stamps ctx via claims.WithParentClaimID so
// caused_by attribution works across agent boundaries.
//
// All fields are optional. ParentClaimID overrides the route-claim
// lookup when present (route lookup is the legacy fallback path).
// HandoffFromClaimID activates Layer 2 (receive-side verification).
type ForwardedRequestCycleOpts struct {
	ParentClaimID      string
	CycleID            string
	HandoffFromClaimID string
}

// MetadataKeyParentClaimID is the canonical key for the parent claim
// ID propagated through guide.RouteRequest / ForwardedRequest metadata.
// UI_DESIGN.md §4.7.3.
const (
	MetadataKeyParentClaimID      = "parent_claim_id"
	MetadataKeyCycleID            = "cycle_id"
	MetadataKeyHandoffFromClaimID = "handoff_from_claim_id"
)

// CycleOptsFromMetadata extracts the three cycle-context fields from
// envelope metadata. Whitespace-only values are treated as empty.
// Returns the zero value when metadata is nil.
func CycleOptsFromMetadata(meta map[string]string) ForwardedRequestCycleOpts {
	if meta == nil {
		return ForwardedRequestCycleOpts{}
	}
	return ForwardedRequestCycleOpts{
		ParentClaimID:      strings.TrimSpace(meta[MetadataKeyParentClaimID]),
		CycleID:            strings.TrimSpace(meta[MetadataKeyCycleID]),
		HandoffFromClaimID: strings.TrimSpace(meta[MetadataKeyHandoffFromClaimID]),
	}
}

// CycleOptsFromAnyMetadata is the map[string]any-flavored variant of
// CycleOptsFromMetadata. Bus envelopes (guide.RouteRequest /
// ForwardedRequest) carry Metadata as map[string]any, so consultee /
// challengee bus handlers extract cycle opts via this helper.
// Whitespace-only and non-string values yield empty fields.
func CycleOptsFromAnyMetadata(meta map[string]any) ForwardedRequestCycleOpts {
	if meta == nil {
		return ForwardedRequestCycleOpts{}
	}
	pick := func(key string) string {
		v, ok := meta[key]
		if !ok {
			return ""
		}
		s, ok := v.(string)
		if !ok {
			return ""
		}
		return strings.TrimSpace(s)
	}
	return ForwardedRequestCycleOpts{
		ParentClaimID:      pick(MetadataKeyParentClaimID),
		CycleID:            pick(MetadataKeyCycleID),
		HandoffFromClaimID: pick(MetadataKeyHandoffFromClaimID),
	}
}

// CycleOptsToAnyMetadata is the map[string]any-flavored counterpart of
// CycleOptsToMetadata. Used by dispatchers (consult / challenge) that
// build guide.RouteRequest envelopes whose Metadata is map[string]any.
// Empty fields are skipped so existing keys aren't disturbed.
func CycleOptsToAnyMetadata(meta map[string]any, opts ForwardedRequestCycleOpts) map[string]any {
	if opts.ParentClaimID == "" && opts.CycleID == "" && opts.HandoffFromClaimID == "" {
		return meta
	}
	if meta == nil {
		meta = make(map[string]any, 3)
	}
	if v := strings.TrimSpace(opts.ParentClaimID); v != "" {
		meta[MetadataKeyParentClaimID] = v
	}
	if v := strings.TrimSpace(opts.CycleID); v != "" {
		meta[MetadataKeyCycleID] = v
	}
	if v := strings.TrimSpace(opts.HandoffFromClaimID); v != "" {
		meta[MetadataKeyHandoffFromClaimID] = v
	}
	return meta
}

// CycleOptsToMetadata writes the cycle-context fields into the given
// metadata map (creating entries if needed). Empty fields are skipped
// so callers can safely call this without erasing other keys.
func CycleOptsToMetadata(meta map[string]string, opts ForwardedRequestCycleOpts) map[string]string {
	if opts.ParentClaimID == "" && opts.CycleID == "" && opts.HandoffFromClaimID == "" {
		return meta
	}
	if meta == nil {
		meta = make(map[string]string, 3)
	}
	if v := strings.TrimSpace(opts.ParentClaimID); v != "" {
		meta[MetadataKeyParentClaimID] = v
	}
	if v := strings.TrimSpace(opts.CycleID); v != "" {
		meta[MetadataKeyCycleID] = v
	}
	if v := strings.TrimSpace(opts.HandoffFromClaimID); v != "" {
		meta[MetadataKeyHandoffFromClaimID] = v
	}
	return meta
}

// NewClaimsEntryAccumulator constructs a TestamentAccumulator bound
// to the parent claim carried on a GraphEntryPoint, so the agent's
// response testament references that claim via RelationshipClaim.
// The bridge's claimToInvocationArtifact index then nests the
// agent's artifact tree (its tool calls + sub-consults +
// sub-challenges) under the issuer's peer-interaction row in the
// chat panel.
//
// Used by every agent's processClaimsEntry: a challenge claim
// (issuer=A, subject=B, ActionType=Challenge) wakes B's inbox; B's
// processClaimsEntry runs an LLM tool loop that emits artifacts;
// without this binding the testament is free-floating and B's
// artifacts never nest under A's challenge_started row. Same shape
// for consult-via-claims and any other directed-work claim that
// reaches an agent through its standing subscription.
func NewClaimsEntryAccumulator(agentID, sessionID string, entry *claims.GraphEntryPoint) *claims.TestamentAccumulator {
	acc := claims.NewTestamentAccumulator(agentID, sessionID)
	if entry == nil || entry.Node.Claim == nil {
		return acc
	}
	if id := strings.TrimSpace(entry.Node.Claim.ID); id != "" {
		acc.WithClaimID(id)
	}
	return acc
}

// BeginForwardedRequestCycle is the canonical entry point every
// agent's bus handler should call when processing a forwarded
// request. It extracts cycle-context fields from the envelope's
// Metadata (parent_claim_id, cycle_id, handoff_from_claim_id) and
// hands them to BeginForwardedRequestAccumulatorWithOpts so the
// agent's testament binds to the issuer's parent claim — the
// consult/challenge claim for peer-dispatched fulfillments, the
// guide's route claim for top-level prompts. The bridge's
// claimToInvocationArtifact index can then nest the agent's tool
// calls + sub-consults + sub-challenges under the issuer's
// peer-interaction row.
//
// Returns the same shape as BeginForwardedRequestAccumulatorWithOpts
// — a non-nil error indicates Layer 2 handoff verification rejected
// the envelope; the caller MUST surface this to the issuer.
func BeginForwardedRequestCycle(
	ctx context.Context,
	agentID string,
	fwd *guide.ForwardedRequest,
	scope *concurrency.GoroutineScope,
) (context.Context, func(), error) {
	if fwd == nil {
		return ctx, func() {}, nil
	}
	opts := CycleOptsFromAnyMetadata(fwd.Metadata)
	return BeginForwardedRequestAccumulatorWithOpts(ctx, agentID, fwd.SessionID, fwd.CorrelationID, scope, opts)
}

// BeginForwardedRequestAccumulatorWithOpts is the cycle-aware variant
// of BeginForwardedRequestAccumulator. It honors envelope-supplied
// ParentClaimID + CycleID + HandoffFromClaimID (Layer 3 propagation),
// and runs the receive-side handoff verification (Layer 2) when the
// envelope claims to be a handoff.
//
// Returns a non-nil error if Layer 2 verification fails — the caller
// MUST surface this as a structured rejection (typically by returning
// it from the request handler).
func BeginForwardedRequestAccumulatorWithOpts(
	ctx context.Context,
	agentID string,
	sessionID string,
	correlationID string,
	scope *concurrency.GoroutineScope,
	opts ForwardedRequestCycleOpts,
) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	correlationID = strings.TrimSpace(correlationID)

	board := claims.DefaultSessionBoardRegistry().Lookup(sessionID)

	// Layer 2 (receive-side verification) — UI_DESIGN.md §4.7.2.
	// Run BEFORE any state mutation so a rejected envelope leaves no
	// trace. The predecessor's owner agent is whoever issued the
	// handoff predecessor claim; HandoffEligible runs against them.
	if opts.HandoffFromClaimID != "" && board != nil {
		if err := verifyHandoffPredecessorDrained(board, opts.HandoffFromClaimID); err != nil {
			return ctx, func() {}, fmt.Errorf("handoff_rejected_by_receiver: %w", err)
		}
	}

	// Layer 3: prefer envelope-supplied ParentClaimID over route-claim
	// lookup. Route lookup remains the legacy fallback for older
	// dispatchers that don't yet stamp envelope metadata. The
	// correlation-ID-as-fake-claim fallback is intentionally REMOVED:
	// when no real claim ID is available, the bridge's cycle resolver
	// never recognizes the fake anchor and every artifact emitted in
	// this cycle is silently dropped from the UI plane. Leaving
	// claimID empty here means the accumulator runs unbound to a
	// claim — its testament submits as a free-standing record (no
	// claim relation) which the UI does not depend on for cycle
	// rendering. See docs/CLAIMS_UI.md "Forwarded-request claim
	// binding".
	claimID := strings.TrimSpace(opts.ParentClaimID)
	if claimID == "" {
		claimID = lookupRouteClaimID(board, agentID, correlationID)
	}

	if claimID != "" {
		ctx = claims.WithParentClaimID(ctx, claimID)
	}

	acc := claims.NewTestamentAccumulator(agentID, sessionID)
	if claimID != "" {
		acc.WithClaimID(claimID)
	}
	ctx = claims.WithTestamentAccumulator(ctx, acc)

	flush := func() {
		_ = acc.FlushBlocking(context.Background(), board)
	}
	return ctx, flush, nil
}

// verifyHandoffPredecessorDrained looks up the predecessor claim's
// issuer agent and runs HandoffEligible against them. Returns nil if
// the predecessor's cycle is fully drained (handoff is allowed to
// take effect on the receiver), or a HandoffNotEligibleError when
// open work remains.
func verifyHandoffPredecessorDrained(board *claims.ClaimsBoard, predecessorClaimID string) error {
	if board == nil || predecessorClaimID == "" {
		return nil
	}
	proj := board.Projection()
	if proj == nil {
		return nil
	}
	for i := range proj.Claims {
		c := &proj.Claims[i]
		if c.ID != predecessorClaimID {
			continue
		}
		issuer := claims.IssuerAgentID(c.Relations)
		if issuer == "" {
			return nil
		}
		return claims.HandoffEligible(board, issuer)
	}
	// Predecessor not on board (yet). Allow the envelope through —
	// the publish-side guard already enforced eligibility at dispatch
	// time; an out-of-order delivery shouldn't block the receiver.
	return nil
}

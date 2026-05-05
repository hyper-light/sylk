package claims

import "strings"

// Topic grammar (CLAIMS_BUS.md §6):
//
//   claims.<session_id>.inbox.<agent_id>.<relationship>.<action_kind>
//   claims.<session_id>.claim.<claim_id>.<status>
//   claims.<session_id>.validation.<validation_id>.<verdict>
//   claims.<session_id>.phase.<phase>
//
// Constants are exported so adapters and tests can share the same
// literal prefixes.

const (
	TopicNamespace             = "claims"
	TopicSegmentInbox            = "inbox"
	TopicSegmentClaim            = "claim"
	TopicSegmentValidation       = "validation"
	TopicSegmentPhase            = "phase"
	TopicSegmentConsultResolved  = "consult_resolved"
	TopicSegmentClaimContext     = "claim_context"
	TopicSegmentTestamentContext = "testament_context"
	TopicWildcard              = "*"
	topicSeparator             = "."
)

// InboxTopic builds the canonical topic for an InboxDelta.
// Use TopicWildcard for any segment the caller wants to leave open
// when *constructing a pattern* (wildcard matching is the subscriber
// responsibility, the publisher should always supply concrete values).
func InboxTopic(sessionID, agentID, relationship string, kind ActionType) string {
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicSegmentInbox,
		normalizeTopicSegment(agentID),
		normalizeTopicSegment(relationship),
		normalizeTopicSegment(string(kind)),
	)
}

// ClaimStatusTopic builds the canonical topic for a claim status
// transition (used by TestamentDelta and ClaimStatusDelta).
func ClaimStatusTopic(sessionID, claimID string, status ClaimStatus) string {
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicSegmentClaim,
		normalizeTopicSegment(claimID),
		normalizeTopicSegment(string(status)),
	)
}

// ValidationTopic builds the canonical topic for a ValidationDelta.
func ValidationTopic(sessionID, validationID string, verdict ValidationStatus) string {
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicSegmentValidation,
		normalizeTopicSegment(validationID),
		normalizeTopicSegment(string(verdict)),
	)
}

// PhaseTopic builds the canonical topic for a PhaseDelta.
func PhaseTopic(sessionID string, phase BoardPhase) string {
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicSegmentPhase,
		normalizeTopicSegment(string(phase)),
	)
}

// ClaimContextTopic builds the topic for a ClaimContextDelta. Routed
// per-claim so observers can subscribe to a single claim or wildcard
// across the session. UI subscribes via ClaimContextPattern("*","*")
// in its RoleObserver inbox configuration.
func ClaimContextTopic(sessionID, claimID string) string {
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicSegmentClaimContext,
		normalizeTopicSegment(claimID),
	)
}

// TestamentContextTopic builds the topic for a TestamentContextDelta.
// Anchored on AccumulatorID before flush (testament has no ID yet) and
// on TestamentID after; the helper picks whichever is non-empty.
func TestamentContextTopic(sessionID, testamentID, accumulatorID string) string {
	anchor := strings.TrimSpace(testamentID)
	if anchor == "" {
		anchor = "acc:" + strings.TrimSpace(accumulatorID)
	}
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicSegmentTestamentContext,
		normalizeTopicSegment(anchor),
	)
}

// ConsultResolvedTopic builds the canonical topic for a
// ConsultResolvedDelta. Routed to the originating agent's personal
// consult-resolved channel so only the awaiter receives the
// resolution; broadcast fan-out is avoided. The trailing consult_id
// segment makes individual resolutions independently addressable for
// targeted dedup and replay.
func ConsultResolvedTopic(sessionID, originatorAgentID, consultID string) string {
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicSegmentConsultResolved,
		normalizeTopicSegment(originatorAgentID),
		normalizeTopicSegment(consultID),
	)
}

// ────────────────────────────────────────────────────────────────────
// Subscription pattern helpers
// ────────────────────────────────────────────────────────────────────

// AgentInboxPattern matches every InboxDelta directed at agentID,
// across all relationships and action kinds. Sessions may be
// specified or wildcarded.
func AgentInboxPattern(sessionFilter, agentID string) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentInbox,
		normalizeTopicSegment(agentID),
		TopicWildcard,
		TopicWildcard,
	)
}

// AgentInboxRelationshipPattern matches InboxDeltas directed at
// agentID with a specific relationship (e.g. "subject" or
// "evaluator"), across all action kinds.
func AgentInboxRelationshipPattern(sessionFilter, agentID, relationship string) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentInbox,
		normalizeTopicSegment(agentID),
		normalizeTopicSegment(relationship),
		TopicWildcard,
	)
}

// AgentInboxActionPattern matches InboxDeltas directed at agentID
// with a specific (relationship, ActionType) tuple. Used by
// InboxPatternsFor to subscribe RoleSubject to the closed set of
// activation-bearing action types only — system-internal types
// (boot/activation/shutdown/archival/testament/checkpoint) never
// match these patterns and therefore never wake an agent.
func AgentInboxActionPattern(sessionFilter, agentID, relationship string, kind ActionType) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentInbox,
		normalizeTopicSegment(agentID),
		normalizeTopicSegment(relationship),
		normalizeTopicSegment(string(kind)),
	)
}

// ClaimStatusPattern matches every claim status transition on a
// given status across all claims in a session.
func ClaimStatusPattern(sessionFilter string, status ClaimStatus) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentClaim,
		TopicWildcard,
		normalizeTopicSegment(string(status)),
	)
}

// ValidationVerdictPattern matches validation deltas with a given
// verdict across all validations.
func ValidationVerdictPattern(sessionFilter string, verdict ValidationStatus) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentValidation,
		TopicWildcard,
		normalizeTopicSegment(string(verdict)),
	)
}

// PhasePattern matches phase deltas across all phase transitions in
// a session.
func PhasePattern(sessionFilter string) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentPhase,
		TopicWildcard,
	)
}

// ConsultResolvedPattern matches every ConsultResolvedDelta routed to
// agentID in the given session. Each issuing agent subscribes to its
// own pattern so only it sees the resolutions of consults it issued —
// no broadcast fan-out across the session.
func ConsultResolvedPattern(sessionFilter, agentID string) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentConsultResolved,
		normalizeTopicSegment(agentID),
		TopicWildcard,
	)
}

// ClaimContextPattern matches ClaimContextDeltas. Pass "*" for either
// filter to wildcard. The UI's RoleObserver inbox uses
// ClaimContextPattern("*", "*") to receive every claim's context
// updates across the session.
func ClaimContextPattern(sessionFilter, claimFilter string) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentClaimContext,
		wildcardOrSegment(claimFilter),
	)
}

// TestamentContextPattern matches TestamentContextDeltas. Pass "*"
// for either filter to wildcard.
func TestamentContextPattern(sessionFilter, testamentFilter string) string {
	return joinTopic(
		TopicNamespace,
		wildcardOrSegment(sessionFilter),
		TopicSegmentTestamentContext,
		wildcardOrSegment(testamentFilter),
	)
}

// SessionWildcardPattern matches every claims-namespaced topic in a
// session. Used by observers that want the full firehose.
func SessionWildcardPattern(sessionID string) string {
	return joinTopic(
		TopicNamespace,
		normalizeTopicSegment(sessionID),
		TopicWildcard,
		TopicWildcard,
	) + topicSeparator + TopicWildcard
}

// ────────────────────────────────────────────────────────────────────
// Internal helpers
// ────────────────────────────────────────────────────────────────────

func joinTopic(segments ...string) string {
	return strings.Join(segments, topicSeparator)
}

// normalizeTopicSegment replaces characters that conflict with the
// topic separator or would break wildcard matching. Empty segments
// collapse to "_" so topic construction never produces an empty
// segment, which would otherwise create adjacent separators and
// break matchers.
func normalizeTopicSegment(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "_"
	}
	if !strings.ContainsAny(trimmed, ". \t\n\r") {
		return trimmed
	}
	// Replace separator-conflicting characters. Keep the mapping
	// idempotent: running it twice yields the same result.
	r := strings.NewReplacer(
		".", "_",
		" ", "_",
		"\t", "_",
		"\n", "_",
		"\r", "_",
	)
	return r.Replace(trimmed)
}

func wildcardOrSegment(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return TopicWildcard
	}
	return normalizeTopicSegment(trimmed)
}

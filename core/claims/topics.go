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
	TopicNamespace        = "claims"
	TopicSegmentInbox      = "inbox"
	TopicSegmentClaim      = "claim"
	TopicSegmentValidation = "validation"
	TopicSegmentPhase      = "phase"
	TopicWildcard          = "*"
	topicSeparator         = "."
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

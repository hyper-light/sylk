package shared

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/versioning"
)

// MergeReplicaAgentID returns the deterministic runtime agent ID for
// a per-merge audit replica. The format is:
//
//	{agent_type}#replica-{session_id}:{major}.{minor}
//
// Examples:
//
//	inspector-global#replica-sess-1:3.7
//	tester-global#replica-sess-1:3.7
//
// Determinism is critical: on session restart, the SessionVFS's
// control WAL replay restores the commit-queue state but does NOT
// restart audit replicas. The orchestrator re-dispatches replicas
// for every entry still in the Auditing state. Re-dispatch MUST
// produce the same runtime agent ID the original launch did so that
//
//   - The replica's steering ledger (keyed by this ID) rehydrates
//     the conversation history.
//   - The chat panel's resumable-primary logic treats the relaunched
//     replica as a continuation of the pre-close row, not a sibling.
//   - The fabric / observability lineage does not fork across
//     restart.
//
// Correlation IDs are NOT part of this derivation. A correlation ID
// is per-request and is regenerated on every dispatch; using it as
// the disambiguator would break identity stability across restart.
// (session_id, merge_seq) is the durable key: both are WAL-backed
// and survive any process lifecycle.
func MergeReplicaAgentID(agentType, sessionID string, ver versioning.SemanticVersion) string {
	agentType = strings.TrimSpace(agentType)
	sessionID = strings.TrimSpace(sessionID)
	if agentType == "" {
		return ""
	}
	if sessionID == "" {
		return fmt.Sprintf("%s#replica-:%d.%d", agentType, ver.Major, ver.Minor)
	}
	return fmt.Sprintf("%s#replica-%s:%d.%d", agentType, sessionID, ver.Major, ver.Minor)
}

// ParseMergeReplicaAgentID returns the (agentType, sessionID,
// mergedVersion) tuple encoded in id, or (_, _, zero, false) if id
// does not match the MergeReplicaAgentID format. Used by the
// orchestrator to recover the merge seq from a replica ID emitted by
// a control-plane event or log entry.
func ParseMergeReplicaAgentID(id string) (string, string, versioning.SemanticVersion, bool) {
	id = strings.TrimSpace(id)
	hashIdx := strings.Index(id, "#replica-")
	if hashIdx < 0 {
		return "", "", versioning.SemanticVersion{}, false
	}
	agentType := id[:hashIdx]
	rest := id[hashIdx+len("#replica-"):]
	colonIdx := strings.LastIndex(rest, ":")
	if colonIdx < 0 {
		return "", "", versioning.SemanticVersion{}, false
	}
	sessionID := rest[:colonIdx]
	verStr := rest[colonIdx+1:]
	dotIdx := strings.Index(verStr, ".")
	if dotIdx < 0 {
		return "", "", versioning.SemanticVersion{}, false
	}
	var major, minor uint32
	if _, err := fmt.Sscanf(verStr, "%d.%d", &major, &minor); err != nil {
		return "", "", versioning.SemanticVersion{}, false
	}
	return agentType, sessionID, versioning.SemanticVersion{Major: major, Minor: minor}, true
}

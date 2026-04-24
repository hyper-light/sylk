package claims

import "time"

// WorkUnitPriority determines delivery order when multiple resolved
// entries are ready simultaneously. Lower numeric value = higher
// priority. Remediation claims surface before advisory observations.
type WorkUnitPriority int

const (
	PriorityRemediation WorkUnitPriority = 1 // rejected claims requiring fix
	PriorityChallenge   WorkUnitPriority = 2 // challenges where I am subject
	PriorityDirected    WorkUnitPriority = 3 // new claims where I am subject
	PriorityResponse    WorkUnitPriority = 4 // answers to my consultations
	PriorityEvaluation  WorkUnitPriority = 5 // validations I must judge
	PriorityPhase       WorkUnitPriority = 6 // board phase transitions
	PriorityAdvisory    WorkUnitPriority = 7 // coalesced advisory deltas
)

// Expectation is registered at post_action time. The inbox watches
// for the matching delta and delivers it as a graph entry point.
type Expectation struct {
	// ClaimID is the claim whose response we await.
	ClaimID string

	// ExpectedDelta is the delta kind that constitutes a response.
	// Uses DeltaKindTestament for consultations, DeltaKindValidation
	// for evaluations.
	ExpectedDelta string

	// ActionID is the action that spawned this expectation.
	ActionID string

	// IssuedAt records when the expectation was registered (for aging).
	IssuedAt time.Time

	// Priority determines delivery order relative to other resolved
	// entries. Derived from the action kind at registration time.
	Priority WorkUnitPriority
}

// GraphEntryPoint is the unit of work delivered to the agent's turn
// loop. It contains the triggering delta and the immediate graph
// node that delta references (depth-1). The agent traverses deeper
// via the traverse tool.
type GraphEntryPoint struct {
	// Delta is the bus-carried event that triggered this entry point.
	Delta Delta

	// Node is the immediate graph node referenced by the delta.
	// For InboxDelta: the Claim where the agent is subject.
	// For TestamentDelta: the Testament submitted against the agent's claim.
	// For ValidationDelta: the Validation verdict on the agent's testament.
	// For PhaseDelta: empty (phase deltas reference the board itself).
	Node GraphNode

	// Priority determines delivery order when multiple entries resolve
	// simultaneously. Derived from the delta kind and any expectation.
	Priority WorkUnitPriority

	// Expectation is the expectation this entry resolved. Nil when the
	// entry matched a standing subscription rather than an explicit
	// expectation.
	Expectation *Expectation
}

// GraphNode is the depth-1 view of a single node in the claims
// graph. Contains the node's own data and the IDs of its immediate
// neighbors — enough for the agent to decide whether to traverse
// deeper, without pre-loading the full subgraph.
//
// Exactly one pointer field is set, matching the node type. The
// tagged-union pattern matches the rest of the codebase.
type GraphNode struct {
	Action     *Action     `json:"action,omitempty"`
	Claim      *Claim      `json:"claim,omitempty"`
	Testament  *Testament  `json:"testament,omitempty"`
	Validation *Validation `json:"validation,omitempty"`
	Artifact   *Artifact   `json:"artifact,omitempty"`

	// Edges are the immediate neighbor IDs and their relationship
	// types. Not resolved — the agent calls traverse() to load any
	// of these it needs.
	Edges []GraphEdge `json:"edges,omitempty"`
}

// NodeID returns the ID of whichever entity is set on this node.
// Returns empty string if no entity is set.
func (n GraphNode) NodeID() string {
	switch {
	case n.Claim != nil:
		return n.Claim.ID
	case n.Action != nil:
		return n.Action.ID
	case n.Testament != nil:
		return n.Testament.ID
	case n.Validation != nil:
		return n.Validation.ID
	case n.Artifact != nil:
		return n.Artifact.ID
	}
	return ""
}

// GraphEdge is a typed pointer to an adjacent node in the claims
// graph. The agent follows edges by calling traverse(TargetID).
type GraphEdge struct {
	TargetID     string `json:"target_id"`
	TargetType   string `json:"target_type"`   // RelatedType constants
	Relationship string `json:"relationship"`  // Relationship constants
}

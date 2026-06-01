package forest

import "context"

// ProjectionInput configures a node-kind projection query.
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

type IntentProjection struct {
	Query         string          `json:"query"`
	PrimaryIntent string          `json:"primary_intent,omitempty"`
	Active        []*ForestPacket `json:"active,omitempty"`
	Dormant       []*ForestPacket `json:"dormant,omitempty"`
	Flagged       []*ForestPacket `json:"flagged,omitempty"`
}

type ConstraintProjection struct {
	Query    string          `json:"query"`
	Enforced []*ForestPacket `json:"enforced,omitempty"`
	Disputed []*ForestPacket `json:"disputed,omitempty"`
	Flagged  []*ForestPacket `json:"flagged,omitempty"`
}

type EvidenceProjection struct {
	Query   string          `json:"query"`
	Current []*ForestPacket `json:"current,omitempty"`
	Refuted []*ForestPacket `json:"refuted,omitempty"`
	Flagged []*ForestPacket `json:"flagged,omitempty"`
}

type DecisionProjection struct {
	Query        string          `json:"query"`
	Candidates   []*ForestPacket `json:"candidates,omitempty"`
	Chosen       []*ForestPacket `json:"chosen,omitempty"`
	Superseded   []*ForestPacket `json:"superseded,omitempty"`
	Contradicted []*ForestPacket `json:"contradicted,omitempty"`
	Flagged      []*ForestPacket `json:"flagged,omitempty"`
}

type OutcomeProjection struct {
	Query       string          `json:"query"`
	Successes   []*ForestPacket `json:"successes,omitempty"`
	Regressions []*ForestPacket `json:"regressions,omitempty"`
	Pending     []*ForestPacket `json:"pending,omitempty"`
	Flagged     []*ForestPacket `json:"flagged,omitempty"`
}

type PreferenceProjection struct {
	Query   string          `json:"query"`
	Active  []*ForestPacket `json:"active,omitempty"`
	Dormant []*ForestPacket `json:"dormant,omitempty"`
	Flagged []*ForestPacket `json:"flagged,omitempty"`
}

type CapabilityProjection struct {
	Query      string          `json:"query"`
	Proven     []*ForestPacket `json:"proven,omitempty"`
	Claimed    []*ForestPacket `json:"claimed,omitempty"`
	Unreliable []*ForestPacket `json:"unreliable,omitempty"`
	Flagged    []*ForestPacket `json:"flagged,omitempty"`
}

type OpportunityProjection struct {
	Query    string          `json:"query"`
	Proposed []*ForestPacket `json:"proposed,omitempty"`
	Accepted []*ForestPacket `json:"accepted,omitempty"`
	Rejected []*ForestPacket `json:"rejected,omitempty"`
	Flagged  []*ForestPacket `json:"flagged,omitempty"`
}

func (m *MemoryForest) ProjectIntent(ctx context.Context, input ProjectionInput) (*IntentProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyIntent), false)
	if err != nil {
		return nil, err
	}
	out := &IntentProjection{Query: input.Query}
	for _, packet := range packets {
		if packet == nil {
			continue
		}
		if forestPacketFlagged(packet) {
			out.Flagged = append(out.Flagged, packet)
			continue
		}
		if packet.Node.Status == string(BranchStateDormant) {
			out.Dormant = append(out.Dormant, packet)
			continue
		}
		out.Active = append(out.Active, packet)
		if out.PrimaryIntent == "" {
			out.PrimaryIntent = firstNonEmptyString(packet.Node.Summary, packet.Node.Title)
		}
	}
	return out, nil
}

func (m *MemoryForest) ProjectConstraints(ctx context.Context, input ProjectionInput) (*ConstraintProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyConstraint), true)
	if err != nil {
		return nil, err
	}
	out := &ConstraintProjection{Query: input.Query}
	for _, packet := range packets {
		placeConstraintPacket(packet, out)
	}
	return out, nil
}

func (m *MemoryForest) ProjectEvidence(ctx context.Context, input ProjectionInput) (*EvidenceProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyEvidence), true)
	if err != nil {
		return nil, err
	}
	out := &EvidenceProjection{Query: input.Query}
	for _, packet := range packets {
		placeEvidencePacket(packet, out)
	}
	return out, nil
}

func (m *MemoryForest) ProjectDecisions(ctx context.Context, input ProjectionInput) (*DecisionProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyIntent), true)
	if err != nil {
		return nil, err
	}
	out := &DecisionProjection{Query: input.Query}
	for _, packet := range packets {
		placeDecisionPacket(packet, out)
	}
	return out, nil
}

func (m *MemoryForest) ProjectOutcomes(ctx context.Context, input ProjectionInput) (*OutcomeProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyOutcome), true)
	if err != nil {
		return nil, err
	}
	out := &OutcomeProjection{Query: input.Query}
	for _, packet := range packets {
		placeOutcomePacket(packet, out)
	}
	return out, nil
}

func (m *MemoryForest) ProjectPreferences(ctx context.Context, input ProjectionInput) (*PreferenceProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyConstraint), false)
	if err != nil {
		return nil, err
	}
	out := &PreferenceProjection{Query: input.Query}
	for _, packet := range packets {
		placePreferencePacket(packet, out)
	}
	return out, nil
}

func (m *MemoryForest) ProjectCapabilities(ctx context.Context, input ProjectionInput) (*CapabilityProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyIntent), true)
	if err != nil {
		return nil, err
	}
	out := &CapabilityProjection{Query: input.Query}
	for _, packet := range packets {
		placeCapabilityPacket(packet, out)
	}
	return out, nil
}

func (m *MemoryForest) ProjectOpportunities(ctx context.Context, input ProjectionInput) (*OpportunityProjection, error) {
	packets, err := m.retrieveNodeKindProjection(ctx, input, nodeKindsForFamily(TreeFamilyIntent), true)
	if err != nil {
		return nil, err
	}
	out := &OpportunityProjection{Query: input.Query}
	for _, packet := range packets {
		placeOpportunityPacket(packet, out)
	}
	return out, nil
}

func (m *MemoryForest) retrieveNodeKindProjection(ctx context.Context, input ProjectionInput, kinds []ForestNodeKind, includeCounter bool) ([]*ForestPacket, error) {
	return m.RetrieveForest(ctx, Query{
		Query:                  input.Query,
		SessionID:              input.SessionID,
		TaskID:                 input.TaskID,
		AgentID:                input.AgentID,
		AgentType:              input.AgentType,
		IntentID:               input.IntentID,
		Horizon:                input.Horizon,
		Limit:                  input.Limit,
		Kinds:                  kinds,
		IncludeCounterEvidence: includeCounter,
	})
}

func placeConstraintPacket(packet *ForestPacket, out *ConstraintProjection) {
	if packet == nil {
		return
	}
	if forestPacketFlagged(packet) {
		out.Flagged = append(out.Flagged, packet)
		return
	}
	if forestPacketRefuted(packet) {
		out.Disputed = append(out.Disputed, packet)
		return
	}
	out.Enforced = append(out.Enforced, packet)
}

func placeEvidencePacket(packet *ForestPacket, out *EvidenceProjection) {
	if packet == nil {
		return
	}
	if forestPacketFlagged(packet) {
		out.Flagged = append(out.Flagged, packet)
		return
	}
	if forestPacketRefuted(packet) {
		out.Refuted = append(out.Refuted, packet)
		return
	}
	out.Current = append(out.Current, packet)
}

func placeDecisionPacket(packet *ForestPacket, out *DecisionProjection) {
	if packet == nil {
		return
	}
	if forestPacketFlagged(packet) {
		out.Flagged = append(out.Flagged, packet)
		return
	}
	switch packet.Node.Status {
	case string(BranchStateCandidate), ForestProposalStatusProposed:
		out.Candidates = append(out.Candidates, packet)
	case string(BranchStateSuperseded), string(BranchStateDormant):
		out.Superseded = append(out.Superseded, packet)
	case string(BranchStateContradicted), ForestProposalStatusRejected:
		out.Contradicted = append(out.Contradicted, packet)
	default:
		out.Chosen = append(out.Chosen, packet)
	}
}

func placeOutcomePacket(packet *ForestPacket, out *OutcomeProjection) {
	if packet == nil {
		return
	}
	if forestPacketFlagged(packet) {
		out.Flagged = append(out.Flagged, packet)
		return
	}
	switch packet.Node.EvidenceGrade {
	case EvidenceGradeValidated:
		out.Successes = append(out.Successes, packet)
	case EvidenceGradeContradicted, EvidenceGradeFailed:
		out.Regressions = append(out.Regressions, packet)
	default:
		out.Pending = append(out.Pending, packet)
	}
}

func placePreferencePacket(packet *ForestPacket, out *PreferenceProjection) {
	if packet == nil {
		return
	}
	if forestPacketFlagged(packet) {
		out.Flagged = append(out.Flagged, packet)
		return
	}
	if packet.Node.Status == string(BranchStateDormant) {
		out.Dormant = append(out.Dormant, packet)
		return
	}
	out.Active = append(out.Active, packet)
}

func placeCapabilityPacket(packet *ForestPacket, out *CapabilityProjection) {
	if packet == nil {
		return
	}
	if forestPacketFlagged(packet) {
		out.Flagged = append(out.Flagged, packet)
		return
	}
	switch packet.Node.EvidenceGrade {
	case EvidenceGradeValidated:
		out.Proven = append(out.Proven, packet)
	case EvidenceGradeContradicted, EvidenceGradeFailed:
		out.Unreliable = append(out.Unreliable, packet)
	default:
		out.Claimed = append(out.Claimed, packet)
	}
}

func placeOpportunityPacket(packet *ForestPacket, out *OpportunityProjection) {
	if packet == nil {
		return
	}
	if forestPacketFlagged(packet) {
		out.Flagged = append(out.Flagged, packet)
		return
	}
	switch packet.Node.Status {
	case ForestProposalStatusProposed, string(BranchStateCandidate):
		out.Proposed = append(out.Proposed, packet)
	case ForestProposalStatusRejected, string(BranchStateContradicted), string(BranchStateSuperseded):
		out.Rejected = append(out.Rejected, packet)
	default:
		out.Accepted = append(out.Accepted, packet)
	}
}

func forestPacketFlagged(packet *ForestPacket) bool {
	return packet != nil && (packet.Quarantine.Quarantined || len(packet.CounterEvidence) > 0 || packet.RiskScore > packet.Score)
}

func forestPacketRefuted(packet *ForestPacket) bool {
	if packet == nil {
		return false
	}
	return packet.Node.EvidenceGrade == EvidenceGradeContradicted ||
		packet.Node.EvidenceGrade == EvidenceGradeFailed ||
		packet.Node.Status == string(BranchStateContradicted) ||
		packet.Node.Status == string(BranchStateSuperseded)
}

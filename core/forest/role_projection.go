package forest

import (
	"fmt"
	"sort"
	"strings"
)

type ForestRoleProjection struct {
	Role               string          `json:"role"`
	Cursor             *ForestCursor   `json:"cursor,omitempty"`
	Packets            []*ForestPacket `json:"packets,omitempty"`
	EvidenceRefs       []string        `json:"evidence_refs,omitempty"`
	RiskFlags          []string        `json:"risk_flags,omitempty"`
	ValidationNeeds    []string        `json:"validation_needs,omitempty"`
	RoleFocus          []string        `json:"role_focus,omitempty"`
	NoProjectionReason string          `json:"no_projection_reason,omitempty"`
	Budget             int             `json:"budget"`
	Text               string          `json:"text"`
}

func BuildRoleForestProjection(role string, packets []*ForestPacket, cursor *ForestCursor, budget int) (*ForestRoleProjection, error) {
	role = normalizeForestRole(role)
	if role == "" {
		return nil, fmt.Errorf("forest role is required")
	}
	budget = roleProjectionBudget(role, budget)
	packets = compactCursorPackets(packets, budget)
	for _, packet := range packets {
		if len(packet.Evidence) == 0 {
			return nil, fmt.Errorf("role projection packet %s missing evidence refs", packet.Node.ID)
		}
	}
	projection := &ForestRoleProjection{
		Role:            role,
		Cursor:          cursor,
		Packets:         packets,
		EvidenceRefs:    projectionEvidenceRefs(packets, budget),
		RiskFlags:       projectionRiskFlags(packets, cursor, budget),
		ValidationNeeds: projectionValidationNeeds(packets, budget),
		RoleFocus:       projectionRoleFocus(role, packets, budget),
		Budget:          budget,
	}
	if len(packets) == 0 {
		projection.NoProjectionReason = "no_packets"
	}
	projection.Text = projection.render()
	return projection, nil
}

func normalizeForestRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "inspector-pipeline":
		return "inspector"
	case "tester-pipeline":
		return "tester"
	default:
		return role
	}
}

func roleProjectionBudget(role string, requested int) int {
	if requested > 0 {
		return requested
	}
	if role == "guide" || role == "scribe" {
		return len(defaultEcologyPolicy().Channels)
	}
	return len(defaultEcologyPolicy().Channels) + nodeProjectionRetryLimit()
}

func projectionEvidenceRefs(packets []*ForestPacket, budget int) []string {
	var refs []string
	for _, packet := range packets {
		for _, evidence := range packet.Evidence {
			refs = append(refs, evidence.RefType+":"+evidence.RefID)
		}
		for _, evidence := range packet.CounterEvidence {
			refs = append(refs, "counter:"+evidence.RefType+":"+evidence.RefID)
		}
	}
	return compactStrings(refs, budget*nodeProjectionRetryLimit())
}

func projectionRiskFlags(packets []*ForestPacket, cursor *ForestCursor, budget int) []string {
	var flags []string
	if cursor != nil {
		flags = append(flags, cursor.RiskFlags...)
	}
	for _, packet := range packets {
		if packet.Quarantine.Quarantined {
			flags = append(flags, "quarantine:"+packet.Node.ID)
		}
		for _, risk := range packet.BridgeRisks {
			if risk.GuardianReviewRequired {
				flags = append(flags, "guardian_review:"+risk.BridgeID)
			}
		}
	}
	return compactStrings(flags, budget)
}

func projectionValidationNeeds(packets []*ForestPacket, budget int) []string {
	var needs []string
	for _, packet := range packets {
		if packet.ValidationNeed <= 0 {
			continue
		}
		needs = append(needs, fmt.Sprintf("%s:%.3f", packet.Node.ID, packet.ValidationNeed))
	}
	return compactStrings(needs, budget)
}

func (p *ForestRoleProjection) render() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Memory Forest projection role=%s", p.Role)
	if p.Cursor != nil {
		fmt.Fprintf(&b, " cursor=%s", p.Cursor.ID)
	}
	if p.NoProjectionReason != "" {
		fmt.Fprintf(&b, " reason=%s", p.NoProjectionReason)
		return b.String()
	}
	writeProjectionList(&b, "evidence_refs", p.EvidenceRefs)
	writeProjectionList(&b, "risk_flags", p.RiskFlags)
	writeProjectionList(&b, "validation_needs", p.ValidationNeeds)
	writeProjectionList(&b, "role_focus", p.RoleFocus)
	for _, packet := range sortedProjectionPackets(p.Packets) {
		fmt.Fprintf(&b, "\n- node=%s kind=%s score=%.3f", packet.Node.ID, packet.Node.Kind, packet.Score)
		if packet.Node.Title != "" {
			fmt.Fprintf(&b, " title=%q", packet.Node.Title)
		}
		if packet.Quarantine.Quarantined {
			fmt.Fprintf(&b, " quarantine=%.3f", packet.Quarantine.Factor)
		}
	}
	return b.String()
}

func projectionRoleFocus(role string, packets []*ForestPacket, budget int) []string {
	var focus []string
	for _, packet := range sortedProjectionPackets(packets) {
		item := rolePacketFocus(role, packet)
		if item == "" {
			continue
		}
		focus = append(focus, item)
	}
	return compactStrings(focus, budget)
}

func rolePacketFocus(role string, packet *ForestPacket) string {
	if packet == nil {
		return ""
	}
	switch role {
	case "guardian":
		return riskFocus(packet)
	case "tester", "inspector":
		return validationFocus(packet)
	case "scribe", "archivalist":
		return preservationFocus(packet)
	case "orchestrator":
		return bridgeFocus(packet)
	default:
		return evidenceFocus(packet)
	}
}

func evidenceFocus(packet *ForestPacket) string {
	return packetFocus("evidence", packet)
}

func validationFocus(packet *ForestPacket) string {
	if packet.ValidationNeed > 0 {
		return packetFocus("validate", packet)
	}
	return evidenceFocus(packet)
}

func riskFocus(packet *ForestPacket) string {
	if packet.Quarantine.Quarantined {
		return packetFocus("quarantine", packet)
	}
	if len(packet.CounterEvidence) > 0 || len(packet.BridgeRisks) > 0 {
		return packetFocus("review", packet)
	}
	return evidenceFocus(packet)
}

func preservationFocus(packet *ForestPacket) string {
	return packetFocus("preserve", packet)
}

func bridgeFocus(packet *ForestPacket) string {
	if len(packet.BridgeRisks) > 0 {
		return packetFocus("bridge", packet)
	}
	return evidenceFocus(packet)
}

func packetFocus(prefix string, packet *ForestPacket) string {
	return prefix + ":" + packet.Node.ID + ":" + string(packet.Node.Kind)
}

func writeProjectionList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:", label)
	for _, value := range values {
		fmt.Fprintf(b, " %s", value)
	}
}

func sortedProjectionPackets(packets []*ForestPacket) []*ForestPacket {
	out := append([]*ForestPacket(nil), packets...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Node.ID < out[j].Node.ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

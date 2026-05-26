package claims

import (
	"fmt"
	"sort"
	"strings"
)

// PrependBoardPreamble prepends the claims board state to a user
// message. Returns the original message unchanged if the board is nil
// or empty.
func PrependBoardPreamble(message string, board *ClaimsBoard, agentType string) string {
	if board == nil {
		return message
	}
	proj := board.Projection()
	if proj == nil || proj.TotalClaims == 0 {
		return message
	}
	preamble := RenderBoardPreamble(proj, agentType)
	if preamble == "" {
		return message
	}
	return preamble + "\n\n" + message
}

// RenderBoardPreamble produces a concise text preamble showing the
// claims board state for an agent. Injected into the user message so
// the LLM sees its claims, the board phase, and what skills to use.
func RenderBoardPreamble(proj *ClaimsBoardProjection, agentType string) string {
	if proj == nil {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Claims Board\n")
	fmt.Fprintf(&b, "Phase: %s | Iteration: %d\n\n", proj.Phase, proj.Iteration)

	// My claims (where this agent is the subject).
	var myClaims []Claim
	var peerClaims []Claim
	for _, c := range proj.Claims {
		if c.Status == ClaimStatusSuperseded {
			continue
		}
		if SubjectAgentID(c.Relations) == agentType || hasAgentTypeRelation(c.Relations, agentType, RelationshipSubject) {
			myClaims = append(myClaims, c)
		} else {
			peerClaims = append(peerClaims, c)
		}
	}

	if len(myClaims) > 0 {
		fmt.Fprintf(&b, "### My Claims (%d)\n", len(myClaims))
		for _, c := range myClaims {
			pendingV := 0
			for _, v := range c.Validations {
				if v.Required && v.Status == ValidationStatusPending {
					pendingV++
				}
			}
			fmt.Fprintf(&b, "- [%s] \"%s\"\n", c.Status, c.Title)
			if c.Description != "" {
				fmt.Fprintf(&b, "  %s\n", truncate(c.Description, 120))
			}
			if len(c.Scope) > 0 {
				scopeStrs := make([]string, 0, len(c.Scope))
				for _, s := range c.Scope {
					scopeStrs = append(scopeStrs, s.Key)
				}
				fmt.Fprintf(&b, "  Scope: %s\n", strings.Join(scopeStrs, ", "))
			}
			if pendingV > 0 {
				fmt.Fprintf(&b, "  Validations: %d pending\n", pendingV)
			}
		}
		b.WriteString("\n")
	}

	if len(peerClaims) > 0 {
		fmt.Fprintf(&b, "### Peer Progress (%d/%d)\n", proj.AcceptedCount+proj.TestifiedCount, proj.TotalClaims)
		shown := 0
		for _, c := range peerClaims {
			if shown >= 5 {
				fmt.Fprintf(&b, "- ... and %d more\n", len(peerClaims)-5)
				break
			}
			subject := SubjectAgentID(c.Relations)
			fmt.Fprintf(&b, "- %s: \"%s\" %s\n", subject, c.Title, strings.ToUpper(string(c.Status)))
			shown++
		}
		b.WriteString("\n")
	}

	if continuity := renderCarryForwardContinuityPreamble(proj, agentType); continuity != "" {
		b.WriteString(continuity)
		b.WriteString("\n")
	}

	// Phase guidance.
	switch proj.Phase {
	case BoardPhaseImplementation:
		b.WriteString("Use `update_claim_progress` to report work on your claims.\n")
		b.WriteString("Use `submit_testaments` when your claims are complete.\n")
	case BoardPhaseValidation:
		b.WriteString("Use `evaluate_validation` to assess each claim's validations.\n")
		b.WriteString("Use `post_remediation_claims` if validations fail.\n")
	case BoardPhaseComplete:
		b.WriteString("All claims accepted. Board is complete.\n")
	}

	return b.String()
}

type continuityPreambleItem struct {
	topic        string
	testamentID  string
	through      uint64
	sourceCount  int
	sequence     uint64
	stale        bool
	contradicted bool
	superseded   bool
}

func renderCarryForwardContinuityPreamble(proj *ClaimsBoardProjection, agentType string) string {
	if proj == nil {
		return ""
	}
	superseded := make(map[string]struct{})
	for _, t := range proj.Testaments {
		for _, rel := range t.Relations {
			if rel.RelatedType == RelatedTypeTestament && rel.Relationship == RelationshipSupersedes {
				superseded[strings.TrimSpace(rel.Related)] = struct{}{}
			}
		}
	}
	latest := make(map[string]continuityPreambleItem)
	for _, t := range proj.Testaments {
		if !agentMatchesType(t.AgentID, agentType) {
			continue
		}
		item, ok := continuityPreambleItemFromTestament(t)
		if !ok {
			continue
		}
		if _, ok := superseded[t.ID]; ok {
			item.superseded = true
		}
		if item.superseded {
			continue
		}
		prev, exists := latest[item.topic]
		if !exists || item.sequence > prev.sequence {
			latest[item.topic] = item
		}
	}
	if len(latest) == 0 {
		return ""
	}
	items := make([]continuityPreambleItem, 0, len(latest))
	for _, item := range latest {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].sequence > items[j].sequence
	})
	if len(items) > 3 {
		items = items[:3]
	}
	var b strings.Builder
	b.WriteString("### My Carry-Forward Continuity\n")
	b.WriteString("Before repeating consult/search/planning work on these topics, call `recall_forward(topic=...)` and reuse fresh carried testaments/artifacts when they answer the question.\n")
	for _, item := range items {
		status := "fresh"
		switch {
		case item.contradicted:
			status = "contradicted"
		case item.stale:
			status = "stale"
		}
		fmt.Fprintf(&b, "- topic %q: testament %s through sequence %d (%s, %d source(s))\n", item.topic, item.testamentID, item.through, status, item.sourceCount)
	}
	return b.String()
}

func continuityPreambleItemFromTestament(t Testament) (continuityPreambleItem, bool) {
	item := continuityPreambleItem{testamentID: t.ID, sequence: t.Sequence}
	for _, artifact := range t.Artifacts {
		if artifact == nil {
			continue
		}
		stale, contradicted := continuityArtifactStatus(artifact)
		item.stale = item.stale || stale
		item.contradicted = item.contradicted || contradicted
		if artifact.Kind != ArtifactKindContinuityCursor {
			continue
		}
		item.topic = normalizeContinuityTopic(metadataString(artifact.Metadata, "topic"))
		item.through = metadataUint64(artifact.Metadata, "through_sequence")
		item.sourceCount = len(metadataStringSlice(artifact.Metadata, "source_testament_ids")) +
			len(metadataStringSlice(artifact.Metadata, "source_artifact_ids"))
	}
	return item, item.topic != "" && item.testamentID != ""
}

func metadataStringSlice(md map[string]any, key string) []string {
	if len(md) == 0 {
		return nil
	}
	switch v := md[key].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func agentMatchesType(agentID, agentType string) bool {
	agentID = strings.TrimSpace(agentID)
	agentType = strings.TrimSpace(agentType)
	return agentID == agentType || strings.HasPrefix(agentID, agentType+"-")
}

func hasAgentTypeRelation(relations []Relation, agentType, relationship string) bool {
	for _, r := range relations {
		if r.Relationship == relationship && r.RelatedType == RelatedTypeAgent {
			// Match exact agent type or agent IDs that start with the
			// type prefix (e.g., "engineer" matches "engineer",
			// "engineer-pipeline-abc123"). The delimiter "-" prevents
			// "engineer" from matching "chief-engineer".
			if agentMatchesType(r.Related, agentType) {
				return true
			}
		}
	}
	return false
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

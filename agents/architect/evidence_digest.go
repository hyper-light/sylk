package architect

import (
	"encoding/json"
	"sort"
	"strings"
)

const planningEvidenceDigestMaxBytes = 4800

func planningEvidenceDigest(plan *DesignPlan, focus string) []map[string]any {
	return planningEvidenceDigestWithBudget(plan, focus, planningEvidenceDigestMaxBytes)
}

func planningEvidenceDigestWithBudget(plan *DesignPlan, focus string, maxBytes int) []map[string]any {
	if plan == nil || maxBytes <= 0 {
		return nil
	}
	candidates := evidenceDigestCandidates(plan, focus)
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].received.After(candidates[j].received)
	})
	var selected []evidenceDigestCandidate
	used := 2 // "[]"
	for _, candidate := range candidates {
		if candidate.size == 0 {
			candidate.size = digestEntrySize(candidate.entry)
		}
		if len(selected) > 0 {
			candidate.size++ // comma
		}
		if used+candidate.size > maxBytes {
			continue
		}
		selected = append(selected, candidate)
		used += candidate.size
	}
	sort.SliceStable(selected, func(i, j int) bool {
		left, right := selected[i], selected[j]
		if left.target != right.target {
			return left.target < right.target
		}
		if !left.received.Equal(right.received) {
			return left.received.After(right.received)
		}
		return left.id < right.id
	})
	out := make([]map[string]any, 0, len(selected))
	for _, candidate := range selected {
		out = append(out, candidate.entry)
	}
	return out
}

type evidenceDigestCandidate struct {
	id       string
	target   string
	received sortableTime
	score    int
	size     int
	entry    map[string]any
}

type sortableTime struct{ value int64 }

func (t sortableTime) After(other sortableTime) bool { return t.value > other.value }
func (t sortableTime) Equal(other sortableTime) bool { return t.value == other.value }

func evidenceDigestCandidates(plan *DesignPlan, focus string) []evidenceDigestCandidate {
	trail := plan.EvidenceTrail
	if len(trail) == 0 {
		trail = evidenceTrailFromConsultationIndex(plan.Consultations)
	}
	out := make([]evidenceDigestCandidate, 0, len(trail))
	for _, evidence := range trail {
		if evidence == nil {
			continue
		}
		entry := evidenceDigestEntry(evidence)
		if len(entry) == 0 {
			continue
		}
		received := evidence.ReceivedAt.UnixNano()
		out = append(out, evidenceDigestCandidate{
			id:       strings.TrimSpace(evidence.ID),
			target:   strings.ToLower(strings.TrimSpace(evidence.Target)),
			received: sortableTime{value: received},
			score:    evidenceDigestScore(evidence, focus),
			entry:    entry,
		})
	}
	return out
}

func evidenceTrailFromConsultationIndex(index map[string]*ConsultationEvidence) []*PlanEvidence {
	if len(index) == 0 {
		return nil
	}
	targets := make([]string, 0, len(index))
	for target := range index {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	out := make([]*PlanEvidence, 0, len(targets))
	for _, target := range targets {
		evidence := index[target]
		if evidence == nil {
			continue
		}
		ev := &PlanEvidence{
			Kind:        EvidenceKindConsult,
			Target:      target,
			Query:       evidence.Query,
			Scope:       evidence.Scope,
			Correlation: evidence.Correlation,
			Success:     evidence.Success,
			Data:        evidence.Data,
			Summary:     consultSummaryFromResponse(evidence.Data),
			Error:       evidence.Error,
			RequestedAt: evidence.RequestedAt,
			ReceivedAt:  evidence.ReceivedAt,
			SourceTool:  "legacy_consultation_index",
		}
		ev.ID = planEvidenceID(ev)
		out = append(out, ev)
	}
	return out
}

func evidenceDigestEntry(evidence *PlanEvidence) map[string]any {
	if evidence == nil {
		return nil
	}
	entry := map[string]any{
		"kind":     string(evidence.Kind),
		"target":   strings.ToLower(strings.TrimSpace(evidence.Target)),
		"query":    truncatePlannerEvidenceString(evidence.Query, 240),
		"success":  evidence.Success,
		"summary":  truncatePlannerEvidenceString(firstNonEmptyString(evidence.Summary, consultSummaryFromResponse(evidence.Data)), 700),
		"evidence_id": strings.TrimSpace(evidence.ID),
	}
	if scope := strings.TrimSpace(evidence.Scope); scope != "" {
		entry["scope"] = scope
	}
	if !evidence.ReceivedAt.IsZero() {
		entry["received_at"] = evidence.ReceivedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if evidence.Error != "" {
		entry["error"] = truncatePlannerEvidenceString(evidence.Error, 240)
	}
	if data, ok := asMap(evidence.Data); ok {
		if files := evidenceStringListFromAny(data["relevant_files"], 8); len(files) > 0 {
			entry["relevant_files"] = files
		}
		if patterns := evidencePatternNames(data["patterns"], 6); len(patterns) > 0 {
			entry["patterns"] = patterns
		}
	}
	return entry
}

func evidenceDigestScore(evidence *PlanEvidence, focus string) int {
	score := 0
	if evidence == nil {
		return score
	}
	score += tokenOverlapScore(focus, evidence.Query+" "+evidence.Summary+" "+consultSummaryFromResponse(evidence.Data)) * 20
	if strings.EqualFold(evidence.Target, "librarian") {
		score += 15
	}
	if evidence.Success {
		score += 10
	}
	if !evidence.ReceivedAt.IsZero() {
		score += 5
	}
	return score
}

func tokenOverlapScore(a, b string) int {
	left := digestTokenSet(a)
	right := digestTokenSet(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	score := 0
	for token := range left {
		if _, ok := right[token]; ok {
			score++
		}
	}
	return score
}

func digestTokenSet(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range strings.Fields(strings.ToLower(value)) {
		token = strings.Trim(token, "`'\".,;:()[]{}<>")
		if len(token) < 3 {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

func evidenceStringListFromAny(value any, limit int) []string {
	items, ok := value.([]any)
	if !ok {
		if stringsSlice, ok := value.([]string); ok {
			items = make([]any, 0, len(stringsSlice))
			for _, item := range stringsSlice {
				items = append(items, item)
			}
		}
	}
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, min(limit, len(items)))
	for _, item := range items {
		text := strings.TrimSpace(stringFromAny(item))
		if text == "" {
			continue
		}
		out = append(out, text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func evidencePatternNames(value any, limit int) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, min(limit, len(items)))
	for _, item := range items {
		pattern, ok := asMap(item)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringFromAny(pattern["name"]))
		if name == "" {
			continue
		}
		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func digestEntrySize(entry map[string]any) int {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return 0
	}
	return len(encoded)
}

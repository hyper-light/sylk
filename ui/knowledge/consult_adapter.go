package knowledge

import (
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
)

// UI-05: adapter from inter-agent ConsultResponsePayload to the
// knowledge-panel ResultEntry shape.
//
// Librarian and Academic responses arrive at the UI as
// shared.ConsultResponsePayload (summary + citations + metadata).
// The knowledge panel renders ResultEntry slices (title + source +
// score + source-type badge). The shapes don't line up one-to-one:
// a single consult response produces N+1 result entries — one for
// the summary itself and one per citation — so downstream code gets
// a flat, rankable list it can drop straight into SetResults.
//
// Scoring is intentionally conservative. The summary entry scores
// highest (1.0) since it's the agent's authoritative answer;
// citations inherit a descending score so the rendering order is
// stable and the summary stays on top. A regression that equalized
// scores would jumble the order every render pass.

// summaryScore is the relevance score assigned to the summary
// result entry. Derived from: the summary is the canonical answer,
// so it should always rank first.
const summaryScore = 1.0

// citationScoreStart is the score assigned to the first citation.
// Later citations step down proportionally so the list stays
// stable-sorted.
const citationScoreStart = 0.9

// citationScoreStep is the per-citation decrement applied to keep
// successive citations monotonically below their predecessors.
// Derived from: supports up to ~10 visible citations before the
// score goes negative, which is more than any real consult emits.
const citationScoreStep = 0.05

// citationScoreFloor prevents scores from going negative if a
// consult produces an unusually large citation list.
const citationScoreFloor = 0.1

// ResultsFromConsultResponse converts a ConsultResponsePayload into
// a ResultEntry slice suitable for knowledge.Model.SetResults.
//
// Source is the agent that produced the response ("librarian" /
// "academic"). When provided, it populates the Source column on the
// entries; an empty source is acceptable and renders as blank.
//
// Returns nil when the payload has no meaningful content
// (empty summary AND no citations) — callers use the nil return as
// a signal to skip the panel update rather than clear it.
func ResultsFromConsultResponse(source string, payload *shared.ConsultResponsePayload) []ResultEntry {
	if payload == nil {
		return nil
	}
	summary := strings.TrimSpace(payload.Summary)
	if summary == "" && len(payload.Citations) == 0 {
		return nil
	}

	entries := make([]ResultEntry, 0, 1+len(payload.Citations))
	if summary != "" {
		entries = append(entries, ResultEntry{
			Title:      summary,
			Source:     strings.TrimSpace(source),
			Score:      summaryScore,
			SourceType: sourceTypeForAgent(source),
		})
	}
	entries = append(entries, resultEntriesFromCitations(source, payload.Citations)...)
	return entries
}

// resultEntriesFromCitations maps each ConsultCitation to a
// ResultEntry, preserving citation order with stepped scores.
func resultEntriesFromCitations(source string, citations []shared.ConsultCitation) []ResultEntry {
	out := make([]ResultEntry, 0, len(citations))
	for i, c := range citations {
		title := firstNonEmpty(
			strings.TrimSpace(c.Title),
			strings.TrimSpace(c.URL),
			strings.TrimSpace(c.Ref),
		)
		if title == "" {
			continue
		}
		out = append(out, ResultEntry{
			Title:      title,
			Source:     firstNonEmpty(strings.TrimSpace(c.URL), strings.TrimSpace(c.Ref), strings.TrimSpace(source)),
			Score:      citationScoreAt(i),
			SourceType: sourceTypeForAgent(source),
		})
	}
	return out
}

// citationScoreAt computes the score for the Nth citation,
// monotonically decreasing but floored so we never go negative.
func citationScoreAt(index int) float64 {
	score := citationScoreStart - citationScoreStep*float64(index)
	if score < citationScoreFloor {
		return citationScoreFloor
	}
	return score
}

// sourceTypeForAgent maps a source agent name to the badge color key
// used by sourceBadgeTable. Known agents get distinct badges so the
// UI differentiates at a glance; unknown sources fall through to the
// defaultBadge rendering path.
func sourceTypeForAgent(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "librarian":
		return "bleve"
	case "academic":
		return "vector"
	case "archivalist":
		return "graph"
	default:
		return "combined"
	}
}

// firstNonEmpty returns the first non-empty string from its args,
// or "" if all are empty. Keeps the call sites above free of
// cascading if/else chains on optional title/url/ref fields.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

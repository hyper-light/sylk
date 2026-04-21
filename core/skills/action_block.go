package skills

import (
	"fmt"
	"strings"
)

// RenderActionBlock produces a structured per-action block for inclusion
// in an enum-dispatched façade's description. It renders every
// load-bearing instructional field the delegate carries (purpose,
// required fields, usage, requirements, satisfies, avoid, best
// practices, examples) under a single action heading.
//
// The motivation: when a unified skill collapses N delegates under an
// `action` enum, the LLM's tool catalog shows only the façade's
// description. Per-delegate constraints that live on Requirement /
// Satisfies / Avoid / BestPractice strings must propagate into the
// façade's description text — otherwise load-bearing post-conditions
// ("after action=finalize returns ready_for_ot, call handoff_to_ot
// next") become invisible and the protocol silently wedges.
//
// The rendered block is stable in layout (same field order for every
// action) and uses explicit "[action=<name>]" tags on every guidance
// sentence so the LLM can bind instructions to the action enum value
// without ambiguity.
//
// Returns an empty string when delegate is nil or has no content worth
// rendering, so callers can concatenate output unconditionally.
func RenderActionBlock(delegate *Skill, action string) string {
	if delegate == nil {
		return ""
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "- %s:\n", action)

	if purpose := FirstSentence(delegate.Description); purpose != "" {
		fmt.Fprintf(&sb, "    purpose: %s\n", purpose)
	}

	if delegate.InputSchema != nil && len(delegate.InputSchema.Required) > 0 {
		fmt.Fprintf(&sb, "    required: %s\n", strings.Join(delegate.InputSchema.Required, ", "))
	}

	if usage := strings.TrimSpace(delegate.UsageDoc); usage != "" {
		fmt.Fprintf(&sb, "    usage: %s\n", collapseWhitespace(usage))
	}

	// Requirements are the critical propagation target: this is where
	// load-bearing post-conditions live (e.g. "if ready_for_ot is true,
	// call handoff_to_ot next"). Every entry gets its own line so the
	// LLM cannot accidentally gloss over a rule.
	for _, req := range delegate.Requirements {
		if trimmed := collapseWhitespace(req); trimmed != "" {
			fmt.Fprintf(&sb, "    requirement: %s\n", trimmed)
		}
	}

	for _, sat := range delegate.Satisfies {
		if trimmed := collapseWhitespace(sat); trimmed != "" {
			fmt.Fprintf(&sb, "    satisfies: %s\n", trimmed)
		}
	}

	for _, avoid := range delegate.Avoids {
		if trimmed := collapseWhitespace(avoid); trimmed != "" {
			fmt.Fprintf(&sb, "    avoid: %s\n", trimmed)
		}
	}

	for _, practice := range delegate.BestPractices {
		if trimmed := collapseWhitespace(practice); trimmed != "" {
			fmt.Fprintf(&sb, "    best_practice: %s\n", trimmed)
		}
	}

	return sb.String()
}

// FirstSentence returns the first sentence of a description string,
// trimmed. Sentences end on ". " or end-of-string. Exported so
// façade description builders can reuse the same sentence-segmentation
// logic the RenderActionBlock helper uses internally.
func FirstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.Index(s, ". "); idx > 0 {
		return strings.TrimSpace(s[:idx+1])
	}
	return s
}

// collapseWhitespace normalizes runs of whitespace (including embedded
// newlines from multi-line Requirement / Avoid / BestPractice strings)
// into single spaces, so a single guidance line in the rendered block
// stays on one line. The LLM reads the tool catalog as a flat string;
// embedded line breaks inside a "requirement: …" entry make the next
// field look like a continuation of the same rule.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

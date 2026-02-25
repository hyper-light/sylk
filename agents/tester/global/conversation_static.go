package global

import (
	"fmt"
	"strings"
	"unicode"
)

// tryStaticTesterReply returns a fast, deterministic response for trivial
// queries that don't need an LLM call. Returns ("", false) for queries that
// require LLM processing.
func tryStaticTesterReply(cr testerConversationRequest) (string, bool) {
	query := normalizeTesterQuery(cr.UserQuery)
	if query == "" {
		return staticTesterOnlineReply(cr), true
	}
	if isTesterGreetingQuery(query) {
		return staticTesterGreetingReply(cr), true
	}
	if isTesterStatusQuery(query) {
		return staticTesterStatusReply(cr), true
	}
	if isTesterCoverageQuery(query) {
		return staticTesterCoverageReply(cr), true
	}
	return "", false
}

func normalizeTesterQuery(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

// --- Query classifiers ---

func isTesterGreetingQuery(query string) bool {
	tokens := tokenizeTesterQuery(query)
	if len(tokens) == 0 || len(tokens) > 3 {
		return false
	}
	first := tokens[0]
	return first == "hello" || first == "hi" || first == "hey" || first == "howdy" || first == "yo"
}

// tokenizeTesterQuery splits on non-letter/non-number runes, stripping
// punctuation from tokens. Matches the guide's tokenizeGuideQuery pattern.
func tokenizeTesterQuery(query string) []string {
	return strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func isTesterStatusQuery(query string) bool {
	return testerContainsAny(query, "status", "overview", "what's going on", "what is going on", "sitrep")
}

func isTesterCoverageQuery(query string) bool {
	return testerContainsAny(query, "coverage", "covered", "uncovered")
}

// --- Static reply builders ---

func staticTesterOnlineReply(cr testerConversationRequest) string {
	return fmt.Sprintf("Tester online. %d agents registered, %d diagnosis reports tracked.",
		len(cr.AgentIDs), cr.Diagnoses)
}

func staticTesterGreetingReply(cr testerConversationRequest) string {
	base := fmt.Sprintf("Hello. Test quality engineer operational with %d agents registered.", len(cr.AgentIDs))
	if cr.Diagnoses > 0 {
		base += fmt.Sprintf(" %d diagnosis reports on file.", cr.Diagnoses)
	}
	if cr.TestPlan != "" {
		base += " Active test plan in progress."
	}
	return base
}

func staticTesterStatusReply(cr testerConversationRequest) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Test status: %d agents registered, %d diagnosis reports.\n",
		len(cr.AgentIDs), cr.Diagnoses))
	if cr.TestPlan != "" {
		b.WriteString(fmt.Sprintf("Active plan: %s\n", cr.TestPlan))
	}
	if cr.Harness != "" {
		b.WriteString(fmt.Sprintf("Harness: %s\n", cr.Harness))
	}
	return strings.TrimRight(b.String(), "\n")
}

func staticTesterCoverageReply(cr testerConversationRequest) string {
	base := "Coverage data requires tool access. Use the query tools to fetch current coverage metrics."
	if cr.TestPlan != "" {
		base = fmt.Sprintf("Active test plan exists. %s", base)
	}
	return base
}

// testerContainsAny returns true if query contains any of the fragments.
func testerContainsAny(query string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(query, fragment) {
			return true
		}
	}
	return false
}

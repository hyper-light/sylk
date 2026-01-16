package archivalist

import (
	"github.com/adalundhe/sylk/agents/guide"
)

// =============================================================================
// Archivalist Routing Information
// =============================================================================
//
// This file defines the Archivalist's routing configuration for the Guide.
// It includes:
// - DSL aliases (arch -> archivalist)
// - Action shortcuts (@archive)
// - Trigger phrases for natural language detection
//
// The Archivalist registers this information with the Guide so the Guide
// can properly route requests to the Archivalist.

// GetRoutingInfo implements guide.AgentRouter interface.
// Returns the Archivalist's routing configuration.
func (a *Archivalist) GetRoutingInfo() *guide.AgentRoutingInfo {
	return ArchivalistRoutingInfo()
}

// ArchivalistRoutingInfo returns the Archivalist's routing configuration.
// This can be used to register with the Guide even without an Archivalist instance.
func ArchivalistRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:   "archivalist",
		Name: "archivalist",

		// DSL aliases
		Aliases: []string{"arch"},

		// Action shortcuts
		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "archive",
				Description:   "Direct invoke Archivalist for storing or querying historical data",
				DefaultIntent: guide.IntentRecall,
				DefaultDomain: guide.DomainPatterns,
			},
			{
				Name:          "recall",
				Description:   "Query historical data from the Archivalist",
				DefaultIntent: guide.IntentRecall,
			},
			{
				Name:          "remember",
				Description:   "Store data in the Archivalist",
				DefaultIntent: guide.IntentStore,
			},
		},

		// Natural language triggers
		Triggers: guide.AgentTriggers{
			StrongTriggers: ArchivalistStrongTriggers(),
			WeakTriggers:   ArchivalistWeakTriggers(),
			IntentTriggers: ArchivalistIntentTriggers(),
		},

		// Registration with capabilities and constraints
		Registration: ArchivalistRegistration(),
	}
}

// ArchivalistRegistration returns the Guide registration for the Archivalist
func ArchivalistRegistration() *guide.AgentRegistration {
	return &guide.AgentRegistration{
		ID:      "archivalist",
		Name:    "archivalist",
		Aliases: []string{"arch"},
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{
				guide.IntentRecall,
				guide.IntentStore,
				guide.IntentCheck,
				guide.IntentDeclare,
				guide.IntentComplete,
			},
			Domains: []guide.Domain{
				guide.DomainPatterns,
				guide.DomainFailures,
				guide.DomainDecisions,
				guide.DomainFiles,
				guide.DomainLearnings,
				guide.DomainIntents,
			},
		},
		Constraints: guide.AgentConstraints{
			RetrospectiveOnly: true,
			TemporalFocus:     guide.TemporalPast,
		},
		Description: "Historical data, patterns, failures, decisions, learnings",
		Priority:    100,
	}
}

// =============================================================================
// Trigger Phrases
// =============================================================================
//
// These trigger phrases help the Guide detect when to route to the Archivalist.
// They are organized by strength (how confident we are) and by intent.

// ArchivalistStrongTriggers returns phrases that strongly indicate Archivalist routing
func ArchivalistStrongTriggers() []string {
	return []string{
		// Past tense question patterns
		"what did we",
		"what have we",
		"what had we",
		"how did we",
		"how have we",
		"why did we",
		"when did we",
		"where did we",
		"who did we",

		// Experience queries
		"have we seen",
		"have we tried",
		"have we used",
		"have we encountered",
		"did we try",
		"did we use",
		"did we see",

		// Historical patterns
		"what patterns",
		"what failures",
		"what errors",
		"what decisions",
		"what approach",
		"what worked",
		"what failed",

		// Temporal markers
		"previously",
		"before this",
		"earlier",
		"last time",
		"in the past",
		"historically",

		// Learning/experience
		"what we learned",
		"lessons learned",
		"what we discovered",
		"what we found",

		// Verification of past
		"have we already",
		"did we already",
		"was there",
		"were there",
		"was it",
		"were we",
	}
}

// ArchivalistWeakTriggers returns phrases that weakly suggest Archivalist routing
func ArchivalistWeakTriggers() []string {
	return []string{
		// Generic past references
		"before",
		"ago",
		"past",
		"history",
		"previous",
		"old",
		"former",
	}
}

// ArchivalistIntentTriggers returns intent-specific trigger phrases
func ArchivalistIntentTriggers() map[guide.Intent][]string {
	return map[guide.Intent][]string{
		// Store intent triggers
		guide.IntentStore: {
			"remember that",
			"record that",
			"log that",
			"store that",
			"save that",
			"note that",
			"this failed",
			"this didn't work",
			"this broke",
			"we tried and",
			"i tried and",
			"we learned that",
			"i learned that",
			"lesson learned",
			"we discovered that",
			"we found that",
			"we decided to",
			"i decided to",
			"the decision was",
			"we chose to",
		},

		// Recall intent triggers
		guide.IntentRecall: {
			"what patterns",
			"show me",
			"find",
			"search for",
			"look up",
			"retrieve",
			"get me",
			"list the",
			"tell me about",
		},

		// Check intent triggers
		guide.IntentCheck: {
			"have we",
			"did we",
			"was there",
			"check if",
			"verify",
			"confirm",
		},

		// Declare intent triggers
		guide.IntentDeclare: {
			"i'm working on",
			"starting to",
			"beginning",
			"about to",
			"going to work on",
		},

		// Complete intent triggers
		guide.IntentComplete: {
			"finished",
			"completed",
			"done with",
			"wrapped up",
			"concluded",
		},
	}
}

// =============================================================================
// DSL Convenience Functions
// =============================================================================

// RouteToArchivalist creates a DSL command for archivalist queries
func RouteToArchivalist(intent guide.Intent, domain guide.Domain, params map[string]string) string {
	return guide.RouteToAgent("arch", intent, domain, params)
}

// QueryPatterns creates a DSL command to query patterns
func QueryPatterns(scope string, limit int) string {
	params := make(map[string]string)
	if scope != "" {
		params["scope"] = scope
	}
	if limit > 0 {
		params["limit"] = intToStr(limit)
	}
	return RouteToArchivalist(guide.IntentRecall, guide.DomainPatterns, params)
}

// QueryFailures creates a DSL command to query failures
func QueryFailures(errorType string, limit int) string {
	params := make(map[string]string)
	if errorType != "" {
		params["error"] = errorType
	}
	if limit > 0 {
		params["limit"] = intToStr(limit)
	}
	return RouteToArchivalist(guide.IntentRecall, guide.DomainFailures, params)
}

// QueryDecisions creates a DSL command to query decisions
func QueryDecisions(scope string) string {
	params := make(map[string]string)
	if scope != "" {
		params["scope"] = scope
	}
	return RouteToArchivalist(guide.IntentRecall, guide.DomainDecisions, params)
}

// QueryFiles creates a DSL command to query file states
func QueryFiles(path string) string {
	params := make(map[string]string)
	if path != "" {
		params["path"] = path
	}
	return RouteToArchivalist(guide.IntentRecall, guide.DomainFiles, params)
}

// StoreFailure creates a DSL command to store a failure
func StoreFailure(approach, outcome, resolution string) string {
	return "@arch:store:failures{approach:\"" + approach + "\",outcome:\"" + outcome + "\",resolution:\"" + resolution + "\"}"
}

// StorePattern creates a DSL command to store a pattern
func StorePattern(pattern, category, scope string) string {
	return "@arch:store:patterns{pattern:\"" + pattern + "\",category:\"" + category + "\",scope:\"" + scope + "\"}"
}

// intToStr converts int to string without fmt import
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// =============================================================================
// Trigger Detection Functions
// =============================================================================

// MatchesTrigger checks if input matches any Archivalist trigger
func MatchesTrigger(input string) bool {
	info := ArchivalistRoutingInfo()
	return matchesAnyTrigger(input, info.Triggers.StrongTriggers) ||
		matchesAnyTrigger(input, info.Triggers.WeakTriggers)
}

// MatchesStrongTrigger checks if input matches a strong trigger
func MatchesStrongTrigger(input string) bool {
	return matchesAnyTrigger(input, ArchivalistStrongTriggers())
}

// matchesAnyTrigger checks if input contains any of the triggers
func matchesAnyTrigger(input string, triggers []string) bool {
	inputLower := toLower(input)
	for _, trigger := range triggers {
		if contains(inputLower, trigger) {
			return true
		}
	}
	return false
}

// toLower converts string to lowercase (avoiding import cycle with strings)
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

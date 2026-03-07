package librarian

import "github.com/adalundhe/sylk/agents/guide"

// LibrarianRoutingInfo returns static routing metadata for the librarian
// agent using the provided canonical ID. This enables pre-registration
// with the Guide before the librarian container is activated.
func LibrarianRoutingInfo(canonicalID string) *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      canonicalID,
		Type:    "librarian",
		Name:    "librarian",
		Aliases: []string{"lib", "search", "find"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "search",
				Description:   "Search the codebase for code, patterns, or symbols",
				DefaultIntent: guide.IntentSearch,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "find",
				Description:   "Find specific files, symbols, or patterns",
				DefaultIntent: guide.IntentFind,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "locate",
				Description:   "Locate where a symbol is defined or used",
				DefaultIntent: guide.IntentLocate,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "clone",
				Description:   "Clone a remote repository for local code analysis",
				DefaultIntent: guide.IntentFetch,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"find",
				"search",
				"locate",
				"where is",
				"show me",
				"look for",
				"grep",
				"definition of",
				"usages of",
				"references to",
				"pattern",
				"linter",
				"formatter",
				"test framework",
				"clone",
				"clone repo",
				"fetch repo",
				"fetch package",
				"download repo",
				"git clone",
			},
			WeakTriggers: []string{
				"code",
				"file",
				"function",
				"class",
				"method",
				"symbol",
				"repository",
				"package",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentFind: {
					"find",
					"where is",
					"locate",
					"show me where",
				},
				guide.IntentSearch: {
					"search",
					"look for",
					"grep",
					"scan",
				},
				guide.IntentLocate: {
					"definition",
					"declaration",
					"usages",
					"references",
					"implementations",
				},
				guide.IntentFetch: {
					"clone",
					"fetch repo",
					"fetch package",
					"download repo",
					"git clone",
					"pull repo",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      canonicalID,
			Name:    "librarian",
			Aliases: []string{"lib", "search", "find"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentFind,
					guide.IntentSearch,
					guide.IntentLocate,
					guide.IntentFetch,
					guide.IntentRecall,
					guide.IntentCheck,
					guide.IntentHelp,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
				},
				Tags: []string{"search", "code", "patterns", "symbols", "tooling", "clone", "packages"},
				Keywords: []string{
					"find", "search", "locate", "grep", "pattern",
					"symbol", "definition", "reference", "usage",
					"linter", "formatter", "test", "tooling",
					"clone", "fetch", "download", "repository", "package",
				},
				Priority: 80,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.6,
			},
			Description: "Code search and pattern detection. SINGLE SOURCE OF TRUTH for formatters, linters, test frameworks, and coding patterns.",
			Priority:    80,
		},
	}
}

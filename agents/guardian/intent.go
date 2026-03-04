package guardian

import "strings"

// classifyGuardianIntent determines the intent of a user message directed at Guardian.
// Uses keyword matching (deterministic, no LLM needed).
func classifyGuardianIntent(input string) GuardianIntent {
	lower := strings.ToLower(input)

	if matchesAnyKeyword(lower, checkpointKeywords) {
		return IntentCheckpoint
	}
	if matchesAnyKeyword(lower, reportKeywords) {
		return IntentReport
	}
	if matchesAnyKeyword(lower, assessKeywords) {
		return IntentAssess
	}
	return IntentConverse
}

var checkpointKeywords = []string{
	"checkpoint", "safety commit", "save point", "savepoint",
	"snapshot", "backup commit",
}

var reportKeywords = []string{
	"health report", "status report", "agent health", "agent status",
	"budget status", "token usage", "cost report", "how are agents",
	"registered", "activation", "tier", "daemon", "goroutines",
	"memory usage", "vfs", "logs", "system status",
}

var assessKeywords = []string{
	"security assessment", "security audit", "scan for", "vulnerability",
	"credential check", "injection check", "safety assessment",
	"protected branches", "security status",
}

func matchesAnyKeyword(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

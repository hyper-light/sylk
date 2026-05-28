package claims

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type participantDocLintFinding struct {
	line   int
	reason string
	text   string
}

func TestParticipantDocsLintAllowsAgentSpecificRuntimeSections(t *testing.T) {
	fixture := strings.Join([]string{
		"Agent participants are LLM-driven and use the LLM tool-loop.",
		"Service participants use deterministic Go handlers.",
		"`AgentRef` remains the compatibility alias during migration.",
	}, "\n")
	if findings := lintParticipantLanguage(fixture); len(findings) != 0 {
		t.Fatalf("expected fixture to pass, got %#v", findings)
	}
}

func TestParticipantDocsLintRejectsAgentOnlyLanguageForServiceParticipants(t *testing.T) {
	fixture := "A service participant is the target agent and receives agent-only instructions."
	findings := lintParticipantLanguage(fixture)
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want one service participant wording finding", findings)
	}
	if findings[0].line != 1 || !strings.Contains(findings[0].reason, "service participant") {
		t.Fatalf("finding = %#v, want service participant line and reason", findings[0])
	}
}

func TestParticipantDocsLintAllowsHistoricalMigrationNotes(t *testing.T) {
	fixture := "Historical migration note: service participant sections previously said target agent while compatibility text was being reconciled."
	if findings := lintParticipantLanguage(fixture); len(findings) != 0 {
		t.Fatalf("expected migration note to pass, got %#v", findings)
	}
}

func TestParticipantDocsCarryCompatibilityAndAgentSpecificMarkers(t *testing.T) {
	root := claimsRepoRoot(t)
	for _, rel := range []string{
		"docs/CLAIMS.md",
		"docs/CLAIMS_AND_DELTAS.md",
		"docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md",
		"docs/CLAIMS_VISIBILITY.md",
		"docs/CLAIMS_AND_INFRASTRUCTURE.md",
	} {
		body := readClaimsFile(t, filepath.Join(root, rel))
		if !strings.Contains(body, "participant") {
			t.Fatalf("%s does not contain participant terminology", rel)
		}
		if findings := lintParticipantLanguage(body); len(findings) != 0 {
			t.Fatalf("%s participant wording findings: %s", rel, formatParticipantDocFindings(findings))
		}
	}
	infrastructure := readClaimsFile(t, filepath.Join(root, "docs/CLAIMS_AND_INFRASTRUCTURE.md"))
	for _, required := range []string{
		"agent-specific",
		"LLM-driven",
		"`AgentRef`",
		"compatibility",
	} {
		if !strings.Contains(infrastructure, required) {
			t.Fatalf("CLAIMS_AND_INFRASTRUCTURE.md missing participant doc-lint marker %q", required)
		}
	}
	if findings := lintParticipantLanguage(infrastructure); len(findings) != 0 {
		t.Fatalf("CLAIMS_AND_INFRASTRUCTURE.md participant wording findings: %s", formatParticipantDocFindings(findings))
	}
}

func lintParticipantLanguage(body string) []participantDocLintFinding {
	lines := strings.Split(body, "\n")
	findings := make([]participantDocLintFinding, 0)
	inFence := false
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		normalized := strings.ToLower(stripBacktickSpans(line))
		if participantDocLintAllowed(normalized) {
			continue
		}
		if mentionsNonAgentParticipant(normalized) && mentionsAgentOnlyRuntime(normalized) {
			findings = append(findings, participantDocLintFinding{
				line:   idx + 1,
				reason: "service participant/system participant/external participant text must not use agent-only runtime wording",
				text:   strings.TrimSpace(line),
			})
		}
	}
	return findings
}

func participantDocLintAllowed(line string) bool {
	return strings.Contains(line, "historical migration note") ||
		strings.Contains(line, "compatibility text") ||
		strings.Contains(line, "previously said") ||
		strings.Contains(line, "allowlisted excerpt")
}

func mentionsNonAgentParticipant(line string) bool {
	return strings.Contains(line, "service participant") ||
		strings.Contains(line, "system participant") ||
		strings.Contains(line, "external participant")
}

func mentionsAgentOnlyRuntime(line string) bool {
	return strings.Contains(line, "target agent") ||
		strings.Contains(line, "agent-only") ||
		strings.Contains(line, "agent's tool") ||
		strings.Contains(line, "agent tool") ||
		strings.Contains(line, "llm tool-loop")
}

func stripBacktickSpans(line string) string {
	var b strings.Builder
	inCode := false
	for _, r := range line {
		if r == '`' {
			inCode = !inCode
			continue
		}
		if !inCode {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatParticipantDocFindings(findings []participantDocLintFinding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, fmt.Sprintf("line %d: %s: %s", finding.line, finding.reason, finding.text))
	}
	return strings.Join(parts, "; ")
}

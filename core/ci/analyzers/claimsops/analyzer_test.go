package claimsops

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzerFlagsClaimsOperationsViolations(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "violations")
}

func TestAnalyzerAcceptsBoundedParticipantAwareCode(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "clean")
}

func TestDirectVFSBypassAllowlistIsNarrow(t *testing.T) {
	allowed := []string{
		"github.com/adalundhe/sylk/core/versioning",
		"github.com/adalundhe/sylk/core/claims",
		"github.com/adalundhe/sylk/agents/shared",
	}
	for _, pkg := range allowed {
		if !directVFSBypassAllowed(pkg) {
			t.Fatalf("expected %s to be explicitly allowlisted", pkg)
		}
	}
	blocked := []string{
		"github.com/adalundhe/sylk/agents/orchestrator",
		"github.com/adalundhe/sylk/agents/engineer",
		"github.com/adalundhe/sylk/ui/bridge",
	}
	for _, pkg := range blocked {
		if directVFSBypassAllowed(pkg) {
			t.Fatalf("expected %s to be blocked from direct VFS mutation", pkg)
		}
	}
}

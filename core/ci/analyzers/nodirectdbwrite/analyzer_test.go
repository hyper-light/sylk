package nodirectdbwrite

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer_FlagsDirectDBWrites runs the analyzer against a fixture
// package that calls write builders directly on a *bun.DB receiver.
// Every flagged call indicates a bypass of database.RunInWriteTx and
// therefore a bypass of the Activity Fabric chokepoint — exactly the
// regression class this lint exists to catch.
func TestAnalyzer_FlagsDirectDBWrites(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "leaky")
}

// TestAnalyzer_AcceptsTransactionalWrites verifies the legitimate
// write path produces zero diagnostics. Without this test the analyzer
// could refuse the safe path and not be caught by CI.
func TestAnalyzer_AcceptsTransactionalWrites(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "clean")
}

// TestIsAllowedPackage_CoversDocumentedBoundaries asserts the concrete
// package paths called out in the audit are covered by the allowlist.
// If someone edits allowedPathPrefixes and accidentally drops a
// boundary package, this test catches the regression — the allowlist
// stops being "what was audited" and starts being "whatever happens to
// be in the slice right now."
func TestIsAllowedPackage_CoversDocumentedBoundaries(t *testing.T) {
	covered := []string{
		"github.com/adalundhe/sylk/core/database",
		"github.com/adalundhe/sylk/core/activity/activitystore",
	}
	for _, path := range covered {
		if !isAllowedPackage(path) {
			t.Errorf("expected %s to be allowlisted (boundary package), got false", path)
		}
	}
}

// TestIsAllowedPackage_BlocksAgentPaths asserts agent packages are NOT
// on the allowlist. A future edit that accidentally adds "agents/" or
// similar would silently disable the chokepoint mandate for every
// agent; this test fails first.
func TestIsAllowedPackage_BlocksAgentPaths(t *testing.T) {
	blocked := []string{
		"github.com/adalundhe/sylk/agents/orchestrator",
		"github.com/adalundhe/sylk/agents/architect",
		"github.com/adalundhe/sylk/agents/tester/pipeline",
		"github.com/adalundhe/sylk/agents/engineer",
		"github.com/adalundhe/sylk/agents/designer",
		"github.com/adalundhe/sylk/agents/inspector/pipeline",
	}
	for _, path := range blocked {
		if isAllowedPackage(path) {
			t.Errorf("agent package %s must NOT be allowlisted; chokepoint mandate is non-optional for agents", path)
		}
	}
}

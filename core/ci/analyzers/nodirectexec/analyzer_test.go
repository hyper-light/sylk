package nodirectexec

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer_FlagsDirectExecInAgentCode runs the analyzer against a fixture
// package that simulates agent-layer code invoking exec.Command directly.
// Every direct call must be flagged — this is the core invariant of the
// strict-disk broker mandate.
func TestAnalyzer_FlagsDirectExecInAgentCode(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "leaky")
}

// TestAnalyzer_AllowsAllowlistedPackages verifies the analyzer does not flag
// direct exec calls inside packages on the allowlist (the broker itself,
// credential stores, bootstrap paths, read-only probes). Without this
// exception the allowlist mechanism itself would be untested.
func TestAnalyzer_AllowsAllowlistedPackages(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "github.com/adalundhe/sylk/core/purevfs/fakepkg")
}

// TestAnalyzer_AcceptsBrokerRoutedExec verifies ordinary agent-layer code
// that routes commands through the broker (no direct exec.Command reference)
// produces zero diagnostics.
func TestAnalyzer_AcceptsBrokerRoutedExec(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "brokerok")
}

// TestIsAllowedPackage_CoversDocumentedBoundaries asserts the concrete package
// paths called out in the audit are covered by the allowlist. If someone
// edits allowedPathPrefixes and accidentally drops a boundary package, this
// test catches the regression — the allowlist stops being "what was audited"
// and starts being "whatever happens to be in the slice right now."
func TestIsAllowedPackage_CoversDocumentedBoundaries(t *testing.T) {
	covered := []string{
		"github.com/adalundhe/sylk/core/purevfs",
		"github.com/adalundhe/sylk/core/purevfs/somechild",
		"github.com/adalundhe/sylk/core/oauth",
		"github.com/adalundhe/sylk/core/credentials",
		"github.com/adalundhe/sylk/core/treesitter",
		"github.com/adalundhe/sylk/core/lsp",
		"github.com/adalundhe/sylk/ui/browser",
		"github.com/adalundhe/sylk/ui/fonts",
		"github.com/adalundhe/sylk/ui/editor/register",
		"github.com/adalundhe/sylk/core/boot",
	}
	for _, path := range covered {
		if !isAllowedPackage(path) {
			t.Errorf("expected %s to be allowlisted (boundary package), got false", path)
		}
	}
}

// TestIsAllowedPackage_BlocksAgentPaths asserts agent packages are NOT on the
// allowlist. A future edit that accidentally adds "agents/" or similar would
// silently disable the barrier for every agent; this test fails first.
func TestIsAllowedPackage_BlocksAgentPaths(t *testing.T) {
	blocked := []string{
		"github.com/adalundhe/sylk/agents/engineer",
		"github.com/adalundhe/sylk/agents/designer",
		"github.com/adalundhe/sylk/agents/tester/pipeline",
		"github.com/adalundhe/sylk/agents/tester/global",
		"github.com/adalundhe/sylk/agents/inspector/shared",
		"github.com/adalundhe/sylk/agents/shared",
	}
	for _, path := range blocked {
		if isAllowedPackage(path) {
			t.Errorf("agent package %s must NOT be allowlisted; broker mandate is non-optional for agents", path)
		}
	}
}

package pipeline

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/tester"
)

func TestTaskStageResult_ResponseTextHumanizesStageSnapshot(t *testing.T) {
	stage := &TaskStageResult{
		CreatedFiles: []string{"tests/test_cli.py"},
		SuiteResult:  &tester.TestSuiteResult{TotalTests: 1, Passed: 0, Failed: 1},
	}

	text := stage.ResponseText()
	if text == "" {
		t.Fatal("expected non-empty response text")
	}
	if text == `{"Plan":null,"CreatedFiles":["tests/test_cli.py"]}` {
		t.Fatalf("expected prose summary, got raw JSON %q", text)
	}
	if want := "Created test artifacts: tests/test_cli.py."; !strings.Contains(text, want) {
		t.Fatalf("response text = %q, want substring %q", text, want)
	}
	if want := "Latest test run: 0 passed, 1 failed, 0 skipped, 0 errors out of 1."; !strings.Contains(text, want) {
		t.Fatalf("response text = %q, want substring %q", text, want)
	}
}

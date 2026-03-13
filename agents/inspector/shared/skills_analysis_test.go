package shared

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectAnalysisLanguage_PrefersExplicitPythonPaths(t *testing.T) {
	runner := NewToolRunner(ToolRunnerConfig{
		WorkingDir: filepath.Clean(filepath.Join("..", "..", "..")),
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if got := detectAnalysisLanguage(context.Background(), runner, []string{"src/hello/greeter.py"}); got != "python" {
		t.Fatalf("detectAnalysisLanguage() = %q, want %q", got, "python")
	}
}

func TestRunSecurityAnalysis_PythonUsesPythonAnalyzer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unsafe.py"), []byte("result = eval(user_input)\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runner := NewToolRunner(ToolRunnerConfig{
		WorkingDir: dir,
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	issues := runSecurityAnalysis(context.Background(), runner, []string{"unsafe.py"})
	if !containsIssueRule(issues, "python-security-eval") {
		t.Fatalf("expected python security scan to flag eval, got %#v", issues)
	}
	for _, issue := range issues {
		if strings.Contains(strings.ToLower(issue.Message), "gosec") {
			t.Fatalf("expected python scan, got gosec issue: %#v", issue)
		}
	}
}

func TestRunTypeCheckerAnalysis_PythonDoesNotFallBackToGoToolFailures(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "greeter.py"), []byte("def greet(name: str = 'World') -> str:\n    return f'Hello, {name}!'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runner := NewToolRunner(ToolRunnerConfig{
		WorkingDir: dir,
		Timeout:    5 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	issues := runTypeCheckerAnalysis(context.Background(), runner, []string{"greeter.py"})
	if HasBlockingIssues(issues) {
		t.Fatalf("expected python type-check fallback to remain non-blocking, got %#v", issues)
	}
	for _, issue := range issues {
		message := strings.ToLower(issue.Message)
		if strings.Contains(message, "go vet") || strings.Contains(message, "staticcheck") {
			t.Fatalf("expected python-aware result, got go tool issue: %#v", issue)
		}
	}
}

func TestUnavailableToolIssuesAreInformational(t *testing.T) {
	issues := unavailableToolIssues("golangci-lint")
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1", len(issues))
	}
	if issues[0].Severity != Info {
		t.Fatalf("severity = %q, want %q", issues[0].Severity, Info)
	}
}

func containsIssueRule(issues []ValidationIssue, ruleID string) bool {
	for _, issue := range issues {
		if issue.RuleID == ruleID {
			return true
		}
	}
	return false
}

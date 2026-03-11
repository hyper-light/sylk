package commandapproval

import (
	"path/filepath"
	"testing"
)

func TestAnalyze_GeneralizesWorkspaceMkdir(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "workspace", "repo")
	first, err := Analyze(Request{
		Command:       "mkdir -p src/hello_cli",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(first): %v", err)
	}
	second, err := Analyze(Request{
		Command:       "mkdir -p pkg/generated",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(second): %v", err)
	}
	if first.TemplateKey != second.TemplateKey {
		t.Fatalf("template key mismatch: %q != %q", first.TemplateKey, second.TemplateKey)
	}
	if first.PersistKey != second.PersistKey {
		t.Fatalf("persist key mismatch: %q != %q", first.PersistKey, second.PersistKey)
	}
	if first.PersistKey != first.TemplateKey {
		t.Fatalf("persist key = %q, want workspace template %q", first.PersistKey, first.TemplateKey)
	}
	if first.OutsideWorkspace {
		t.Fatal("expected inside-workspace mkdir to remain inside workspace")
	}
}

func TestAnalyze_DistinguishesOutsideWorkspace(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "workspace", "repo")
	inside, err := Analyze(Request{
		Command:       "mkdir -p src/hello_cli",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(inside): %v", err)
	}
	outside, err := Analyze(Request{
		Command:       "mkdir -p ../outside",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(outside): %v", err)
	}
	if inside.TemplateKey == outside.TemplateKey {
		t.Fatalf("expected outside-workspace command to produce a different rule key")
	}
	if inside.PersistKey == outside.PersistKey {
		t.Fatalf("expected outside-workspace command to produce a different persist key")
	}
	if !outside.OutsideWorkspace {
		t.Fatal("expected ../outside to be marked outside the workspace")
	}
}

func TestAnalyze_UsesExactPersistRuleForNonGeneralizedCommands(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "workspace", "repo")
	first, err := Analyze(Request{
		Command:       "chmod +x scripts/bootstrap.sh",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(first): %v", err)
	}
	second, err := Analyze(Request{
		Command:       "chmod +x scripts/migrate.sh",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(second): %v", err)
	}
	if first.TemplateKey != second.TemplateKey {
		t.Fatalf("template key mismatch: %q != %q", first.TemplateKey, second.TemplateKey)
	}
	if first.PersistKey == second.PersistKey {
		t.Fatalf("expected exact persist keys for non-generalized commands, got %q", first.PersistKey)
	}
	if first.PersistKey != first.ExactKey {
		t.Fatalf("persist key = %q, want exact key %q", first.PersistKey, first.ExactKey)
	}
}

func TestAnalyze_ScopesOutsideRulesByZone(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "workspace", "repo")
	parentA, err := Analyze(Request{
		Command:       "mkdir -p ../cache-a",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(parentA): %v", err)
	}
	parentB, err := Analyze(Request{
		Command:       "mkdir -p ../cache-b",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(parentB): %v", err)
	}
	absTmp, err := Analyze(Request{
		Command:       "mkdir -p /tmp/cache-b",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("Analyze(absTmp): %v", err)
	}
	if parentA.PersistKey != parentB.PersistKey {
		t.Fatalf("expected sibling-parent mkdir commands to share a persist key")
	}
	if parentA.PersistKey == absTmp.PersistKey {
		t.Fatalf("expected parent-relative and /tmp mkdir commands to use different persist keys")
	}
}

func TestEvaluator_DefaultPolicy(t *testing.T) {
	evaluator := NewEvaluator(nil)
	allowed, err := evaluator.Evaluate(Request{
		Command:       "go test ./...",
		WorkspaceRoot: "/workspace/repo",
	})
	if err != nil {
		t.Fatalf("Evaluate(allow): %v", err)
	}
	if allowed.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want allow", allowed.Decision)
	}
	prompt, err := evaluator.Evaluate(Request{
		Command:       "mkdir -p src/hello_cli",
		WorkspaceRoot: "/workspace/repo",
	})
	if err != nil {
		t.Fatalf("Evaluate(prompt): %v", err)
	}
	if prompt.Decision != DecisionPrompt {
		t.Fatalf("decision = %s, want prompt", prompt.Decision)
	}
}

func TestEvaluator_SavedRuleOverridesPrompt(t *testing.T) {
	store := NewRuleStore("")
	evaluator := NewEvaluator(store)
	eval, err := evaluator.Evaluate(Request{
		Command:       "mkdir -p src/hello_cli",
		WorkspaceRoot: "/workspace/repo",
	})
	if err != nil {
		t.Fatalf("Evaluate(initial): %v", err)
	}
	if eval.Decision != DecisionPrompt {
		t.Fatalf("decision = %s, want prompt", eval.Decision)
	}
	if err := store.Record(Rule{
		MatchKey: eval.Analysis.PersistKey,
		Action:   RuleActionAllow,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	approved, err := evaluator.Evaluate(Request{
		Command:       "mkdir -p another/dir",
		WorkspaceRoot: "/workspace/repo",
	})
	if err != nil {
		t.Fatalf("Evaluate(saved): %v", err)
	}
	if approved.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want allow", approved.Decision)
	}
}

func TestEvaluator_ExactPersistRuleDoesNotGeneralizeNonGeneralizedCommands(t *testing.T) {
	store := NewRuleStore("")
	evaluator := NewEvaluator(store)
	first, err := evaluator.Evaluate(Request{
		Command:       "chmod +x scripts/bootstrap.sh",
		WorkspaceRoot: "/workspace/repo",
	})
	if err != nil {
		t.Fatalf("Evaluate(first): %v", err)
	}
	if first.Decision != DecisionPrompt {
		t.Fatalf("decision = %s, want prompt", first.Decision)
	}
	if err := store.Record(Rule{
		MatchKey: first.Analysis.PersistKey,
		Action:   RuleActionAllow,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	second, err := evaluator.Evaluate(Request{
		Command:       "chmod +x scripts/migrate.sh",
		WorkspaceRoot: "/workspace/repo",
	})
	if err != nil {
		t.Fatalf("Evaluate(second): %v", err)
	}
	if second.Decision != DecisionPrompt {
		t.Fatalf("decision = %s, want prompt for different script", second.Decision)
	}
}

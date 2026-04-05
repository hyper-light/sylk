package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type GlobalExecutionState struct {
	mu                 sync.RWMutex
	contract           *GlobalExecutionContract
	successfulTools    map[string]int
	readPaths          map[string]struct{}
	testReadPaths      map[string]struct{}
	sourceReadPaths    map[string]struct{}
	workspaceInspected bool
	riskCount          int
	plannedCases       int
	suiteRuns          int
	diagnoses          int
	testWrites         int
	harnessBuilds      int
	toolingRecovery    int
}

type globalExecutionStateKey struct{}

func WithGlobalExecutionState(ctx context.Context, state *GlobalExecutionState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, globalExecutionStateKey{}, state)
}

func GlobalExecutionStateFromContext(ctx context.Context) *GlobalExecutionState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(globalExecutionStateKey{}).(*GlobalExecutionState)
	return state
}

func NewGlobalExecutionState(contract *GlobalExecutionContract) *GlobalExecutionState {
	return &GlobalExecutionState{
		contract:        contract,
		successfulTools: make(map[string]int),
		readPaths:       make(map[string]struct{}),
		testReadPaths:   make(map[string]struct{}),
		sourceReadPaths: make(map[string]struct{}),
	}
}

func RecordGlobalExecutionSuccess(ctx context.Context, toolName string, input map[string]any, output string) {
	state := GlobalExecutionStateFromContext(ctx)
	if state == nil {
		return
	}
	state.recordSuccess(toolName, input, decodeGlobalExecutionOutput(output))
}

func ValidateGlobalTesterHandoffEvidence(ctx context.Context, action *GlobalReviewTurnAction) error {
	if action == nil {
		return nil
	}
	if normalizeGlobalReviewAgent(action.AgentType) != GlobalReviewAgentTester {
		return nil
	}
	if action.Type != GlobalReviewActionHandoff || normalizeGlobalReviewAgent(action.TargetAgent) != GlobalReviewAgentInspector {
		return nil
	}
	state := GlobalExecutionStateFromContext(ctx)
	if state == nil {
		return nil
	}
	return state.validateTesterHandoff()
}

func (s *GlobalExecutionState) recordSuccess(toolName string, input, output map[string]any) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.successfulTools[toolName]++
	switch toolName {
	case "read_workspace_file", "read_file":
		path := normalizeGlobalExecutionPath(stringInput(input, "path"))
		if path != "" {
			s.readPaths[path] = struct{}{}
			if looksLikeGlobalTestPath(path) {
				s.testReadPaths[path] = struct{}{}
			} else {
				s.sourceReadPaths[path] = struct{}{}
			}
		}
	case "inspect_workspace_state", "summarize_workspace_state", "workspace_glob", "workspace_grep", "diff_workspace_file":
		s.workspaceInspected = true
	case "analyze_risk", "analyze_integration_risks":
		s.riskCount += maxGlobalExecutionCount(output, "risk_count", len(mapSlice(output, "risk_areas")))
	case "plan_tests", "plan_integration_tests", "plan_e2e_tests":
		s.plannedCases += maxGlobalExecutionCount(output, "case_count", plannedCaseCount(output))
	case "run_test_suite":
		s.suiteRuns++
	case "diagnose_failure":
		s.diagnoses++
	case "write_test", "write_integration_test", "write_e2e_test":
		s.testWrites++
	case "build_harness":
		s.harnessBuilds++
	case "research_test_tool_install", "install_test_tooling":
		s.toolingRecovery++
	case "analyze_batch":
		if files := stringSliceInput(input, "changed_files"); len(files) > 0 {
			s.workspaceInspected = true
		}
	}
}

func (s *GlobalExecutionState) validateTesterHandoff() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.contract == nil {
		return nil
	}
	if len(s.sourceReadPaths) == 0 && !s.workspaceInspected {
		return fmt.Errorf("Before handoff_next, inspect the relevant implementation with read_workspace_file, inspect_workspace_state, summarize_workspace_state, workspace_glob, or workspace_grep")
	}

	switch s.contract.Mode {
	case GlobalExecutionModePlan:
		if s.riskCount == 0 {
			return fmt.Errorf("Before handoff_next, ground the plan with analyze_risk or analyze_integration_risks")
		}
		if s.plannedCases == 0 {
			return fmt.Errorf("Before handoff_next, produce a concrete plan with plan_tests, plan_integration_tests, or plan_e2e_tests that yields at least one planned case")
		}
		if len(s.testReadPaths) == 0 && s.suiteRuns == 0 {
			return fmt.Errorf("Before handoff_next, read the relevant existing tests or run_test_suite so the plan is grounded in the current test surface")
		}
	case GlobalExecutionModeAuthor:
		if s.plannedCases == 0 {
			return fmt.Errorf("Before handoff_next, produce a concrete authored-test plan before returning the work")
		}
		if s.testWrites == 0 {
			return fmt.Errorf("Before handoff_next, materialize the requested tests with write_test, write_integration_test, or write_e2e_test")
		}
		if s.suiteRuns == 0 {
			return fmt.Errorf("Before handoff_next, run_test_suite to gather execution evidence for the authored tests")
		}
	case GlobalExecutionModeDiagnose:
		if s.suiteRuns == 0 {
			return fmt.Errorf("Before handoff_next, run_test_suite to capture the failure signal you are diagnosing")
		}
		if s.diagnoses == 0 {
			return fmt.Errorf("Before handoff_next, use diagnose_failure so the returned diagnosis is grounded in the concrete failure evidence")
		}
	default:
		if s.riskCount == 0 {
			return fmt.Errorf("Before handoff_next, ground the tester pass with analyze_risk or analyze_integration_risks")
		}
		if len(s.testReadPaths) == 0 && s.suiteRuns == 0 {
			return fmt.Errorf("Before handoff_next, read the relevant existing tests or run_test_suite so the returned audit is grounded in actual test evidence")
		}
		if s.suiteRuns == 0 {
			if s.toolingRecovery > 0 || s.harnessBuilds > 0 {
				return fmt.Errorf("Execution is still blocked. Finish the harness or tooling recovery work, or return a targeted challenge instead of a top-level handoff")
			}
			return fmt.Errorf("Before handoff_next, run_test_suite to gather concrete execution evidence")
		}
	}

	return nil
}

func decodeGlobalExecutionOutput(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload
}

func maxGlobalExecutionCount(values map[string]any, key string, fallback int) int {
	if count := intAny(values, key); count > 0 {
		return count
	}
	return fallback
}

func plannedCaseCount(values map[string]any) int {
	plan, ok := values["plan"].(map[string]any)
	if !ok || plan == nil {
		return 0
	}
	return len(mapSlice(plan, "planned_cases"))
}

func mapSlice(values map[string]any, key string) []any {
	if values == nil {
		return nil
	}
	items, _ := values[key].([]any)
	return items
}

func stringSliceInput(input map[string]any, key string) []string {
	if input == nil {
		return nil
	}
	switch typed := input[key].(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				text = strings.TrimSpace(text)
				if text != "" {
					out = append(out, text)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeGlobalExecutionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	for strings.HasPrefix(clean, "./") {
		clean = strings.TrimPrefix(clean, "./")
	}
	if clean == "." {
		return ""
	}
	return clean
}

func looksLikeGlobalTestPath(path string) bool {
	path = strings.ToLower(normalizeGlobalExecutionPath(path))
	switch {
	case path == "":
		return false
	case strings.Contains(path, "/test/"), strings.Contains(path, "/tests/"), strings.Contains(path, "/spec/"), strings.Contains(path, "/specs/"):
		return true
	case strings.Contains(path, "_test."), strings.Contains(path, "_spec."), strings.Contains(path, ".test."), strings.Contains(path, ".spec."):
		return true
	case strings.Contains(filepath.Base(path), "test_"):
		return true
	default:
		return false
	}
}

package shared

import (
	"fmt"
	"regexp"
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

var inspectorTestExecutionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|\n\r]\s*)(?:env\s+)?(?:[A-Za-z_][A-Za-z0-9_]*=\S+\s+)*(?:go\s+test|cargo\s+test|npm\s+test|pnpm\s+test|yarn\s+test|bun\s+test|pytest(?:\s|$)|vitest(?:\s|$)|jest(?:\s|$)|rspec(?:\s|$)|ctest(?:\s|$)|playwright\s+test|cypress(?:\s+(?:run|open|test))?|python(?:3)?\s+-m\s+(?:pytest|unittest))(?:\s|$)`),
}

var inspectorTestToolTokens = map[string]struct{}{
	"bun-test":   {},
	"cypress":    {},
	"go-test":    {},
	"jest":       {},
	"mocha":      {},
	"playwright": {},
	"pytest":     {},
	"rspec":      {},
	"test":       {},
	"unittest":   {},
	"vitest":     {},
}

func InspectorRejectTestExecution(command, toolName string) error {
	if !inspectorCommandLooksLikeTestExecution(command) {
		return nil
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "command execution"
	}
	return fmt.Errorf("%w: %s is not permitted for inspector when it executes tests; inspectors must route test execution to Tester instead", agentshared.ErrRouteTestExecutionToTester, name)
}

func InspectorRejectTestDependencyResearch(missingTool, frameworkID, runCommand, failure string) error {
	if inspectorValueLooksLikeTestTool(missingTool) ||
		inspectorValueLooksLikeTestTool(frameworkID) ||
		inspectorCommandLooksLikeTestExecution(runCommand) ||
		inspectorFailureLooksLikeTestTooling(failure) {
		return fmt.Errorf("%w: dependency(action=research) is not permitted for inspector when the missing dependency is for test execution; route test tooling requests to Tester instead", agentshared.ErrRouteTestToolingToTester)
	}
	return nil
}

func InspectorRejectTestDependencyInstallPlan(plan *agentshared.DependencyInstallPlan) error {
	if plan == nil {
		return nil
	}
	if inspectorValueLooksLikeTestTool(plan.MissingTool) || inspectorValueLooksLikeTestTool(plan.Framework) {
		return fmt.Errorf("%w: dependency(action=install) is not permitted for inspector when the plan targets test tooling; route test tooling installs to Tester instead", agentshared.ErrRouteTestToolingToTester)
	}
	if inspectorCommandLooksLikeTestExecution(plan.ValidationCommand) {
		return fmt.Errorf("%w: dependency(action=install) is not permitted for inspector when validation_command executes tests; route test validation to Tester instead", agentshared.ErrRouteTestToolingToTester)
	}
	for _, step := range plan.Steps {
		if inspectorInstallStepTargetsTestTooling(step.Command) {
			return fmt.Errorf("%w: dependency(action=install) is not permitted for inspector when the plan installs test tooling; route test tooling installs to Tester instead", agentshared.ErrRouteTestToolingToTester)
		}
	}
	return nil
}

func inspectorCommandLooksLikeTestExecution(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return false
	}
	for _, pattern := range inspectorTestExecutionPatterns {
		if pattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func inspectorFailureLooksLikeTestTooling(failure string) bool {
	lower := strings.ToLower(strings.TrimSpace(failure))
	if lower == "" {
		return false
	}
	if !(strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "no module named") ||
		strings.Contains(lower, "cannot find") ||
		strings.Contains(lower, "missing")) {
		return false
	}
	for token := range inspectorTestToolTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func inspectorValueLooksLikeTestTool(value string) bool {
	normalized := normalizeInspectorToolToken(value)
	if normalized == "" {
		return false
	}
	_, ok := inspectorTestToolTokens[normalized]
	return ok
}

func inspectorInstallStepTargetsTestTooling(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	if inspectorCommandLooksLikeTestExecution(lower) {
		return true
	}
	installMarkers := []string{
		" pip install ",
		" pip3 install ",
		" python -m pip install ",
		" python3 -m pip install ",
		" npm install ",
		" npm add ",
		" pnpm add ",
		" pnpm install ",
		" yarn add ",
		" yarn install ",
		" bun add ",
		" cargo add ",
		" go install ",
	}
	padded := " " + lower + " "
	hasInstallMarker := false
	for _, marker := range installMarkers {
		if strings.Contains(padded, marker) {
			hasInstallMarker = true
			break
		}
	}
	if !hasInstallMarker {
		return false
	}
	for token := range inspectorTestToolTokens {
		if token == "test" {
			continue
		}
		if strings.Contains(padded, " "+token+" ") || strings.Contains(padded, "/"+token+" ") {
			return true
		}
	}
	return false
}

func normalizeInspectorToolToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	switch value {
	case "gotest":
		return "go-test"
	case "python-unittest", "python3-unittest":
		return "unittest"
	}
	return value
}

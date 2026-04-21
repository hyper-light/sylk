// Phase 2.K / GT-4 + GI-5 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// research_dependency_install + install_dependency_tooling collapsed
// into one `dependency` skill with action ∈ {research, install}.
//
// Each action keeps its distinct implementation (research bus-routes
// to academic; install executes a plan through the strict-disk broker),
// but the LLM sees one tool. Test-tooling variants (pipeline and
// global tester) pass category="test" to get the test-specific
// prompt wording — behavior matches the old research_test_tool_install
// / install_test_tooling skills, and the old names are retired.
package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// DependencyResearchHandler runs the research path — typically a bus
// RPC to academic that returns a structured install plan.
type DependencyResearchHandler func(
	ctx context.Context,
	missingTool, failure, frameworkID, runCommand string,
	files []string,
	taskSpec string,
) (*DependencyInstallPlan, error)

// DependencyInstallHandler runs the install path — typically the
// strict-disk broker invocation that executes a validated plan.
type DependencyInstallHandler func(ctx context.Context, plan *DependencyInstallPlan) (map[string]any, error)

// DependencyManagementSkillConfig wires the one-primitive dependency
// skill onto a specific agent. Exactly one ResearchHandler and one
// InstallHandler must be non-nil. Category switches the prompt
// phrasing between general dependency tooling and test tooling.
type DependencyManagementSkillConfig struct {
	// Category is either "general" (default) or "test". "test" swaps
	// the skill's wording to emphasize test-framework tooling rather
	// than arbitrary project dependencies.
	Category string

	ResearchHandler DependencyResearchHandler
	InstallHandler  DependencyInstallHandler
}

// NewDependencyManagementSkill returns the `dependency` skill with
// `action ∈ {research, install}`. Replaces the old four-skill matrix
// (research_dependency_install, install_dependency_tooling,
// research_test_tool_install, install_test_tooling).
func NewDependencyManagementSkill(cfg DependencyManagementSkillConfig) *skills.Skill {
	category := strings.ToLower(strings.TrimSpace(cfg.Category))
	if category == "" {
		category = "general"
	}
	noun := "project tooling or dependencies"
	examples := "pytest, vitest, a missing linter, or any other project package"
	if category == "test" {
		noun = "test frameworks, harnesses, or related test-only tooling"
		examples = "pytest, vitest, jest, playwright, cypress, or test runners"
	}

	stepProps := map[string]*skills.Property{
		"command": {Type: "string", Description: "Single install command to run inside the strict-disk execution sandbox. No pipes, chaining, or shell control operators."},
		"reason":  {Type: "string", Description: "Why the step is needed."},
	}

	return skills.NewSkill("dependency").
		Description(fmt.Sprintf("Research or execute an install plan for %s. One primitive with action ∈ {research, install}; research asks Academic for concrete install steps, install executes an approved plan through the command-approval dialogue. Examples: %s.", noun, examples)).
		Domain("code").
		Keywords("install", "dependency", "tooling", "research", "approval", "package manager", "academic", category).
		Priority(87).
		Usage("action=research: call when implementation or validation is blocked by missing tooling and you need concrete install steps. action=install: call after research once you have a concrete plan. Each install command goes through the existing approval dialogue and executes in the strict-disk sandbox against the agent's active workspace view.").
		Requirement("action=research: provide missing_tool or failure so Academic can scope the research. action=install: provide summary + a list of single install commands. Each step must be one command without chaining or shell control operators.").
		Satisfies("Produces a concrete install plan (research) or installs missing project tooling and captures execution evidence (install).").
		Avoid("Do not guess install commands when Academic can research the repository-specific package manager first.").
		EnumParam("action", "Dependency action to perform", []string{"research", "install"}, true).
		// Research-only fields.
		StringParam("missing_tool", "Missing executable or package if already known (research).", false).
		StringParam("failure", "Error output showing the missing dependency or tool (research).", false).
		StringParam("framework_id", "Detected framework or ecosystem identifier (research).", false).
		StringParam("run_command", "Blocked command that would verify or use the dependency (research).", false).
		ArrayParam("files", "Relevant source or config files for project context (research).", "string", false).
		StringParam("task_spec", "Task brief or acceptance criteria (research).", false).
		// Install-only fields.
		StringParam("summary", "Short explanation of the install plan (install).", false).
		StringParam("validation_command", "Optional non-mutating command to verify the install succeeded (install).", false).
		ArrayParam("notes", "Important caveats or assumptions (install).", "string", false).
		ArrayObjectParam("steps", "Concrete single-command install steps to execute after approval (install).", stepProps, []string{"command"}, false).
		// Both actions carry framework for prompt / telemetry.
		StringParam("framework", "Framework or ecosystem context for the install plan (install).", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p struct {
				Action string `json:"action"`
				// research
				MissingTool string   `json:"missing_tool,omitempty"`
				Failure     string   `json:"failure,omitempty"`
				FrameworkID string   `json:"framework_id,omitempty"`
				RunCommand  string   `json:"run_command,omitempty"`
				Files       []string `json:"files,omitempty"`
				TaskSpec    string   `json:"task_spec,omitempty"`
				// install
				Summary           string                  `json:"summary,omitempty"`
				ValidationCommand string                  `json:"validation_command,omitempty"`
				Notes             []string                `json:"notes,omitempty"`
				Steps             []DependencyInstallStep `json:"steps,omitempty"`
				Framework         string                  `json:"framework,omitempty"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.ToLower(strings.TrimSpace(p.Action)) {
			case "research":
				if cfg.ResearchHandler == nil {
					return nil, fmt.Errorf("dependency(action=research) is not wired on this agent")
				}
				return cfg.ResearchHandler(ctx, p.MissingTool, p.Failure, p.FrameworkID, p.RunCommand, p.Files, p.TaskSpec)
			case "install":
				if cfg.InstallHandler == nil {
					return nil, fmt.Errorf("dependency(action=install) is not wired on this agent")
				}
				plan := &DependencyInstallPlan{
					Summary:           p.Summary,
					MissingTool:       p.MissingTool,
					Framework:         p.Framework,
					ValidationCommand: p.ValidationCommand,
					Notes:             append([]string(nil), p.Notes...),
					Steps:             append([]DependencyInstallStep(nil), p.Steps...),
				}
				return cfg.InstallHandler(ctx, plan)
			default:
				return nil, fmt.Errorf("unknown dependency action: %q (expected research|install)", p.Action)
			}
		}).
		Build()
}

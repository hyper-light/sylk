package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/versioning"
)

type DependencyInstallStep struct {
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type DependencyInstallPlan struct {
	Summary           string                  `json:"summary"`
	MissingTool       string                  `json:"missing_tool,omitempty"`
	Framework         string                  `json:"framework,omitempty"`
	ValidationCommand string                  `json:"validation_command,omitempty"`
	Notes             []string                `json:"notes,omitempty"`
	Steps             []DependencyInstallStep `json:"steps"`
}

type DependencyInstallResearchRequest struct {
	Bus             guide.EventBus
	ResponseTopic   string
	SourceAgentID   string
	SourceAgentName string
	SessionID       string
	RepositoryRoot  string
	FrameworkID     string
	RunCommand      string
	MissingTool     string
	Failure         string
	TaskSpec        string
	Files           []string
	ProjectSignals  []string
}

func ResearchDependencyInstallPlan(ctx context.Context, req DependencyInstallResearchRequest) (*DependencyInstallPlan, error) {
	if req.Bus == nil || strings.TrimSpace(req.ResponseTopic) == "" {
		return nil, fmt.Errorf("dependency install research bus is unavailable")
	}
	prompt := BuildDependencyInstallResearchPrompt(req)
	summary := firstNonEmpty(
		strings.TrimSpace(req.MissingTool),
		strings.TrimSpace(req.FrameworkID),
		"dependency install plan",
	)
	branchCtx, branch := BeginInterAgentBranch(ctx, InterAgentBranchSpec{
		Kind:       InterAgentToolEventKindConsult,
		ToolName:   "consult_academic_dependency_install",
		AgentTypes: []string{"academic"},
		Summary:    "research install plan for " + summary,
		Args: map[string]any{
			"target":       "academic",
			"query":        prompt,
			"missing_tool": strings.TrimSpace(req.MissingTool),
			"framework":    strings.TrimSpace(req.FrameworkID),
		},
	})
	msg, err := RequestGuideRouteSync(branchCtx, GuideRouteSyncRequest{
		Bus:           req.Bus,
		ResponseTopic: req.ResponseTopic,
		Request: &guide.RouteRequest{
			Input:           prompt,
			TargetAgentID:   "academic",
			ExplicitTarget:  true,
			SourceAgentID:   strings.TrimSpace(req.SourceAgentID),
			SourceAgentName: strings.TrimSpace(req.SourceAgentName),
			SessionID:       strings.TrimSpace(req.SessionID),
			Metadata:        branch.ApplyMetadata(branchCtx, nil),
		},
	})
	branch.CompleteFromMessage(branchCtx, msg, err)
	if err != nil {
		return nil, fmt.Errorf("research install steps via Academic: %w", err)
	}
	content, err := ExtractDependencyInstallResearchContent(msg)
	if err != nil {
		return nil, err
	}
	plan, err := ParseDependencyInstallPlan(content)
	if err != nil {
		return nil, fmt.Errorf("parse Academic install plan: %w", err)
	}
	if strings.TrimSpace(plan.MissingTool) == "" {
		plan.MissingTool = strings.TrimSpace(req.MissingTool)
	}
	if strings.TrimSpace(plan.Framework) == "" {
		plan.Framework = strings.TrimSpace(req.FrameworkID)
	}
	return plan, nil
}

func BuildDependencyInstallResearchPrompt(req DependencyInstallResearchRequest) string {
	signalBlock := "(none)"
	if len(req.ProjectSignals) > 0 {
		signalBlock = "- " + strings.Join(req.ProjectSignals, "\n- ")
	}
	targetFiles := "(none)"
	if len(req.Files) > 0 {
		targetFiles = "- " + strings.Join(req.Files, "\n- ")
	}
	return strings.TrimSpace(fmt.Sprintf(
		`You are helping an agent restore missing project tooling in a repository.

Return JSON only. No prose outside the JSON.

Schema:
{
  "summary": "short explanation",
  "missing_tool": "tool name",
  "framework": "framework id or name",
  "validation_command": "optional single command",
  "notes": ["optional caveat"],
  "steps": [
    {
      "command": "single install command",
      "reason": "why this step is needed"
    }
  ]
}

Hard requirements:
- Each step command must be exactly one command.
- Do not use pipes, &&, ||, ;, redirection, subshells, or multi-line shell.
- Do not create ad-hoc virtual environments or activation steps such as 'python -m venv', 'python3 -m venv', 'virtualenv', 'uv venv', 'source .venv/bin/activate', or '.venv/bin/...'.
- Do not install tooling into temporary scratch locations such as '/tmp'; choose the repository package manager or interpreter-coupled package installation instead.
- When Python package installation is needed, prefer 'python -m pip ...' or 'python3 -m pip ...' over bare 'pip' or 'pip3'.
- Prefer the package manager already implied by the repository files.
- Prefer workspace-local or project-scoped installation when possible.
- Keep the plan minimal.

Repository root: %s
Detected framework: %s
Expected run command: %s
Missing tool hint: %s
Failure output:
%s

Relevant task files:
%s

Relevant project signals:
%s

Task specification:
%s
`,
		strings.TrimSpace(req.RepositoryRoot),
		strings.TrimSpace(req.FrameworkID),
		strings.TrimSpace(req.RunCommand),
		strings.TrimSpace(req.MissingTool),
		strings.TrimSpace(req.Failure),
		targetFiles,
		signalBlock,
		strings.TrimSpace(req.TaskSpec),
	))
}

func ExtractDependencyInstallResearchContent(msg *guide.Message) (string, error) {
	if msg == nil {
		return "", fmt.Errorf("academic response is missing")
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		if errText, ok := msg.GetError(); ok {
			return "", fmt.Errorf("%s", strings.TrimSpace(errText))
		}
		return "", fmt.Errorf("academic response payload is unsupported")
	}
	if !resp.Success {
		return "", fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
	if content := extractDependencyInstallResponseContent(resp.Data); strings.TrimSpace(content) != "" {
		return content, nil
	}
	return "", fmt.Errorf("academic response did not include install-plan content")
}

func extractDependencyInstallResponseContent(data any) string {
	switch typed := data.(type) {
	case map[string]any:
		if content, _ := typed["content"].(string); strings.TrimSpace(content) != "" {
			return strings.TrimSpace(content)
		}
		for _, key := range []string{"result", "response", "answer", "text"} {
			if content, _ := typed[key].(string); strings.TrimSpace(content) != "" {
				return strings.TrimSpace(content)
			}
		}
		if nested, ok := typed["data"]; ok {
			if content := extractDependencyInstallResponseContent(nested); strings.TrimSpace(content) != "" {
				return content
			}
		}
		if looksLikeDependencyInstallPlanMap(typed) {
			raw, err := json.Marshal(typed)
			if err == nil {
				return strings.TrimSpace(string(raw))
			}
		}
		return ""
	case string:
		return strings.TrimSpace(typed)
	default:
		raw, err := json.Marshal(data)
		if err != nil {
			return ""
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return strings.TrimSpace(string(raw))
		}
		if content := extractDependencyInstallResponseContent(payload); strings.TrimSpace(content) != "" {
			return content
		}
		if looksLikeDependencyInstallPlanMap(payload) {
			return strings.TrimSpace(string(raw))
		}
		return ""
	}
}

func looksLikeDependencyInstallPlanMap(payload map[string]any) bool {
	if len(payload) == 0 {
		return false
	}
	steps, ok := payload["steps"]
	if !ok {
		return false
	}
	switch typed := steps.(type) {
	case []any:
		return len(typed) > 0
	case []map[string]any:
		return len(typed) > 0
	default:
		return false
	}
}

func ParseDependencyInstallPlan(raw string) (*DependencyInstallPlan, error) {
	candidates := []string{
		strings.TrimSpace(raw),
		ExtractFencedJSON(raw),
		ExtractJSONObject(raw),
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		var plan DependencyInstallPlan
		if err := json.Unmarshal([]byte(candidate), &plan); err != nil {
			continue
		}
		if err := ValidateDependencyInstallPlan(&plan); err != nil {
			return nil, err
		}
		return &plan, nil
	}
	return nil, fmt.Errorf("academic response did not contain valid install-plan JSON")
}

func ExtractFencedJSON(raw string) string {
	start := strings.Index(raw, "```json")
	if start == -1 {
		start = strings.Index(raw, "```")
		if start == -1 {
			return ""
		}
		start += len("```")
	} else {
		start += len("```json")
	}
	end := strings.Index(raw[start:], "```")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(raw[start : start+end])
}

func ExtractJSONObject(raw string) string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end < start {
		return ""
	}
	return strings.TrimSpace(raw[start : end+1])
}

func ValidateDependencyInstallPlan(plan *DependencyInstallPlan) error {
	if plan == nil {
		return fmt.Errorf("install plan is required")
	}
	if len(plan.Steps) == 0 {
		return fmt.Errorf("install plan must contain at least one step")
	}
	for idx, step := range plan.Steps {
		command := strings.TrimSpace(step.Command)
		if command == "" {
			return fmt.Errorf("install step %d is missing a command", idx+1)
		}
		if DependencyCommandCreatesAdHocVirtualenv(command) {
			return fmt.Errorf("install step %d command creates an ad-hoc virtual environment; prefer the repository package manager or python -m pip", idx+1)
		}
		if DependencyCommandUsesLocalVenvExecutable(command) {
			return fmt.Errorf("install step %d command depends on a local virtualenv executable; prefer the repository package manager or python -m pip", idx+1)
		}
		if DependencyCommandHasUnsafeShellSyntax(command) {
			return fmt.Errorf("install step %d command uses unsupported shell syntax", idx+1)
		}
	}
	if validationCommand := strings.TrimSpace(plan.ValidationCommand); validationCommand != "" {
		switch {
		case DependencyCommandCreatesAdHocVirtualenv(validationCommand):
			return fmt.Errorf("validation_command creates an ad-hoc virtual environment; prefer the repository package manager or python -m pip")
		case DependencyCommandUsesLocalVenvExecutable(validationCommand):
			return fmt.Errorf("validation_command depends on a local virtualenv executable; prefer the repository package manager or python -m pip")
		case DependencyCommandHasUnsafeShellSyntax(validationCommand):
			return fmt.Errorf("validation_command uses unsupported shell syntax")
		}
	}
	if strings.TrimSpace(plan.Summary) == "" {
		plan.Summary = "Install missing project tooling"
	}
	return nil
}

func DependencyCommandCreatesAdHocVirtualenv(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}
	base := strings.ToLower(filepath.Base(fields[0]))
	switch base {
	case "virtualenv":
		return true
	case "uv":
		return len(fields) >= 2 && strings.EqualFold(fields[1], "venv")
	case "python", "python3":
		return len(fields) >= 3 && fields[1] == "-m" && (strings.EqualFold(fields[2], "venv") || strings.EqualFold(fields[2], "virtualenv"))
	default:
		return false
	}
}

func DependencyCommandUsesLocalVenvExecutable(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}
	exe := strings.ToLower(filepath.ToSlash(fields[0]))
	return strings.HasPrefix(exe, ".venv/") ||
		strings.Contains(exe, "/.venv/") ||
		strings.HasPrefix(exe, "venv/") ||
		strings.Contains(exe, "/venv/")
}

func DependencyCommandHasUnsafeShellSyntax(command string) bool {
	_, unsafe := DetectShellControlOperator(command)
	return unsafe
}

func FormatDependencyInstallPlan(plan *DependencyInstallPlan) string {
	lines := []string{strings.TrimSpace(plan.Summary)}
	for idx, step := range plan.Steps {
		lines = append(lines, fmt.Sprintf("%d. %s", idx+1, strings.TrimSpace(step.Command)))
	}
	return strings.Join(lines, "\n")
}

func ProjectInstallSignals(ctx context.Context, fileAccess versioning.FileAccess, workingDir string) []string {
	candidates := []string{
		"package.json",
		"pnpm-lock.yaml",
		"yarn.lock",
		"package-lock.json",
		"bun.lockb",
		"bun.lock",
		"pyproject.toml",
		"requirements.txt",
		"requirements-dev.txt",
		"setup.cfg",
		"setup.py",
		"Pipfile",
		"Pipfile.lock",
		"poetry.lock",
		"go.mod",
		"Cargo.toml",
		"Gemfile",
		"composer.json",
		"phpunit.xml",
		"pom.xml",
		"build.gradle",
		"build.gradle.kts",
		"gradlew",
		"gradlew.bat",
	}
	signals := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		exists, err := dependencySignalExists(ctx, fileAccess, workingDir, candidate)
		if err != nil || !exists {
			continue
		}
		signals = append(signals, candidate)
	}
	return signals
}

func dependencySignalExists(ctx context.Context, fileAccess versioning.FileAccess, workingDir, candidate string) (bool, error) {
	if fileAccess != nil {
		return fileAccess.Exists(ctx, candidate)
	}
	root := strings.TrimSpace(workingDir)
	if root == "" {
		root = "."
	}
	_, err := os.Stat(filepath.Join(root, candidate))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

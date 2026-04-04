package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
- If you provide validation_command, make it a simple non-mutating availability check such as '--version', '--help', or an import probe. Do not run the project test suite, lint the repo, or depend on files/tests that may not exist yet.
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
	return "", fmt.Errorf("academic response did not include install instructions")
}

type dependencyInstallResponseTexter interface {
	ResponseText() string
}

func extractDependencyInstallResponseContent(data any) string {
	if data == nil {
		return ""
	}
	if text, ok := data.(dependencyInstallResponseTexter); ok {
		return strings.TrimSpace(text.ResponseText())
	}
	switch typed := data.(type) {
	case map[string]any:
		for _, key := range []string{
			"content", "result", "response", "answer", "text", "output", "message",
			"instructions", "install_plan", "installPlan", "plan", "body", "payload", "data",
			"Response", "Result", "Answer", "Text", "Output", "Message", "Instructions", "Plan",
		} {
			if nested, ok := typed[key]; ok {
				if content := extractDependencyInstallResponseContent(nested); strings.TrimSpace(content) != "" {
					return content
				}
			}
		}
		if blocks := extractDependencyInstallContentBlocksFromMap(typed); strings.TrimSpace(blocks) != "" {
			return blocks
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
	case []any:
		return extractDependencyInstallResponseContentFromSlice(typed)
	case []string:
		return extractDependencyInstallResponseContentFromStrings(typed)
	case []map[string]any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return extractDependencyInstallResponseContentFromSlice(items)
	default:
		if content := extractDependencyInstallResponseContentFromReflectedValue(reflect.ValueOf(data)); strings.TrimSpace(content) != "" {
			return content
		}
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

func extractDependencyInstallResponseContentFromSlice(items []any) string {
	texts := make([]string, 0, len(items))
	for _, item := range items {
		if content := extractDependencyInstallResponseContent(item); strings.TrimSpace(content) != "" {
			texts = append(texts, content)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func extractDependencyInstallResponseContentFromStrings(items []string) string {
	texts := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			texts = append(texts, trimmed)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func extractDependencyInstallContentBlocksFromMap(payload map[string]any) string {
	for _, key := range []string{"content_blocks", "contentBlocks", "blocks", "items"} {
		raw, ok := payload[key]
		if !ok || raw == nil {
			continue
		}
		switch typed := raw.(type) {
		case []any:
			return extractDependencyInstallResponseContentFromSlice(typed)
		case []map[string]any:
			items := make([]any, 0, len(typed))
			for _, item := range typed {
				items = append(items, item)
			}
			return extractDependencyInstallResponseContentFromSlice(items)
		}
	}
	if blockType, _ := payload["type"].(string); strings.EqualFold(strings.TrimSpace(blockType), "text") {
		for _, key := range []string{"text", "content", "value"} {
			if text, _ := payload[key].(string); strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func extractDependencyInstallResponseContentFromReflectedValue(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		for _, name := range []string{"Response", "Content", "Text", "Message", "Output", "Instructions"} {
			field := v.FieldByName(name)
			if field.IsValid() && field.Kind() == reflect.String {
				if trimmed := strings.TrimSpace(field.String()); trimmed != "" {
					return trimmed
				}
			}
		}
	case reflect.Slice, reflect.Array:
		items := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			items = append(items, v.Index(i).Interface())
		}
		return extractDependencyInstallResponseContentFromSlice(items)
	}
	return ""
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
	if plan, err := parseDependencyInstallPlanText(raw); err == nil {
		if err := ValidateDependencyInstallPlan(plan); err != nil {
			return nil, err
		}
		return plan, nil
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

func parseDependencyInstallPlanText(raw string) (*DependencyInstallPlan, error) {
	lines := strings.Split(raw, "\n")
	steps := make([]DependencyInstallStep, 0, 4)
	seen := map[string]struct{}{}
	summary := ""
	validation := ""

	addStep := func(command string) {
		command = strings.TrimSpace(command)
		if command == "" {
			return
		}
		if _, ok := seen[command]; ok {
			return
		}
		seen[command] = struct{}{}
		steps = append(steps, DependencyInstallStep{Command: command})
	}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if summary == "" && !dependencyLineLooksCommandLike(line) {
			summary = dependencyInstallSummaryLine(line)
		}
		if cmd := dependencyValidationCommandFromLine(line); cmd != "" {
			validation = cmd
			continue
		}
		if cmd := dependencyInstallCommandFromLine(line); cmd != "" {
			addStep(cmd)
		}
	}
	if len(steps) == 0 {
		for _, segment := range extractInlineCodeSegments(raw) {
			if cmd := strings.TrimSpace(segment); looksLikeDependencyInstallCommand(cmd) {
				addStep(cmd)
			}
		}
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("no executable install commands found in academic response")
	}
	if summary == "" {
		summary = "Install missing project tooling"
	}
	return &DependencyInstallPlan{
		Summary:           summary,
		ValidationCommand: validation,
		Steps:             steps,
	}, nil
}

func dependencyInstallSummaryLine(line string) string {
	line = strings.TrimSpace(stripDependencyListPrefix(line))
	line = strings.TrimLeft(line, "#")
	line = strings.TrimSpace(line)
	lower := strings.ToLower(line)
	switch {
	case strings.HasPrefix(lower, "summary:"):
		return strings.TrimSpace(line[len("summary:"):])
	case strings.HasPrefix(lower, "plan:"):
		return strings.TrimSpace(line[len("plan:"):])
	default:
		return line
	}
}

func dependencyInstallCommandFromLine(line string) string {
	for _, segment := range extractInlineCodeSegments(line) {
		candidate := strings.TrimSpace(segment)
		if looksLikeDependencyInstallCommand(candidate) {
			return candidate
		}
	}
	candidate := strings.TrimSpace(stripDependencyListPrefix(line))
	if looksLikeDependencyInstallCommand(candidate) {
		return candidate
	}
	return ""
}

func dependencyValidationCommandFromLine(line string) string {
	cleaned := strings.TrimSpace(stripDependencyListPrefix(line))
	lower := strings.ToLower(cleaned)
	isValidationLine := strings.HasPrefix(lower, "validation_command") ||
		strings.HasPrefix(lower, "validation command") ||
		strings.HasPrefix(lower, "validation:") ||
		strings.HasPrefix(lower, "validate with") ||
		strings.HasPrefix(lower, "verify with") ||
		strings.HasPrefix(lower, "verification:")
	if !isValidationLine {
		return ""
	}
	for _, segment := range extractInlineCodeSegments(cleaned) {
		candidate := strings.TrimSpace(segment)
		if dependencyLineLooksCommandLike(candidate) {
			return candidate
		}
	}
	if idx := strings.Index(cleaned, ":"); idx >= 0 {
		candidate := strings.TrimSpace(cleaned[idx+1:])
		if dependencyLineLooksCommandLike(candidate) {
			return candidate
		}
	}
	fields := strings.Fields(cleaned)
	for i := 0; i < len(fields); i++ {
		candidate := strings.Join(fields[i:], " ")
		if dependencyLineLooksCommandLike(candidate) {
			return candidate
		}
	}
	return ""
}

func stripDependencyListPrefix(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '.' || ch == ')' {
			if i+1 < len(line) && line[i+1] == ' ' {
				return strings.TrimSpace(line[i+2:])
			}
		}
		break
	}
	return line
}

func extractInlineCodeSegments(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	segments := make([]string, 0, 4)
	for {
		start := strings.Index(raw, "`")
		if start < 0 {
			break
		}
		raw = raw[start+1:]
		end := strings.Index(raw, "`")
		if end < 0 {
			break
		}
		segment := strings.TrimSpace(raw[:end])
		if segment != "" {
			segments = append(segments, segment)
		}
		raw = raw[end+1:]
	}
	return segments
}

func dependencyLineLooksCommandLike(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || DependencyCommandHasUnsafeShellSyntax(line) {
		return false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(filepath.Base(fields[0]))
	switch first {
	case "python", "python3", "pip", "pip3", "npm", "pnpm", "yarn", "bun", "uv", "poetry", "rye", "pixi",
		"go", "cargo", "brew", "apt", "apt-get", "dnf", "yum", "apk", "pacman", "choco", "scoop", "winget",
		"gem", "bundle", "composer", "php", "dotnet", "nuget", "mvn", "gradle", "gradlew", "sbt", "nix", "nix-env",
		"pytest", "vitest", "jest", "mocha", "playwright", "ruff", "black", "mypy", "eslint", "prettier":
		return true
	}
	if strings.HasPrefix(strings.ToLower(fields[0]), "./") {
		return true
	}
	lower := strings.ToLower(line)
	return strings.Contains(lower, " --version") ||
		strings.Contains(lower, " -m ") ||
		strings.Contains(lower, " install ") ||
		strings.Contains(lower, " add ")
}

func looksLikeDependencyInstallCommand(command string) bool {
	command = strings.TrimSpace(command)
	if !dependencyLineLooksCommandLike(command) {
		return false
	}
	lower := strings.ToLower(command)
	return strings.Contains(lower, " install ") ||
		strings.Contains(lower, " add ") ||
		strings.HasPrefix(lower, "go install ") ||
		strings.HasPrefix(lower, "cargo install ") ||
		strings.HasPrefix(lower, "brew install ") ||
		strings.HasPrefix(lower, "apt install ") ||
		strings.HasPrefix(lower, "apt-get install ") ||
		strings.HasPrefix(lower, "dnf install ") ||
		strings.HasPrefix(lower, "yum install ") ||
		strings.HasPrefix(lower, "apk add ") ||
		strings.HasPrefix(lower, "pacman -s ") ||
		strings.HasPrefix(lower, "choco install ") ||
		strings.HasPrefix(lower, "scoop install ") ||
		strings.HasPrefix(lower, "winget install ")
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

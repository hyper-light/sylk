package engineer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/detect"
	"github.com/adalundhe/sylk/core/escalation"
	"github.com/adalundhe/sylk/core/format"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

func (e *Engineer) registerCoreSkills() {
	writeCfg := versioning.WorkspaceWriteSkillConfig{
		GetFileAccess:      func() versioning.FileAccess { return e.fileAccess },
		GetViews:           func() versioning.WorkspaceViewAccess { return e.workspaceViews },
		DefaultPipelineID:  func() string { return e.pipelineID },
		WritesEnabledCheck: func() bool { return e.config.EngineerConfig.EnableFileWrites },
	}

	// File operations
	e.skills.Register(readFileSkill(e))
	e.skills.Register(runCommandSkill(e))
	e.skills.Register(runShellScriptSkill(e))
	e.skills.Register(globSkill(e))
	e.skills.Register(grepSkill(e))
	e.skills.Register(versioning.NewReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return e.workspaceViews }, func() string { return e.pipelineID }))
	e.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return e.workspaceViews }, func() string { return e.pipelineID }))
	e.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return e.workspaceViews }, func() string { return e.pipelineID }))
	e.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return e.workspaceViews }, func() string { return e.pipelineID }))
	e.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return e.workspaceViews }, func() string { return e.pipelineID }))
	e.skills.Register(versioning.NewDiffWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return e.workspaceViews }, func() string { return e.pipelineID }, nil))
	e.skills.Register(versioning.NewPreparePipelineWriteContextSkill(func() versioning.WorkspaceViewAccess { return e.workspaceViews }, func() string { return e.pipelineID }, nil))
	e.skills.Register(versioning.NewListPipelineChangesSkill(func() versioning.FileAccess { return e.fileAccess }))
	e.skills.Register(versioning.NewWritePipelineFileSkill(writeCfg))
	e.skills.Register(versioning.NewEditPipelineFileSkill(writeCfg))
	e.skills.Register(versioning.NewDeletePipelineFileSkill(writeCfg))
	e.skills.Register(versioning.NewCreatePipelineDirectorySkill(writeCfg))

	// Code analysis & quality
	e.skills.Register(lspSkill(e))
	e.skills.Register(formatSkill(e))
	e.skills.Register(lintSkill(e))

	// Consultation
	e.skills.Register(consultSkill(e))
	e.skills.Register(researchDependencyInstallSkill(e))
	e.skills.Register(installDependencyToolingSkill(e))

	for _, skill := range shared.CoordinationSkills(shared.CoordinationSkillConfig{
		Client: shared.CoordinationClient{
			BusProvider:     func() guide.EventBus { return e.bus },
			SourceAgentID:   func() string { return e.id },
			SourceAgentType: func() string { return "engineer" },
			SessionID:       func() string { return e.config.SessionID },
			RegisterPending: func(correlationID string) <-chan *guide.Message { return e.registerPendingConsult(correlationID) },
			ClearPending:    e.clearPendingConsult,
			Timeout:         routeSyncTimeout,
		},
		CurrentTaskID:   func() string { return e.pipelineID },
		CurrentTaskName: func() string { return firstNonEmptyCoordinationName(e.pipelineName, e.pipelineSlug) },
		WorkerType:      func() string { return "engineer" },
	}) {
		e.skills.Register(skill)
	}
	for _, skill := range shared.PipelineProtocolSkills(shared.PipelineProtocolSkillConfig{
		AgentType:      func() string { return "engineer" },
		WorkspaceViews: func() versioning.WorkspaceViewAccess { return e.workspaceViews },
		Route: shared.PipelineProtocolRouteConfig{
			BusProvider: func() guide.EventBus { return e.bus },
			SessionID:   func() string { return e.config.SessionID },
			PublishReroute: func(ctx context.Context, toAgentID, reason, newCorrelationID string) {
				shared.PublishPipelineHandoffReroute(e.bus, e.channels, ctx, "engineer", toAgentID, reason, newCorrelationID)
			},
		},
	}) {
		e.skills.Register(skill)
	}

	// Discovery
	e.skills.Register(discoverProjectToolsSkill(e))
	e.skills.Register(discoverCodePatternsSkill(e))

	// Quality & reporting
	e.skills.Register(auditSkill(e))
	e.skills.Register(reportConfidenceSkill(e))

	// Communication
	e.skills.Register(signalOrchestratorSkill(e))

	// Diagnostics
	e.skills.Register(shared.NewSelfDiagnosticSkill(&engineerDiag{e: e}))

	// Reroute
	e.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   "engineer",
		SessionID: func() string { return e.config.SessionID },
		Publish:   e.publishRerouteRequest,
	}))
}

func firstNonEmptyCoordinationName(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type engineerDiag struct{ e *Engineer }

func (d *engineerDiag) AgentName() string { return "engineer" }
func (d *engineerDiag) SessionID() string { return d.e.config.SessionID }
func (d *engineerDiag) LogsDir() string {
	return shared.LogsDirForAgent(d.e.steering.SessionDir(), "engineer")
}
func (d *engineerDiag) EventLogger() *agentlog.SessionEventLogger { return d.e.steering.EventLogger() }
func (d *engineerDiag) PeerLogsDirs() map[string]string           { return nil }
func (d *engineerDiag) RecoveryHints() []string                   { return nil }

func (d *engineerDiag) AgentSpecificDiagnostics() map[string]any {
	d.e.requestMu.Lock()
	inFlight := len(d.e.requestCancels)
	d.e.requestMu.Unlock()
	return map[string]any{
		"in_flight_requests": inFlight,
		"pipeline_id":        d.e.pipelineID,
	}
}

// =============================================================================
// Consolidated Consultation Skill
// =============================================================================

// consultTargets enumerates valid consultation targets.
var consultTargets = map[string]string{
	"librarian":   "Codebase patterns, existing implementations, and dependency information",
	"archivalist": "Historical context on code decisions and past changes",
	"academic":    "Theoretical guidance, alternative approaches, and research-backed solutions",
}

func consultSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("consult").
		Description("Consult a domain expert agent. Targets: librarian (codebase patterns), archivalist (historical context), academic (theoretical guidance).").
		Domain("consultation").
		Keywords("consult", "librarian", "archivalist", "academic", "knowledge", "patterns", "history", "research").
		Priority(85).
		EnumParam("target", "Agent to consult", []string{"librarian", "archivalist", "academic"}, true).
		StringParam("query", "Consultation question", true).
		StringParam("scope", "Scope for consultation", false).
		StringParam("session_id", "Session identifier", false).
		Usage("Use to gather evidence from domain experts. Consultation is synchronous — you will receive the result before proceeding.").
		Example(`{"target": "librarian", "query": "What patterns exist for error handling in this codebase?", "scope": "backend"}`).
		BestPractice("Consult before implementing, not after. Results are cached — do not re-consult the same agent for the same query.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Target    string `json:"target"`
				Query     string `json:"query"`
				Scope     string `json:"scope"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if _, ok := consultTargets[params.Target]; !ok {
				return nil, fmt.Errorf("invalid target %q: must be librarian, archivalist, or academic", params.Target)
			}
			if params.Query == "" {
				return nil, fmt.Errorf("query is required")
			}
			sessionID := params.SessionID
			if sessionID == "" {
				sessionID = e.config.SessionID
			}
			evidence, err := e.requestConsultation(ctx, params.Target, params.Query, params.Scope, sessionID)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"target":  params.Target,
				"success": evidence.Success,
				"data":    evidence.Data,
			}, nil
		}).
		Build()
}

// =============================================================================
// Consolidated Audit Skill
// =============================================================================

func auditSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("audit").
		Description("Self-audit code for quality issues against readability, correctness, performance, and maintainability standards. Returns a structured verdict with quality score and issues.").
		Domain("quality").
		Keywords("audit", "review", "quality", "check", "validate", "standards", "code").
		Priority(85).
		StringParam("code", "The code or implementation output to audit", true).
		StringParam("criteria", "Acceptance criteria or standards to audit against", false).
		Usage("Call after completing implementation to self-review. If audit fails, fix issues and re-audit.").
		BestPractice("Always audit before reporting completion. If audit fails, fix issues and re-audit.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Code     string `json:"code"`
				Criteria string `json:"criteria"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Code == "" {
				return nil, fmt.Errorf("code is required")
			}
			verdict, err := e.selfAudit(ctx, params.Code, params.Criteria)
			if err != nil {
				return nil, err
			}
			return verdict, nil
		}).
		Build()
}

// =============================================================================
// Communication Skills
// =============================================================================

func signalOrchestratorSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("signal_orchestrator").
		Description("Signal the Orchestrator with progress, questions, or blocks.").
		Domain("communication").
		Keywords("signal", "orchestrator", "progress", "question", "block", "stuck").
		Priority(70).
		StringParam("signal_type", "Type of signal: progress, question, blocked, completed, failed", true).
		StringParam("message", "Signal message content", true).
		StringParam("task_id", "Task identifier", false).
		Usage("Use when you need to communicate with the Orchestrator about task progress or issues.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				SignalType string `json:"signal_type"`
				Message    string `json:"message"`
				TaskID     string `json:"task_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.SignalType == "" || params.Message == "" {
				return nil, fmt.Errorf("signal_type and message are required")
			}
			if e.bus == nil || !e.running {
				return nil, fmt.Errorf("engineer bus is unavailable")
			}
			routeReq := &guide.RouteRequest{
				Input:         fmt.Sprintf("[%s] %s", params.SignalType, params.Message),
				TargetAgentID: "orchestrator",
				FireAndForget: true,
				SessionID:     e.config.SessionID,
			}
			if err := e.PublishRequest(routeReq); err != nil {
				return nil, err
			}
			return map[string]any{"signaled": true, "type": params.SignalType}, nil
		}).
		Build()
}

func reportConfidenceSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("report_confidence").
		Description("Report a multi-dimensional numeric confidence assessment for the current task. Returns composite score, category, and escalation target if warranted.").
		Domain("quality").
		Keywords("confidence", "quality", "assessment", "escalate", "score").
		Priority(80).
		FloatParam("correctness", "Functional correctness score [0.0, 1.0]", true).
		FloatParam("completeness", "Completeness score — all requirements addressed [0.0, 1.0]", true).
		FloatParam("quality", "Code quality score [0.0, 1.0]", true).
		FloatParam("integration", "Integration score — fits cleanly in codebase [0.0, 1.0]", true).
		StringParam("reasoning", "Explanation of scores and rationale", true).
		StringParam("task_id", "Task identifier", false).
		Usage("Call to report your confidence that the implementation is correct and complete. Scores are numeric [0,1] per dimension. Composite is the weighted geometric mean — a single low dimension tanks the composite. If below threshold, escalation is triggered.").
		Example(`{"correctness": 0.9, "completeness": 0.8, "quality": 0.85, "integration": 0.7, "reasoning": "All tests pass, but integration with existing error handler is uncertain."}`).
		BestPractice("Report confidence after every implementation pass. Use honestly — inflated scores lead to silent defects. Low integration score triggers design review.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Correctness  float64 `json:"correctness"`
				Completeness float64 `json:"completeness"`
				Quality      float64 `json:"quality"`
				Integration  float64 `json:"integration"`
				Reasoning    string  `json:"reasoning"`
				TaskID       string  `json:"task_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Reasoning == "" {
				return nil, fmt.Errorf("reasoning is required")
			}

			conf := escalation.NewConfidenceLevel(e.id, "engineer", params.TaskID)
			conf.Correctness = params.Correctness
			conf.Completeness = params.Completeness
			conf.Quality = params.Quality
			conf.Integration = params.Integration
			conf.Reasoning = params.Reasoning

			composite := conf.Composite(escalation.DefaultWeights())
			category := escalation.CategorizeConfidence(composite)

			result := map[string]any{
				"composite": composite,
				"category":  category.String(),
				"dimensions": map[string]float64{
					"correctness":  params.Correctness,
					"completeness": params.Completeness,
					"quality":      params.Quality,
					"integration":  params.Integration,
				},
			}

			// Evaluate escalation via the escalation system
			if e.escalator != nil {
				target, warranted := e.escalator.ReportConfidence(conf, handoff.CategoryPipeline, 0)
				if warranted && target != nil {
					result["escalation"] = map[string]any{
						"warranted":        true,
						"target_agent":     target.TargetAgent,
						"reason":           target.Reason.String(),
						"suggested_action": target.SuggestedAction.String(),
					}
				}
			}

			return result, nil
		}).
		Build()
}

func (e *Engineer) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if e.bus == nil {
		return fmt.Errorf("engineer bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   "engineer",
		SuggestedTarget: suggestedTarget,
		SessionID:       e.config.SessionID,
		ExcludeAgents:   []string{"engineer"},
	}
	return e.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

// =============================================================================
// read_file - Read file contents
// =============================================================================

type readFileParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func readFileSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("read_file").
		Description("Read the contents of a file. Returns file content with optional offset and limit for large files.").
		Domain("filesystem").
		Keywords("read", "file", "content", "view", "cat").
		Priority(100).
		Usage("Use to inspect the current implementation before mutating unfamiliar code or when disk-level context is needed beyond the workspace overlay views.").
		Satisfies("Provides concrete file evidence for planning, implementation, and self-audit.").
		StringParam("path", "Path to the file to read (relative to working directory)", true).
		IntParam("offset", "Line offset to start reading from (0-based)", false).
		IntParam("limit", "Maximum number of lines to read (default: 1000)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params readFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			if e.fileAccess == nil {
				return nil, fmt.Errorf("file access not configured")
			}

			content, err := e.fileAccess.ReadFile(ctx, params.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			lines := strings.Split(string(content), "\n")

			// Apply offset and limit
			offset := params.Offset
			if offset < 0 {
				offset = 0
			}
			if offset >= len(lines) {
				return map[string]any{
					"path":        params.Path,
					"content":     "",
					"total_lines": len(lines),
					"offset":      offset,
					"truncated":   false,
				}, nil
			}

			limit := params.Limit
			if limit <= 0 {
				limit = 1000
			}

			endLine := offset + limit
			truncated := false
			if endLine > len(lines) {
				endLine = len(lines)
			} else {
				truncated = true
			}

			selectedLines := lines[offset:endLine]

			return map[string]any{
				"path":        params.Path,
				"content":     strings.Join(selectedLines, "\n"),
				"total_lines": len(lines),
				"offset":      offset,
				"limit":       limit,
				"truncated":   truncated,
			}, nil
		}).
		Build()
}

func runCommandSkill(e *Engineer) *skills.Skill {
	return shared.NewRunCommandSkill(engineerCommandSkillConfig(e))
}

func runShellScriptSkill(e *Engineer) *skills.Skill {
	return shared.NewRunShellScriptSkill(engineerCommandSkillConfig(e))
}

func engineerCommandSkillConfig(e *Engineer) shared.CommandSkillConfig {
	return shared.CommandSkillConfig{
		AgentType:       "engineer",
		AgentID:         func() string { return e.id },
		SessionID:       func() string { return e.config.SessionID },
		CommandsEnabled: func() bool { return e.config.EngineerConfig.EnableCommands },
		WorkspaceRoot:   e.effectiveWorkingDirectory,
		DefaultTimeout:  func() time.Duration { return e.config.EngineerConfig.CommandTimeout },
		PrepareExecution: func(ctx context.Context, workingDir string) (shared.CommandExecContext, error) {
			execCtx, err := e.commandExecutionContext(ctx, workingDir)
			if err != nil {
				return shared.CommandExecContext{}, err
			}
			return shared.CommandExecContext{
				WorkDir: execCtx.workDir,
				Plan:    execCtx.plan,
			}, nil
		},
		ExecutionBroker:      func() purevfs.ExecutionBroker { return e.executionBroker },
		ExecutionWorkspace:   e.executionWorkspace,
		AllowWorkspaceWrites: true,
		PreAuthorizeCheck: func(command, _ string) error {
			if isCommandBlocked(command, e.config.EngineerConfig.ApprovedCommands) {
				return fmt.Errorf("command is blocked: %s", command)
			}
			return nil
		},
	}
}

func isCommandApproved(command string, approved ApprovedCommandPatterns) bool {
	for _, pattern := range approved.Patterns {
		matched, err := regexp.MatchString(pattern, command)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func isCommandBlocked(command string, approved ApprovedCommandPatterns) bool {
	for _, pattern := range approved.Blocklist {
		matched, err := regexp.MatchString(pattern, command)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func commandHasUnsafeShellSyntax(command string) bool {
	_, unsafe := shared.DetectShellControlOperator(command)
	return unsafe
}

// =============================================================================
// glob - Find files by pattern
// =============================================================================

type globParams struct {
	Pattern string   `json:"pattern"`
	Exclude []string `json:"exclude,omitempty"`
}

func globSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("glob").
		Description("Find files matching a glob pattern. Supports ** for recursive matching.").
		Domain("filesystem").
		Keywords("glob", "find", "files", "pattern", "match").
		Priority(90).
		StringParam("pattern", "Glob pattern (e.g., '**/*.go', 'src/**/*.ts')", true).
		ArrayParam("exclude", "Patterns to exclude (e.g., 'vendor/**', 'node_modules/**')", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params globParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Pattern == "" {
				return nil, fmt.Errorf("pattern is required")
			}

			if e.fileAccess == nil {
				return nil, fmt.Errorf("file access not configured")
			}

			workDir := e.config.EngineerConfig.WorkingDirectory
			if workDir == "" {
				workDir = "."
			}

			matches, err := e.fileAccess.Glob(ctx, workDir, params.Pattern, params.Exclude)
			if err != nil {
				return nil, fmt.Errorf("glob failed: %w", err)
			}

			return map[string]any{
				"pattern": params.Pattern,
				"matches": matches,
				"count":   len(matches),
			}, nil
		}).
		Build()
}

// =============================================================================
// grep - Search file contents
// =============================================================================

type grepParams struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path,omitempty"`
	Include      string `json:"include,omitempty"`
	ContextLines int    `json:"context_lines,omitempty"`
	MaxMatches   int    `json:"max_matches,omitempty"`
}

func grepSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("grep").
		Description("Search file contents using regular expressions. Returns matching lines with optional context.").
		Domain("code").
		Keywords("grep", "search", "find", "regex", "pattern").
		Priority(90).
		StringParam("pattern", "Regular expression pattern to search for", true).
		StringParam("path", "Directory path to search in (default: working directory)", false).
		StringParam("include", "File pattern to include (e.g., '*.go', '*.ts')", false).
		IntParam("context_lines", "Number of context lines to include before/after match", false).
		IntParam("max_matches", "Maximum number of matches to return (default: 100)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params grepParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Pattern == "" {
				return nil, fmt.Errorf("pattern is required")
			}

			if e.fileAccess == nil {
				return nil, fmt.Errorf("file access not configured")
			}

			searchPath := params.Path
			if searchPath == "" {
				searchPath = e.config.EngineerConfig.WorkingDirectory
			}
			if searchPath == "" {
				searchPath = "."
			}

			maxMatches := params.MaxMatches
			if maxMatches <= 0 {
				maxMatches = 100
			}

			faMatches, err := e.fileAccess.Grep(ctx, searchPath, params.Pattern, params.Include, params.ContextLines, maxMatches)
			if err != nil {
				return nil, fmt.Errorf("search failed: %w", err)
			}

			return map[string]any{
				"pattern":   params.Pattern,
				"matches":   faMatches,
				"count":     len(faMatches),
				"truncated": len(faMatches) >= maxMatches,
			}, nil
		}).
		Build()
}

// =============================================================================
// lsp - Language Server Protocol code intelligence
// =============================================================================

type lspInput struct {
	Action    string `json:"action"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	Query     string `json:"query,omitempty"`
	Direction string `json:"direction,omitempty"`
}

func lspSkill(e *Engineer) *skills.Skill {
	type handler = func(context.Context, *lspInput) (any, error)

	locationAction := func(subcommand string) handler {
		return func(ctx context.Context, p *lspInput) (any, error) {
			if strings.TrimSpace(p.File) == "" {
				return nil, fmt.Errorf("file is required for %s", subcommand)
			}
			loc := lspLocation(p.File, p.Line, p.Column)
			output, status := e.runGoplsCommand(ctx, e.effectiveWorkingDirectory(), subcommand, loc)
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		}
	}

	dispatch := map[string]handler{
		"goto_definition": locationAction("definition"),
		"find_references": locationAction("references"),
		"hover":           locationAction("hover"),
		"symbols": func(ctx context.Context, p *lspInput) (any, error) {
			argument := strings.TrimSpace(p.Query)
			if argument == "" {
				argument = strings.TrimSpace(p.File)
			}
			if argument == "" {
				argument = "."
			}
			output, status := e.runGoplsCommand(ctx, e.effectiveWorkingDirectory(), "symbols", argument)
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		},
		"call_hierarchy": func(ctx context.Context, p *lspInput) (any, error) {
			loc := lspLocation(p.File, p.Line, p.Column)
			refs, refsStatus := e.runGoplsCommand(ctx, e.effectiveWorkingDirectory(), "references", loc)
			defn, defStatus := e.runGoplsCommand(ctx, e.effectiveWorkingDirectory(), "definition", loc)
			if refsStatus != "ok" && defStatus != "ok" {
				return map[string]any{"status": "unavailable", "reason": refs}, nil
			}
			return map[string]any{
				"status":     "ok",
				"direction":  p.Direction,
				"references": refs,
				"definition": defn,
			}, nil
		},
	}

	return skills.NewSkill("lsp").
		Description("Query gopls language server for code intelligence.\n\n"+
			"Actions:\n"+
			"- goto_definition: Navigate to symbol definition (params: file, line, column)\n"+
			"- find_references: Find all references to a symbol (params: file, line, column)\n"+
			"- hover: Get type info and documentation for a symbol (params: file, line, column)\n"+
			"- symbols: List symbols in a file or search workspace (params: file or query)\n"+
			"- call_hierarchy: Trace callers/callees of a symbol (params: file, line, column, direction)").
		Domain("code_analysis").
		Keywords("lsp", "symbol", "definition", "references", "hover",
			"outline", "structure", "call hierarchy", "callers", "callees").
		Priority(95).
		Usage("Use to navigate unfamiliar code, confirm symbol relationships, and limit mutation scope before editing.").
		Satisfies("Provides symbol-level evidence for implementation planning and safe code changes.").
		Avoid("Do not guess symbol relationships when the language server can answer them directly.").
		EnumParam("action", "LSP action to execute", []string{
			"goto_definition", "find_references", "hover", "symbols", "call_hierarchy",
		}, true).
		StringParam("file", "File path", false).
		IntParam("line", "Line number (1-based)", false).
		IntParam("column", "Column number (1-based)", false).
		StringParam("query", "Symbol query for workspace search", false).
		StringParam("direction", "Call hierarchy direction: incoming|outgoing|both", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown lsp action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func lspLocation(file string, line, column int) string {
	return fmt.Sprintf("%s:%d:%d", file, line, column)
}

func (e *Engineer) runGoplsCommand(ctx context.Context, workDir, subcommand, arg string) (string, string) {
	if detect.Which("gopls") == "" {
		return "gopls is not installed", "unavailable"
	}
	output, err := e.runToolInDir(ctx, workDir, false, "gopls", subcommand, arg)
	if err != nil {
		return err.Error(), "error"
	}
	return output, "ok"
}

func (e *Engineer) runToolInDir(ctx context.Context, dir string, allowWorkspaceWrite bool, bin string, args ...string) (string, error) {
	authReq := commandapproval.Request{
		Command:       strings.Join(append([]string{bin}, args...), " "),
		WorkingDir:    dir,
		WorkspaceRoot: e.effectiveWorkingDirectory(),
		ToolName:      bin,
	}
	shared.PopulateCommandApprovalScope(ctx, &authReq)
	if _, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), authReq); err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	execCtx, err := e.commandExecutionContext(callCtx, dir)
	if err != nil {
		return "", err
	}
	defer execCtx.cleanup()

	if e.executionBroker == nil || !execCtx.plan.RequiresBroker {
		return "", purevfs.ErrStrictExecutionUnavailable
	}
	runResult, err := e.executionBroker.Run(callCtx, purevfs.BrokerRunRequest{
		Plan:      execCtx.plan,
		Argv:      append([]string{bin}, args...),
		Workspace: e.executionWorkspace(allowWorkspaceWrite),
	})
	if err != nil {
		return "", err
	}
	output := strings.TrimSpace(joinCommandOutput(runResult.Stdout, runResult.Stderr))
	if runResult.ExitCode != 0 {
		return "", fmt.Errorf("%s %s failed: exit code %d\n%s", bin, strings.Join(args, " "), runResult.ExitCode, output)
	}
	return output, nil
}

func joinCommandOutput(stdout, stderr []byte) string {
	if len(stdout) == 0 {
		return string(stderr)
	}
	if len(stderr) == 0 {
		return string(stdout)
	}
	return string(stdout) + "\n" + string(stderr)
}

// =============================================================================
// format - Code formatting
// =============================================================================

// formatterCheckArgs maps formatter IDs to their check-mode arguments.
// $FILE is replaced with the actual file path at invocation time.
var formatterCheckArgs = map[format.FormatterID][]string{
	"goimports":     {"-l", "$FILE"},
	"gofmt":         {"-l", "$FILE"},
	"prettier":      {"--check", "$FILE"},
	"biome":         {"format", "--check", "$FILE"},
	"ruff-format":   {"format", "--check", "$FILE"},
	"black":         {"--check", "$FILE"},
	"rustfmt":       {"--check", "$FILE"},
	"clang-format":  {"--dry-run", "-Werror", "$FILE"},
	"shfmt":         {"-d", "$FILE"},
	"rubocop":       {"--lint", "$FILE"},
	"terraform-fmt": {"fmt", "-check", "$FILE"},
}

func formatSkill(e *Engineer) *skills.Skill {
	type handler = func(context.Context, *formatInput) (any, error)

	selector := format.NewFormatterSelector()

	dispatch := map[string]handler{
		"check": func(ctx context.Context, p *formatInput) (any, error) {
			if p.File == "" {
				return nil, fmt.Errorf("file is required for check")
			}
			root := e.effectiveWorkingDirectory()
			f := selector.SelectFormatter(root, p.File)
			if f == nil {
				return map[string]any{"formatted": true, "reason": "no formatter available for extension"}, nil
			}
			checkArgs, ok := formatterCheckArgs[f.ID]
			if !ok {
				return map[string]any{"formatted": true, "reason": "no check mode for " + string(f.ID)}, nil
			}
			fullPath := resolvePath(root, p.File)
			args := substituteFileArg(checkArgs, fullPath)
			output, err := e.runToolInDir(ctx, root, false, f.Command, args...)
			if err != nil {
				return map[string]any{"formatted": false, "formatter": string(f.ID), "output": err.Error()}, nil
			}
			return map[string]any{"formatted": strings.TrimSpace(output) == "", "formatter": string(f.ID), "output": output}, nil
		},
		"apply": func(ctx context.Context, p *formatInput) (any, error) {
			if p.File == "" {
				return nil, fmt.Errorf("file is required for apply")
			}
			root := e.effectiveWorkingDirectory()
			f := selector.SelectFormatter(root, p.File)
			if f == nil {
				return map[string]any{"success": false, "reason": "no formatter available for extension"}, nil
			}
			fullPath := resolvePath(root, p.File)
			args := substituteFileArg(f.Args, fullPath)
			_, err := e.runToolInDir(ctx, root, true, f.Command, args...)
			if err != nil {
				return map[string]any{"success": false, "formatter": string(f.ID), "error": err.Error()}, nil
			}
			return map[string]any{"success": true, "formatter": string(f.ID)}, nil
		},
		"detect": func(_ context.Context, p *formatInput) (any, error) {
			root := e.effectiveWorkingDirectory()
			all := selector.DetectFormatters(root, p.File)
			detected := make([]map[string]any, 0, len(all))
			for _, d := range all {
				detected = append(detected, map[string]any{
					"formatter":  string(d.FormatterID),
					"confidence": d.Confidence,
					"reason":     d.Reason,
				})
			}
			return map[string]any{"formatters": detected, "count": len(detected)}, nil
		},
	}

	return skills.NewSkill("format").
		Description("Format source files using project-appropriate formatters.\n\n"+
			"Actions:\n"+
			"- check: Verify if a file is formatted (params: file)\n"+
			"- apply: Format a file in-place (params: file)\n"+
			"- detect: List available formatters for a file (params: file)").
		Domain("code_quality").
		Keywords("format", "formatter", "gofmt", "goimports", "prettier", "style", "whitespace").
		Priority(90).
		Usage("Use after mutating code to verify or apply the project-appropriate formatter before final reporting.").
		Satisfies("Provides formatting evidence or performs the formatting step for the changed files.").
		Avoid("Do not rely on it to fix semantic or lint issues.").
		EnumParam("action", "Format action to execute", []string{"check", "apply", "detect"}, true).
		StringParam("file", "File path to format or check", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params formatInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown format action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

type formatInput struct {
	Action string `json:"action"`
	File   string `json:"file,omitempty"`
}

func substituteFileArg(args []string, filePath string) []string {
	result := make([]string, len(args))
	for i, arg := range args {
		if arg == "$FILE" {
			result[i] = filePath
		} else {
			result[i] = arg
		}
	}
	return result
}

// =============================================================================
// lint - Code linting
// =============================================================================

type linterDef struct {
	id        string
	command   string
	args      []string
	fixArgs   []string
	languages []string
	indicator string
}

var knownLinters = []linterDef{
	{id: "golangci-lint", command: "golangci-lint", args: []string{"run", "--out-format", "json"}, fixArgs: []string{"run", "--fix"}, languages: []string{".go"}, indicator: "go.mod"},
	{id: "eslint", command: "eslint", args: []string{"--format", "json"}, fixArgs: []string{"--fix"}, languages: []string{".js", ".jsx", ".ts", ".tsx"}, indicator: "package.json"},
	{id: "ruff", command: "ruff", args: []string{"check", "--output-format", "json"}, fixArgs: []string{"check", "--fix"}, languages: []string{".py"}, indicator: "ruff.toml"},
	{id: "clippy", command: "cargo", args: []string{"clippy", "--message-format", "json"}, fixArgs: []string{"clippy", "--fix"}, languages: []string{".rs"}, indicator: "Cargo.toml"},
}

type lintInput struct {
	Action string   `json:"action"`
	Paths  []string `json:"paths,omitempty"`
	Fix    bool     `json:"fix,omitempty"`
}

type lintIssue struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Rule     string `json:"rule"`
}

func lintSkill(e *Engineer) *skills.Skill {
	type handler = func(context.Context, *lintInput) (any, error)

	dispatch := map[string]handler{
		"run": func(ctx context.Context, p *lintInput) (any, error) {
			root := e.effectiveWorkingDirectory()
			linter := selectLinter(root, p.Paths)
			if linter == nil {
				return map[string]any{"issues": []lintIssue{}, "linter": "none", "reason": "no linter detected"}, nil
			}
			args := linter.args
			if p.Fix {
				args = linter.fixArgs
			}
			args = append(args, p.Paths...)
			output, err := e.runToolInDir(ctx, root, p.Fix, linter.command, args...)
			if err != nil {
				exitErr := &exec.ExitError{}
				if !errors.As(err, &exitErr) {
					return map[string]any{"linter": linter.id, "error": err.Error()}, nil
				}
				// Linters exit non-zero when issues found — extract output from error
				parts := strings.SplitN(err.Error(), "\n", 2)
				if len(parts) > 1 {
					output = parts[1]
				}
			}
			return map[string]any{"linter": linter.id, "output": output, "fix": p.Fix}, nil
		},
		"detect": func(_ context.Context, _ *lintInput) (any, error) {
			root := e.effectiveWorkingDirectory()
			detected := detectLinters(root)
			result := make([]map[string]string, 0, len(detected))
			for _, l := range detected {
				result = append(result, map[string]string{
					"id":        l.id,
					"command":   l.command,
					"indicator": l.indicator,
				})
			}
			return map[string]any{"linters": result, "count": len(result)}, nil
		},
	}

	return skills.NewSkill("lint").
		Description("Run linters on source files to detect code issues.\n\n"+
			"Actions:\n"+
			"- run: Run the appropriate linter on paths (params: paths, fix)\n"+
			"- detect: List available linters for the project").
		Domain("code_quality").
		Keywords("lint", "linter", "golangci-lint", "eslint", "ruff", "clippy", "issues", "warnings").
		Priority(90).
		Usage("Use after implementation changes to gather code-quality evidence on the affected scope before auditing or reporting completion.").
		Satisfies("Produces lint evidence for self-audit, confidence reporting, and review artifacts.").
		Avoid("Do not use detect/run on the whole repo when the task only changed a narrow area unless the task explicitly requires global validation.").
		EnumParam("action", "Lint action to execute", []string{"run", "detect"}, true).
		ArrayParam("paths", "File or directory paths to lint", "string", false).
		BoolParam("fix", "Attempt to auto-fix issues", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lintInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown lint action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func detectLinters(root string) []linterDef {
	var detected []linterDef
	for _, l := range knownLinters {
		if detect.Which(l.command) == "" {
			continue
		}
		if l.indicator != "" && !detect.FileExists(root, l.indicator) {
			continue
		}
		detected = append(detected, l)
	}
	return detected
}

func selectLinter(root string, paths []string) *linterDef {
	detected := detectLinters(root)
	if len(detected) == 0 {
		return nil
	}
	if len(paths) == 0 {
		return &detected[0]
	}
	ext := filepath.Ext(paths[0])
	for i := range detected {
		for _, lang := range detected[i].languages {
			if lang == ext {
				return &detected[i]
			}
		}
	}
	return &detected[0]
}

// =============================================================================
// Helpers
// =============================================================================

func resolvePath(workDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if workDir == "" {
		return path
	}
	return filepath.Join(workDir, path)
}

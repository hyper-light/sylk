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
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/fabric"
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

	// File operations. read_file / glob / grep dropped — workspace_read
	// covers all three via op=read|glob|grep with explicit view
	// selection (disk | global | pipeline). Single reader skill in the
	// catalog, one fewer ambiguity for the LLM to resolve per turn.
	e.skills.Register(bashSkill(e))
	// Phase 2.K / CR-2 refactor: 12 workspace skills collapsed to 3
	// verb-dispatched primitives. Internal deterministic callers and
	// the LLM catalog both route through these three.
	getViews := func() versioning.WorkspaceViewAccess { return e.workspaceViews }
	getFA := func() versioning.FileAccess { return e.fileAccess }
	defaultPipelineID := func() string { return e.pipelineID }
	// prepare_write_context folded into workspace_read(op=prepare_write).
	e.skills.Register(versioning.NewWorkspaceReadSkill(versioning.WorkspaceReadSkillConfig{
		GetViews:          getViews,
		GetFileAccess:     getFA,
		DefaultPipelineID: defaultPipelineID,
	}))
	e.skills.Register(versioning.NewWorkspaceWriteSkill(writeCfg))

	// Code analysis & quality
	e.skills.Register(lspSkill(e))
	e.skills.Register(formatSkill(e))
	e.skills.Register(lintSkill(e))

	// Phase 1 refactor:
	//   - consult removed. Use consult_peer (fabric async) with
	//     target_agent_type="librarian"|"archivalist"|"academic".
	//     When blocking is needed Phase 3 will add sync=true to
	//     consult_peer; until then callers await the
	//     ActionConsultResponse via the ambient envelope.
	// Phase 2.K / GT-4 + GI-5 refactor: research_dependency_install +
	// install_dependency_tooling collapsed into dependency(action=…).
	e.skills.Register(dependencySkill(e))
	e.skills.Register(shared.BuildAskUserClarificationSkill(shared.AskUserClarificationConfig{
		Bus:          e.bus,
		AgentID:      e.id,
		AgentName:    "engineer",
		SessionID:    e.config.SessionID,
		NewMessageID: e.generateMessageID,
	}))

	for _, skill := range shared.CoordinationSkills(shared.CoordinationSkillConfig{
		Client: shared.CoordinationClient{
			BusProvider:     func() guide.EventBus { return e.bus },
			SourceAgentID:   func() string { return e.id },
			SourceAgentType: func() string { return "engineer" },
			SessionID:       func() string { return e.config.SessionID },
			RegisterPending: func(correlationID string) <-chan *guide.Message {
				return e.registerPendingConsult(correlationID).Response
			},
			ClearPending: e.clearPendingConsult,
			Timeout:      routeSyncTimeout,
		},
		CurrentTaskID:   func() string { return e.pipelineID },
		CurrentTaskName: func() string { return firstNonEmptyCoordinationName(e.pipelineName, e.pipelineSlug) },
		WorkerType:      func() string { return "engineer" },
	}) {
		e.skills.Register(skill)
	}
	// Activity Fabric: uniform awareness skills + cross-pipeline primitives.
	for _, skill := range fabric.AwarenessSkills(fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return e.config.SessionID },
		AgentID:        func() string { return e.id },
		AgentType:      func() string { return "engineer" },
	}) {
		e.skills.Register(skill)
	}
	// Phase 5 of SCRIBE_FABRIC.md: recall_my_history.
	for _, skill := range fabric.RecallSkills(fabric.RecallSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return e.config.SessionID },
		AgentID:        func() string { return e.id },
		AgentType:      func() string { return "engineer" },
	}) {
		e.skills.Register(skill)
	}

	// ── Claims skills (unconditional) ──────────────────────────────
	//
	// Every pipeline uses claims. No legacy protocol path.
	boardProvider := func() (*claims.ClaimsBoard, error) {
		if b := e.claimsBoard; b != nil {
			return b, nil
		}
		sid, _ := e.activeSessionID.Load().(string)
		if sid == "" {
			return nil, fmt.Errorf("engineer: no active session ID — agent invoked before session binding")
		}
		board := claims.DefaultSessionBoardRegistry().Lookup(sid)
		if board == nil {
			return nil, fmt.Errorf("engineer: session %q has no claims board registered", sid)
		}
		return board, nil
	}
	inboxProvider := func() *claims.ClaimsInbox { return e.claimsInbox }
	e.skills.Register(claims.QueryClaimsBoardSkill(boardProvider))
	e.skills.Register(claims.QueryBoardSkill(boardProvider, "engineer"))
	e.skills.Register(claims.PostActionSkill(boardProvider, inboxProvider))
	e.skills.Register(claims.SubmitTestamentsSkill(boardProvider))
	e.skills.Register(claims.EvaluateValidationSkill(boardProvider))
	e.skills.Register(claims.UpdateClaimProgressSkill(boardProvider))
	e.skills.Register(claims.InspectClaimConflictsSkill(boardProvider))
	e.skills.Register(claims.TraverseSkill(boardProvider))

	fabricCfg := fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return e.config.SessionID },
		AgentID:        func() string { return e.id },
		AgentType:      func() string { return "engineer" },
	}
	for _, skill := range fabric.ClaimsAwarenessSkills(fabricCfg) {
		e.skills.Register(skill)
	}


	// Discovery
	e.skills.Register(discoverProjectToolsSkill(e))
	e.skills.Register(discoverCodePatternsSkill(e))

	// Quality & reporting
	e.skills.Register(auditSkill(e))
	e.skills.Register(reportConfidenceSkill(e))

	// Phase 1 refactor:
	//   - signal_orchestrator removed. The orchestrator consumes
	//     fabric activities via amplifiers; engineer-emitted work
	//     (format, lint, audit) already surfaces there. For a
	//     blocked-state signal, emit ActionRemediationOpened.

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
		EnumParam("depth", "Research depth for Academic consultations", shared.ResearchDepthEnumValues(), false).
		StringParam("session_id", "Session identifier", false).
		Usage("Use to gather evidence from domain experts whenever the next implementation decision is blocked by missing repository, historical, or external context. Consultation is synchronous — you will receive the result before proceeding.").
		Example(`{"target": "librarian", "query": "What patterns exist for error handling in this codebase?", "scope": "backend"}`).
		BestPractice("Consult before implementing, and re-consult as implementation uncovers new uncertainty. Results are cached — do not repeat the same broad query, but do issue a follow-up consult when the unresolved question, evidence, or candidate approach materially changes.").
		BestPractice("Prefer repeated targeted consults over one large review request. Ask one concrete blocking question at a time.").
		BestPractice("When consulting Academic, re-evaluate depth each time: use `minimal` or `quick` for narrow validation, `standard` for ordinary tradeoff analysis, `deep` for decision-critical design or correctness work, and `comprehensive` for high-stakes or reusable research artifacts.").
		BestPractice("Do not ask Academic for `comprehensive` depth on routine implementation questions; reserve it for questions where broader corroboration or a durable memo materially changes the outcome.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Target    string `json:"target"`
				Query     string `json:"query"`
				Scope     string `json:"scope"`
				Depth     string `json:"depth"`
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
			e.engineerPostClaim(ctx,
				claims.Action{AgentID: "engineer", Type: claims.ActionTypeConsultation},
				engineerConsultClaim(
					"Consult "+params.Target+": "+truncateEngineer(params.Query, 60),
					"Evidence gathering via consultation",
					params.Target,
					[]claims.ClaimScopeEntry{{Kind: "consultation", Key: params.Target}},
					[]*claims.Validation{
						engineerValidation(claims.ValidationTypeReceipt, true, "Consultation succeeded", "evidence.Success == true"),
					},
				),
			)
			evidence, err := e.requestConsultationWithMetadata(
				ctx,
				params.Target,
				params.Query,
				params.Scope,
				sessionID,
				shared.ConsultationMetadataWithResearchDepth(nil, params.Depth),
			)
			if err != nil {
				if errors.Is(err, skills.ErrDelegatedRequested) {
					return nil, err
				}
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
		Description("Run a grounded self-audit: the formatter and linter are executed deterministically on the given files, and the LLM interprets the tool output in context to catch issues the tools cannot see (missed edge cases, hidden invariants, scope creep, subtle correctness defects). Returns a verdict whose pass signal combines both layers — tool findings + reflective analysis — so the score reflects observable evidence, not self-reported confidence.").
		Domain("quality").
		Keywords("audit", "review", "quality", "check", "validate", "standards", "code", "format", "lint").
		Priority(85).
		ArrayParam("files", "Paths (workspace-relative) of files to audit. Format-check and lint run per file.", "string", true).
		StringParam("implementation", "Optional implementation narrative or snippet to review alongside the tool output. Leave empty to audit only against the files on disk.", false).
		StringParam("criteria", "Acceptance criteria or standards to audit against", false).
		Usage("Call after completing implementation to self-review. Format issues drop the quality score; lint errors or reflective defects fail the audit. Fix the surfaced issues and re-audit until pass.").
		BestPractice("Always audit before reporting completion. Pass the exact files you changed — a passing audit on the wrong files is no signal.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Files          []string `json:"files"`
				Implementation string   `json:"implementation"`
				Criteria       string   `json:"criteria"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			files := normalizeAuditFiles(params.Files)
			if len(files) == 0 && strings.TrimSpace(params.Implementation) == "" {
				return nil, fmt.Errorf("at least one file or an implementation narrative is required")
			}
			verdict, err := e.selfAudit(ctx, files, params.Implementation, params.Criteria)
			if err != nil {
				return nil, err
			}
			// Publish self-audit outcome so inspector sees the engineer's
			// own verdict before grading. Evidence now includes
			// deterministic tool outcomes alongside reflective issues.
			if verdict != nil {
				evidence := make([]string, 0, len(verdict.Issues)+len(verdict.ToolFindings))
				for _, finding := range verdict.ToolFindings {
					status := "clean"
					if !finding.Clean {
						status = "issues"
					}
					evidence = append(evidence, fmt.Sprintf("%s[%s] %s: %s", finding.Tool, finding.Backend, finding.File, status))
				}
				for _, issue := range verdict.Issues {
					evidence = append(evidence, issue.Description)
				}
				value := "pass"
				if !verdict.Pass {
					value = "fail"
				}
				shared.AutoPublishAdvisory(ctx, shared.AutoPublishAdvisoryInput{
					SessionID:        e.config.SessionID,
					AuthorAgentID:    e.id,
					AuthorAgentType:  "engineer",
					AuthorPipelineID: e.pipelineID,
					TriggerSkill:     "audit",
					Domain:           "self_audit",
					Value:            value,
					Summary:          fmt.Sprintf("quality_score=%.2f tools=%d issues=%d", verdict.QualityScore, len(verdict.ToolFindings), len(verdict.Issues)),
					Evidence:         evidence,
				})
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

			// Phase 4 refactor: publish engineer confidence as a typed
			// fabric decision so inspector sees the self-assessment in
			// ambient context before grading.
			shared.AutoPublishCommitted(ctx, shared.AutoPublishInput{
				SessionID:        e.config.SessionID,
				AuthorAgentID:    e.id,
				AuthorAgentType:  "engineer",
				AuthorPipelineID: e.pipelineID,
				TriggerSkill:     "report_confidence",
				Domain:           "engineer_confidence",
				Value:            category.String(),
				Scope:            params.TaskID,
				Coordinates:      map[string]string{"composite": fmt.Sprintf("%.3f", composite)},
				Evidence:         []string{params.Reasoning},
			})

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


// Phase: run_command + run_shell_script merged into the unified bash
// skill. Single skill, single script param, dynamic approval policy
// based on script shape (plain command = default/fast-path, compound
// script = exact approval). See shared.NewBashSkill.
func bashSkill(e *Engineer) *skills.Skill {
	return shared.NewBashSkill(engineerCommandSkillConfig(e))
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
// lsp - Language Server Protocol code intelligence
// =============================================================================

// lspSkill wires the engineer's LSP skill with a composite backend:
// gopls (Go-only cross-file accelerator, executed through the
// broker-aware runner so in-flight VFS content is visible) layered
// over a polyglot treesitter backend (reads through versioning.FileAccess
// so VFS overlays appear, works across 20+ grammars). Non-Go files
// skip gopls entirely and resolve through treesitter.
func lspSkill(e *Engineer) *skills.Skill {
	backend := &shared.CompositeBackend{
		Primary: &shared.GoplsBackend{
			Run:        e.runGoplsCommand,
			GetWorkDir: e.effectiveWorkingDirectory,
		},
		Secondary: &shared.TreesitterBackend{
			Tool:          shared.SharedTreeSitter(),
			FileAccess:    func() versioning.FileAccess { return e.fileAccess },
			WorkspaceRoot: e.effectiveWorkingDirectory,
		},
		GoFirst: true,
	}
	return shared.NewLSPSkill(shared.LSPSkillConfig{
		Backend:  backend,
		Priority: 95,
		Domain:   "code_analysis",
		Usage:    "Reach for lsp before grep when you need a symbol's identity (who defines it, who calls it, what type it has). VFS-aware reads mean the answer reflects your in-flight edits, not stale disk state. Polyglot via treesitter; Go files get a gopls-backed accelerator for cross-file lookups.",
	})
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
		return "", shared.WrapApprovalDenied(bin, err)
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
			// Activity Fabric auto-publish: format apply commits a
			// code_style choice. Other agents see the convention
			// in their ambient context.
			shared.AutoPublishCommitted(ctx, shared.AutoPublishInput{
				SessionID:        e.config.SessionID,
				AuthorAgentID:    e.id,
				AuthorAgentType:  "engineer",
				AuthorPipelineID: e.pipelineID,
				TriggerSkill:     "format",
				Domain:           "code_style",
				Value:            string(f.ID),
				Scope:            p.File,
				Evidence:         []string{"formatter applied: " + string(f.ID)},
			})
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
			// Activity Fabric auto-publish: lint commits a
			// linter_backend choice for the project — peers see
			// which linter is in play in their ambient context.
			scope := ""
			if len(p.Paths) > 0 {
				scope = p.Paths[0]
			}
			shared.AutoPublishCommitted(ctx, shared.AutoPublishInput{
				SessionID:        e.config.SessionID,
				AuthorAgentID:    e.id,
				AuthorAgentType:  "engineer",
				AuthorPipelineID: e.pipelineID,
				TriggerSkill:     "lint",
				Domain:           "linter_backend",
				Value:            linter.id,
				Scope:            scope,
				Evidence:         []string{"linter run: " + linter.id},
			})
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

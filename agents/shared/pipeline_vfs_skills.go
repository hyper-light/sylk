package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// PipelineVFSSkillConfig supplies the dependencies the three standalone
// VFS skills need.
type PipelineVFSSkillConfig struct {
	AgentType func() string
	AgentID   func() string
	SessionID func() string
	Board     func() *claims.ClaimsBoard
	Committer func() PipelineCommitter
	Bus       guide.EventBus // Required — agents register skills after Start() wires the bus.
}

// vfsAgentType resolves the agent type from the config or from the
// request context (TaskExecutionContract, then PipelineTaskInput).
func vfsAgentType(ctx context.Context, cfg PipelineVFSSkillConfig) string {
	if contract := TaskExecutionContractFromContext(ctx); contract != nil {
		if agentType := normalizePipelineAgentType(contract.RuntimeAgentType); agentType != "" {
			return agentType
		}
	}
	if cfg.AgentType != nil {
		if agentType := normalizePipelineAgentType(cfg.AgentType()); agentType != "" {
			return agentType
		}
	}
	return ""
}

// vfsAgentID resolves the agent ID from the config or context.
func vfsAgentID(ctx context.Context, cfg PipelineVFSSkillConfig) string {
	if cfg.AgentID != nil {
		if agentID := strings.TrimSpace(cfg.AgentID()); agentID != "" {
			return agentID
		}
	}
	if meta := LogMetaFromContext(ctx); strings.TrimSpace(meta.AgentID) != "" {
		return strings.TrimSpace(meta.AgentID)
	}
	return ""
}

// vfsTerminalUpdateTask builds or retrieves the PipelineTaskInput that
// handoff_to_ot and discard_pipeline use for publishing updates.
func vfsTerminalUpdateTask(ctx context.Context, cfg PipelineVFSSkillConfig) *PipelineTaskInput {
	if task := PipelineTaskFromContext(ctx); task != nil {
		return task
	}
	stream, ok := StreamMetadataFromContext(ctx)
	if !ok {
		return nil
	}
	agentType := vfsAgentType(ctx, cfg)
	taskID := vfsFirstNonEmpty(
		pipelineTaskMetadataString(stream.Metadata, "task_id"),
		pipelineTaskMetadataString(stream.Metadata, "pipeline_id"),
	)
	if taskID == "" || agentType == "" {
		return nil
	}
	contextData := map[string]any{
		"pipeline_stage": vfsFirstNonEmpty(
			pipelineTaskMetadataString(stream.Metadata, "pipeline_stage"),
			pipelineStageForAgents([]string{agentType}),
		),
	}
	if taskSlug := pipelineTaskMetadataString(stream.Metadata, "task_slug"); taskSlug != "" {
		contextData["task_slug"] = taskSlug
	}
	if taskName := pipelineTaskMetadataString(stream.Metadata, "task_name"); taskName != "" {
		contextData["task_name"] = taskName
	}
	sessionID := ""
	if cfg.SessionID != nil {
		sessionID = strings.TrimSpace(cfg.SessionID())
	}
	return &PipelineTaskInput{
		NodeID:        vfsFirstNonEmpty(pipelineTaskMetadataString(stream.Metadata, "node_id"), taskID),
		DAGID:         pipelineTaskMetadataString(stream.Metadata, "dag_id"),
		TaskID:        taskID,
		AgentType:     agentType,
		TargetAgentID: PipelineWorkerCanonicalID(sessionID, taskID, normalizePipelineAgentType(agentType)),
		SessionID:     sessionID,
		Context:       contextData,
	}
}

// vfsTesterVerdict derives a tester verdict string from the claims board.
// Checks whether all claims are accepted.
func vfsTesterVerdict(cfg PipelineVFSSkillConfig) string {
	if cfg.Board == nil {
		return "skip"
	}
	board := cfg.Board()
	if board == nil {
		return "skip"
	}
	if board.AllAccepted() {
		return "pass"
	}
	if board.ReadyForValidation() {
		return "pending"
	}
	return "skip"
}

// vfsFirstNonEmpty returns the first non-blank value.
func vfsFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ────────────────────────────────────────────────────────────────────
// 1. PipelineHandoffOTVFSSkill  (handoff_to_ot)
// ────────────────────────────────────────────────────────────────────

// PipelineHandoffOTVFSSkill creates the standalone handoff_to_ot skill.
// It performs the authoritative VFS merge, publishes the pipeline success
// update, and routes the global review.
func PipelineHandoffOTVFSSkill(cfg PipelineVFSSkillConfig) *skills.Skill {
	return skills.NewSkill("handoff_to_ot").
		Description("Finalize an accepted pipeline and hand the result to Operational Transform for merge into green. Inspector only.").
		Domain("pipeline").
		Keywords("green", "ot", "merge", "accept", "finalize", "pipeline").
		Priority(100).
		Usage("Use immediately after `finalize_pipeline` reports `ready_for_ot: true` / `must_handoff_to_ot: true`, or when the inspector has otherwise already determined the latest audit cycle passed and the pipeline should terminate successfully.").
		Requirement("When `finalize_pipeline` says the pipeline is ready for OT, invoke this immediately as the next terminal protocol action. Do not narrate the handoff instead of calling the tool, and do not continue with other queued work or other pipelines before invoking it.").
		Satisfies("Marks the pipeline as accepted and ready for OT merge, including the required terminal step after a passing `finalize_pipeline` result.").
		StringParam("summary", "Why the pipeline is ready for OT merge", true).
		StringParam("declared_scope", "What this pipeline accepted as done (e.g. 'implemented fn A in pkg/x', 'added tests for case Y'). Surfaced to global audit replicas via the MergeDescriptor's PipelineInspectorCertificate.", false).
		ArrayParam("open_concerns", "Non-blocking issues the pipeline inspector flagged but accepted anyway (e.g. 'minor lint warnings in pkg/y'). Surfaced to global audit replicas on the certificate.", "string", false).
		ArrayParam("evidence_refs", "Criteria, tests, artifacts, and files supporting acceptance", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Summary       string   `json:"summary"`
				DeclaredScope string   `json:"declared_scope"`
				OpenConcerns  []string `json:"open_concerns"`
				EvidenceRefs  []string `json:"evidence_refs"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentType := vfsAgentType(ctx, cfg)
			if agentType != PipelineAgentInspector {
				return nil, fmt.Errorf("handoff_to_ot is only permitted for the pipeline inspector")
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}

			evidenceRefs := normalizeStringList(params.EvidenceRefs)
			openConcerns := normalizeStringList(params.OpenConcerns)
			declaredScope := strings.TrimSpace(params.DeclaredScope)

			// ── VFS merge ──────────────────────────────────────────
			task := vfsTerminalUpdateTask(ctx, cfg)
			pipelineID := ""
			if task != nil {
				pipelineID = strings.TrimSpace(task.TaskID)
			}
			hadDraft := false
			var baseVersion versioning.SemanticVersion
			var mergedVersion versioning.SemanticVersion
			pathCount := 0
			if pipelineID != "" {
				if cfg.Committer == nil {
					return nil, fmt.Errorf("handoff_to_ot requires a configured pipeline committer (inspector misconfiguration)")
				}
				committer := cfg.Committer()
				if committer == nil {
					return nil, fmt.Errorf("handoff_to_ot requires a configured pipeline committer (inspector misconfiguration)")
				}
				cert := versioning.PipelineInspectorCertificate{
					DeclaredScope: declaredScope,
					OpenConcerns:  openConcerns,
					Summary:       summary,
					TesterVerdict: vfsTesterVerdict(cfg),
				}
				result, mergeErr := committer.MergePipelineIntoGreen(ctx, pipelineID, cert)
				if mergeErr != nil {
					// Submit failure testament before returning error.
					if cfg.Board != nil {
						if board := cfg.Board(); board != nil {
							if tErr := board.SubmitTestaments(ctx,
								claims.Action{Type: claims.ActionTypeTestament, AgentID: vfsAgentID(ctx, cfg)},
								[]claims.Testament{{
									AgentID: vfsAgentID(ctx, cfg),
									Summary: "pipeline merge failed: " + mergeErr.Error(),
									Artifacts: []*claims.Artifact{{
										AgentID: vfsAgentID(ctx, cfg), Kind: "error", Reference: mergeErr.Error(),
									}},
								}},
							); tErr != nil {
								slog.Error("handoff_ot_failure_testament_failed", "error", tErr.Error())
								board.RecordNotificationError("handoff_to_ot failure testament: " + tErr.Error())
							}
						}
					}
					return nil, fmt.Errorf("merge pipeline %s into green: %w", pipelineID, mergeErr)
				}
				hadDraft = result.HadDraft
				baseVersion = result.BaseVersion
				mergedVersion = result.MergedVersion
				pathCount = result.PathCount
			}

			// Claims board testament: "pipeline accepted" — submitted
			// AFTER successful merge so the board reflects reality.
			if cfg.Board != nil {
				if board := cfg.Board(); board != nil {
					if err := board.SubmitTestaments(ctx,
						claims.Action{Type: claims.ActionTypeTestament, AgentID: vfsAgentID(ctx, cfg)},
						[]claims.Testament{{
							AgentID: vfsAgentID(ctx, cfg),
							Summary: "pipeline accepted: " + summary,
							Artifacts: []*claims.Artifact{
								{AgentID: vfsAgentID(ctx, cfg), Kind: "merge_result", Reference: mergedVersion.String()},
							},
						}},
					); err != nil {
						slog.Error("handoff_ot_testament_failed", "error", err.Error())
						board.RecordNotificationError("handoff_to_ot testament: " + err.Error())
					}
				}
			}

			// ── Pipeline success update ────────────────────────────
			if task != nil {
				PublishPipelineTaskSuccessUpdate(
					cfg.Bus, agentType, task, summary,
					map[string]any{
						"summary":            summary,
						"evidence_refs":      evidenceRefs,
						"declared_scope":     declaredScope,
						"open_concerns":      openConcerns,
						"had_draft":          hadDraft,
						"base_version":       baseVersion.String(),
						"merged_version":     mergedVersion.String(),
						"paths_merged":       pathCount,
						"checkpoint_version": mergedVersion.String(),
					},
					PipelineTaskAttempt(task),
				)
			}

			// ── Global review route ────────────────────────────────
			if task != nil {
				reviewCtx := GlobalReviewContext{
					TaskID:            strings.TrimSpace(task.TaskID),
					TaskName:          pipelineTaskMetadataString(task.Context, "task_name"),
					TaskSlug:          pipelineTaskMetadataString(task.Context, "task_slug"),
					NodeID:            strings.TrimSpace(task.NodeID),
					DAGID:             strings.TrimSpace(task.DAGID),
					SessionID:         strings.TrimSpace(task.SessionID),
					HadDraft:          hadDraft,
					CheckpointVersion: mergedVersion,
					SessionDir:        pipelineTaskMetadataString(task.Context, "session_dir"),
					AcceptanceSummary: summary,
					EvidenceRefs:      evidenceRefs,
					PipelineAgentType: agentType,
					PlanID:            pipelineTaskMetadataString(task.Context, "plan_id"),
					PlanFilePath:      pipelineTaskMetadataString(task.Context, "plan_file_path"),
					TaskDescription:   strings.TrimSpace(task.Prompt),
					AffectedFiles:     decodeAnyStringList(task.Context["affected_files"]),
				}
				if planSnapshot := pipelineTaskMetadataString(task.Context, "plan_snapshot"); planSnapshot != "" {
					reviewCtx.PlanSnapshot = planSnapshot
				}
				if criteria := pipelineTaskMetadataString(task.Context, "task_criteria_snapshot"); criteria != "" {
					reviewCtx.TaskCriteriaSnapshot = criteria
				}
				reviewCtx.AcceptanceCriteria = decodeAnyStringList(task.Context["acceptance_criteria"])
				reviewCtx.SuccessCriteria = decodeAnyStringList(task.Context["success_criteria"])
				reviewCtx.TestRequirements = decodeAnyStringList(task.Context["test_requirements"])

				req := BuildGlobalReviewRouteRequest(reviewCtx, vfsAgentID(ctx, cfg))
				if req != nil {
					if err := cfg.Bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage("", req)); err != nil {
						slog.Error("handoff_ot_global_review_publish_failed", "error", err.Error())
						if cfg.Board != nil {
							if board := cfg.Board(); board != nil {
								board.RecordNotificationError("handoff_to_ot global review publish: " + err.Error())
							}
						}
					}
				}
			}

			return map[string]any{
				"handoff_to_ot":       true,
				"agent_type":          agentType,
				"evidence_refs":       evidenceRefs,
				"had_draft":           hadDraft,
				"base_version":        baseVersion.String(),
				"merged_version":      mergedVersion.String(),
				"paths_merged":        pathCount,
				"review_candidate_id": "",
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// 2. PipelineDiscardPipelineVFSSkill  (discard_pipeline)
// ────────────────────────────────────────────────────────────────────

// PipelineDiscardPipelineVFSSkill creates the standalone discard_pipeline
// skill. It performs the VFS rollback and publishes the failure update.
func PipelineDiscardPipelineVFSSkill(cfg PipelineVFSSkillConfig) *skills.Skill {
	return skills.NewSkill("discard_pipeline").
		Description("Rollback the active pipeline draft when the work is irrecoverable. Inspector only.").
		Domain("pipeline").
		Keywords("rollback", "discard", "abort", "fail", "pipeline").
		Priority(95).
		Usage("Use when an audit cycle has concluded that the pipeline cannot be salvaged — repeated failures, fundamentally wrong approach, or unrecoverable peer error. Prefer challenge_agent or handoff_next for any path where the work is still potentially recoverable. This is a destructive terminal action.").
		Requirement("Provide a concrete reason explaining why the pipeline must be discarded.").
		Satisfies("Removes the pipeline VFS draft from the session and clears its in-flight modifications.").
		StringParam("reason", "Why the pipeline must be discarded", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentType := vfsAgentType(ctx, cfg)
			if agentType != PipelineAgentInspector {
				return nil, fmt.Errorf("discard_pipeline is only permitted for the pipeline inspector")
			}
			reason := strings.TrimSpace(params.Reason)
			if reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			task := vfsTerminalUpdateTask(ctx, cfg)
			pipelineID := ""
			if task != nil {
				pipelineID = strings.TrimSpace(task.TaskID)
			}
			if pipelineID == "" {
				return nil, fmt.Errorf("discard_pipeline requires a pipeline task id")
			}
			if cfg.Committer == nil {
				return nil, fmt.Errorf("discard_pipeline requires a configured pipeline committer (inspector misconfiguration)")
			}
			committer := cfg.Committer()
			if committer == nil {
				return nil, fmt.Errorf("discard_pipeline requires a configured pipeline committer (inspector misconfiguration)")
			}

			// ── VFS rollback ───────────────────────────────────────
			if err := committer.Rollback(ctx, pipelineID); err != nil {
				return nil, fmt.Errorf("rollback pipeline %s: %w", pipelineID, err)
			}

			// ── Claims board testament ─────────────────────────────
			if cfg.Board != nil {
				if board := cfg.Board(); board != nil {
					if err := board.SubmitTestaments(ctx,
						claims.Action{Type: claims.ActionTypeTestament, AgentID: vfsAgentID(ctx, cfg)},
						[]claims.Testament{{
							AgentID: vfsAgentID(ctx, cfg),
							Summary: "pipeline discarded: " + reason,
							Artifacts: []*claims.Artifact{
								{AgentID: vfsAgentID(ctx, cfg), Kind: "discard_reason", Reference: reason},
								{AgentID: vfsAgentID(ctx, cfg), Kind: "pipeline_id", Reference: pipelineID},
							},
						}},
					); err != nil {
						slog.Error("discard_pipeline_testament_failed", "error", err.Error())
						board.RecordNotificationError("discard_pipeline testament: " + err.Error())
					}
				}
			}

			// ── Failure update ─────────────────────────────────────
			if task != nil {
				PublishPipelineTaskFailureUpdate(
					cfg.Bus, agentType, task, reason,
					PipelineTaskAttempt(task),
				)
			}
			return map[string]any{
				"discard_pipeline": true,
				"agent_type":       agentType,
				"reason":           reason,
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// 3. PipelineFinalizePipelineVFSSkill  (finalize_pipeline)
// ────────────────────────────────────────────────────────────────────

// PipelineFinalizePipelineVFSSkill creates the standalone finalize_pipeline
// skill. It consults the claims board to decide whether the pipeline is
// ready for OT.
func PipelineFinalizePipelineVFSSkill(cfg PipelineVFSSkillConfig) *skills.Skill {
	return skills.NewSkill("finalize_pipeline").
		Description("Run the inspector closure gate that determines whether the pipeline is ready for OT. Consults the claims board for acceptance state. Inspector only.").
		Domain("pipeline").
		Keywords("audit", "challenge", "review", "finalize", "pipeline").
		Priority(100).
		Usage("Invoke this only after you have completed the current inspector audit of the returned implementation and processed any challenge responses needed for that audit. This tool is the closure gate: it checks the claims board to determine whether all claims have been accepted and the pipeline is ready for OT. Do not use it as the default replacement for a targeted challenge or an ordinary top-level handoff.").
		Requirement("Do not use ad hoc prose, local re-grading, or direct reroutes as a substitute for the closure path. If returned work is still unclear, challenge the responsible agent first. Once the current inspector audit is actually settled and any needed challenge responses have been consumed, use `finalize_pipeline` to determine whether the pipeline is ready for OT.").
		Requirement("If this tool returns `ready_for_ot: true` or `must_handoff_to_ot: true`, your next terminal action in this turn must be `handoff_to_ot`. Do not end the turn, summarize the handoff, pick another terminal action, or continue with other queued work or other pipelines first. This completed pipeline takes priority until `handoff_to_ot` is invoked.").
		Satisfies("Runs the pipeline closure gate using claims board state, and when that gate passes, requiring the inspector to call `handoff_to_ot` next.").
		StringParam("summary", "The inspector's current closure judgment and why the pipeline is or is not ready to move toward OT", true).
		ArrayParam("evidence_refs", "Criteria, tests, challenge responses, artifacts, and files the inspector used in the current closure decision", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Summary      string   `json:"summary"`
				EvidenceRefs []string `json:"evidence_refs"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentType := vfsAgentType(ctx, cfg)
			if agentType != PipelineAgentInspector {
				return nil, fmt.Errorf("finalize_pipeline is only permitted for the pipeline inspector")
			}
			summary := strings.TrimSpace(params.Summary)
			if summary == "" {
				return nil, fmt.Errorf("summary is required")
			}

			// ── Claims board consultation ──────────────────────────
			if cfg.Board == nil {
				return nil, fmt.Errorf("finalize_pipeline requires a claims board (inspector misconfiguration)")
			}
			board := cfg.Board()
			if board == nil {
				return nil, fmt.Errorf("finalize_pipeline requires a claims board (inspector misconfiguration)")
			}

			evidenceRefs := normalizeStringList(params.EvidenceRefs)

			// All claims accepted => ready for OT.
			if board.AllAccepted() {
				return map[string]any{
					"finalize_pipeline":         true,
					"ready_for_ot":              true,
					"must_handoff_to_ot":        true,
					"must_invoke_now":           "handoff_to_ot",
					"required_next_action":      "handoff_to_ot",
					"required_next_action_only": true,
					"next_required_action":      "handoff_to_ot",
					"agent_type":                agentType,
					"evidence_refs":             evidenceRefs,
					"board_phase":               string(board.Phase()),
				}, nil
			}

			// Board in validation phase but not all accepted yet.
			if board.Phase() == claims.BoardPhaseValidation {
				return map[string]any{
					"finalize_pipeline":      false,
					"verification_requested": true,
					"agent_type":             agentType,
					"board_phase":            string(board.Phase()),
					"reason":                 "claims are in validation but not all accepted yet",
				}, nil
			}

			// Board still in implementation phase -- not ready.
			if board.ReadyForValidation() {
				return map[string]any{
					"finalize_pipeline":      false,
					"verification_requested": false,
					"agent_type":             agentType,
					"board_phase":            string(board.Phase()),
					"reason":                 "claims are ready for validation but validation has not started",
				}, nil
			}

			return map[string]any{
				"finalize_pipeline":      false,
				"verification_requested": false,
				"agent_type":             agentType,
				"board_phase":            string(board.Phase()),
				"reason":                 "not all claims are testified yet; pipeline is not ready for closure",
			}, nil
		}).
		Build()
}

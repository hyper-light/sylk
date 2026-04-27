package global

import (
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func globalInspectorVisibleSkillNames() []string {
	base := agentShared.AppendMemoryForestVisibleSkillNames([]string{
		// Phase 2.K / GI-C: run_linter + run_type_checker +
		// run_formatter_check + run_security_scan + analyze_complexity +
		// detect_deadlocks + detect_memory_leaks collapsed into
		// run_analyzer(kind=…).
		"run_analyzer",
		// Phase 2.K / CR-2: 12 workspace skills collapsed into 3 verbs.
		"workspace_read",
		"workspace_write",
		"bash",
		"determine_audit_depth",
		// Phase 2.K / GI-D: challenge_global_tester + challenge_architect
		// + challenge_orchestrator collapsed into challenge_global_agent(target=…).
		"challenge_global_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		// Phase 2.K / CR-1: finalize_global_review + accept_checkpoint +
		// discard_checkpoint + commit_to_disk collapsed into
		// global_review(action=…). query_global_review_state stays
		// separate (shared introspection across all 3 agent types).
		"global_review",
		"query_global_review_state",
		// Phase 2.K / GI-1: validate_plan_adherence + cross_reference_changes
		// + grade_layer_quality + load_plan_context collapsed into
		// audit(aspect=…). determine_audit_depth stays named so the
		// tool_loop first-call gate can enforce it by name.
		"audit",
		// Phase 2.K / GI-A: consult_librarian_style +
		// consult_academic_approach + consult_archivalist_context +
		// request_architect_research folded into shared
		// consult_peer(target_agent_type=…). consult_peer is already
		// registered via CrossPipelineSkills.
		// Phase 2.K / GI-B: request_user_clarification + escalate_findings
		// collapsed into shared ask_user_clarification and
		// publish_work_event(kind=artifact, artifact_kind=audit_finding).
		"ask_user_clarification",
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…).
		"dependency",
		// Claims skills: read-only board introspection is Local (visible).
		"query_claims_board",
		"query_board",
		"inspect_claim_conflicts",
		"traverse",
	}, "inspector")
	// Global inspector audits at session level — gets the full
	// fabric awareness + audit skill bundle.
	return fabric.AppendFabricInspectorSkillNames(base)
}

func globalInspectorMutatingSkillNames() []string {
	return agentShared.AppendMemoryForestMutatingSkillNames([]string{
		"bash",
		// Phase 2.K / GI-D: collapsed into challenge_global_agent(target=…).
		"challenge_global_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		// Phase 2.K / CR-1: finalize_global_review + accept_checkpoint +
		// discard_checkpoint + commit_to_disk collapsed into
		// global_review(action=…).
		"global_review",
		"dependency",
		// Phase 2.K / CR-2: 4 write skills collapsed into workspace_write.
		"workspace_write",
		// Phase 2.K / GI-A: consult_librarian_style +
		// consult_academic_approach + consult_archivalist_context +
		// request_architect_research folded into shared
		// consult_peer(target_agent_type=…). consult_peer is the
		// mutating surface now (emits a fabric activity).
		// Phase 2.K / GI-B: request_user_clarification + escalate_findings
		// collapsed into shared ask_user_clarification and
		// publish_work_event(kind=artifact, artifact_kind=audit_finding).
		"ask_user_clarification",
		"reroute_request",
		// Claims skills: mutating operations need LocalWorker policy.
		"post_action",
		"submit_testaments",
		"evaluate_validation",
		"update_claim_progress",
	})
}

func globalInspectorToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "inspector",
		CapabilityScope:  "inspector.global",
		Registry:         registry,
		VisibleByDefault: agentShared.FilterRegisteredSkillNames(registry, globalInspectorVisibleSkillNames()),
		Mutating:         agentShared.FilterRegisteredSkillNames(registry, globalInspectorMutatingSkillNames()),
	})
}

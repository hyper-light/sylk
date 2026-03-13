package shared

import (
	"context"
	"fmt"
	"strings"
)

type taskExecutionCallValidator func(*TaskExecutionContract, *TaskExecutionState, string, map[string]any) error
type taskExecutionCompletionValidator func(*TaskExecutionContract, *TaskExecutionState) error

var (
	taskExecutionCallValidatorSet = map[string]taskExecutionCallValidator{
		"designer":           validateDesignerTaskCall,
		"engineer":           validateEngineerTaskCall,
		"inspector-pipeline": validatePipelineInspectorCallAdapter,
		"tester-pipeline":    validatePipelineTesterCallAdapter,
	}
	taskExecutionCompletionValidatorSet = map[string]taskExecutionCompletionValidator{
		"designer":           validateDesignerCompletion,
		"engineer":           validateEngineerCompletion,
		"inspector-pipeline": validatePipelineInspectorCompletion,
		"tester-pipeline":    validatePipelineTesterCompletion,
	}
	testerDeliverableEvidenceTools = map[TaskExecutionDeliverable][]string{
		TaskDeliverableTestPlan:         {"plan_tests"},
		TaskDeliverableTestArtifact:     {"write_test"},
		TaskDeliverableSuiteExecution:   {"run_test_suite"},
		TaskDeliverableFailureDiagnosis: {"diagnose_failure"},
		TaskDeliverableFailureReport:    {"report_to_engineer", "report_to_designer"},
		TaskDeliverableHarnessPrepared:  {"prepare_test_harness"},
	}
	testerDeliverableRecoveryHints = map[TaskExecutionDeliverable]string{
		TaskDeliverableTestArtifact:     "Call prepare_pipeline_write_context + write_test for the planned coverage.",
		TaskDeliverableSuiteExecution:   "Call run_test_suite for the relevant packages or files.",
		TaskDeliverableFailureDiagnosis: "Call diagnose_failure after gathering failing suite evidence.",
		TaskDeliverableHarnessPrepared:  "Call prepare_test_harness once the required write contexts are prepared.",
		TaskDeliverableTestPlan:         "Call plan_tests before concluding.",
		TaskDeliverableFailureReport:    "Call report_to_engineer or report_to_designer with the diagnosed findings.",
	}
	inspectorDeliverableRecoveryHints = map[TaskExecutionDeliverable]string{
		TaskDeliverableCriteriaContract:   "Call define_criteria or reuse the seeded criteria contract before concluding.",
		TaskDeliverableScopeInspection:    "Inspect the declared workspace scope with read_workspace_file, inspect_workspace_state, or summarize_workspace_state.",
		TaskDeliverablePendingValidation:  "Record pending validation state with get_validation_status or publish an artifact that captures it.",
		TaskDeliverableHandoffContract:    "Call coord_publish_artifact with the explicit requirements, constraints, and downstream handoff guidance.",
		TaskDeliverableCriteriaEvaluation: "Call validate_criteria against the current implementation evidence.",
		TaskDeliverableQualityChecks:      "Run validate_criteria or targeted validation tools to gather quality-check evidence.",
		TaskDeliverableValidationReport:   "Publish the validation findings with coord_publish_artifact before concluding.",
		TaskDeliverableQualityGrade:       "Call grade_task_quality after validation completes.",
	}
	engineerDesignerDeliverableRecoveryHints = map[TaskExecutionDeliverable]string{
		TaskDeliverableReviewContext:    "Inspect the review context with the seeded task ledger, coord_query_view, and relevant workspace reads before resolving it.",
		TaskDeliverableReviewAddressed:  "Address the review with concrete file changes or publish an artifact describing the applied resolution before resolving it.",
		TaskDeliverableReviewResolution: "Call coord_resolve_artifact with the pending review_id after the requested review work is actually addressed.",
		TaskDeliverableRequestedChange:  "Produce the requested file changes with the pipeline write tools before concluding or releasing scope.",
	}
)

func ValidateTaskExecutionCall(ctx context.Context, role, toolName string, input map[string]any) error {
	contract := TaskExecutionContractFromContext(ctx)
	state := TaskExecutionStateFromContext(ctx)
	if contract == nil || state == nil {
		return nil
	}
	validator, ok := taskExecutionCallValidatorSet[role]
	if !ok {
		return nil
	}
	return validator(contract, state, toolName, input)
}

func ValidateTaskExecutionCompletion(ctx context.Context, role string) error {
	contract := TaskExecutionContractFromContext(ctx)
	state := TaskExecutionStateFromContext(ctx)
	if contract == nil || state == nil {
		return nil
	}
	validator, ok := taskExecutionCompletionValidatorSet[role]
	if !ok {
		return nil
	}
	return validator(contract, state)
}

func RecordTaskExecutionSuccess(ctx context.Context, toolName string, input map[string]any) {
	state := TaskExecutionStateFromContext(ctx)
	if state == nil {
		return
	}
	state.recordSuccess(toolName, input)
}

func validateEngineerTaskCall(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	target := extractTaskToolPath(toolName, input)
	operation := contract.OperationForPath(target)
	if !requiresReadBeforeMutation(toolName, operation) {
		return validateEngineerDesignerProgress("engineer", contract, state, toolName)
	}
	if state.sawReadPath(target) {
		return validateEngineerDesignerProgress("engineer", contract, state, toolName)
	}
	return fmt.Errorf("%s targets requested %s path %s; inspect that path before mutating it", toolName, operation, target)
}

func validateDesignerTaskCall(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	target := extractTaskToolPath(toolName, input)
	operation := contract.OperationForPath(target)
	if err := validateDesignerCreatePlanning(state, toolName, operation, target); err != nil {
		return err
	}
	if err := validateDesignerModifyPlanning(state, toolName, operation, target); err != nil {
		return err
	}
	return validateEngineerDesignerProgress("designer", contract, state, toolName)
}

func validateEngineerCompletion(contract *TaskExecutionContract, state *TaskExecutionState) error {
	return validateEngineerDesignerCompletion("engineer", contract, state)
}

func validateDesignerCompletion(contract *TaskExecutionContract, state *TaskExecutionState) error {
	return validateEngineerDesignerCompletion("designer", contract, state)
}

func validatePipelineTesterTaskCall(
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	toolName string,
	input map[string]any,
) error {
	if missing := missingTesterPrerequisites(state, toolName, input); len(missing) > 0 {
		return fmt.Errorf("%s requires %s first", toolName, strings.Join(missing, ", "))
	}
	if err := validatePipelineTesterArtifactPublication(contract, state, toolName, input); err != nil {
		return err
	}
	if !requiresTesterExecutionProgress(toolName) {
		return nil
	}
	unmet := unmetTesterDeliverables(contract, state)
	if len(unmet) == 0 {
		return nil
	}
	return fmt.Errorf("%s is premature; unmet tester deliverables: %s. %s", toolName, formatTesterDeliverables(unmet), testerRecoveryHint(unmet))
}

func validatePipelineTesterCompletion(contract *TaskExecutionContract, state *TaskExecutionState) error {
	unmet := unmetTesterDeliverables(contract, state)
	if len(unmet) == 0 {
		return nil
	}
	return fmt.Errorf("requested tester deliverables are still unmet: %s. %s", formatTesterDeliverables(unmet), testerRecoveryHint(unmet))
}

func validateEngineerDesignerCompletion(role string, contract *TaskExecutionContract, state *TaskExecutionState) error {
	unmet := unmetEngineerDesignerDeliverables(contract, state)
	if len(unmet) == 0 {
		return nil
	}
	return fmt.Errorf("requested %s deliverables are still unmet: %s. %s", role, formatEngineerDesignerDeliverables(unmet), engineerDesignerRecoveryHint(unmet))
}

func validatePipelineInspectorCompletion(contract *TaskExecutionContract, state *TaskExecutionState) error {
	unmet := unmetInspectorDeliverables(contract, state)
	if len(unmet) == 0 {
		return nil
	}
	return fmt.Errorf("requested inspector deliverables are still unmet: %s. %s", formatInspectorDeliverables(unmet), inspectorRecoveryHint(unmet))
}

func validatePipelineTesterPrerequisites(state *TaskExecutionState, toolName string) error {
	if missing := missingTesterPrerequisites(state, toolName, nil); len(missing) > 0 {
		return fmt.Errorf("%s requires %s first", toolName, strings.Join(missing, ", "))
	}
	return nil
}

func validatePipelineInspectorTaskCall(
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	toolName string,
	input map[string]any,
) error {
	if err := validatePipelineInspectorReadOnlyCall(toolName, input); err != nil {
		return err
	}
	if err := validatePreImplementationInspectorCall(contract, toolName); err != nil {
		return err
	}
	if !requiresInspectorExecutionProgress(toolName) {
		return nil
	}
	unmet := unmetInspectorDeliverables(contract, state)
	if len(unmet) == 0 {
		return nil
	}
	return fmt.Errorf("%s is premature; unmet inspector deliverables: %s. %s", toolName, formatInspectorDeliverables(unmet), inspectorRecoveryHint(unmet))
}

func validateEngineerDesignerProgress(
	role string,
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	toolName string,
) error {
	switch toolName {
	case "coord_resolve_artifact":
		unmet := unmetEngineerDesignerDeliverablesExcept(contract, state, TaskDeliverableReviewResolution)
		if len(unmet) == 0 {
			return nil
		}
		return fmt.Errorf("%s is premature; unmet %s deliverables: %s. %s", toolName, role, formatEngineerDesignerDeliverables(unmet), engineerDesignerRecoveryHint(unmet))
	case "coord_release_scope":
		unmet := unmetEngineerDesignerDeliverables(contract, state)
		if len(unmet) == 0 {
			return nil
		}
		return fmt.Errorf("%s is premature; unmet %s deliverables: %s. %s", toolName, role, formatEngineerDesignerDeliverables(unmet), engineerDesignerRecoveryHint(unmet))
	default:
		return nil
	}
}

func requiresReadBeforeMutation(toolName, operation string) bool {
	return mutationToolRequiresRead(toolName) && operationRequiresReadBeforeMutation(operation)
}

func isDesignerMutation(toolName string) bool {
	return toolName == "write_pipeline_file" || toolName == "edit_pipeline_file"
}

func requiresTesterGate(toolName string) bool {
	switch toolName {
	case "prepare_pipeline_write_context", "prepare_test_harness", "write_test", "run_test_suite":
		return true
	default:
		return false
	}
}

func requiresTesterHarness(toolName string) bool {
	switch toolName {
	case "prepare_test_harness", "write_test", "run_test_suite":
		return true
	default:
		return false
	}
}

func requiresTesterRisk(toolName string) bool {
	switch toolName {
	case "write_test", "run_test_suite":
		return true
	default:
		return false
	}
}

func requiresTesterPlan(toolName string) bool {
	switch toolName {
	case "write_test", "run_test_suite":
		return true
	default:
		return false
	}
}

func requiresTesterExecutionProgress(toolName string) bool {
	switch toolName {
	case "report_to_engineer", "report_to_designer", "coord_release_scope":
		return true
	default:
		return false
	}
}

func testerPrerequisites(toolName string) []string {
	requirements := []struct {
		required bool
		name     string
	}{
		{required: requiresTesterGate(toolName), name: "check_inspector_gate"},
		{required: requiresTesterHarness(toolName), name: "detect_test_harness"},
		{required: requiresTesterRisk(toolName), name: "analyze_risk"},
		{required: requiresTesterPlan(toolName), name: "plan_tests"},
	}
	prerequisites := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		if !requirement.required {
			continue
		}
		prerequisites = append(prerequisites, requirement.name)
	}
	return prerequisites
}

func missingTesterPrerequisites(state *TaskExecutionState, toolName string, input map[string]any) []string {
	missing := make([]string, 0, 4)
	for _, prerequisite := range testerPrerequisites(toolName) {
		if testerPrerequisiteSatisfied(state, toolName, prerequisite, input) {
			continue
		}
		missing = append(missing, prerequisite)
	}
	return missing
}

func testerPrerequisiteSatisfied(
	state *TaskExecutionState,
	toolName string,
	prerequisite string,
	input map[string]any,
) bool {
	if state != nil && state.sawAnyTool(prerequisite) {
		return true
	}
	if strings.TrimSpace(toolName) != "write_test" {
		return false
	}
	switch prerequisite {
	case "analyze_risk":
		return writeTestCarriesRiskEvidence(input)
	case "plan_tests":
		return writeTestCarriesPlanEvidence(input)
	default:
		return false
	}
}

func unmetTesterDeliverables(contract *TaskExecutionContract, state *TaskExecutionState) []TaskExecutionDeliverable {
	if contract == nil || state == nil {
		return nil
	}
	unmet := make([]TaskExecutionDeliverable, 0, len(contract.Deliverables))
	for _, deliverable := range contract.Deliverables {
		if testerDeliverableSatisfied(state, deliverable) {
			continue
		}
		unmet = append(unmet, deliverable)
	}
	return unmet
}

func unmetTesterDeliverablesByKinds(
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	filter ...TaskExecutionDeliverable,
) []TaskExecutionDeliverable {
	if contract == nil || state == nil || len(filter) == 0 {
		return nil
	}
	allowed := make(map[TaskExecutionDeliverable]struct{}, len(filter))
	for _, deliverable := range filter {
		allowed[deliverable] = struct{}{}
	}
	unmet := make([]TaskExecutionDeliverable, 0, len(filter))
	for _, deliverable := range contract.Deliverables {
		if _, ok := allowed[deliverable]; !ok {
			continue
		}
		if testerDeliverableSatisfied(state, deliverable) {
			continue
		}
		unmet = append(unmet, deliverable)
	}
	return unmet
}

func testerDeliverableSatisfied(state *TaskExecutionState, deliverable TaskExecutionDeliverable) bool {
	if state == nil {
		return false
	}
	tools, ok := testerDeliverableEvidenceTools[deliverable]
	if !ok {
		return true
	}
	return state.sawAnyTool(tools...)
}

func formatTesterDeliverables(deliverables []TaskExecutionDeliverable) string {
	if len(deliverables) == 0 {
		return ""
	}
	parts := make([]string, 0, len(deliverables))
	for _, deliverable := range deliverables {
		parts = append(parts, describeTaskExecutionDeliverable(deliverable))
	}
	return strings.Join(parts, ", ")
}

func testerRecoveryHint(deliverables []TaskExecutionDeliverable) string {
	if len(deliverables) == 0 {
		return ""
	}
	if hint, ok := testerDeliverableRecoveryHints[deliverables[0]]; ok {
		return hint
	}
	return "Continue the tester protocol until the requested deliverables are satisfied."
}

func validatePipelineTesterArtifactPublication(
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	toolName string,
	input map[string]any,
) error {
	if toolName != "coord_publish_artifact" {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(stringInput(input, "kind")))
	switch kind {
	case "test_plan":
		if state != nil && state.sawAnyTool("plan_tests") {
			return nil
		}
		return fmt.Errorf("coord_publish_artifact kind %q is premature; call plan_tests before publishing a test plan", kind)
	case "verification_result":
		unmet := unmetTesterDeliverablesByKinds(contract, state, TaskDeliverableTestArtifact, TaskDeliverableSuiteExecution)
		if len(unmet) == 0 {
			return nil
		}
		return fmt.Errorf("coord_publish_artifact kind %q is premature; unmet tester deliverables: %s. %s", kind, formatTesterDeliverables(unmet), testerRecoveryHint(unmet))
	default:
		return nil
	}
}

func validatePreImplementationInspectorCall(contract *TaskExecutionContract, toolName string) error {
	if contract == nil || contract.HasImplementationEvidence || !contract.PreImplementation {
		return nil
	}
	if !isImplementationValidationInspectorTool(toolName) {
		return nil
	}
	return fmt.Errorf("%s is not valid during pre-implementation inspect stage; define criteria, inspect declared scope, and report pending validation instead", toolName)
}

func validatePipelineInspectorReadOnlyCall(toolName string, input map[string]any) error {
	if !isInspectorWorkspaceMutationTool(toolName) {
		return nil
	}
	target := extractTaskToolPath(toolName, input)
	if target == "" {
		target = "<workspace>"
	}
	return fmt.Errorf("%s is not permitted for pipeline inspector; inspectors must not implement or mutate %s and should publish coordination artifacts instead", toolName, target)
}

func requiresInspectorExecutionProgress(toolName string) bool {
	switch toolName {
	case "coord_release_scope":
		return true
	default:
		return false
	}
}

func unmetInspectorDeliverables(contract *TaskExecutionContract, state *TaskExecutionState) []TaskExecutionDeliverable {
	if contract == nil || state == nil {
		return nil
	}
	unmet := make([]TaskExecutionDeliverable, 0, len(contract.Deliverables))
	for _, deliverable := range contract.Deliverables {
		if inspectorDeliverableSatisfied(contract, state, deliverable) {
			continue
		}
		unmet = append(unmet, deliverable)
	}
	return unmet
}

func inspectorDeliverableSatisfied(
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	deliverable TaskExecutionDeliverable,
) bool {
	if contract == nil || state == nil {
		return false
	}
	switch deliverable {
	case TaskDeliverableCriteriaContract:
		return contract.CriteriaDefined || state.sawAnyTool("define_criteria")
	case TaskDeliverableScopeInspection:
		return state.sawAnyTool("read_workspace_file", "inspect_workspace_state", "summarize_workspace_state")
	case TaskDeliverablePendingValidation:
		return state.sawAnyTool("get_validation_status", "coord_publish_artifact")
	case TaskDeliverableHandoffContract:
		return state.sawAnyTool("coord_publish_artifact")
	case TaskDeliverableCriteriaEvaluation:
		return state.sawAnyTool("validate_criteria")
	case TaskDeliverableQualityChecks:
		return state.sawAnyTool(inspectorQualityEvidenceTools()...)
	case TaskDeliverableValidationReport:
		return state.sawAnyTool("coord_publish_artifact")
	case TaskDeliverableQualityGrade:
		return state.sawAnyTool("grade_task_quality")
	default:
		return true
	}
}

func inspectorQualityEvidenceTools() []string {
	return []string{
		"validate_criteria",
		"run_linter",
		"run_type_checker",
		"run_formatter_check",
		"run_security_scan",
		"check_coverage",
		"analyze_complexity",
		"detect_race_conditions",
		"detect_deadlocks",
		"detect_memory_leaks",
		"validate_token_usage",
		"validate_accessibility",
		"validate_component_api",
		"validate_design_consistency",
	}
}

func formatInspectorDeliverables(deliverables []TaskExecutionDeliverable) string {
	if len(deliverables) == 0 {
		return ""
	}
	parts := make([]string, 0, len(deliverables))
	for _, deliverable := range deliverables {
		parts = append(parts, describeTaskExecutionDeliverable(deliverable))
	}
	return strings.Join(parts, ", ")
}

func inspectorRecoveryHint(deliverables []TaskExecutionDeliverable) string {
	if len(deliverables) == 0 {
		return ""
	}
	if hint, ok := inspectorDeliverableRecoveryHints[deliverables[0]]; ok {
		return hint
	}
	return "Continue the inspection until the requested deliverables are satisfied."
}

func unmetEngineerDesignerDeliverables(contract *TaskExecutionContract, state *TaskExecutionState) []TaskExecutionDeliverable {
	return unmetEngineerDesignerDeliverablesExcept(contract, state, "")
}

func unmetEngineerDesignerDeliverablesExcept(
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	skip TaskExecutionDeliverable,
) []TaskExecutionDeliverable {
	if contract == nil || state == nil {
		return nil
	}
	unmet := make([]TaskExecutionDeliverable, 0, len(contract.Deliverables))
	for _, deliverable := range contract.Deliverables {
		if deliverable == skip {
			continue
		}
		if engineerDesignerDeliverableSatisfied(contract, state, deliverable) {
			continue
		}
		unmet = append(unmet, deliverable)
	}
	return unmet
}

func engineerDesignerDeliverableSatisfied(
	contract *TaskExecutionContract,
	state *TaskExecutionState,
	deliverable TaskExecutionDeliverable,
) bool {
	if contract == nil || state == nil {
		return false
	}
	switch deliverable {
	case TaskDeliverableReviewIntake:
		return reviewIntakeSatisfied(contract, state)
	case TaskDeliverableReviewContext:
		return state.sawAnyTool(
			"coord_query_view",
			"coord_watch_updates",
			"read_workspace_file",
			"inspect_workspace_state",
			"summarize_workspace_state",
			"diff_workspace_file",
			"component_search",
		)
	case TaskDeliverableReviewAddressed:
		return state.sawAnyTool("coord_publish_artifact", "write_pipeline_file", "edit_pipeline_file", "delete_pipeline_file", "create_pipeline_directory")
	case TaskDeliverableReviewResolution:
		return reviewResolutionSatisfied(contract, state)
	case TaskDeliverableRequestedChange:
		return requestedChangeSatisfied(contract, state)
	default:
		return true
	}
}

func reviewIntakeSatisfied(contract *TaskExecutionContract, state *TaskExecutionState) bool {
	return seededPendingReviewLedger(contract) || (state != nil && state.sawAnyTool("coord_query_view", "coord_watch_updates"))
}

func reviewResolutionSatisfied(contract *TaskExecutionContract, state *TaskExecutionState) bool {
	if contract == nil || state == nil {
		return false
	}
	ids := contract.PendingReviewIDs()
	if len(ids) == 0 {
		return state.sawAnyTool("coord_resolve_artifact")
	}
	return resolvedAllReviews(state, ids)
}

func requestedChangeSatisfied(contract *TaskExecutionContract, state *TaskExecutionState) bool {
	if contract == nil || state == nil {
		return false
	}
	for _, file := range contract.RequestedFiles {
		if !isRequestedMutationOperation(file.Operation) {
			continue
		}
		if state.sawMutatedPath(file.Path) {
			return true
		}
	}
	return false
}

func formatEngineerDesignerDeliverables(deliverables []TaskExecutionDeliverable) string {
	if len(deliverables) == 0 {
		return ""
	}
	parts := make([]string, 0, len(deliverables))
	for _, deliverable := range deliverables {
		parts = append(parts, describeTaskExecutionDeliverable(deliverable))
	}
	return strings.Join(parts, ", ")
}

func engineerDesignerRecoveryHint(deliverables []TaskExecutionDeliverable) string {
	if len(deliverables) == 0 {
		return ""
	}
	if hint, ok := engineerDesignerDeliverableRecoveryHints[deliverables[0]]; ok {
		return hint
	}
	return "Continue the task-scoped review and implementation work until the required deliverables are satisfied."
}

func seededPendingReviewLedger(contract *TaskExecutionContract) bool {
	return contract != nil && contract.Ledger != nil && contract.Ledger.Seeded && contract.HasPendingReviews()
}

func resolvedAllReviews(state *TaskExecutionState, reviewIDs []string) bool {
	for _, reviewID := range reviewIDs {
		if !state.resolvedReview(reviewID) {
			return false
		}
	}
	return true
}

func isRequestedMutationOperation(operation string) bool {
	switch operation {
	case "create", "modify", "delete":
		return true
	default:
		return false
	}
}

func mutationToolRequiresRead(toolName string) bool {
	switch toolName {
	case "edit_pipeline_file", "delete_pipeline_file", "write_pipeline_file":
		return true
	default:
		return false
	}
}

func operationRequiresReadBeforeMutation(operation string) bool {
	return operation == "modify" || operation == "delete"
}

func validateDesignerCreatePlanning(state *TaskExecutionState, toolName, operation, target string) error {
	if toolName != "write_pipeline_file" || operation != "create" || state.sawAnyTool("component_create") {
		return nil
	}
	return fmt.Errorf("requested create path %s requires component_create planning before write_pipeline_file", target)
}

func validateDesignerModifyPlanning(state *TaskExecutionState, toolName, operation, target string) error {
	if !isDesignerMutation(toolName) || operation != "modify" || state.sawAnyTool("component_modify") {
		return nil
	}
	return fmt.Errorf("requested modify path %s requires component_modify planning before %s", target, toolName)
}

func validatePipelineInspectorCallAdapter(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	return validatePipelineInspectorTaskCall(contract, state, toolName, input)
}

func validatePipelineTesterCallAdapter(contract *TaskExecutionContract, state *TaskExecutionState, toolName string, input map[string]any) error {
	return validatePipelineTesterTaskCall(contract, state, toolName, input)
}

func isImplementationValidationInspectorTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "validate_criteria",
		"grade_task_quality",
		"request_correction",
		"request_override",
		"run_linter",
		"run_type_checker",
		"run_formatter_check",
		"run_security_scan",
		"check_coverage",
		"analyze_complexity",
		"detect_race_conditions",
		"detect_deadlocks",
		"detect_memory_leaks",
		"validate_token_usage",
		"validate_accessibility",
		"validate_component_api",
		"validate_design_consistency":
		return true
	default:
		return false
	}
}

func isInspectorWorkspaceMutationTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "prepare_pipeline_write_context",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory":
		return true
	default:
		return false
	}
}

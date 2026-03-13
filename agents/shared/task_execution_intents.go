package shared

import "strings"

type TaskExecutionIntent string

const (
	TaskIntentPlanTests               TaskExecutionIntent = "plan_tests"
	TaskIntentAuthorTests             TaskExecutionIntent = "author_tests"
	TaskIntentRunTests                TaskExecutionIntent = "run_tests"
	TaskIntentDiagnoseFailures        TaskExecutionIntent = "diagnose_failures"
	TaskIntentVerifySpec              TaskExecutionIntent = "verify_spec"
	TaskIntentPrepareHarness          TaskExecutionIntent = "prepare_harness"
	TaskIntentReportFindings          TaskExecutionIntent = "report_findings"
	TaskIntentCoordinate              TaskExecutionIntent = "coordinate"
	TaskIntentSynthesizeContract      TaskExecutionIntent = "synthesize_contract"
	TaskIntentInspectScope            TaskExecutionIntent = "inspect_scope"
	TaskIntentRecordPendingValidation TaskExecutionIntent = "record_pending_validation"
	TaskIntentPublishHandoffContract  TaskExecutionIntent = "publish_handoff_contract"
	TaskIntentValidateImplementation  TaskExecutionIntent = "validate_implementation"
	TaskIntentRunQualityChecks        TaskExecutionIntent = "run_quality_checks"
	TaskIntentPublishValidationReport TaskExecutionIntent = "publish_validation_report"
	TaskIntentGradeQuality            TaskExecutionIntent = "grade_quality"
	TaskIntentConsumeReviews          TaskExecutionIntent = "consume_reviews"
	TaskIntentInspectReviewContext    TaskExecutionIntent = "inspect_review_context"
	TaskIntentAddressReview           TaskExecutionIntent = "address_review"
	TaskIntentResolveReviews          TaskExecutionIntent = "resolve_reviews"
	TaskIntentProduceRequestedChange  TaskExecutionIntent = "produce_requested_change"
)

type TaskExecutionDeliverable string

const (
	TaskDeliverableTestPlan           TaskExecutionDeliverable = "test_plan"
	TaskDeliverableTestArtifact       TaskExecutionDeliverable = "test_artifact"
	TaskDeliverableSuiteExecution     TaskExecutionDeliverable = "suite_execution"
	TaskDeliverableFailureDiagnosis   TaskExecutionDeliverable = "failure_diagnosis"
	TaskDeliverableFailureReport      TaskExecutionDeliverable = "failure_report"
	TaskDeliverableHarnessPrepared    TaskExecutionDeliverable = "harness_prepared"
	TaskDeliverableCriteriaContract   TaskExecutionDeliverable = "criteria_contract"
	TaskDeliverableScopeInspection    TaskExecutionDeliverable = "scope_inspection"
	TaskDeliverablePendingValidation  TaskExecutionDeliverable = "pending_validation_state"
	TaskDeliverableHandoffContract    TaskExecutionDeliverable = "handoff_contract"
	TaskDeliverableCriteriaEvaluation TaskExecutionDeliverable = "criteria_evaluation"
	TaskDeliverableQualityChecks      TaskExecutionDeliverable = "quality_checks"
	TaskDeliverableValidationReport   TaskExecutionDeliverable = "validation_report"
	TaskDeliverableQualityGrade       TaskExecutionDeliverable = "quality_grade"
	TaskDeliverableReviewIntake       TaskExecutionDeliverable = "review_intake"
	TaskDeliverableReviewContext      TaskExecutionDeliverable = "review_context"
	TaskDeliverableReviewAddressed    TaskExecutionDeliverable = "review_addressed"
	TaskDeliverableReviewResolution   TaskExecutionDeliverable = "review_resolution"
	TaskDeliverableRequestedChange    TaskExecutionDeliverable = "requested_change"
)

var (
	taskExecutionIntentDescriptions = map[TaskExecutionIntent]string{
		TaskIntentPlanTests:               "produce a concrete test plan",
		TaskIntentAuthorTests:             "write or update executable test artifacts",
		TaskIntentRunTests:                "execute a relevant test suite",
		TaskIntentDiagnoseFailures:        "diagnose failing test behavior",
		TaskIntentVerifySpec:              "verify behavior against the requested specification",
		TaskIntentPrepareHarness:          "prepare missing test harness/configuration",
		TaskIntentReportFindings:          "report findings to the right downstream worker",
		TaskIntentCoordinate:              "coordinate with peer workers on the shared task surface",
		TaskIntentSynthesizeContract:      "turn the task into explicit implementation criteria and constraints",
		TaskIntentInspectScope:            "inspect the declared scope and current workspace reality",
		TaskIntentRecordPendingValidation: "record that validation remains pending until implementation exists",
		TaskIntentPublishHandoffContract:  "publish the inspection contract for downstream workers",
		TaskIntentValidateImplementation:  "validate the current implementation against the task contract",
		TaskIntentRunQualityChecks:        "run the relevant quality and safety checks",
		TaskIntentPublishValidationReport: "publish reusable validation findings for downstream workers",
		TaskIntentGradeQuality:            "produce a structured quality grade",
		TaskIntentConsumeReviews:          "consume pending task-scoped review requests from the coordination ledger",
		TaskIntentInspectReviewContext:    "inspect the review context, artifacts, and affected workspace surfaces",
		TaskIntentAddressReview:           "address the requested review with concrete work or a published artifact",
		TaskIntentResolveReviews:          "resolve satisfied review requests back into the coordination ledger",
		TaskIntentProduceRequestedChange:  "produce the requested file or implementation changes for this task",
	}
	taskExecutionDeliverableDescriptions = map[TaskExecutionDeliverable]string{
		TaskDeliverableTestPlan:           "a completed test plan",
		TaskDeliverableTestArtifact:       "at least one written test artifact",
		TaskDeliverableSuiteExecution:     "test execution evidence",
		TaskDeliverableFailureDiagnosis:   "a structured failure diagnosis",
		TaskDeliverableFailureReport:      "a downstream failure report",
		TaskDeliverableHarnessPrepared:    "prepared harness/configuration changes",
		TaskDeliverableCriteriaContract:   "an explicit task criteria contract",
		TaskDeliverableScopeInspection:    "declared-scope workspace inspection evidence",
		TaskDeliverablePendingValidation:  "a recorded pending-validation state",
		TaskDeliverableHandoffContract:    "a published handoff contract artifact",
		TaskDeliverableCriteriaEvaluation: "criteria evaluation against the current implementation",
		TaskDeliverableQualityChecks:      "quality and safety check evidence",
		TaskDeliverableValidationReport:   "a published validation findings artifact",
		TaskDeliverableQualityGrade:       "a structured quality grade",
		TaskDeliverableReviewIntake:       "consumed task-scoped review intake",
		TaskDeliverableReviewContext:      "inspected review context and relevant evidence",
		TaskDeliverableReviewAddressed:    "change evidence or an addressing artifact for the review",
		TaskDeliverableReviewResolution:   "resolved pending review requests",
		TaskDeliverableRequestedChange:    "evidence that the requested change work was produced",
	}
	testerIntentDeliverableMap = map[TaskExecutionIntent][]TaskExecutionDeliverable{
		TaskIntentPlanTests:        {TaskDeliverableTestPlan},
		TaskIntentAuthorTests:      {TaskDeliverableTestArtifact},
		TaskIntentRunTests:         {TaskDeliverableSuiteExecution},
		TaskIntentVerifySpec:       {TaskDeliverableSuiteExecution},
		TaskIntentDiagnoseFailures: {TaskDeliverableFailureDiagnosis},
		TaskIntentPrepareHarness:   {TaskDeliverableHarnessPrepared},
		TaskIntentReportFindings:   {TaskDeliverableFailureReport},
	}
	inspectorIntentDeliverableMap = map[TaskExecutionIntent][]TaskExecutionDeliverable{
		TaskIntentSynthesizeContract:      {TaskDeliverableCriteriaContract},
		TaskIntentInspectScope:            {TaskDeliverableScopeInspection},
		TaskIntentRecordPendingValidation: {TaskDeliverablePendingValidation},
		TaskIntentPublishHandoffContract:  {TaskDeliverableHandoffContract},
		TaskIntentValidateImplementation:  {TaskDeliverableCriteriaEvaluation},
		TaskIntentRunQualityChecks:        {TaskDeliverableQualityChecks},
		TaskIntentPublishValidationReport: {TaskDeliverableValidationReport},
		TaskIntentGradeQuality:            {TaskDeliverableQualityGrade},
	}
	engineerDesignerIntentDeliverableMap = map[TaskExecutionIntent][]TaskExecutionDeliverable{
		TaskIntentConsumeReviews:         {TaskDeliverableReviewIntake},
		TaskIntentInspectReviewContext:   {TaskDeliverableReviewContext},
		TaskIntentAddressReview:          {TaskDeliverableReviewAddressed},
		TaskIntentResolveReviews:         {TaskDeliverableReviewResolution},
		TaskIntentProduceRequestedChange: {TaskDeliverableRequestedChange},
	}
)

func classifyTaskExecutionIntents(task *PipelineTaskInput, contract *TaskExecutionContract) []TaskExecutionIntent {
	if task == nil {
		return nil
	}
	switch taskExecutionRole(task, contract) {
	case "tester-pipeline":
		return classifyTesterTaskExecutionIntents(task)
	case "inspector-pipeline":
		return classifyInspectorTaskExecutionIntents(contract)
	case "engineer", "designer":
		return classifyEngineerDesignerTaskExecutionIntents(contract)
	default:
		return nil
	}
}

func classifyTesterTaskExecutionIntents(task *PipelineTaskInput) []TaskExecutionIntent {
	text := normalizedTaskExecutionText(task)
	intents := []TaskExecutionIntent{TaskIntentCoordinate}
	if testerPlanOnlyRequest(text) {
		return uniqueTaskExecutionIntents(append(intents, TaskIntentPlanTests))
	}
	if testerNeedsAuthoring(task, text) {
		intents = append(intents, TaskIntentPlanTests, TaskIntentAuthorTests)
	}
	if testerNeedsExecution(text) {
		intents = append(intents, TaskIntentRunTests, TaskIntentVerifySpec)
	}
	if testerNeedsDiagnosis(text) {
		intents = append(intents, TaskIntentRunTests, TaskIntentDiagnoseFailures)
	}
	if testerNeedsHarness(text) {
		intents = append(intents, TaskIntentPrepareHarness)
	}
	if testerNeedsReport(text) {
		intents = append(intents, TaskIntentReportFindings)
	}
	return uniqueTaskExecutionIntents(append(intents, defaultTesterTaskExecutionIntents(task, intents)...))
}

func classifyInspectorTaskExecutionIntents(contract *TaskExecutionContract) []TaskExecutionIntent {
	intents := []TaskExecutionIntent{TaskIntentCoordinate}
	if inspectorNeedsContractSynthesis(contract) {
		intents = append(intents,
			TaskIntentSynthesizeContract,
			TaskIntentInspectScope,
			TaskIntentRecordPendingValidation,
			TaskIntentPublishHandoffContract,
		)
		return uniqueTaskExecutionIntents(intents)
	}
	intents = append(intents,
		TaskIntentValidateImplementation,
		TaskIntentRunQualityChecks,
		TaskIntentPublishValidationReport,
		TaskIntentGradeQuality,
	)
	return uniqueTaskExecutionIntents(intents)
}

func classifyEngineerDesignerTaskExecutionIntents(contract *TaskExecutionContract) []TaskExecutionIntent {
	intents := []TaskExecutionIntent{TaskIntentCoordinate}
	if contract != nil && contract.HasPendingReviews() {
		intents = append(intents,
			TaskIntentConsumeReviews,
			TaskIntentInspectReviewContext,
			TaskIntentAddressReview,
			TaskIntentResolveReviews,
		)
	}
	if contract != nil && contract.HasRequestedWriteOperations() {
		intents = append(intents, TaskIntentProduceRequestedChange)
	}
	return uniqueTaskExecutionIntents(intents)
}

func buildTaskExecutionDeliverables(
	task *PipelineTaskInput,
	contract *TaskExecutionContract,
	intents []TaskExecutionIntent,
) []TaskExecutionDeliverable {
	if task == nil {
		return nil
	}
	switch taskExecutionRole(task, contract) {
	case "tester-pipeline":
		return testerTaskExecutionDeliverables(intents)
	case "inspector-pipeline":
		return inspectorTaskExecutionDeliverables(contract, intents)
	case "engineer", "designer":
		return engineerDesignerTaskExecutionDeliverables(intents)
	default:
		return nil
	}
}

func testerTaskExecutionDeliverables(intents []TaskExecutionIntent) []TaskExecutionDeliverable {
	deliverables := make([]TaskExecutionDeliverable, 0, len(intents))
	for _, intent := range intents {
		deliverables = append(deliverables, testerIntentDeliverableMap[intent]...)
	}
	return uniqueTaskExecutionDeliverables(deliverables)
}

func inspectorTaskExecutionDeliverables(_ *TaskExecutionContract, intents []TaskExecutionIntent) []TaskExecutionDeliverable {
	deliverables := make([]TaskExecutionDeliverable, 0, len(intents))
	for _, intent := range intents {
		deliverables = append(deliverables, inspectorIntentDeliverableMap[intent]...)
	}
	return uniqueTaskExecutionDeliverables(deliverables)
}

func engineerDesignerTaskExecutionDeliverables(intents []TaskExecutionIntent) []TaskExecutionDeliverable {
	deliverables := make([]TaskExecutionDeliverable, 0, len(intents))
	for _, intent := range intents {
		deliverables = append(deliverables, engineerDesignerIntentDeliverableMap[intent]...)
	}
	return uniqueTaskExecutionDeliverables(deliverables)
}

func describeTaskExecutionIntent(intent TaskExecutionIntent) string {
	if description, ok := taskExecutionIntentDescriptions[intent]; ok {
		return description
	}
	return strings.ReplaceAll(string(intent), "_", " ")
}

func describeTaskExecutionDeliverable(deliverable TaskExecutionDeliverable) string {
	if description, ok := taskExecutionDeliverableDescriptions[deliverable]; ok {
		return description
	}
	return strings.ReplaceAll(string(deliverable), "_", " ")
}

func normalizedTaskExecutionText(task *PipelineTaskInput) string {
	if task == nil {
		return ""
	}
	parts := []string{
		task.Prompt,
		strings.Join(extractAffectedPaths(task.Context), " "),
		strings.Join(decodeStringList(task.Context, "test_requirements"), " "),
		strings.Join(decodeStringList(task.Context, "success_criteria"), " "),
		strings.Join(decodeAcceptanceCriteria(task.Context), " "),
		strings.Join(decodeWorkspacePaths(task.Context, "test_surface"), " "),
		strings.Join(decodeWorkspacePaths(task.Context, "write_set"), " "),
	}
	for _, agentType := range testerWorkerPacketTypes(task) {
		if packet := decodeWorkerPacket(task.Context, agentType); len(packet) > 0 {
			parts = append(parts,
				stringValue(packet["objective"]),
				strings.Join(decodeAnyStringList(packet["responsibilities"]), " "),
				strings.Join(decodeAnyStringList(packet["guidelines"]), " "),
				strings.Join(decodeAnyStringList(packet["test_requirements"]), " "),
			)
		}
	}
	if intent := decodeMap(task.Context, "task_intent"); len(intent) > 0 {
		parts = append(parts,
			stringValue(intent["task_name"]),
			stringValue(intent["why_this_task_exists"]),
			stringValue(intent["user_visible_outcome"]),
		)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func taskExecutionRole(task *PipelineTaskInput, contract *TaskExecutionContract) string {
	if contract != nil && strings.TrimSpace(contract.WorkerType) != "" {
		return strings.TrimSpace(contract.WorkerType)
	}
	if task == nil {
		return ""
	}
	return strings.TrimSpace(task.AgentType)
}

func defaultTesterTaskExecutionIntents(task *PipelineTaskInput, intents []TaskExecutionIntent) []TaskExecutionIntent {
	if testerHasPrimaryIntent(intents) {
		return nil
	}
	if testerStageDefaultsToAuthoring(task) && !testerExplicitlySkipsAuthoring(normalizedTaskExecutionText(task)) {
		return []TaskExecutionIntent{TaskIntentPlanTests, TaskIntentAuthorTests}
	}
	if defaults := defaultTesterStageIntents(pipelineStageFromTask(task)); len(defaults) > 0 {
		return defaults
	}
	if testerHasDeclaredTestRequirements(task) {
		return []TaskExecutionIntent{TaskIntentPlanTests, TaskIntentAuthorTests}
	}
	return []TaskExecutionIntent{TaskIntentPlanTests}
}

func testerPlanOnlyRequest(text string) bool {
	if !containsAny(text,
		"plan tests",
		"plan test coverage",
		"testing plan",
		"test plan",
		"plan coverage",
		"test coverage",
		"coverage plan",
	) {
		return false
	}
	if containsAny(text,
		"plan only",
		"do not write",
		"don't write",
		"do not execute",
		"don't execute",
		"do not run",
		"don't run",
	) {
		return true
	}
	return !containsAny(text,
		"add tests",
		"write tests",
		"create tests",
		"run tests",
		"execute tests",
		"diagnose",
		"investigate",
		"verify",
		"validate",
	)
}

func testerNeedsAuthoring(task *PipelineTaskInput, text string) bool {
	if testerExplicitlySkipsAuthoring(text) {
		return false
	}
	if containsAny(text,
		"add tests",
		"write tests",
		"create tests",
		"new tests",
		"spec-driven tests",
		"specification-driven tests",
		"red-phase",
		"failing tests",
		"test requirements",
	) {
		return true
	}
	if testerHasDeclaredTestRequirements(task) {
		return true
	}
	if testerExplicitExecutionOnlyRequest(text) {
		return false
	}
	return testerStageDefaultsToAuthoring(task) && testerHasAuthoringScope(task)
}

func testerNeedsExecution(text string) bool {
	if containsAny(text,
		"run tests",
		"execute tests",
		"test suite",
		"verify implementation",
		"verify behavior",
		"validate implementation",
		"validate behavior",
		"confirm behavior",
		"reproduce",
	) {
		return true
	}
	return false
}

func testerNeedsDiagnosis(text string) bool {
	return containsAny(text,
		"diagnose",
		"investigate",
		"debug",
		"root cause",
		"why does",
		"why is",
		"why are",
	)
}

func testerNeedsHarness(text string) bool {
	return containsAny(text,
		"prepare harness",
		"test harness",
		"pytest config",
		"vitest config",
		"jest config",
		"test setup",
	)
}

func testerNeedsReport(text string) bool {
	return containsAny(text,
		"report findings",
		"hand off findings",
		"send findings",
		"report failure",
	)
}

func testerWorkerPacketTypes(task *PipelineTaskInput) []string {
	if task == nil {
		return nil
	}
	candidates := []string{
		strings.TrimSpace(task.AgentType),
		strings.TrimSpace(PipelineWorkerType(task)),
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func testerHasDeclaredTestRequirements(task *PipelineTaskInput) bool {
	if task == nil {
		return false
	}
	if len(decodeStringList(task.Context, "test_requirements")) > 0 {
		return true
	}
	for _, agentType := range testerWorkerPacketTypes(task) {
		packet := decodeWorkerPacket(task.Context, agentType)
		if len(decodeAnyStringList(packet["test_requirements"])) > 0 {
			return true
		}
	}
	return false
}

func testerStageDefaultsToAuthoring(task *PipelineTaskInput) bool {
	switch strings.ToLower(strings.TrimSpace(pipelineStageFromTask(task))) {
	case "test", "tester", "create_tests", "creating_tests":
		return true
	default:
		return false
	}
}

func testerHasAuthoringScope(task *PipelineTaskInput) bool {
	if task == nil {
		return false
	}
	if len(decodeRequestedFileOperations(task.Context)) > 0 {
		return true
	}
	if len(extractAffectedPaths(task.Context)) > 0 {
		return true
	}
	if len(decodeWorkspacePaths(task.Context, "write_set")) > 0 {
		return true
	}
	if len(decodeWorkspacePaths(task.Context, "test_surface")) > 0 {
		return true
	}
	for _, agentType := range testerWorkerPacketTypes(task) {
		packet := decodeWorkerPacket(task.Context, agentType)
		if len(packet) == 0 {
			continue
		}
		if len(decodeAnyStringList(packet["write_set"])) > 0 || len(decodeAnyStringList(packet["read_set"])) > 0 {
			return true
		}
		if strings.TrimSpace(stringValue(packet["objective"])) != "" {
			return true
		}
	}
	return strings.TrimSpace(task.Prompt) != ""
}

func testerExplicitlySkipsAuthoring(text string) bool {
	return containsAny(text,
		"plan only",
		"do not write",
		"don't write",
		"do not author",
		"don't author",
		"no new tests",
		"without writing tests",
	)
}

func testerExplicitExecutionOnlyRequest(text string) bool {
	if !containsAny(text,
		"run tests",
		"execute tests",
		"only run tests",
		"verify implementation",
		"validate implementation",
		"investigate",
		"diagnose",
		"debug",
	) {
		return false
	}
	return !containsAny(text,
		"add tests",
		"write tests",
		"create tests",
		"new tests",
		"failing tests",
		"spec-driven tests",
		"specification-driven tests",
	)
}

func inspectorNeedsContractSynthesis(contract *TaskExecutionContract) bool {
	if contract == nil {
		return true
	}
	return !contract.HasImplementationEvidence
}

func hasTaskExecutionIntent(intents []TaskExecutionIntent, want TaskExecutionIntent) bool {
	for _, intent := range intents {
		if intent == want {
			return true
		}
	}
	return false
}

func testerHasPrimaryIntent(intents []TaskExecutionIntent) bool {
	return hasTaskExecutionIntent(intents, TaskIntentPlanTests) ||
		hasTaskExecutionIntent(intents, TaskIntentAuthorTests) ||
		hasTaskExecutionIntent(intents, TaskIntentRunTests) ||
		hasTaskExecutionIntent(intents, TaskIntentDiagnoseFailures)
}

func defaultTesterStageIntents(stage string) []TaskExecutionIntent {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "test", "tester", "create_tests", "creating_tests":
		return []TaskExecutionIntent{TaskIntentPlanTests, TaskIntentAuthorTests}
	case "validate", "validating":
		return []TaskExecutionIntent{TaskIntentRunTests, TaskIntentVerifySpec}
	default:
		return nil
	}
}

func uniqueTaskExecutionIntents(intents []TaskExecutionIntent) []TaskExecutionIntent {
	if len(intents) == 0 {
		return nil
	}
	out := make([]TaskExecutionIntent, 0, len(intents))
	seen := make(map[TaskExecutionIntent]struct{}, len(intents))
	for _, intent := range intents {
		if intent == "" {
			continue
		}
		if _, ok := seen[intent]; ok {
			continue
		}
		seen[intent] = struct{}{}
		out = append(out, intent)
	}
	return out
}

func uniqueTaskExecutionDeliverables(deliverables []TaskExecutionDeliverable) []TaskExecutionDeliverable {
	if len(deliverables) == 0 {
		return nil
	}
	out := make([]TaskExecutionDeliverable, 0, len(deliverables))
	seen := make(map[TaskExecutionDeliverable]struct{}, len(deliverables))
	for _, deliverable := range deliverables {
		if deliverable == "" {
			continue
		}
		if _, ok := seen[deliverable]; ok {
			continue
		}
		seen[deliverable] = struct{}{}
		out = append(out, deliverable)
	}
	return out
}

func containsAny(text string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}

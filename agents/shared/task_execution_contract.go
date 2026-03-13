package shared

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/toolruntime"
)

type RequestedFileOperation struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

type TaskExecutionContract struct {
	TaskID                    string
	Stage                     string
	WorkerType                string
	RequestedFiles            []RequestedFileOperation
	ReadSet                   []string
	WriteSet                  []string
	TestSurface               []string
	Ledger                    *TaskExecutionLedger
	Intents                   []TaskExecutionIntent
	Deliverables              []TaskExecutionDeliverable
	PreImplementation         bool
	HasImplementationEvidence bool
	CriteriaDefined           bool
	ValidationResultAvailable bool
}

type TaskExecutionState struct {
	mu              sync.RWMutex
	successfulTools map[string]int
	readPaths       map[string]struct{}
	mutatedPaths    map[string]struct{}
	resolvedReviews map[string]struct{}
}

type taskExecutionContractKey struct{}
type taskExecutionStateKey struct{}

type workspaceSurface struct {
	ReadSet     []string `json:"read_set,omitempty"`
	WriteSet    []string `json:"write_set,omitempty"`
	TestSurface []string `json:"test_surface,omitempty"`
}

func BuildTaskExecutionContract(task *PipelineTaskInput) *TaskExecutionContract {
	if task == nil {
		return nil
	}
	stage := pipelineStageFromTask(task)
	requestedFiles := decodeRequestedFileOperations(task.Context)
	workspace := decodeWorkspaceSurface(task.Context)
	contract := &TaskExecutionContract{
		TaskID:                    strings.TrimSpace(task.TaskID),
		Stage:                     stage,
		WorkerType:                PipelineWorkerType(task),
		RequestedFiles:            requestedFiles,
		ReadSet:                   normalizeTaskPaths(workspace.ReadSet),
		WriteSet:                  normalizeTaskPaths(workspace.WriteSet),
		TestSurface:               normalizeTaskPaths(workspace.TestSurface),
		HasImplementationEvidence: taskHasImplementationEvidence(task),
	}
	contract.Ledger = buildTaskExecutionLedger(task, contract.WorkerType)
	return RebuildTaskExecutionContract(task, contract)
}

func RebuildTaskExecutionContract(task *PipelineTaskInput, contract *TaskExecutionContract) *TaskExecutionContract {
	if task == nil || contract == nil {
		return contract
	}
	contract.PreImplementation = strings.EqualFold(strings.TrimSpace(contract.Stage), "inspect") &&
		!contract.HasImplementationEvidence
	contract.Intents = classifyTaskExecutionIntents(task, contract)
	contract.Deliverables = buildTaskExecutionDeliverables(task, contract, contract.Intents)
	return contract
}

func AppendTaskExecutionGuidance(base string, contract *TaskExecutionContract, role string) string {
	guidance := strings.TrimSpace(taskExecutionGuidance(contract, role))
	if guidance == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return guidance
	}
	return strings.TrimSpace(base) + "\n\n---\n\n" + guidance
}

func TaskToolSurface(runtime *toolruntime.Runtime, contract *TaskExecutionContract, role string) (toolruntime.Surface, error) {
	if runtime == nil || contract == nil {
		return runtime, nil
	}
	names := contract.RequiredToolNames(role)
	if len(names) == 0 {
		return runtime, nil
	}
	return runtime.ScopedView(names...)
}

func WithTaskExecutionContract(ctx context.Context, contract *TaskExecutionContract) context.Context {
	if contract == nil {
		return ctx
	}
	return context.WithValue(ctx, taskExecutionContractKey{}, contract)
}

func TaskExecutionContractFromContext(ctx context.Context) *TaskExecutionContract {
	if ctx == nil {
		return nil
	}
	contract, _ := ctx.Value(taskExecutionContractKey{}).(*TaskExecutionContract)
	return contract
}

func WithTaskExecutionState(ctx context.Context, state *TaskExecutionState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, taskExecutionStateKey{}, state)
}

func TaskExecutionStateFromContext(ctx context.Context) *TaskExecutionState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(taskExecutionStateKey{}).(*TaskExecutionState)
	return state
}

func NewTaskExecutionState() *TaskExecutionState {
	return &TaskExecutionState{
		successfulTools: make(map[string]int),
		readPaths:       make(map[string]struct{}),
		mutatedPaths:    make(map[string]struct{}),
		resolvedReviews: make(map[string]struct{}),
	}
}

func (c *TaskExecutionContract) RequiredToolNames(role string) []string {
	names := baseRequiredToolNames(role)
	switch role {
	case "engineer":
		names = append(names,
			"prepare_pipeline_write_context",
			"write_pipeline_file",
			"edit_pipeline_file",
			"delete_pipeline_file",
			"create_pipeline_directory",
		)
	case "designer":
		names = append(names,
			"prepare_pipeline_write_context",
			"write_pipeline_file",
			"edit_pipeline_file",
			"request_engineer_review",
			"request_inspector_check",
			"request_tester_validation",
			"report_to_engineer",
		)
		names = append(names, designerPlanningTools(c)...)
	case "tester-pipeline":
		names = append(names,
			"check_inspector_gate",
			"detect_test_harness",
			"prepare_test_harness",
			"analyze_risk",
			"plan_tests",
			"prepare_pipeline_write_context",
			"write_test",
			"run_test_suite",
		)
	case "inspector-pipeline":
		names = append(names,
			"define_criteria",
			"get_validation_status",
			"read_workspace_file",
			"inspect_workspace_state",
			"summarize_workspace_state",
			"coord_publish_artifact",
		)
		if c != nil && c.HasImplementationEvidence {
			names = append(names, inspectorValidationTools()...)
		}
	}
	return uniqueSorted(names)
}

func (c *TaskExecutionContract) OperationForPath(target string) string {
	normalized := normalizeTaskPath(target)
	if normalized == "" {
		return ""
	}
	for _, file := range c.RequestedFiles {
		if normalizeTaskPath(file.Path) == normalized {
			return strings.ToLower(strings.TrimSpace(file.Operation))
		}
	}
	return ""
}

func (c *TaskExecutionContract) RequiresDeliverable(want TaskExecutionDeliverable) bool {
	if c == nil {
		return false
	}
	for _, deliverable := range c.Deliverables {
		if deliverable == want {
			return true
		}
	}
	return false
}

func (c *TaskExecutionContract) HasPendingReviews() bool {
	return c != nil && c.Ledger != nil && len(c.Ledger.PendingReviews) > 0
}

func (c *TaskExecutionContract) PendingReviewIDs() []string {
	if c == nil || c.Ledger == nil || len(c.Ledger.PendingReviews) == 0 {
		return nil
	}
	ids := make([]string, 0, len(c.Ledger.PendingReviews))
	for _, review := range c.Ledger.PendingReviews {
		if trimmed := strings.TrimSpace(review.ID); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}

func (c *TaskExecutionContract) HasRequestedWriteOperations() bool {
	if c == nil {
		return false
	}
	for _, file := range c.RequestedFiles {
		switch file.Operation {
		case "create", "modify", "delete":
			return true
		}
	}
	return false
}

func (s *TaskExecutionState) sawAnyTool(names ...string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, name := range names {
		if s.successfulTools[strings.TrimSpace(name)] > 0 {
			return true
		}
	}
	return false
}

func (s *TaskExecutionState) sawReadPath(target string) bool {
	normalized := normalizeTaskPath(target)
	if normalized == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.readPaths[normalized]
	return ok
}

func (s *TaskExecutionState) sawMutatedPath(target string) bool {
	normalized := normalizeTaskPath(target)
	if normalized == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.mutatedPaths[normalized]
	return ok
}

func (s *TaskExecutionState) resolvedReview(reviewID string) bool {
	reviewID = strings.TrimSpace(reviewID)
	if reviewID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.resolvedReviews[reviewID]
	return ok
}

func (s *TaskExecutionState) recordSuccess(toolName string, input map[string]any) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successfulTools[toolName]++
	recordSyntheticTesterEvidence(s, toolName, input)
	if isTaskReadTool(toolName) {
		if target := extractTaskToolPath(toolName, input); target != "" {
			s.readPaths[target] = struct{}{}
		}
	}
	if isTaskMutationTool(toolName) {
		if target := extractTaskToolPath(toolName, input); target != "" {
			s.mutatedPaths[target] = struct{}{}
		}
	}
	if toolName == "coord_resolve_artifact" {
		if reviewID := stringInput(input, "review_id"); reviewID != "" {
			s.resolvedReviews[reviewID] = struct{}{}
		}
	}
}

func recordSyntheticTesterEvidence(s *TaskExecutionState, toolName string, input map[string]any) {
	if s == nil || strings.TrimSpace(toolName) != "write_test" {
		return
	}
	if writeTestCarriesRiskEvidence(input) {
		s.successfulTools["analyze_risk"]++
	}
	if writeTestCarriesPlanEvidence(input) {
		s.successfulTools["plan_tests"]++
	}
}

func pipelineStageFromTask(task *PipelineTaskInput) string {
	if task == nil || task.Context == nil {
		return ""
	}
	stage, _ := task.Context["pipeline_stage"].(string)
	return strings.TrimSpace(stage)
}

func decodeRequestedFileOperations(ctx map[string]any) []RequestedFileOperation {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx["affected_files"]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var files []RequestedFileOperation
	if err := json.Unmarshal(data, &files); err != nil {
		return nil
	}
	for i := range files {
		files[i].Path = normalizeTaskPath(files[i].Path)
		files[i].Operation = strings.ToLower(strings.TrimSpace(files[i].Operation))
		files[i].Reason = strings.TrimSpace(files[i].Reason)
	}
	return files
}

func decodeWorkspaceSurface(ctx map[string]any) workspaceSurface {
	if ctx == nil {
		return workspaceSurface{}
	}
	raw, ok := ctx["workspace"]
	if !ok || raw == nil {
		return workspaceSurface{}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return workspaceSurface{}
	}
	var surface workspaceSurface
	if err := json.Unmarshal(data, &surface); err != nil {
		return workspaceSurface{}
	}
	return surface
}

func normalizeTaskPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		normalized := normalizeTaskPath(candidate)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func normalizeTaskPath(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	clean := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	for strings.HasPrefix(clean, "./") {
		clean = strings.TrimPrefix(clean, "./")
	}
	if clean == "." {
		return ""
	}
	return clean
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func baseRequiredToolNames(role string) []string {
	switch role {
	case "engineer":
		return []string{
			"read_workspace_file",
			"diff_workspace_file",
			"consult",
			"format",
			"lint",
			"coord_query_view",
			"coord_watch_updates",
			"coord_claim_scope",
			"coord_release_scope",
			"coord_publish_artifact",
			"coord_request_review",
			"coord_resolve_artifact",
		}
	case "designer":
		return []string{
			"read_workspace_file",
			"component_search",
			"token_validate",
			"a11y_audit",
			"token_suggest",
			"coord_query_view",
			"coord_watch_updates",
			"coord_claim_scope",
			"coord_release_scope",
			"coord_publish_artifact",
			"coord_request_review",
			"coord_resolve_artifact",
		}
	case "tester-pipeline":
		return []string{"read_workspace_file", "inspect_workspace_state", "summarize_workspace_state"}
	case "inspector-pipeline":
		return []string{"coord_query_view", "coord_claim_scope", "coord_release_scope"}
	default:
		return nil
	}
}

func designerPlanningTools(contract *TaskExecutionContract) []string {
	if contract == nil {
		return []string{"component_create", "component_modify"}
	}
	hasCreate := false
	hasModify := false
	for _, file := range contract.RequestedFiles {
		switch file.Operation {
		case "create":
			hasCreate = true
		case "modify":
			hasModify = true
		}
	}
	switch {
	case hasCreate && !hasModify:
		return []string{"component_create"}
	case hasModify && !hasCreate:
		return []string{"component_modify"}
	default:
		return []string{"component_create", "component_modify"}
	}
}

func inspectorValidationTools() []string {
	return []string{
		"validate_criteria",
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
		"validate_design_consistency",
	}
}

func taskExecutionGuidance(contract *TaskExecutionContract, role string) string {
	if contract == nil {
		return ""
	}
	lines := []string{
		"# Task Execution Contract",
		"",
		"- Treat the requested work as the primary driver for tool choice. Current workspace state is evidence, not the sole definition of scope.",
	}
	if c := contract; c != nil && c.Ledger != nil && strings.TrimSpace(c.Ledger.TaskID) != "" {
		lines = append(lines, "- This coordination ledger is scoped only to pipeline task "+c.Ledger.TaskID+"; do not mix state across task IDs.")
	}
	if hasCreateOperations(contract) {
		lines = append(lines, "- Requested create operations remain valid even when the target path does not exist yet.")
	}
	lines = append(lines, roleSpecificGuidance(contract, role)...)
	if len(contract.Intents) > 0 {
		lines = append(lines, "", "Derived Intents:")
		for _, intent := range contract.Intents {
			lines = append(lines, "- "+describeTaskExecutionIntent(intent))
		}
	}
	if len(contract.Deliverables) > 0 {
		lines = append(lines, "", "Required Deliverables:")
		for _, deliverable := range contract.Deliverables {
			lines = append(lines, "- "+describeTaskExecutionDeliverable(deliverable))
		}
	}
	if len(contract.RequestedFiles) > 0 {
		lines = append(lines, "", "Requested File Operations:")
		for _, file := range contract.RequestedFiles {
			line := "- " + file.Operation + " " + file.Path
			if file.Reason != "" {
				line += " (" + file.Reason + ")"
			}
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func roleSpecificGuidance(contract *TaskExecutionContract, role string) []string {
	switch role {
	case "engineer":
		lines := []string{"- For requested modify/delete paths, inspect current file state before mutating. For requested create paths, prepare and create directly instead of treating missing reads as blockers."}
		if contract.HasPendingReviews() {
			lines = append(lines, "- Pending coordination reviews in this task ledger are active obligations. Consume the review context, address it with concrete change evidence or a published artifact, and resolve the review before concluding or releasing scope.")
		}
		return lines
	case "designer":
		lines := []string{"- Use the requested create/modify operations to decide whether `component_create` or `component_modify` should drive the plan before any file mutation."}
		if contract.HasPendingReviews() {
			lines = append(lines, "- Pending coordination reviews in this task ledger are active obligations. Inspect the review context, address the requested design change, and resolve the review before concluding or releasing scope.")
		}
		return lines
	case "tester-pipeline":
		return []string{
			"- Derive harness, risks, and tests from the task contract first. Missing implementation files can still be valid red-phase input when the work requests new behavior.",
			"- Choose the path that satisfies the required deliverables. Do not conclude once a plan exists if the requested deliverable is written tests or execution evidence.",
		}
	case "inspector-pipeline":
		if contract.RequiresDeliverable(TaskDeliverableCriteriaEvaluation) {
			return []string{
				"- Implementation evidence exists for this task. Validate the current implementation against the requested contract, publish reusable findings, and produce a structured grade before concluding.",
				"- Use targeted validation tools when they help explain or deepen a criteria failure, but satisfy the validation contract even if the workspace arrived through upstream results instead of fresh local edits.",
			}
		}
		return []string{
			"- This is contract-synthesis inspection. Define explicit criteria, inspect the declared workspace scope, and publish a handoff artifact that records pending validation for downstream workers.",
			"- Missing implementation is expected in this mode. Do not grade or fail criteria solely because the requested implementation has not been written yet.",
			"- Do not implement the requested functionality or mutate workspace files. Inspect, synthesize requirements, and publish coordination artifacts for downstream workers instead.",
		}
	default:
		return nil
	}
}

func hasCreateOperations(contract *TaskExecutionContract) bool {
	if contract == nil {
		return false
	}
	for _, file := range contract.RequestedFiles {
		if file.Operation == "create" {
			return true
		}
	}
	return false
}

func isTaskReadTool(toolName string) bool {
	switch toolName {
	case "read_file", "read_workspace_file", "inspect_workspace_state", "diff_workspace_file":
		return true
	default:
		return false
	}
}

func isTaskMutationTool(toolName string) bool {
	switch toolName {
	case "write_pipeline_file", "edit_pipeline_file", "delete_pipeline_file", "create_pipeline_directory", "write_test":
		return true
	default:
		return false
	}
}

func extractTaskToolPath(toolName string, input map[string]any) string {
	switch toolName {
	case "write_test":
		return normalizeTaskPath(stringInput(input, "output_file"))
	default:
		return normalizeTaskPath(stringInput(input, "path"))
	}
}

func stringInput(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

func nestedStringInput(input map[string]any, parentKey, key string) string {
	if input == nil {
		return ""
	}
	parent, _ := input[parentKey].(map[string]any)
	if len(parent) == 0 {
		return ""
	}
	value, _ := parent[key].(string)
	return strings.TrimSpace(value)
}

func writeTestCarriesRiskEvidence(input map[string]any) bool {
	return nestedStringInput(input, "test_case", "failure_hypothesis") != ""
}

func writeTestCarriesPlanEvidence(input map[string]any) bool {
	return nestedStringInput(input, "test_case", "name") != "" &&
		nestedStringInput(input, "test_case", "target_file") != "" &&
		nestedStringInput(input, "test_case", "expected_behavior") != ""
}

func taskHasImplementationEvidence(task *PipelineTaskInput) bool {
	if task == nil {
		return false
	}
	return parentResultsContainImplementationEvidence(task.ParentResults)
}

func parentResultsContainImplementationEvidence(results map[string]any) bool {
	for _, raw := range results {
		if parentResultContainsImplementation(raw) {
			return true
		}
	}
	return false
}

func parentResultContainsImplementation(raw any) bool {
	result, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	if !parentResultStateSucceeded(result["state"]) {
		return false
	}
	return parentResultOutputPresent(result["output"])
}

func parentResultStateSucceeded(value any) bool {
	state, _ := value.(string)
	return strings.EqualFold(strings.TrimSpace(state), "succeeded")
}

func parentResultOutputPresent(value any) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

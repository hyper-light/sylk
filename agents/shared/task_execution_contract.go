package shared

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"sync"
)

type RequestedFileOperation struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

type TaskExecutionContract struct {
	TaskID                    string
	Stage                     string
	RuntimeAgentType          string
	WorkerType                string
	RequestedFiles            []RequestedFileOperation
	ReadSet                   []string
	WriteSet                  []string
	TestSurface               []string
	Ledger                    *TaskExecutionLedger
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
		RuntimeAgentType:          strings.TrimSpace(task.AgentType),
		WorkerType:                PipelineWorkerType(task),
		RequestedFiles:            requestedFiles,
		ReadSet:                   normalizeTaskPaths(workspace.ReadSet),
		WriteSet:                  normalizeTaskPaths(workspace.WriteSet),
		TestSurface:               normalizeTaskPaths(workspace.TestSurface),
		HasImplementationEvidence: taskHasImplementationEvidence(task),
	}
	contract.Ledger = buildTaskExecutionLedger(task, contract.RuntimeAgentType)
	contract.PreImplementation = determinePreImplementation(task, contract)
	return contract
}

func RebuildTaskExecutionContract(task *PipelineTaskInput, contract *TaskExecutionContract) *TaskExecutionContract {
	if task == nil || contract == nil {
		return contract
	}
	contract.PreImplementation = determinePreImplementation(task, contract)
	return contract
}

func determinePreImplementation(task *PipelineTaskInput, contract *TaskExecutionContract) bool {
	return strings.EqualFold(strings.TrimSpace(pipelineStageFromTask(task)), "inspect") &&
		(contract == nil || !contract.HasImplementationEvidence)
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

func (s *TaskExecutionState) recordSuccess(toolName string, input map[string]any, output string) {
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
		if reviewID := resolvedReviewID(input, output); reviewID != "" {
			s.resolvedReviews[reviewID] = struct{}{}
		}
	}
}

func resolvedReviewID(input map[string]any, output string) string {
	if reviewID := stringInput(input, "review_id"); reviewID != "" {
		return reviewID
	}
	output = strings.TrimSpace(output)
	if output == "" || !json.Valid([]byte(output)) {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return ""
	}
	return stringInput(payload, "review_id")
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

func stringValue(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
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
	for key, raw := range results {
		if parentResultContainsImplementation(key, raw) {
			return true
		}
	}
	return false
}

func parentResultContainsImplementation(key string, raw any) bool {
	result, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	if !parentResultStateSucceeded(result["state"]) {
		return false
	}
	if stage := strings.TrimSpace(key); stage == "inspect" || stage == "test" {
		return false
	}
	if stage := strings.TrimSpace(key); stage != "" && stage != "execute" {
		return genericParentResultOutputPresent(result["output"])
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
		if parentResultOutputPresent(typed["worker_output"]) {
			return true
		}
		if changed, ok := typed["changed_files"].([]any); ok && len(changed) > 0 {
			return true
		}
		if changed, ok := typed["files_changed"].([]any); ok && len(changed) > 0 {
			return true
		}
		if changed, ok := typed["changed_files"].([]string); ok && len(changed) > 0 {
			return true
		}
		if changed, ok := typed["files_changed"].([]string); ok && len(changed) > 0 {
			return true
		}
		return false
	default:
		return true
	}
}

func genericParentResultOutputPresent(value any) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/claims"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

const (
	defaultCoverageThreshold = 80.0
	defaultMaxCyclomatic     = 4
	defaultMaxCognitive      = 8
)

type taskFileTarget struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

type acceptanceCriterion struct {
	Given    string `json:"given"`
	When     string `json:"when"`
	Then     string `json:"then"`
	Priority string `json:"priority"`
}

type validationEvaluation struct {
	Issues             []shared.ValidationIssue
	CriteriaMet        []string
	CriteriaFailed     []string
	QualityGateResults map[string]bool
}

func (pi *PipelineInspector) seedCriteriaFromTask(task *pipelineTaskFields) {
	if task == nil || strings.TrimSpace(task.TaskID) == "" {
		return
	}
	files := affectedPathsFromTask(task)
	pi.mu.RLock()
	_, exists := pi.criteria[task.TaskID]
	pi.mu.RUnlock()
	if len(files) > 0 {
		pi.mu.Lock()
		pi.taskFiles[task.TaskID] = files
		pi.mu.Unlock()
	}
	if exists {
		return
	}
	criteria := compileCriteriaFromTask(task)
	if criteria != nil {
		pi.DefineCriteria(task.TaskID, criteria)

		// Criteria seeding testament.
		criteriaCount := len(criteria.SuccessCriteria)
		gateCount := len(criteria.QualityGates)
		pi.inspectorSubmitTestament(context.Background(), pi.inspectorTestament(
			fmt.Sprintf("Criteria seeded from task: %d criteria, %d gates", criteriaCount, gateCount),
			"committed",
			[]*claims.Artifact{
				pi.inspectorArtifact("task_id", task.TaskID),
				pi.inspectorArtifact("criteria_count", fmt.Sprintf("%d", criteriaCount)),
				pi.inspectorArtifact("gate_count", fmt.Sprintf("%d", gateCount)),
				pi.inspectorArtifact("source", "task_contract"),
			},
		))
	}
}

func compileCriteriaFromTask(task *pipelineTaskFields) *shared.InspectorCriteria {
	if task == nil || strings.TrimSpace(task.TaskID) == "" {
		return nil
	}
	workerType := agentShared.PipelineWorkerType(task)
	if workerType == "" {
		workerType = task.AgentType
	}

	successCriteria := decodeStringList(task.Context, "success_criteria")
	guidelines := decodeStringList(task.Context, "guidelines")
	testRequirements := decodeStringList(task.Context, "test_requirements")
	riskFactors := decodeStringList(task.Context, "risk_factors")
	acceptanceCriteria := decodeAcceptanceCriteria(task.Context)
	affectedFiles := decodeAffectedFiles(task.Context)

	criteria := &shared.InspectorCriteria{
		TaskID:          task.TaskID,
		Domain:          shared.ValidationDomainFromWorkerType(workerType),
		SuccessCriteria: make([]shared.SuccessCriterion, 0, len(successCriteria)+len(acceptanceCriteria)+3),
		QualityGates:    defaultQualityGates(workerType, len(testRequirements) > 0),
		Constraints:     make([]shared.Constraint, 0, len(guidelines)+len(riskFactors)),
		CreatedAt:       time.Now(),
	}

	if len(affectedFiles) > 0 {
		criteria.SuccessCriteria = append(criteria.SuccessCriteria, shared.SuccessCriterion{
			ID:                 "files_in_scope",
			Description:        "Implementation stays within the architect-declared affected_files scope.",
			Verifiable:         true,
			VerificationMethod: "affected_files_present",
		})
	}

	criteria.SuccessCriteria = append(criteria.SuccessCriteria, shared.SuccessCriterion{
		ID:                 "quality_gates_pass",
		Description:        "No blocking validation issues remain after automated inspection.",
		Verifiable:         true,
		VerificationMethod: "quality_gates_pass",
	})

	for idx, item := range successCriteria {
		criteria.SuccessCriteria = append(criteria.SuccessCriteria, shared.SuccessCriterion{
			ID:                 fmt.Sprintf("success_%d", idx+1),
			Description:        strings.TrimSpace(item),
			Verifiable:         false,
			VerificationMethod: "architect_success_criteria",
		})
	}
	for idx, item := range acceptanceCriteria {
		description := strings.TrimSpace(strings.Join([]string{
			"Given " + strings.TrimSpace(item.Given),
			"When " + strings.TrimSpace(item.When),
			"Then " + strings.TrimSpace(item.Then),
		}, " "))
		criteria.SuccessCriteria = append(criteria.SuccessCriteria, shared.SuccessCriterion{
			ID:                 fmt.Sprintf("acceptance_%d", idx+1),
			Description:        strings.TrimSpace(description),
			Verifiable:         false,
			VerificationMethod: "architect_acceptance_criteria",
		})
	}
	for _, guideline := range guidelines {
		criteria.Constraints = append(criteria.Constraints, shared.Constraint{
			Type:        "guideline",
			Description: guideline,
			Required:    true,
		})
	}
	for _, risk := range riskFactors {
		criteria.Constraints = append(criteria.Constraints, shared.Constraint{
			Type:        "risk_factor",
			Description: risk,
			Required:    true,
		})
	}
	for _, testRequirement := range testRequirements {
		criteria.SuccessCriteria = append(criteria.SuccessCriteria, shared.SuccessCriterion{
			ID:                 "test_requirement_" + sanitizeCriterionID(testRequirement),
			Description:        testRequirement,
			Verifiable:         false,
			VerificationMethod: "architect_test_requirement",
		})
	}

	return criteria
}

// CompileCriteriaFromTask exposes the pipeline inspector's task-contract
// compiler so other runtimes can seed the same inspect/test/execute loop
// without duplicating criteria logic.
func CompileCriteriaFromTask(task *agentShared.PipelineTaskInput) *shared.InspectorCriteria {
	return compileCriteriaFromTask(task)
}

func defaultQualityGates(workerType string, _ bool) []shared.QualityGate {
	if workerType == "designer" {
		return []shared.QualityGate{
			{Name: "token_usage_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
			{Name: "accessibility_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
			{Name: "component_api_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
			{Name: "design_consistency_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
		}
	}

	gates := []shared.QualityGate{
		{Name: "lint_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
		{Name: "typecheck_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
		{Name: "format_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
		{Name: "security_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
		{Name: "complexity_clean", Metric: "blocking_issues", Operator: "==", Threshold: 0},
	}
	return gates
}

func (pi *PipelineInspector) runDeterministicValidation(
	ctx context.Context,
	files []string,
	workerType string,
	criteria *shared.InspectorCriteria,
) (*validationEvaluation, error) {
	paths := normalizeValidationPaths(files)
	toolResults, issues := pi.collectValidationEvidence(ctx, workerType, paths, criteria)
	issues = shared.DeduplicateIssues(issues)

	gateResults := evaluateQualityGates(criteria, toolResults)
	criteriaMet, criteriaFailed := evaluateCriteria(criteria, paths, issues, gateResults)

	return &validationEvaluation{
		Issues:             issues,
		CriteriaMet:        criteriaMet,
		CriteriaFailed:     criteriaFailed,
		QualityGateResults: gateResults,
	}, nil
}

func normalizeValidationPaths(paths []string) []string {
	if len(paths) == 0 {
		return []string{"./..."}
	}
	clean := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		clean = append(clean, trimmed)
	}
	if len(clean) == 0 {
		return []string{"./..."}
	}
	return clean
}

func (pi *PipelineInspector) collectValidationEvidence(
	ctx context.Context,
	workerType string,
	paths []string,
	criteria *shared.InspectorCriteria,
) (map[string][]shared.ValidationIssue, []shared.ValidationIssue) {
	results := make(map[string][]shared.ValidationIssue)
	var issues []shared.ValidationIssue

	for _, toolName := range validationToolPlan(workerType, criteria) {
		toolIssues, err := pi.runValidationTool(ctx, toolName, paths)
		if err != nil {
			toolIssues = []shared.ValidationIssue{{
				ID:       "tool_failure_" + sanitizeCriterionID(toolName),
				Severity: shared.Medium,
				Message:  fmt.Sprintf("%s failed to execute: %v", toolName, err),
				RuleID:   "inspector/tool-execution",
				Domain:   shared.ValidationDomainFromWorkerType(workerType),
			}}
		}
		results[toolName] = toolIssues
		issues = append(issues, toolIssues...)
	}
	if criteriaHasCoverageGate(criteria) {
		coverageIssues := []shared.ValidationIssue{{
			ID:       "coverage_requires_tester_evidence",
			Severity: shared.Medium,
			Message:  "Coverage validation requires tester-owned execution evidence; inspector does not run test commands locally.",
			RuleID:   "inspector/test-execution-boundary",
			Domain:   shared.ValidationDomainFromWorkerType(workerType),
		}}
		results["coverage_from_tester"] = coverageIssues
		issues = append(issues, coverageIssues...)
	}

	return results, issues
}

func validationToolPlan(workerType string, _ *shared.InspectorCriteria) []string {
	if workerType == "designer" {
		return []string{
			"validate_token_usage",
			"validate_accessibility",
			"validate_component_api",
			"validate_design_consistency",
		}
	}

	plan := []string{
		"run_linter",
		"run_type_checker",
		"run_formatter_check",
		"run_security_scan",
		"analyze_complexity",
	}
	return plan
}

func criteriaHasCoverageGate(criteria *shared.InspectorCriteria) bool {
	if criteria == nil {
		return false
	}
	for _, gate := range criteria.QualityGates {
		if strings.EqualFold(strings.TrimSpace(gate.Name), "coverage_clean") ||
			strings.EqualFold(strings.TrimSpace(gate.Metric), "coverage") {
			return true
		}
	}
	return false
}

func (pi *PipelineInspector) runValidationTool(
	ctx context.Context,
	toolName string,
	paths []string,
) ([]shared.ValidationIssue, error) {
	if pi.toolRuntime() == nil {
		return nil, fmt.Errorf("tool runtime is not configured")
	}

	// Phase 2.K / GI-C: all legacy analyzer names route through
	// run_analyzer(kind=…).
	// Phase 2.K / 10.B: all legacy UI-compliance names route through
	// validate_ui_compliance(aspect=…).
	// The gate result map still keys off the logical tool name so
	// evaluateQualityGates stays unchanged.
	invocationName := toolName
	args := map[string]any{"paths": paths}
	if kind, ok := legacyAnalyzerKindForTool(toolName); ok {
		invocationName = "run_analyzer"
		args["kind"] = kind
	} else if aspect, ok := legacyUIComplianceAspectForTool(toolName); ok {
		invocationName = "validate_ui_compliance"
		args["aspect"] = aspect
	}
	if _, err := pi.toolRuntime().Activate(invocationName); err != nil {
		return nil, err
	}
	switch toolName {
	case "check_coverage":
		args["threshold"] = defaultCoverageThreshold
	case "analyze_complexity":
		args["max_cyclomatic"] = defaultMaxCyclomatic
		args["max_cognitive"] = defaultMaxCognitive
	}

	rawArgs, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal tool args: %w", err)
	}
	correlationID := "pipeline_validation_" + uuid.NewString()[:8]
	if logMeta := agentShared.LogMetaFromContext(ctx); logMeta.CorrID != "" {
		correlationID = logMeta.CorrID
	}

	result, err := pi.toolRuntime().ExecuteRaw(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        "invoke_" + uuid.NewString(),
			Name:      invocationName,
			Arguments: string(rawArgs),
		},
		AgentID:         pi.toolRuntime().AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: pi.toolRuntime().CapabilityScope(),
	})
	if err != nil {
		return nil, err
	}

	return decodeValidationIssues(result.Data)
}

// legacyAnalyzerKindForTool maps the pre-§10.K analyzer skill names to
// the run_analyzer kind enum. Returns ok=false when toolName is not an
// analyzer (e.g., validate_token_usage), so the caller can invoke it
// directly.
func legacyAnalyzerKindForTool(toolName string) (string, bool) {
	switch toolName {
	case "run_linter":
		return "lint", true
	case "run_type_checker":
		return "typecheck", true
	case "run_formatter_check":
		return "format_check", true
	case "run_security_scan":
		return "security", true
	case "check_coverage":
		return "coverage", true
	case "analyze_complexity":
		return "complexity", true
	case "detect_race_conditions":
		return "race", true
	case "detect_deadlocks":
		return "deadlock", true
	case "detect_memory_leaks":
		return "memory_leak", true
	default:
		return "", false
	}
}

// legacyUIComplianceAspectForTool maps the pre-§10.B UI-compliance skill
// names to the validate_ui_compliance aspect enum. Returns ok=false
// when toolName is not a UI-compliance scan, so the caller can invoke
// it directly.
func legacyUIComplianceAspectForTool(toolName string) (string, bool) {
	switch toolName {
	case "validate_token_usage":
		return "tokens", true
	case "validate_accessibility":
		return "a11y", true
	case "validate_component_api":
		return "component_api", true
	case "validate_design_consistency":
		return "consistency", true
	default:
		return "", false
	}
}

func decodeValidationIssues(data any) ([]shared.ValidationIssue, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal validation tool result: %w", err)
	}
	var payload struct {
		Issues []shared.ValidationIssue `json:"issues"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal validation tool result: %w", err)
	}
	return payload.Issues, nil
}

func evaluateQualityGates(criteria *shared.InspectorCriteria, toolResults map[string][]shared.ValidationIssue) map[string]bool {
	results := make(map[string]bool, len(criteria.QualityGates))
	for _, gate := range criteria.QualityGates {
		if strings.EqualFold(strings.TrimSpace(gate.Name), "coverage_clean") ||
			strings.EqualFold(strings.TrimSpace(gate.Metric), "coverage") {
			results[gate.Name] = false
			continue
		}
		switch gate.Name {
		case "lint_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["run_linter"])
		case "typecheck_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["run_type_checker"])
		case "format_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["run_formatter_check"])
		case "security_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["run_security_scan"])
		case "complexity_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["analyze_complexity"])
		case "token_usage_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["validate_token_usage"])
		case "accessibility_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["validate_accessibility"])
		case "component_api_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["validate_component_api"])
		case "design_consistency_clean":
			results[gate.Name] = !shared.HasBlockingIssues(toolResults["validate_design_consistency"])
		default:
			results[gate.Name] = true
		}
	}
	return results
}

func evaluateCriteria(
	criteria *shared.InspectorCriteria,
	files []string,
	issues []shared.ValidationIssue,
	gates map[string]bool,
) ([]string, []string) {
	if criteria == nil {
		return nil, nil
	}
	criteriaMet := make([]string, 0, len(criteria.SuccessCriteria))
	criteriaFailed := make([]string, 0, len(criteria.SuccessCriteria))
	allGatesPass := true
	for _, passed := range gates {
		if !passed {
			allGatesPass = false
			break
		}
	}

	for _, criterion := range criteria.SuccessCriteria {
		passed := false
		switch criterion.VerificationMethod {
		case "affected_files_present":
			passed = len(files) > 0
		case "quality_gates_pass":
			passed = allGatesPass && !shared.HasBlockingIssues(issues)
		default:
			passed = !shared.HasBlockingIssues(issues)
		}
		if passed {
			criteriaMet = append(criteriaMet, criterion.ID)
		} else {
			criteriaFailed = append(criteriaFailed, criterion.ID)
		}
	}
	return criteriaMet, criteriaFailed
}

func qualityGradeForResult(result *shared.InspectorResult, workerType string) shared.QualityGrade {
	penalty := severityPenalty(result.Issues)
	base := clampScore(1.0 - penalty)
	grade := shared.QualityGrade{
		Correctness: base,
		Robustness:  clampScore(base - issueDomainPenalty(result.Issues, "complexity")/2),
		Performance: clampScore(base - issueDomainPenalty(result.Issues, "performance")),
		Security:    clampScore(base - issueDomainPenalty(result.Issues, "security")),
		Adherence:   clampScore(base - gateFailurePenalty(result.QualityGateResults)),
	}
	if workerType == "designer" {
		grade.TokenConsistency = clampScore(base - issueDomainPenalty(result.Issues, "token"))
		grade.Accessibility = clampScore(base - issueDomainPenalty(result.Issues, "a11y"))
		grade.ComponentAPI = clampScore(base - issueDomainPenalty(result.Issues, "component"))
	}
	return grade
}

func severityPenalty(issues []shared.ValidationIssue) float64 {
	total := 0.0
	for _, issue := range issues {
		switch issue.Severity {
		case shared.Critical:
			total += 0.45
		case shared.High:
			total += 0.25
		case shared.Medium:
			total += 0.10
		case shared.Low:
			total += 0.03
		}
	}
	return total
}

func issueDomainPenalty(issues []shared.ValidationIssue, needle string) float64 {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return 0
	}
	penalty := 0.0
	for _, issue := range issues {
		message := strings.ToLower(issue.Message + " " + issue.RuleID)
		if strings.Contains(message, needle) {
			switch issue.Severity {
			case shared.Critical:
				penalty += 0.35
			case shared.High:
				penalty += 0.20
			case shared.Medium:
				penalty += 0.08
			default:
				penalty += 0.02
			}
		}
	}
	return penalty
}

func gateFailurePenalty(results map[string]bool) float64 {
	if len(results) == 0 {
		return 0
	}
	failures := 0
	for _, passed := range results {
		if !passed {
			failures++
		}
	}
	if failures == 0 {
		return 0
	}
	return clampScore(float64(failures) * 0.12)
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func decodeStringList(ctx map[string]any, key string) []string {
	if ctx == nil {
		return nil
	}
	return decodeAnyStringList(ctx[key])
}

func decodeMap(ctx map[string]any, key string) map[string]any {
	if ctx == nil {
		return nil
	}
	value, ok := ctx[key]
	if !ok || value == nil {
		return nil
	}
	typed, _ := value.(map[string]any)
	return typed
}

func decodeAnyStringList(raw any) []string {
	if raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text == "" || text == "<nil>" {
				continue
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
	}
}

func decodeAcceptanceCriteria(ctx map[string]any) []acceptanceCriterion {
	return decodeContextSlice[acceptanceCriterion](ctx, "acceptance_criteria")
}

func decodeAffectedFiles(ctx map[string]any) []taskFileTarget {
	return decodeContextSlice[taskFileTarget](ctx, "affected_files")
}

func decodeContextSlice[T any](ctx map[string]any, key string) []T {
	if ctx == nil {
		return nil
	}
	raw, ok := ctx[key]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func affectedPathsFromTask(task *pipelineTaskFields) []string {
	if task == nil {
		return nil
	}
	files := decodeAffectedFiles(task.Context)
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			continue
		}
		paths = append(paths, file.Path)
	}
	paths = append(paths, decodeAnyStringList(decodeMap(task.Context, "workspace")["write_set"])...)
	paths = append(paths, decodeAnyStringList(decodeMap(task.Context, "workspace")["test_surface"])...)
	return normalizeValidationPaths(paths)
}

func sanitizeCriterionID(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	input = strings.ReplaceAll(input, " ", "_")
	input = strings.ReplaceAll(input, "/", "_")
	input = strings.ReplaceAll(input, "-", "_")
	input = strings.ReplaceAll(input, ".", "_")
	if input == "" {
		return "criterion"
	}
	return input
}

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/agents/tester"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

const testerVerificationArtifactTitle = "Tester Verification Handoff"

func (pt *PipelineTester) publishVerificationArtifact(ctx context.Context, target string) (*coordination.Artifact, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("verification handoff target is required")
	}
	input, err := pt.buildVerificationArtifactInput()
	if err != nil {
		return nil, err
	}
	return pt.coordinationClient().PublishArtifact(ctx, input)
}

func (pt *PipelineTester) buildVerificationArtifactInput() (coordination.PublishArtifactInput, error) {
	task := pt.currentTaskSnapshot()
	if task == nil {
		return coordination.PublishArtifactInput{}, fmt.Errorf("tester task context unavailable")
	}
	taskID := strings.TrimSpace(task.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(pt.pipelineID)
	}
	if taskID == "" {
		return coordination.PublishArtifactInput{}, fmt.Errorf("tester task context unavailable")
	}

	suite := pt.lastSuiteSnapshot()
	if suite == nil {
		return coordination.PublishArtifactInput{}, fmt.Errorf("run_test_suite must complete before publishing the verification handoff artifact")
	}

	plan := pt.planSnapshot()
	diagnoses := pt.diagnosisSnapshots()
	authoredFiles := pt.createdArtifacts()
	harness := pt.currentHarnessState()
	contract := agentshared.BuildTaskExecutionContract(task)
	upstreamArtifacts := relevantArtifactsFromContract(contract)
	upstreamIDs := relevantArtifactIDs(upstreamArtifacts)
	payload := map[string]any{
		"current_request":         agentshared.PipelineCurrentRequest(task),
		"original_task_objective": strings.TrimSpace(task.Prompt),
		"criteria_context": map[string]any{
			"acceptance_criteria": cloneJSONValue(task.Context["acceptance_criteria"]),
			"success_criteria":    cloneJSONValue(task.Context["success_criteria"]),
			"test_requirements":   cloneJSONValue(task.Context["test_requirements"]),
			"guidelines":          cloneJSONValue(task.Context["guidelines"]),
			"risk_factors":        cloneJSONValue(task.Context["risk_factors"]),
			"implementation_guide": strings.TrimSpace(
				verificationStringValue(task.Context["implementation_guide"]),
			),
		},
		"upstream_artifacts":  upstreamArtifacts,
		"test_plan":           plan,
		"authored_test_files": authoredFiles,
		"suite_result":        suite,
		"failures":            suiteFailurePayloads(suite),
		"diagnoses":           diagnoses,
	}
	if harness != nil {
		payload["harness"] = map[string]any{
			"framework_id":        harness.FrameworkID,
			"framework_name":      harness.FrameworkName,
			"run_command":         harness.RunCommand,
			"coverage_command":    harness.CoverageCommand,
			"recommended_outputs": cloneJSONValue(harness.RecommendedOutputs),
			"existing_test_files": append([]string(nil), harness.ExistingTestFiles...),
		}
	}

	return coordination.PublishArtifactInput{
		TaskID:              taskID,
		TaskName:            pt.currentTaskName(),
		Kind:                "verification_result",
		Title:               testerVerificationArtifactTitle,
		Summary:             buildVerificationArtifactSummary(suite, authoredFiles, diagnoses),
		ScopeKind:           coordination.ScopeKindTestSurface,
		ScopeKey:            taskID,
		Payload:             payload,
		Evidence:            buildVerificationEvidence(authoredFiles, diagnoses),
		UpstreamArtifactIDs: upstreamIDs,
		IdempotencyKey:      testerVerificationArtifactID(taskID, suite),
	}, nil
}

func buildVerificationArtifactSummary(
	suite *tester.TestSuiteResult,
	authoredFiles []string,
	diagnoses []*testershared.DiagnosisReport,
) string {
	if suite == nil {
		return "Tester verification handoff artifact."
	}
	total := suite.TotalTests
	if total <= 0 {
		total = len(suite.Results)
	}
	parts := []string{
		fmt.Sprintf(
			"Tester verification handoff after suite execution: %d passed, %d failed, %d skipped, %d errors across %d tests.",
			suite.Passed,
			suite.Failed,
			suite.Skipped,
			suite.Errors,
			total,
		),
	}
	if len(authoredFiles) > 0 {
		parts = append(parts, fmt.Sprintf("%d authored test artifact(s) are attached.", len(authoredFiles)))
	}
	if len(diagnoses) > 0 {
		parts = append(parts, fmt.Sprintf("%d diagnosis report(s) are included.", len(diagnoses)))
	}
	return strings.Join(parts, " ")
}

func testerVerificationArtifactID(taskID string, suite *tester.TestSuiteResult) string {
	taskID = strings.TrimSpace(taskID)
	suiteID := ""
	if suite != nil {
		suiteID = strings.TrimSpace(suite.SuiteID)
	}
	if taskID == "" {
		return "tester-verification-handoff"
	}
	if suiteID == "" {
		return "tester-verification-handoff:" + taskID
	}
	return "tester-verification-handoff:" + taskID + ":" + suiteID
}

func suiteFailurePayloads(suite *tester.TestSuiteResult) []map[string]any {
	if suite == nil || len(suite.Results) == 0 {
		return nil
	}
	failures := make([]map[string]any, 0)
	for _, result := range suite.Results {
		switch result.Status {
		case tester.StatusFailed, tester.StatusError:
		default:
			continue
		}
		failures = append(failures, map[string]any{
			"name":          strings.TrimSpace(result.Name),
			"package":       strings.TrimSpace(result.Package),
			"status":        result.Status,
			"error_message": strings.TrimSpace(result.ErrorMessage),
			"output":        strings.TrimSpace(result.Output),
			"stack_trace":   strings.TrimSpace(result.StackTrace),
		})
	}
	return failures
}

func relevantArtifactsFromContract(contract *agentshared.TaskExecutionContract) []map[string]any {
	if contract == nil || contract.Ledger == nil || len(contract.Ledger.RelevantArtifacts) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(contract.Ledger.RelevantArtifacts))
	for _, artifact := range contract.Ledger.RelevantArtifacts {
		if strings.TrimSpace(artifact.ID) == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":            strings.TrimSpace(artifact.ID),
			"kind":          strings.TrimSpace(artifact.Kind),
			"summary":       strings.TrimSpace(artifact.Summary),
			"producer_type": strings.TrimSpace(artifact.ProducerType),
			"scope_key":     strings.TrimSpace(artifact.ScopeKey),
		})
	}
	return out
}

func relevantArtifactIDs(artifacts []map[string]any) []string {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]string, 0, len(artifacts))
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		id := strings.TrimSpace(verificationStringValue(artifact["id"]))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func buildVerificationEvidence(authoredFiles []string, diagnoses []*testershared.DiagnosisReport) []coordination.EvidenceRef {
	seen := make(map[string]struct{})
	evidence := make([]coordination.EvidenceRef, 0, len(authoredFiles)+len(diagnoses))
	appendRef := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := kind + ":" + value
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		evidence = append(evidence, coordination.EvidenceRef{Kind: kind, Value: value})
	}
	for _, path := range authoredFiles {
		appendRef("file", path)
	}
	for _, report := range diagnoses {
		if report == nil {
			continue
		}
		for _, cause := range report.RootCauses {
			appendRef("file", cause.File)
		}
	}
	return evidence
}

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return value
	}
	return cloned
}

func verificationStringValue(value any) string {
	typed, _ := value.(string)
	return strings.TrimSpace(typed)
}

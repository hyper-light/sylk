package shared

import (
	"context"
	"strings"
	"testing"
)

func TestBuildTaskExecutionContract_TracksRequestedOperations(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "engineer",
		Context: map[string]any{
			"pipeline_stage": "execute",
			"affected_files": []map[string]any{
				{"path": "src/hello_cli/cli.py", "operation": "create", "reason": "new CLI entrypoint"},
				{"path": "./tests/test_cli.py", "operation": "modify"},
			},
			"workspace": map[string]any{
				"read_set":     []string{"README.md"},
				"write_set":    []string{"src/hello_cli/cli.py", "tests/test_cli.py"},
				"test_surface": []string{"tests/test_cli.py"},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.PreImplementation {
		t.Fatal("execute stage should not be pre-implementation")
	}
	if got := contract.OperationForPath("src/hello_cli/cli.py"); got != "create" {
		t.Fatalf("operation for create path = %q, want create", got)
	}
	if got := contract.OperationForPath("tests/test_cli.py"); got != "modify" {
		t.Fatalf("operation for modify path = %q, want modify", got)
	}
	if len(contract.WriteSet) != 2 {
		t.Fatalf("write_set len = %d, want 2", len(contract.WriteSet))
	}
}

func TestBuildTaskExecutionContract_InspectorUsesStageAndEvidenceOnly(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "inspector-pipeline",
		Prompt:    "Inspect this task and define the implementation contract before work begins.",
		Context: map[string]any{
			"pipeline_stage": "inspect",
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if !contract.PreImplementation {
		t.Fatal("expected inspect-stage inspector contract to remain pre-implementation without evidence")
	}
	if contract.HasImplementationEvidence {
		t.Fatal("did not expect implementation evidence")
	}
}

func TestBuildTaskExecutionContract_InspectorUsesParentResultsForImplementationEvidence(t *testing.T) {
	task := &PipelineTaskInput{
		AgentType: "inspector-pipeline",
		Prompt:    "Validate the implementation and publish the inspection findings.",
		Context: map[string]any{
			"pipeline_stage": "inspect",
		},
		ParentResults: map[string]any{
			"engineer": map[string]any{
				"state":  "succeeded",
				"output": map[string]any{"summary": "implemented CLI entrypoint"},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.PreImplementation {
		t.Fatal("did not expect validation contract to stay pre-implementation when parent results provide evidence")
	}
	if !contract.HasImplementationEvidence {
		t.Fatal("expected implementation evidence from parent results")
	}
}

func TestBuildTaskExecutionContract_EngineerBuildsTaskScopedReviewLedger(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task-1",
		AgentType: "engineer",
		Context: map[string]any{
			"coordination_packet": map[string]any{
				"pending_reviews": []any{
					map[string]any{
						"id":          "rev-1",
						"artifact_id": "art-1",
						"summary":     "Address the failing CLI behavior",
					},
				},
				"relevant_artifacts": []any{
					map[string]any{
						"id":            "art-1",
						"kind":          "verification_result",
						"summary":       "CLI test failure details",
						"producer_type": "tester-pipeline",
					},
				},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.TaskID != "task-1" {
		t.Fatalf("TaskID = %q, want task-1", contract.TaskID)
	}
	if contract.Ledger == nil || contract.Ledger.TaskID != "task-1" {
		t.Fatalf("ledger = %#v, want task-scoped ledger", contract.Ledger)
	}
	if !contract.HasPendingReviews() {
		t.Fatal("expected pending reviews from coordination packet")
	}
}

func TestValidateTaskExecutionCall_InspectorAllowsValidationToolsButBlocksMutation(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		Stage:             "inspect",
		PreImplementation: true,
	})

	if err := ValidateTaskExecutionCall(ctx, "inspector-pipeline", "validate_criteria", nil); err != nil {
		t.Fatalf("validate_criteria blocked by runtime gate: %v", err)
	}
	err := ValidateTaskExecutionCall(ctx, "inspector-pipeline", "write_pipeline_file", map[string]any{"path": "src/hello_cli/cli.py"})
	if err == nil {
		t.Fatal("expected inspector workspace mutation to be blocked")
	}
	if !strings.Contains(err.Error(), "must not implement or mutate") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateTaskExecutionCall(ctx, "inspector-pipeline", "install_dependency_tooling", map[string]any{"summary": "Install tooling"}); err != nil {
		t.Fatalf("install_dependency_tooling blocked by runtime gate: %v", err)
	}
}

func TestValidateTaskExecutionCall_EngineerAllowsReleaseAndReviewResolutionWithoutRuntimeGate(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		TaskID: "task-1",
		RequestedFiles: []RequestedFileOperation{
			{Path: "src/hello_cli/cli.py", Operation: "modify"},
		},
		Ledger: &TaskExecutionLedger{
			TaskID:     "task-1",
			WorkerType: "engineer",
			Seeded:     true,
			PendingReviews: []TaskExecutionReview{
				{ID: "rev-1", ArtifactID: "art-1", Summary: "Address failing CLI test"},
			},
		},
	})

	if err := ValidateTaskExecutionCall(ctx, "engineer", "coord_resolve_artifact", map[string]any{"review_id": "rev-1"}); err != nil {
		t.Fatalf("coord_resolve_artifact blocked by runtime gate: %v", err)
	}
	if err := ValidateTaskExecutionCall(ctx, "engineer", "coord_release_scope", nil); err != nil {
		t.Fatalf("coord_release_scope blocked by runtime gate: %v", err)
	}
	RecordTaskExecutionSuccess(ctx, "coord_resolve_artifact", map[string]any{"review_id": "rev-1"}, "")
	if err := ValidateTaskExecutionCompletion(ctx, "engineer"); err != nil {
		t.Fatalf("engineer completion blocked by runtime gate: %v", err)
	}
}

func TestRecordTaskExecutionSuccess_ResolvesReviewFromToolOutput(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		TaskID: "task-1",
		Ledger: &TaskExecutionLedger{
			TaskID:     "task-1",
			WorkerType: "engineer",
			Seeded:     true,
			PendingReviews: []TaskExecutionReview{
				{ID: "rev-1", ArtifactID: "art-1", Summary: "Address failing CLI test"},
			},
		},
	})

	RecordTaskExecutionSuccess(ctx, "coord_resolve_artifact", map[string]any{"artifact_id": "art-1"}, `{"review_id":"rev-1"}`)

	state := TaskExecutionStateFromContext(ctx)
	if state == nil || !state.resolvedReview("rev-1") {
		t.Fatal("expected review resolution to be recorded from tool output")
	}
}

func TestValidateTaskExecutionCompletion_DesignerAllowsIterationEndWithoutRuntimeReviewGate(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{
		TaskID: "task-2",
		Ledger: &TaskExecutionLedger{
			TaskID:     "task-2",
			WorkerType: "designer",
			Seeded:     true,
			PendingReviews: []TaskExecutionReview{
				{ID: "rev-2", ArtifactID: "art-2", Summary: "Review the UX implications"},
			},
		},
	})

	RecordTaskExecutionSuccess(ctx, "component_search", nil, "")
	RecordTaskExecutionSuccess(ctx, "coord_publish_artifact", nil, "")
	if err := ValidateTaskExecutionCompletion(ctx, "designer"); err != nil {
		t.Fatalf("designer completion blocked by runtime gate: %v", err)
	}
	if err := ValidateTaskExecutionCall(ctx, "designer", "coord_resolve_artifact", map[string]any{"review_id": "rev-2"}); err != nil {
		t.Fatalf("coord_resolve_artifact blocked by runtime gate: %v", err)
	}
}

func TestValidateTaskExecutionCall_TesterAllowsIterationWithoutRuntimePrerequisiteLattice(t *testing.T) {
	ctx := taskExecutionContext(&TaskExecutionContract{Stage: "test"})

	for _, toolName := range []string{"write_test", "run_test_suite", "report_to_engineer", "coord_release_scope"} {
		if err := ValidateTaskExecutionCall(ctx, "tester-pipeline", toolName, nil); err != nil {
			t.Fatalf("%s blocked by runtime prerequisite lattice: %v", toolName, err)
		}
	}
	if err := ValidateTaskExecutionCompletion(ctx, "tester-pipeline"); err != nil {
		t.Fatalf("tester completion blocked by runtime gate: %v", err)
	}
}

func TestValidateTaskExecutionCall_DesignerAllowsCreateButRequiresReadForModify(t *testing.T) {
	contract := &TaskExecutionContract{
		RequestedFiles: []RequestedFileOperation{
			{Path: "src/ui/Button.tsx", Operation: "create"},
			{Path: "src/ui/Card.tsx", Operation: "modify"},
		},
	}
	ctx := taskExecutionContext(contract)

	if err := ValidateTaskExecutionCall(ctx, "designer", "write_pipeline_file", map[string]any{"path": "src/ui/Button.tsx"}); err != nil {
		t.Fatalf("create path should be writable without prior read: %v", err)
	}

	if err := ValidateTaskExecutionCall(ctx, "designer", "edit_pipeline_file", map[string]any{"path": "src/ui/Card.tsx"}); err == nil {
		t.Fatal("expected modify path edit to require prior read")
	}
	RecordTaskExecutionSuccess(ctx, "read_workspace_file", map[string]any{"path": "src/ui/Card.tsx"}, "")
	if err := ValidateTaskExecutionCall(ctx, "designer", "edit_pipeline_file", map[string]any{"path": "src/ui/Card.tsx"}); err != nil {
		t.Fatalf("edit_pipeline_file blocked after read_workspace_file: %v", err)
	}
}

func TestValidateTaskExecutionCall_EngineerAllowsCreateButRequiresReadForModify(t *testing.T) {
	contract := &TaskExecutionContract{
		RequestedFiles: []RequestedFileOperation{
			{Path: "src/hello_cli/cli.py", Operation: "create"},
			{Path: "src/hello_cli/config.py", Operation: "modify"},
		},
	}
	ctx := taskExecutionContext(contract)

	if err := ValidateTaskExecutionCall(ctx, "engineer", "write_pipeline_file", map[string]any{"path": "src/hello_cli/cli.py"}); err != nil {
		t.Fatalf("create path should be writable without prior read: %v", err)
	}

	if err := ValidateTaskExecutionCall(ctx, "engineer", "edit_pipeline_file", map[string]any{"path": "src/hello_cli/config.py"}); err == nil {
		t.Fatal("expected modify path edit to require prior read")
	}
	RecordTaskExecutionSuccess(ctx, "read_workspace_file", map[string]any{"path": "src/hello_cli/config.py"}, "")
	if err := ValidateTaskExecutionCall(ctx, "engineer", "edit_pipeline_file", map[string]any{"path": "src/hello_cli/config.py"}); err != nil {
		t.Fatalf("edit_pipeline_file blocked after read: %v", err)
	}
}

func taskExecutionContext(contract *TaskExecutionContract) context.Context {
	ctx := context.Background()
	ctx = WithTaskExecutionContract(ctx, contract)
	ctx = WithTaskExecutionState(ctx, NewTaskExecutionState())
	return ctx
}

package shared

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestEnrichTaskExecutionContractWithWorkspaceEvidence_RebuildsInspectorValidationMode(t *testing.T) {
	task := &PipelineTaskInput{
		TaskID:    "task_1",
		AgentType: "inspector-pipeline",
		Context: map[string]any{
			"pipeline_stage": "inspect",
			"workspace": map[string]any{
				"write_set": []string{"src/hello_cli/cli.py"},
			},
		},
	}

	contract := BuildTaskExecutionContract(task)
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.HasImplementationEvidence {
		t.Fatal("did not expect base contract to have implementation evidence")
	}

	views := stubTaskExecutionWorkspaceViews{
		states: map[string]*versioning.WorkspacePathState{
			"src/hello_cli/cli.py": {
				Path:        "src/hello_cli/cli.py",
				DefaultView: versioning.WorkspaceViewPipeline,
				Disk: versioning.WorkspaceLayerState{
					View:      versioning.WorkspaceViewDisk,
					Available: true,
					Exists:    false,
				},
				Pipeline: &versioning.WorkspaceLayerState{
					View:      versioning.WorkspaceViewPipeline,
					Available: true,
					Exists:    true,
				},
			},
		},
	}

	contract = EnrichTaskExecutionContractWithWorkspaceEvidence(context.Background(), contract, views, task)

	if !contract.HasImplementationEvidence {
		t.Fatal("expected workspace evidence to flip implementation mode")
	}
	if contract.PreImplementation {
		t.Fatal("did not expect enriched contract to stay pre-implementation")
	}
}

type stubTaskExecutionWorkspaceViews struct {
	states map[string]*versioning.WorkspacePathState
}

func (s stubTaskExecutionWorkspaceViews) ReadFile(context.Context, versioning.WorkspaceView, string, string) ([]byte, error) {
	return nil, nil
}

func (s stubTaskExecutionWorkspaceViews) Glob(context.Context, versioning.WorkspaceView, string, string, []string, string) ([]string, error) {
	return nil, nil
}

func (s stubTaskExecutionWorkspaceViews) Grep(context.Context, versioning.WorkspaceView, string, string, string, int, int, string) ([]versioning.GrepMatch, error) {
	return nil, nil
}

func (s stubTaskExecutionWorkspaceViews) InspectPath(_ context.Context, path string, _ string) (*versioning.WorkspacePathState, error) {
	return s.states[path], nil
}

func (s stubTaskExecutionWorkspaceViews) SummarizePaths(context.Context, []string, string) (*versioning.WorkspaceSummary, error) {
	return nil, nil
}

func (s stubTaskExecutionWorkspaceViews) DefaultView() versioning.WorkspaceView {
	return versioning.WorkspaceViewPipeline
}

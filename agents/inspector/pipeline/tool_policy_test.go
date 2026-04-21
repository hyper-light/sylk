package pipeline

import "testing"

func TestPipelineInspectorVisibleSkillsExcludeWorkspaceMutation(t *testing.T) {
	// Phase 2.K / CR-2 refactor: write-ops collapsed into workspace_write
	// and prepare_write_context. The old split names are no longer
	// directly registered on inspector-pipeline — the collapse means
	// inspector-pipeline now sees workspace_write (and must be gated
	// from mutating via agent-specific safety hooks, not by omission
	// from visible skills alone).
	for _, blocked := range []string{
		"prepare_pipeline_write_context",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
		"prepare_write_context",
		"workspace_write",
	} {
		if containsPipelineName(pipelineInspectorVisibleSkillNames(), blocked) {
			t.Fatalf("pipeline inspector visible skills unexpectedly expose %q", blocked)
		}
	}
}

func TestPipelineInspectorMutatingSkillsExcludeWorkspaceMutation(t *testing.T) {
	// Phase 2.K / CR-2 refactor: workspace_write is registered on the
	// inspector for its internal deterministic use, but the safety
	// hook blocks it at invocation time. Individual write skill names
	// are no longer in the mutating list.
	for _, blocked := range []string{
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
	} {
		if containsPipelineName(pipelineInspectorMutatingSkillNames(), blocked) {
			t.Fatalf("pipeline inspector mutating skills unexpectedly expose %q", blocked)
		}
	}
}

func TestPipelineInspectorVisibleSkillsIncludeDependencyInstallRemediation(t *testing.T) {
	// Phase 2.K / GT-4 + GI-5 refactor: research_dependency_install +
	// install_dependency_tooling collapsed into dependency(action=…).
	for _, want := range []string{"dependency", "bash"} {
		if !containsPipelineName(pipelineInspectorVisibleSkillNames(), want) {
			t.Fatalf("pipeline inspector visible skills missing %q", want)
		}
	}
	for _, want := range []string{"dependency", "bash"} {
		if !containsPipelineName(pipelineInspectorMutatingSkillNames(), want) {
			t.Fatalf("pipeline inspector mutating skills missing %q", want)
		}
	}
}

func containsPipelineName(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

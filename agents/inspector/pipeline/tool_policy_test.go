package pipeline

import "testing"

func TestPipelineInspectorVisibleSkillsExcludeWorkspaceMutation(t *testing.T) {
	for _, blocked := range []string{
		"prepare_pipeline_write_context",
		"write_pipeline_file",
		"edit_pipeline_file",
		"delete_pipeline_file",
		"create_pipeline_directory",
	} {
		if containsPipelineName(pipelineInspectorVisibleSkillNames(), blocked) {
			t.Fatalf("pipeline inspector visible skills unexpectedly expose %q", blocked)
		}
	}
}

func TestPipelineInspectorMutatingSkillsExcludeWorkspaceMutation(t *testing.T) {
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
	for _, want := range []string{
		"research_dependency_install",
		"install_dependency_tooling",
		"run_command",
		"run_shell_script",
	} {
		if !containsPipelineName(pipelineInspectorVisibleSkillNames(), want) {
			t.Fatalf("pipeline inspector visible skills missing %q", want)
		}
	}
	for _, want := range []string{"install_dependency_tooling", "run_command", "run_shell_script"} {
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

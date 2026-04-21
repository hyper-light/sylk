package librarian

import "testing"

// TestLibrarianVisibleSkillsAreLayerExplicitOnly pins the post-refactor
// invariant: the librarian's read surface is the unified workspace_read
// verb. workspace_read always requires an explicit `view` parameter, so
// the layer (disk / global / pipeline) is captured at the tool boundary
// and the LLM cannot pick a layerless tool and report "file not found"
// for a file that lives in a VFS overlay.
//
// The bare read_file / glob / grep skills used to coexist as disk-only
// convenience wrappers, but LLM tool-name bias defeated the prompt's
// "use workspace_* for in-flight state" rule and produced the historical
// "librarian opened a disk file that exists only in the pipeline VFS and
// reported it missing" trace. The per-op names (read_workspace_file,
// workspace_glob, workspace_grep, inspect_workspace_state,
// summarize_workspace_state) have since collapsed into workspace_read
// and must NOT reappear on the visible surface.
func TestLibrarianVisibleSkillsAreLayerExplicitOnly(t *testing.T) {
	visible := map[string]struct{}{}
	for _, name := range librarianVisibleSkillNames() {
		visible[name] = struct{}{}
	}

	required := []string{
		"workspace_read",
	}
	for _, name := range required {
		if _, ok := visible[name]; !ok {
			t.Errorf("librarian visible skills must include %q (layer-explicit read surface)", name)
		}
	}

	forbidden := []string{
		"read_file",
		"glob",
		"grep",
		"read_workspace_file",
		"workspace_glob",
		"workspace_grep",
		"inspect_workspace_state",
		"summarize_workspace_state",
	}
	for _, name := range forbidden {
		if _, ok := visible[name]; ok {
			t.Errorf("librarian visible skills must NOT include %q — every read must go through workspace_read(op=…)", name)
		}
	}
}

func TestNew_AllowsLLMModeWithoutSearchSystem(t *testing.T) {
	agent, err := New(Config{
		Factory: newTestFactory(t),
		ID:               "lib-test",
		EnableLLM:        true,
		Model:            "test-model",
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if agent == nil {
		t.Fatal("New() returned nil agent")
	}
}

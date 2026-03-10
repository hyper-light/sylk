package engineer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestRunCommandApprovesLocalPythonScripts(t *testing.T) {
	patterns := DefaultApprovedPatterns()
	if !isCommandApproved("python hello.py", patterns) {
		t.Fatal("python hello.py should be approved")
	}
	if !isCommandApproved("python3 hello.py", patterns) {
		t.Fatal("python3 hello.py should be approved")
	}
}

func TestRunCommandRejectsShellControlOperators(t *testing.T) {
	if !commandHasUnsafeShellSyntax("python hello.py && echo hacked") {
		t.Fatal("expected shell control operators to be rejected")
	}
}

func TestRunCommandMaterializesPipelineVFSWrites(t *testing.T) {
	root := t.TempDir()

	svfs := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	pipe, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: root,
		Files:      []string{"hello.py"},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	fa := svfs.NewPipelineFileAccess(pipe)
	if err := fa.WriteFile(context.Background(), "hello.py", []byte("print('hi from vfs')\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	e, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.SetFileAccess(fa)

	input := json.RawMessage(`{"command":"cat hello.py"}`)
	result := e.skills.Invoke(context.Background(), "run_command", input)
	if !result.Success {
		t.Fatalf("run_command failed: %s", result.Error)
	}

	execResult, ok := result.Data.(*CommandExecution)
	if !ok {
		t.Fatalf("result type = %T, want *CommandExecution", result.Data)
	}
	if !strings.Contains(execResult.Stdout, "hi from vfs") {
		t.Fatalf("stdout = %q, want staged VFS content", execResult.Stdout)
	}
	if execResult.WorkingDir == root {
		t.Fatalf("working dir = %q, want materialized workspace", execResult.WorkingDir)
	}
}

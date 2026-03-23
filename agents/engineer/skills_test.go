package engineer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/purevfs"
	"github.com/adalundhe/sylk/core/versioning"
)

type stubExecutionBroker struct {
	caps purevfs.ExecutionCapabilities
	run  func(context.Context, purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error)
}

func (s stubExecutionBroker) Capabilities(context.Context) (purevfs.ExecutionCapabilities, error) {
	return s.caps, nil
}

func (s stubExecutionBroker) Run(ctx context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
	if s.run == nil {
		return nil, purevfs.ErrStrictExecutionUnavailable
	}
	return s.run(ctx, req)
}

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

func TestRunCommandAllowsQuotedOperatorText(t *testing.T) {
	if commandHasUnsafeShellSyntax(`python -c "print('a && b')"`) {
		t.Fatal("expected quoted operator text to remain valid")
	}
}

func TestRunCommandRejectsPipelineVFSWritesWithoutStrictBroker(t *testing.T) {
	root := t.TempDir()

	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
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
	e.SetExecutionBroker(stubExecutionBroker{})

	input := json.RawMessage(`{"command":"cat hello.py"}`)
	result := e.skills.Invoke(context.Background(), "run_command", input)
	if result.Success {
		t.Fatal("run_command unexpectedly succeeded without a strict execution broker")
	}
	if !strings.Contains(result.Error, purevfs.ErrStrictExecutionUnavailable.Error()) {
		t.Fatalf("run_command error = %q, want %q", result.Error, purevfs.ErrStrictExecutionUnavailable)
	}
}

func TestRunCommandUsesStrictBrokerOverlayWorkspace(t *testing.T) {
	root := t.TempDir()

	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
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
	if err := fa.WriteFile(context.Background(), "hello.py", []byte("print('broker view')\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	e, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.SetFileAccess(fa)
	e.SetExecutionBroker(stubExecutionBroker{
		caps: purevfs.StrictBrokerCapabilities(),
		run: func(ctx context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			if req.Workspace.IsReadOnly() {
				t.Fatal("overlay workspace unexpectedly read-only")
			}
			content, err := req.Workspace.ReadFile(ctx, "hello.py")
			if err != nil {
				return nil, err
			}
			return &purevfs.BrokerRunResult{
				ExitCode: 0,
				Stdout:   content,
			}, nil
		},
	})

	input := json.RawMessage(`{"command":"python hello.py"}`)
	result := e.skills.Invoke(context.Background(), "run_command", input)
	if !result.Success {
		t.Fatalf("run_command error = %v", result.Error)
	}
	execResult, ok := result.Data.(*CommandExecution)
	if !ok {
		t.Fatalf("run_command data = %T, want *CommandExecution", result.Data)
	}
	if got := execResult.Stdout; !strings.Contains(got, "broker view") {
		t.Fatalf("stdout = %q, want broker overlay content", got)
	}
	if execResult.ExecutionStrategy != string(purevfs.StrategyProcessBroker) {
		t.Fatalf("strategy = %q, want %q", execResult.ExecutionStrategy, purevfs.StrategyProcessBroker)
	}
}

func TestRunCommandWrapsDiskWorkspaceReadOnly(t *testing.T) {
	root := t.TempDir()
	fa := versioning.NewDiskFileAccess(root, false)
	if err := fa.WriteFile(context.Background(), "hello.py", []byte("print('ok')\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	e, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.SetFileAccess(fa)
	e.SetExecutionBroker(stubExecutionBroker{
		caps: purevfs.StrictBrokerCapabilities(),
		run: func(ctx context.Context, req purevfs.BrokerRunRequest) (*purevfs.BrokerRunResult, error) {
			if !req.Workspace.IsReadOnly() {
				t.Fatal("disk workspace unexpectedly writable")
			}
			if err := req.Workspace.WriteFile(ctx, "hello.py", []byte("blocked")); err == nil {
				t.Fatal("read-only execution workspace accepted write")
			}
			return &purevfs.BrokerRunResult{
				ExitCode: 0,
				Stdout:   []byte("ok"),
			}, nil
		},
	})

	input := json.RawMessage(`{"command":"python hello.py"}`)
	result := e.skills.Invoke(context.Background(), "run_command", input)
	if !result.Success {
		t.Fatalf("run_command error = %v", result.Error)
	}
}

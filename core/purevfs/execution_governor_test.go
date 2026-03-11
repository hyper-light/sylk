package purevfs

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
)

func TestExecutionGovernorRejectsRunOverflow(t *testing.T) {
	governor := newExecutionGovernor(64, 16, 8, 8)

	guard, err := governor.Admit()
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	defer guard.Release()

	if err := guard.budget.Reserve(8); err != nil {
		t.Fatalf("Reserve(8): %v", err)
	}
	if err := guard.budget.Reserve(1); !errors.Is(err, ErrMemoryPressure) {
		t.Fatalf("Reserve(1) error = %v, want %v", err, ErrMemoryPressure)
	}
}

func TestExecutionCaptureBufferTruncatesOutput(t *testing.T) {
	governor := newExecutionGovernor(64, 32, 8, 10)

	guard, err := governor.Admit()
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	defer guard.Release()

	buffer := newExecutionCaptureBuffer(guard.budget, guard.outputLimit)
	payload := []byte("0123456789abcdef")
	if n, err := buffer.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("Write(...) = (%d, %v), want (%d, nil)", n, err, len(payload))
	}
	if got := string(buffer.Bytes()); got != "0123456789" {
		t.Fatalf("buffer = %q, want %q", got, "0123456789")
	}
	if !buffer.truncated {
		t.Fatal("buffer.truncated = false, want true")
	}
}

func TestMemoryNamespaceRejectsOversizedWrite(t *testing.T) {
	governor := newExecutionGovernor(64, 20, 4, 8)

	guard, err := governor.Admit()
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	defer guard.Release()

	namespace := newMemoryNamespace(guard.budget)
	if err := namespace.WriteFile(context.Background(), "/tmp/a.txt", []byte(strings.Repeat("a", 8))); err != nil {
		t.Fatalf("WriteFile(a.txt): %v", err)
	}
	err = namespace.WriteFile(context.Background(), "/tmp/b.txt", []byte(strings.Repeat("b", 9)))
	if !errors.Is(err, ErrMemoryPressure) {
		t.Fatalf("WriteFile(b.txt) error = %v, want %v", err, ErrMemoryPressure)
	}
}

func TestProjectedRootOpenHandleMissingFileFails(t *testing.T) {
	governor := newExecutionGovernor(64, 16, 4, 8)

	guard, err := governor.Admit()
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	defer guard.Release()

	root, err := newProjectedRoot(BrokerRunRequest{
		Plan: ExecutionPlan{
			Mounts: []MountSpec{
				{Kind: MountTemp, VirtualPath: "/tmp"},
			},
		},
	}, guard.budget)
	if err != nil {
		t.Fatalf("newProjectedRoot: %v", err)
	}

	_, err = root.OpenHandle(context.Background(), "/tmp/missing.txt", false, false)
	if !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("OpenHandle(...) error = %v, want %v", err, syscall.ENOENT)
	}
}

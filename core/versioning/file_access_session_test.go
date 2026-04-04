package versioning

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionRoutingFileAccess_UsesSessionGlobalOverlay(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := WithSessionID(context.Background(), "sess-1")
	target := filepath.Join(dir, "hello.txt")
	if err := svfs.GlobalVFS().Write(context.Background(), target, []byte("overlay")); err != nil {
		t.Fatalf("global write: %v", err)
	}

	router := NewSessionRoutingFileAccess(true, func(sessionID string) *SessionVFS {
		if sessionID == "sess-1" {
			return svfs
		}
		return nil
	}, NewDiskFileAccess(dir, true))

	content, err := router.ReadFile(ctx, target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "overlay" {
		t.Fatalf("content = %q, want overlay content", string(content))
	}
}

func TestSessionRoutingFileAccess_DeniesWritableFallbackWithoutSession(t *testing.T) {
	dir := t.TempDir()
	router := NewSessionRoutingFileAccess(false, func(string) *SessionVFS { return nil }, NewDiskFileAccess(dir, false))

	target := filepath.Join(dir, "hello.txt")
	err := router.WriteFile(context.Background(), target, []byte("disk-bypass"))
	if err != ErrNoActiveSessionVFS {
		t.Fatalf("WriteFile error = %v, want %v", err, ErrNoActiveSessionVFS)
	}

	exists, statErr := NewDiskFileAccess(dir, true).Exists(context.Background(), target)
	if statErr != nil {
		t.Fatalf("Exists: %v", statErr)
	}
	if exists {
		t.Fatal("expected writable fallback to be denied without touching disk")
	}
}

func TestSessionRoutingFileAccess_MkdirAllUsesSessionGlobalOverlay(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	router := NewSessionRoutingFileAccess(false, func(sessionID string) *SessionVFS {
		if sessionID == "sess-1" {
			return svfs
		}
		return nil
	}, NewDiskFileAccess(dir, false))

	ctx := WithSessionID(context.Background(), "sess-1")
	targetDir := filepath.Join("draft", "generated")
	if err := router.MkdirAll(ctx, targetDir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	info, err := svfs.NewGlobalFileAccess(false).Stat(ctx, targetDir)
	if err != nil {
		t.Fatalf("global Stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected routed mkdir target to exist as a directory in the global draft")
	}

	if _, err := os.Stat(filepath.Join(dir, targetDir)); !os.IsNotExist(err) {
		t.Fatalf("expected disk path to remain absent before flush, stat err=%v", err)
	}
}

func TestSessionReviewRoutingFileAccess_UsesActiveReviewOverlay(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(target, []byte("disk"), 0644); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := WithSessionID(context.Background(), "sess-1")
	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
		Files:      []string{"hello.txt"},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	if err := pipe.Write(context.Background(), target, []byte("review")); err != nil {
		t.Fatalf("pipeline write: %v", err)
	}
	candidate, err := svfs.ExtractReviewCandidate("task-1")
	if err != nil {
		t.Fatalf("ExtractReviewCandidate: %v", err)
	}
	if candidate == nil {
		t.Fatal("expected extracted candidate")
	}
	if err := svfs.ActivateReviewCandidate(context.Background(), candidate.ID); err != nil {
		t.Fatalf("ActivateReviewCandidate: %v", err)
	}

	router := NewSessionReviewRoutingFileAccess(true, func(sessionID string) *SessionVFS {
		if sessionID == "sess-1" {
			return svfs
		}
		return nil
	}, NewDiskFileAccess(dir, true))

	content, err := router.ReadFile(ctx, "hello.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(content); got != "review" {
		t.Fatalf("content = %q, want %q", got, "review")
	}
}

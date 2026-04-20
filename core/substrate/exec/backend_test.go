package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/lock"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/projection"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestBackend_New_RejectsMissingDeps(t *testing.T) {
	cases := map[string]Config{
		"missing manifest store": {LockfileResolver: trivialResolver(nil)},
		"missing resolver":       {Manifests: manifest.New(blob.New())},
	}
	for name, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestBackend_Plan_RejectsMissingFields(t *testing.T) {
	b := newTestBackend(t)
	cases := map[string]SpawnRequest{
		"empty pipeline ID": {SessionID: "s", Cmd: []string{"x"}, Platform: linuxPlatform()},
		"empty session ID":  {PipelineID: "p", Cmd: []string{"x"}, Platform: linuxPlatform()},
		"empty cmd":         {PipelineID: "p", SessionID: "s", Platform: linuxPlatform()},
		"empty platform":    {PipelineID: "p", SessionID: "s", Cmd: []string{"x"}},
	}
	for name, req := range cases {
		if _, err := b.Plan(req); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestBackend_Plan_NoLockfileForSession(t *testing.T) {
	b := newTestBackend(t)
	_, err := b.Plan(SpawnRequest{
		PipelineID: "p",
		SessionID:  "missing-session",
		Cmd:        []string{"pytest"},
		Platform:   linuxPlatform(),
	})
	if !errors.Is(err, ErrNoLockfile) {
		t.Fatalf("expected ErrNoLockfile, got %v", err)
	}
}

func TestBackend_Plan_ToolNotProvisioned(t *testing.T) {
	b, lockfile, _ := newTestBackendWithLockfile(t, "session-a")
	_ = lockfile

	_, err := b.Plan(SpawnRequest{
		PipelineID: "p",
		SessionID:  "session-a",
		Cmd:        []string{"never-provisioned"},
		Platform:   linuxPlatform(),
		RequiredTools: []ToolRequirement{
			{Coordinate: lock.Coordinate{Ecosystem: "system-binary", Name: "never-provisioned"}},
		},
	})
	if !errors.Is(err, ErrToolNotProvisioned) {
		t.Fatalf("expected ErrToolNotProvisioned, got %v", err)
	}
}

func TestBackend_Plan_HappyPath_ExplicitRequirement(t *testing.T) {
	b, lockfile, blobs := newTestBackendWithLockfile(t, "session-a")
	rg := pinTool(t, lockfile, blobs, b.manifests, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/bin/rg": "ripgrep binary",
	})

	plan, err := b.Plan(SpawnRequest{
		PipelineID: "pipe-7",
		SessionID:  "session-a",
		Cmd:        []string{"rg", "--version"},
		Platform:   linuxPlatform(),
		RequiredTools: []ToolRequirement{
			{Coordinate: lock.Coordinate{Ecosystem: "system-binary", Name: "ripgrep"}},
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.View.PipelineID != "pipe-7" {
		t.Errorf("View.PipelineID = %q", plan.View.PipelineID)
	}
	if len(plan.Manifests) != 1 || plan.Manifests[0].ID != rg.ID {
		t.Errorf("expected exactly the ripgrep manifest, got %d entries", len(plan.Manifests))
	}
	pathEntry := plan.Env["PATH"]
	if !strings.Contains(pathEntry, "/sylk/views/pipe-7/bin") {
		t.Errorf("PATH = %q, expected to include /sylk/views/pipe-7/bin", pathEntry)
	}
}

func TestBackend_Plan_HappyPath_InferredRequirement(t *testing.T) {
	b, lockfile, blobs := newTestBackendWithLockfile(t, "session-a")
	pinTool(t, lockfile, blobs, b.manifests, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/bin/rg": "ripgrep binary",
	})

	plan, err := b.Plan(SpawnRequest{
		PipelineID: "pipe-7",
		SessionID:  "session-a",
		Cmd:        []string{"ripgrep", "--version"},
		Platform:   linuxPlatform(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Manifests) != 1 {
		t.Errorf("expected inferred-resolution to find 1 manifest, got %d", len(plan.Manifests))
	}
}

func TestBackend_Plan_AttachesToProjection(t *testing.T) {
	b, lockfile, blobs := newTestBackendWithLockfile(t, "session-a")
	pinTool(t, lockfile, blobs, b.manifests, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/bin/rg": "binary",
	})

	if got := len(b.projection.Attachments()); got != 0 {
		t.Fatalf("projection started with %d attachments, want 0", got)
	}

	_, err := b.Plan(SpawnRequest{
		PipelineID: "p",
		SessionID:  "session-a",
		Cmd:        []string{"rg"},
		Platform:   linuxPlatform(),
		RequiredTools: []ToolRequirement{
			{Coordinate: lock.Coordinate{Ecosystem: "system-binary", Name: "ripgrep"}},
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if got := len(b.projection.Attachments()); got != 1 {
		t.Errorf("after Plan, projection has %d attachments, want 1", got)
	}
}

func TestBackend_Spawn_WithoutSandboxReturnsPlan(t *testing.T) {
	b, lockfile, blobs := newTestBackendWithLockfile(t, "session-a")
	pinTool(t, lockfile, blobs, b.manifests, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/bin/rg": "binary",
	})

	result, plan, err := b.Spawn(context.Background(), SpawnRequest{
		PipelineID: "p",
		SessionID:  "session-a",
		Cmd:        []string{"rg"},
		Platform:   linuxPlatform(),
		RequiredTools: []ToolRequirement{
			{Coordinate: lock.Coordinate{Ecosystem: "system-binary", Name: "ripgrep"}},
		},
	})
	if !errors.Is(err, ErrSandboxNotWired) {
		t.Fatalf("expected ErrSandboxNotWired, got %v", err)
	}
	if result != nil {
		t.Error("result should be nil when sandbox not wired")
	}
	if plan == nil {
		t.Fatal("plan should be returned even when sandbox not wired")
	}
}

func TestBackend_Spawn_WithSandboxReturnsResult(t *testing.T) {
	stub := &stubSandbox{result: &SpawnResult{ExitCode: 0, Duration: 5 * time.Millisecond}}
	b, lockfile, blobs := newTestBackendWithLockfile(t, "session-a")
	b.sandbox = stub
	pinTool(t, lockfile, blobs, b.manifests, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/bin/rg": "binary",
	})

	result, _, err := b.Spawn(context.Background(), SpawnRequest{
		PipelineID: "p",
		SessionID:  "session-a",
		Cmd:        []string{"rg"},
		Platform:   linuxPlatform(),
		RequiredTools: []ToolRequirement{
			{Coordinate: lock.Coordinate{Ecosystem: "system-binary", Name: "ripgrep"}},
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d", result.ExitCode)
	}
	if stub.calls != 1 {
		t.Errorf("sandbox.Run called %d times, want 1", stub.calls)
	}
}

func TestBackend_ViewCacheReusesView(t *testing.T) {
	b, lockfile, blobs := newTestBackendWithLockfile(t, "session-a")
	pinTool(t, lockfile, blobs, b.manifests, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/bin/rg": "binary",
	})

	req := SpawnRequest{
		PipelineID: "p",
		SessionID:  "session-a",
		Cmd:        []string{"rg"},
		Platform:   linuxPlatform(),
		RequiredTools: []ToolRequirement{
			{Coordinate: lock.Coordinate{Ecosystem: "system-binary", Name: "ripgrep"}},
		},
	}
	plan1, err := b.Plan(req)
	if err != nil {
		t.Fatalf("Plan 1: %v", err)
	}
	plan2, err := b.Plan(req)
	if err != nil {
		t.Fatalf("Plan 2: %v", err)
	}
	if plan1.View != plan2.View {
		t.Error("expected View to be cached and reused across identical Plan calls")
	}
}

// Helpers.

func newTestBackend(t *testing.T) *Backend {
	t.Helper()
	blobs := blob.New()
	mans := manifest.New(blobs)
	ns, err := projection.New(blobs)
	if err != nil {
		t.Fatalf("projection.New: %v", err)
	}
	b, err := New(Config{
		Manifests:        mans,
		LockfileResolver: trivialResolver(nil),
		Projection:       ns,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func newTestBackendWithLockfile(t *testing.T, sessionID string) (*Backend, *lock.Lockfile, *blob.Store) {
	t.Helper()
	blobs := blob.New()
	mans := manifest.New(blobs)
	ns, err := projection.New(blobs)
	if err != nil {
		t.Fatalf("projection.New: %v", err)
	}
	lockfile, err := lock.New(lock.Config{SessionID: sessionID})
	if err != nil {
		t.Fatalf("lock.New: %v", err)
	}
	resolver := func(s string) (*lock.Lockfile, bool) {
		if s == sessionID {
			return lockfile, true
		}
		return nil, false
	}
	b, err := New(Config{
		Manifests:        mans,
		LockfileResolver: resolver,
		Projection:       ns,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b, lockfile, blobs
}

func pinTool(t *testing.T, lockfile *lock.Lockfile, blobs *blob.Store, mans *manifest.Store, ecosystem, name, version string, files map[string]string) *manifest.Manifest {
	t.Helper()
	entries := make([]manifest.FileEntry, 0, len(files))
	for path, content := range files {
		hash, err := blobs.Put([]byte(content))
		if err != nil {
			t.Fatalf("blob.Put: %v", err)
		}
		entries = append(entries, manifest.FileEntry{
			Path:     path,
			BlobHash: hash,
			Mode:     0o755,
		})
	}
	m := &manifest.Manifest{
		Package: manifest.PackageID{Ecosystem: ecosystem, Name: name, Version: version},
		Files:   entries,
		Metadata: manifest.Metadata{Ecosystem: ecosystem},
	}
	if err := mans.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err := lockfile.Pin(lock.PinRequest{
		Coordinate: lock.Coordinate{Ecosystem: ecosystem, Name: name},
		Version:    version,
		ManifestID: m.ID,
		Witness:    lock.Witness{PipelineID: "p", Constraint: "*"},
	})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	return m
}

func linuxPlatform() recipe.Platform {
	return recipe.Platform{OS: "linux", Arch: "amd64", Libc: "glibc"}
}

func trivialResolver(lockfile *lock.Lockfile) LockfileResolver {
	return func(_ string) (*lock.Lockfile, bool) {
		if lockfile == nil {
			return nil, false
		}
		return lockfile, true
	}
}

type stubSandbox struct {
	result *SpawnResult
	err    error
	calls  int
}

func (s *stubSandbox) Run(_ context.Context, _ *SpawnPlan) (*SpawnResult, error) {
	s.calls++
	return s.result, s.err
}

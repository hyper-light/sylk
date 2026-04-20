package skills

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/exec"
	"github.com/adalundhe/sylk/core/substrate/lock"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/projection"
	"github.com/adalundhe/sylk/core/substrate/provision"
)

func TestBuild_RejectsMissingDeps(t *testing.T) {
	if _, err := Build(Config{}); err == nil {
		t.Error("expected error when provision runtime is nil")
	}
	if _, err := Build(Config{Provision: dummyProvision(t)}); err == nil {
		t.Error("expected error when manifest store is nil")
	}
}

func TestBuild_HappyPath(t *testing.T) {
	cfg := buildTestConfig(t)
	bundle, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if bundle.ProvisionToolDependency == nil {
		t.Error("ProvisionToolDependency missing")
	}
	if bundle.AttachToolToView == nil {
		t.Error("AttachToolToView missing")
	}
	if bundle.QueryView == nil {
		t.Error("QueryView missing")
	}
	if len(bundle.All()) != 3 {
		t.Errorf("All returned %d skills, want 3", len(bundle.All()))
	}
}

func TestSkill_ProvisionToolDependency_NameAndDomain(t *testing.T) {
	bundle, _ := Build(buildTestConfig(t))
	s := bundle.ProvisionToolDependency
	if s.Name != "provision_tool_dependency" {
		t.Errorf("name = %q", s.Name)
	}
	if s.Domain != "substrate" {
		t.Errorf("domain = %q", s.Domain)
	}
}

func TestSkill_ProvisionToolDependency_RejectsMissingFields(t *testing.T) {
	bundle, _ := Build(buildTestConfig(t))
	s := bundle.ProvisionToolDependency

	cases := map[string]string{
		"empty session":   `{"pipeline_id":"p","ecosystem":"x","name":"n","version":"1"}`,
		"empty pipeline":  `{"session_id":"s","ecosystem":"x","name":"n","version":"1"}`,
		"empty ecosystem": `{"session_id":"s","pipeline_id":"p","name":"n","version":"1"}`,
		"empty name":      `{"session_id":"s","pipeline_id":"p","ecosystem":"x","version":"1"}`,
		"empty version":   `{"session_id":"s","pipeline_id":"p","ecosystem":"x","name":"n"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := s.Handler(context.Background(), json.RawMessage(body))
			if err == nil {
				t.Error("expected error for missing field")
			}
		})
	}
}

func TestSkill_ProvisionToolDependency_AlreadyPinned(t *testing.T) {
	cfg, blobs, lockfile := buildTestConfigWithLockfile(t, "session-a")
	pkg := manifest.PackageID{Ecosystem: "system-binary", Name: "ripgrep", Version: "14.1.0"}
	m := registerTestManifest(t, blobs, cfg.Manifests, pkg, map[string]string{"/bin/rg": "binary"})
	_ = lockfile

	bundle, _ := Build(cfg)
	body := `{"session_id":"session-a","pipeline_id":"p","ecosystem":"system-binary","name":"ripgrep","version":"14.1.0"}`
	result, err := bundle.ProvisionToolDependency.Handler(context.Background(), json.RawMessage(body))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	res := result.(*provisionToolDependencyResult)
	if !res.AlreadyPinned {
		t.Error("expected AlreadyPinned = true")
	}
	if res.ManifestID != m.ID.String() {
		t.Errorf("ManifestID = %q, want %q", res.ManifestID, m.ID.String())
	}
}

func TestSkill_AttachToolToView_RoundTrip(t *testing.T) {
	cfg, blobs, _ := buildTestConfigWithLockfile(t, "session-a")
	pkg := manifest.PackageID{Ecosystem: "system-binary", Name: "ripgrep", Version: "14.1.0"}
	m := registerTestManifest(t, blobs, cfg.Manifests, pkg, map[string]string{"/bin/rg": "binary"})

	bundle, _ := Build(cfg)
	body, _ := json.Marshal(map[string]string{
		"manifest_id": m.ID.String(),
		"mount_path":  "/",
	})
	result, err := bundle.AttachToolToView.Handler(context.Background(), body)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	res := result.(*attachToolToViewResult)
	if !res.Attached {
		t.Error("expected Attached = true")
	}
	if res.PackageID != pkg.String() {
		t.Errorf("PackageID = %q", res.PackageID)
	}
}

func TestSkill_AttachToolToView_RejectsUnknownManifest(t *testing.T) {
	cfg := buildTestConfig(t)
	bundle, _ := Build(cfg)
	body := `{"manifest_id":"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"}`
	_, err := bundle.AttachToolToView.Handler(context.Background(), json.RawMessage(body))
	if err == nil {
		t.Fatal("expected error for unknown manifest")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSkill_AttachToolToView_RejectsMalformedID(t *testing.T) {
	cfg := buildTestConfig(t)
	bundle, _ := Build(cfg)
	body := `{"manifest_id":"not-hex"}`
	_, err := bundle.AttachToolToView.Handler(context.Background(), json.RawMessage(body))
	if err == nil {
		t.Fatal("expected error for malformed manifest_id")
	}
}

func TestSkill_QueryView_RoundTrip(t *testing.T) {
	cfg, blobs, lockfile := buildTestConfigWithLockfile(t, "session-a")
	pkg := manifest.PackageID{Ecosystem: "system-binary", Name: "ripgrep", Version: "14.1.0"}
	m := registerTestManifest(t, blobs, cfg.Manifests, pkg, map[string]string{"/bin/rg": "binary"})
	pinTestTool(t, lockfile, pkg, m.ID, "p")

	bundle, _ := Build(cfg)
	body := `{"session_id":"session-a","pipeline_id":"p","platform_os":"linux","platform_arch":"amd64","platform_libc":"glibc"}`
	result, err := bundle.QueryView.Handler(context.Background(), json.RawMessage(body))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	res := result.(*queryViewResult)
	if res.ViewID == "" {
		t.Error("ViewID empty")
	}
	if len(res.AttachedManifests) != 1 {
		t.Errorf("AttachedManifests count = %d, want 1", len(res.AttachedManifests))
	}
	if res.EnvExports["PATH"] == "" {
		t.Error("PATH empty in env exports")
	}
}

func TestSkill_QueryView_RejectsMissingPlatform(t *testing.T) {
	cfg, _, _ := buildTestConfigWithLockfile(t, "session-a")
	bundle, _ := Build(cfg)
	body := `{"session_id":"s","pipeline_id":"p"}`
	_, err := bundle.QueryView.Handler(context.Background(), json.RawMessage(body))
	if err == nil {
		t.Fatal("expected error for missing platform")
	}
}

// Helpers.

func buildTestConfig(t *testing.T) Config {
	t.Helper()
	cfg, _, _ := buildTestConfigWithLockfile(t, "")
	return cfg
}

func buildTestConfigWithLockfile(t *testing.T, sessionID string) (Config, *blob.Store, *lock.Lockfile) {
	t.Helper()
	blobs := blob.New()
	mans := manifest.New(blobs)
	rt, err := provision.New(provision.Config{Blobs: blobs, Manifests: mans})
	if err != nil {
		t.Fatalf("provision.New: %v", err)
	}
	ns, err := projection.New(blobs)
	if err != nil {
		t.Fatalf("projection.New: %v", err)
	}
	var lockfile *lock.Lockfile
	resolver := func(string) (*lock.Lockfile, bool) { return nil, false }
	if sessionID != "" {
		lockfile, err = lock.New(lock.Config{SessionID: sessionID})
		if err != nil {
			t.Fatalf("lock.New: %v", err)
		}
		resolver = func(s string) (*lock.Lockfile, bool) {
			if s == sessionID {
				return lockfile, true
			}
			return nil, false
		}
	}
	backend, err := exec.New(exec.Config{
		Manifests:        mans,
		LockfileResolver: resolver,
		Projection:       ns,
	})
	if err != nil {
		t.Fatalf("exec.New: %v", err)
	}
	cfg := Config{
		Provision:        rt,
		Manifests:        mans,
		Projection:       ns,
		Backend:          backend,
		LockfileResolver: resolver,
	}
	return cfg, blobs, lockfile
}

func dummyProvision(t *testing.T) *provision.Runtime {
	t.Helper()
	rt, err := provision.New(provision.Config{Blobs: blob.New(), Manifests: manifest.New(blob.New())})
	if err != nil {
		t.Fatalf("provision.New: %v", err)
	}
	return rt
}

func registerTestManifest(t *testing.T, blobs *blob.Store, mans *manifest.Store, pkg manifest.PackageID, files map[string]string) *manifest.Manifest {
	t.Helper()
	entries := make([]manifest.FileEntry, 0, len(files))
	for path, content := range files {
		hash, err := blobs.Put([]byte(content))
		if err != nil {
			t.Fatalf("blob.Put: %v", err)
		}
		entries = append(entries, manifest.FileEntry{Path: path, BlobHash: hash, Mode: 0o755})
	}
	m := &manifest.Manifest{
		Package:  pkg,
		Files:    entries,
		Metadata: manifest.Metadata{Ecosystem: pkg.Ecosystem},
	}
	if err := mans.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return m
}

func pinTestTool(t *testing.T, lockfile *lock.Lockfile, pkg manifest.PackageID, manifestID manifest.ID, pipelineID string) {
	t.Helper()
	_, err := lockfile.Pin(lock.PinRequest{
		Coordinate: lock.Coordinate{Ecosystem: pkg.Ecosystem, Name: pkg.Name},
		Version:    pkg.Version,
		ManifestID: manifestID,
		Witness:    lock.Witness{PipelineID: pipelineID, Constraint: "*"},
	})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
}

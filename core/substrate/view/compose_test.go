package view

import (
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestCompose_RejectsEmptyPipelineID(t *testing.T) {
	_, err := Compose(ComposeRequest{Platform: linuxPlatform()})
	if !errors.Is(err, ErrEmptyPipelineID) {
		t.Fatalf("expected ErrEmptyPipelineID, got %v", err)
	}
}

func TestCompose_RejectsEmptyPlatform(t *testing.T) {
	_, err := Compose(ComposeRequest{PipelineID: "p"})
	if !errors.Is(err, ErrEmptyPlatform) {
		t.Fatalf("expected ErrEmptyPlatform, got %v", err)
	}
}

func TestCompose_RejectsNilManifestInSpec(t *testing.T) {
	_, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
		Manifests:  []ManifestSpec{{Manifest: nil, MountPath: "/x"}},
	})
	if !errors.Is(err, ErrNilManifest) {
		t.Fatalf("expected ErrNilManifest, got %v", err)
	}
}

func TestCompose_DefaultMountRootDerivedFromPipelineID(t *testing.T) {
	v, err := Compose(ComposeRequest{
		PipelineID: "pipe-7",
		Platform:   linuxPlatform(),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "/sylk/views/pipe-7"
	if v.MountRoot != want {
		t.Errorf("MountRoot = %q, want %q", v.MountRoot, want)
	}
}

func TestCompose_RespectsExplicitMountRoot(t *testing.T) {
	v, err := Compose(ComposeRequest{
		PipelineID: "pipe-7",
		MountRoot:  "/custom/root",
		Platform:   linuxPlatform(),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if v.MountRoot != "/custom/root" {
		t.Errorf("MountRoot = %q", v.MountRoot)
	}
}

func TestCompose_LayoutComposesPaths(t *testing.T) {
	blobs := blob.New()
	pyt := registerSampleManifest(t, blobs, "python", "pytest", "8.3.2", map[string]string{
		"/__init__.py": "pytest",
	})

	v, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
		Manifests: []ManifestSpec{
			{Manifest: pyt, MountPath: "/python-3.13/site-packages/pytest", Ecosystems: []string{"python"}},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "/python-3.13/site-packages/pytest/__init__.py"
	if _, ok := v.Layout.Paths[want]; !ok {
		t.Errorf("composed layout missing %q (got %v)", want, keysOf(v.Layout.Paths))
	}
}

func TestCompose_PathConflictsRejected(t *testing.T) {
	blobs := blob.New()
	a := registerSampleManifest(t, blobs, "x", "a", "1", map[string]string{"/conflict": "from-a"})
	b := registerSampleManifest(t, blobs, "x", "b", "1", map[string]string{"/conflict": "from-b"})

	_, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
		Manifests: []ManifestSpec{
			{Manifest: a, MountPath: "/", Ecosystems: []string{"custom"}},
			{Manifest: b, MountPath: "/", Ecosystems: []string{"custom"}},
		},
	})
	if !errors.Is(err, ErrPathConflict) {
		t.Fatalf("expected ErrPathConflict, got %v", err)
	}
}

func TestCompose_EnvironmentEcosystemContributions(t *testing.T) {
	blobs := blob.New()
	pyt := registerSampleManifest(t, blobs, "python", "pytest", "8.3.2", map[string]string{
		"/dummy": "x",
	})

	v, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
		Manifests: []ManifestSpec{
			{Manifest: pyt, MountPath: "/python-3.13/site-packages/pytest", Ecosystems: []string{"python"}},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "/sylk/views/p/python-3.13/site-packages/pytest/site-packages"
	if !contains(v.Env.LanguagePaths["PYTHONPATH"], want) {
		t.Errorf("PYTHONPATH = %v, want to contain %q", v.Env.LanguagePaths["PYTHONPATH"], want)
	}
}

func TestCompose_EnvironmentBinaryContributesPATH(t *testing.T) {
	blobs := blob.New()
	rg := registerSampleManifest(t, blobs, "system-binary", "ripgrep", "14.1.0", map[string]string{
		"/bin/rg": "ripgrep binary",
	})

	v, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
		Manifests: []ManifestSpec{
			{Manifest: rg, MountPath: "/", Ecosystems: []string{"system-binary"}},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "/sylk/views/p/bin"
	if !contains(v.Env.PATH, want) {
		t.Errorf("PATH = %v, want to contain %q", v.Env.PATH, want)
	}
}

func TestCompose_AuxiliaryDirsDefaults(t *testing.T) {
	v, err := Compose(ComposeRequest{
		PipelineID: "pipe-7",
		Platform:   linuxPlatform(),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, key := range []string{"HOME", "TMPDIR", "XDG_CACHE_HOME"} {
		if v.Env.AuxiliaryDirs[key] == "" {
			t.Errorf("AuxiliaryDirs[%q] is empty, expected default", key)
		}
	}
}

func TestCompose_Layer4_LinuxBindMounts(t *testing.T) {
	v, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if v.Layer4.Linux == nil {
		t.Fatal("expected Linux layer 4 plan")
	}
	if v.Layer4.Darwin != nil || v.Layer4.Windows != nil {
		t.Error("non-Linux platforms should be nil for linux composition")
	}
	if len(v.Layer4.Linux.BindMounts) == 0 {
		t.Error("expected at least one bind mount in default Linux plan")
	}
}

func TestCompose_Layer4_DarwinAndWindows(t *testing.T) {
	for _, os := range []string{"darwin", "windows"} {
		t.Run(os, func(t *testing.T) {
			v, err := Compose(ComposeRequest{
				PipelineID: "p",
				Platform:   recipe.Platform{OS: os, Arch: "amd64"},
			})
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			switch os {
			case "darwin":
				if v.Layer4.Darwin == nil {
					t.Error("expected Darwin layer 4 plan")
				}
			case "windows":
				if v.Layer4.Windows == nil {
					t.Error("expected Windows layer 4 plan")
				}
			}
		})
	}
}

func TestCompose_DeterministicID(t *testing.T) {
	blobs := blob.New()
	m := registerSampleManifest(t, blobs, "x", "a", "1", map[string]string{"/file": "content"})

	build := func() *View {
		v, err := Compose(ComposeRequest{
			PipelineID: "p",
			Platform:   linuxPlatform(),
			Manifests:  []ManifestSpec{{Manifest: m, MountPath: "/", Ecosystems: []string{"custom"}}},
		})
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		return v
	}
	v1 := build()
	v2 := build()
	if v1.ID != v2.ID {
		t.Errorf("identical inputs produced different View IDs (%s vs %s)", v1.ID, v2.ID)
	}
}

func TestCompose_DifferentInputsDifferentIDs(t *testing.T) {
	blobs := blob.New()
	a := registerSampleManifest(t, blobs, "x", "a", "1", map[string]string{"/file": "content-a"})
	b := registerSampleManifest(t, blobs, "x", "b", "1", map[string]string{"/file": "content-b"})

	build := func(m *manifest.Manifest) *View {
		v, _ := Compose(ComposeRequest{
			PipelineID: "p",
			Platform:   linuxPlatform(),
			Manifests:  []ManifestSpec{{Manifest: m, MountPath: "/", Ecosystems: []string{"custom"}}},
		})
		return v
	}
	if build(a).ID == build(b).ID {
		t.Error("different manifests produced identical View IDs")
	}
}

func TestCompose_DifferentPlatformsDifferentIDs(t *testing.T) {
	build := func(p recipe.Platform) ID {
		v, _ := Compose(ComposeRequest{PipelineID: "p", Platform: p})
		return v.ID
	}
	if build(linuxPlatform()) == build(recipe.Platform{OS: "darwin", Arch: "amd64"}) {
		t.Error("different platforms produced identical View IDs")
	}
}

func TestCompose_UnknownEcosystemSilentlySkipped(t *testing.T) {
	blobs := blob.New()
	m := registerSampleManifest(t, blobs, "x", "a", "1", map[string]string{"/file": "content"})

	v, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
		Manifests:  []ManifestSpec{{Manifest: m, MountPath: "/", Ecosystems: []string{"made-up-ecosystem"}}},
	})
	if err != nil {
		t.Fatalf("unknown ecosystem should not fail Compose, got %v", err)
	}
	if v.ID.IsZero() {
		t.Fatal("expected non-zero View ID")
	}
}

func TestCompose_MultiManifestEnvAggregation(t *testing.T) {
	blobs := blob.New()
	pyt := registerSampleManifest(t, blobs, "python", "pytest", "8.3.2", map[string]string{"/dummy": "x"})
	rg := registerSampleManifest(t, blobs, "system-binary", "ripgrep", "14.1.0", map[string]string{"/bin/rg": "binary"})

	v, err := Compose(ComposeRequest{
		PipelineID: "p",
		Platform:   linuxPlatform(),
		Manifests: []ManifestSpec{
			{Manifest: pyt, MountPath: "/python", Ecosystems: []string{"python"}},
			{Manifest: rg, MountPath: "/", Ecosystems: []string{"system-binary"}},
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(v.Env.PATH) == 0 {
		t.Error("PATH should have at least the ripgrep contribution")
	}
	if len(v.Env.LanguagePaths["PYTHONPATH"]) == 0 {
		t.Error("PYTHONPATH should have at least the pytest contribution")
	}
}

// Helpers.

func linuxPlatform() recipe.Platform {
	return recipe.Platform{OS: "linux", Arch: "amd64", Libc: "glibc"}
}

func registerSampleManifest(t *testing.T, blobs *blob.Store, ecosystem, name, version string, files map[string]string) *manifest.Manifest {
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
			Mode:     0o644,
		})
	}
	m := &manifest.Manifest{
		Package: manifest.PackageID{Ecosystem: ecosystem, Name: name, Version: version},
		Files:   entries,
	}
	store := manifest.New(blobs)
	if err := store.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return m
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func keysOf(m map[string]ResolvedPath) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

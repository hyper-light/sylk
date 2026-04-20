package provision

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/provision/extractor"
	"github.com/adalundhe/sylk/core/substrate/provision/fetcher"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

func TestProvision_EndToEnd_DiskTarGz(t *testing.T) {
	files := map[string]string{
		"sample-1.0/bin/tool":     "binary content",
		"sample-1.0/share/doc.md": "doc content",
	}
	archive := buildTarGz(t, files)
	archivePath := writeFixture(t, archive)

	rcp := recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{
				Source: "disk://" + archivePath,
				Hash:   recipe.Hash{Algorithm: "sha256", Digest: sha256Sum(archive)},
			},
		},
		Verify: recipe.VerifySpec{HashAlgorithm: "sha256", PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{
			Format:      recipe.ExtractFormatTarGz,
			StripPrefix: "sample-1.0/",
		},
		Output: []recipe.OutputMapping{
			{SubstratePath: "/bin/tool", SourcePath: "/bin/tool", Mode: 0755},
			{SubstratePath: "/share/doc.md", SourcePath: "/share/doc.md", Mode: 0644},
		},
		Metadata: recipe.RecipeMetadata{
			Name:      "sample",
			Version:   "1.0.0",
			Ecosystem: "test",
		},
	}

	r := newTestRuntime(t)
	result, err := r.Provision(context.Background(), ProvisionRequest{Recipe: &rcp})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	assertProvisionResult(t, r, result, len(files))
}

func TestProvision_EndToEnd_HTTPS(t *testing.T) {
	files := map[string]string{"top/x": "content"}
	archive := buildTarGz(t, files)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	cfg := fetcher.DefaultHTTPSConfig()
	cfg.InsecureSkipVerify = true
	fetchers := fetcher.NewRegistry()
	fetchers.Install(fetcher.NewHTTPSFetcher(cfg))
	fetchers.Install(fetcher.NewDiskFetcher())

	rcp := recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{Source: server.URL + "/x.tar.gz", Hash: recipe.Hash{Algorithm: "sha256", Digest: sha256Sum(archive)}},
		},
		Verify:  recipe.VerifySpec{HashAlgorithm: "sha256", PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{Format: recipe.ExtractFormatTarGz, StripPrefix: "top/"},
		Output:  []recipe.OutputMapping{{SubstratePath: "/x", SourcePath: "/x", Mode: 0o644}},
		Metadata: recipe.RecipeMetadata{
			Name:      "https-sample",
			Version:   "0.1.0",
			Ecosystem: "test",
		},
	}

	blobs := blob.New()
	mans := manifest.New(blobs)
	rt, err := New(Config{
		Blobs:      blobs,
		Manifests:  mans,
		Fetchers:   fetchers,
		Extractors: extractor.NewDefaultRegistry(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := rt.Provision(context.Background(), ProvisionRequest{Recipe: &rcp})
	if err != nil {
		t.Fatalf("Provision over HTTPS: %v", err)
	}
	if result.ManifestID.IsZero() {
		t.Fatal("expected non-zero ManifestID")
	}
}

func TestProvision_RejectsBuildRecipes(t *testing.T) {
	r := newTestRuntime(t)
	rcp := minimalDataRecipe(t, []byte("anything"))
	rcp.Build = []recipe.BuildStep{{Cmd: []string{"echo", "hi"}}}

	_, err := r.Provision(context.Background(), ProvisionRequest{Recipe: &rcp})
	if !errors.Is(err, ErrBuildNotYetSupported) {
		t.Fatalf("expected ErrBuildNotYetSupported, got %v", err)
	}
}

func TestProvision_RejectsInvalidRecipe(t *testing.T) {
	r := newTestRuntime(t)
	bad := recipe.Recipe{SchemaVersion: 999} // missing fetch, output, etc.
	_, err := r.Provision(context.Background(), ProvisionRequest{Recipe: &bad})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestProvision_FailsOnHashMismatch(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"x": "y"})
	archivePath := writeFixture(t, archive)

	rcp := recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{Source: "disk://" + archivePath, Hash: recipe.Hash{Algorithm: "sha256", Digest: sha256Sum([]byte("wrong"))}},
		},
		Verify:  recipe.VerifySpec{PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{Format: recipe.ExtractFormatTarGz},
		Output:  []recipe.OutputMapping{{SubstratePath: "/x", SourcePath: "/x"}},
	}

	r := newTestRuntime(t)
	_, err := r.Provision(context.Background(), ProvisionRequest{Recipe: &rcp})
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

func TestProvision_FailsWhenOutputMatchesNothing(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"actual/file": "content"})
	archivePath := writeFixture(t, archive)

	rcp := recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{Source: "disk://" + archivePath, Hash: recipe.Hash{Algorithm: "sha256", Digest: sha256Sum(archive)}},
		},
		Verify:  recipe.VerifySpec{PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{Format: recipe.ExtractFormatTarGz},
		Output: []recipe.OutputMapping{
			{SubstratePath: "/this/does/not/match", SourcePath: "/nothing/here"},
		},
	}

	r := newTestRuntime(t)
	_, err := r.Provision(context.Background(), ProvisionRequest{Recipe: &rcp})
	if !errors.Is(err, ErrEmptyOutputMapping) {
		t.Fatalf("expected ErrEmptyOutputMapping, got %v", err)
	}
}

func TestProvision_RecursiveOutputMapping(t *testing.T) {
	archive := buildTarGz(t, map[string]string{
		"top/a/x.txt": "x",
		"top/a/y.txt": "y",
		"top/b/z.txt": "z",
	})
	archivePath := writeFixture(t, archive)

	rcp := recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{Source: "disk://" + archivePath, Hash: recipe.Hash{Algorithm: "sha256", Digest: sha256Sum(archive)}},
		},
		Verify:  recipe.VerifySpec{PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{Format: recipe.ExtractFormatTarGz, StripPrefix: "top/"},
		Output: []recipe.OutputMapping{
			{SubstratePath: "/captured/", SourcePath: "/a/", Mode: 0o644},
		},
		Metadata: recipe.RecipeMetadata{Name: "rec", Version: "1", Ecosystem: "test"},
	}

	r := newTestRuntime(t)
	result, err := r.Provision(context.Background(), ProvisionRequest{Recipe: &rcp})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	mans := r.manifests
	m, ok := mans.Get(result.ManifestID)
	if !ok {
		t.Fatal("manifest not found in store")
	}

	gotPaths := pathsOf(m.Files)
	wantSet := map[string]bool{"/captured/x.txt": true, "/captured/y.txt": true}
	for _, p := range gotPaths {
		if !wantSet[p] {
			t.Errorf("unexpected captured path: %q", p)
		}
	}
	if len(gotPaths) != len(wantSet) {
		t.Errorf("captured %d paths, want %d (got=%v)", len(gotPaths), len(wantSet), gotPaths)
	}
}

func TestProvision_DeduplicatesAcrossEntries(t *testing.T) {
	// Two different paths with identical content should share one blob.
	sameContent := "shared content bytes"
	archive := buildTarGz(t, map[string]string{
		"top/file_a": sameContent,
		"top/file_b": sameContent,
	})
	archivePath := writeFixture(t, archive)

	rcp := recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{Source: "disk://" + archivePath, Hash: recipe.Hash{Algorithm: "sha256", Digest: sha256Sum(archive)}},
		},
		Verify:  recipe.VerifySpec{PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{Format: recipe.ExtractFormatTarGz, StripPrefix: "top/"},
		Output: []recipe.OutputMapping{
			{SubstratePath: "/a", SourcePath: "/file_a"},
			{SubstratePath: "/b", SourcePath: "/file_b"},
		},
		Metadata: recipe.RecipeMetadata{Name: "dedup", Version: "1", Ecosystem: "test"},
	}

	r := newTestRuntime(t)
	result, err := r.Provision(context.Background(), ProvisionRequest{Recipe: &rcp})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.BlobsCreated != 1 {
		t.Errorf("BlobsCreated = %d, want 1 (identical content should dedup)", result.BlobsCreated)
	}
	if result.BlobsReused != 1 {
		t.Errorf("BlobsReused = %d, want 1", result.BlobsReused)
	}
}

func TestProvision_PlatformSubstitution(t *testing.T) {
	// Recipe references {os}/{arch} in source URL; the runtime
	// substitutes from the request's Platform.
	archive := buildTarGz(t, map[string]string{"x": "y"})
	dir := t.TempDir()
	hostPath := filepath.Join(dir, "linux", "amd64", "fixture.tar.gz")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(hostPath, archive, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rcp := recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{Source: "disk://" + dir + "/{os}/{arch}/fixture.tar.gz",
				Hash: recipe.Hash{Algorithm: "sha256", Digest: sha256Sum(archive)}},
		},
		Verify:  recipe.VerifySpec{PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{Format: recipe.ExtractFormatTarGz},
		Output:  []recipe.OutputMapping{{SubstratePath: "/x", SourcePath: "/x"}},
		Metadata: recipe.RecipeMetadata{Name: "subst", Version: "1", Ecosystem: "test"},
	}

	r := newTestRuntime(t)
	_, err := r.Provision(context.Background(), ProvisionRequest{
		Recipe:   &rcp,
		Platform: recipe.Platform{OS: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatalf("Provision with platform substitution: %v", err)
	}
}

// Helpers.

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	blobs := blob.New()
	mans := manifest.New(blobs)
	rt, err := New(Config{Blobs: blobs, Manifests: mans})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rt
}

func minimalDataRecipe(t *testing.T, content []byte) recipe.Recipe {
	t.Helper()
	archivePath := writeFixture(t, content)
	return recipe.Recipe{
		SchemaVersion: recipe.SchemaVersion,
		Fetch: []recipe.FetchSpec{
			{Source: "disk://" + archivePath, Hash: recipe.Hash{Algorithm: "sha256", Digest: sha256Sum(content)}},
		},
		Verify:  recipe.VerifySpec{PolicyClass: recipe.VerifyStrict},
		Extract: recipe.ExtractSpec{Format: recipe.ExtractFormatRaw},
		Output: []recipe.OutputMapping{
			{SubstratePath: "/data", SourcePath: "/data", Mode: 0644},
		},
		Metadata: recipe.RecipeMetadata{Name: "minimal", Version: "1", Ecosystem: "test"},
	}
}

func writeFixture(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Size:     int64(len(content)),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(tarBuf.Bytes()); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return gzBuf.Bytes()
}

func sha256Sum(data []byte) []byte {
	d := sha256.Sum256(data)
	return d[:]
}

func assertProvisionResult(t *testing.T, r *Runtime, result *ProvisionResult, expectedFiles int) {
	t.Helper()
	if result.ManifestID.IsZero() {
		t.Fatal("expected non-zero ManifestID")
	}
	m, ok := r.manifests.Get(result.ManifestID)
	if !ok {
		t.Fatalf("manifest %s not found in store", result.ManifestID)
	}
	if len(m.Files) != expectedFiles {
		t.Errorf("manifest has %d files, want %d", len(m.Files), expectedFiles)
	}
}

func pathsOf(files []manifest.FileEntry) []string {
	out := make([]string, len(files))
	for i := range files {
		out[i] = files[i].Path
	}
	return out
}

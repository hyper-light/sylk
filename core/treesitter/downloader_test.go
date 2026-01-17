package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewGrammarDownloader(t *testing.T) {
	d, err := NewGrammarDownloader()
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	if d.cacheDir == "" {
		t.Error("cacheDir should not be empty")
	}

	if _, err := os.Stat(d.cacheDir); err != nil {
		t.Errorf("cacheDir should exist: %v", err)
	}
}

func TestDownloaderOptions(t *testing.T) {
	tmpDir := t.TempDir()

	auditCb := func(entry AuditEntry) {}

	permCb := func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
		return PermissionResponse{Granted: true, Scope: PermissionGrantedOnce}, nil
	}

	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
		WithDownloadBaseURL("https://example.com"),
		WithAuditCallback(auditCb),
		WithPermissionCallback(permCb),
		WithSandboxEnabled(true),
		WithAllowedDomains([]string{"example.com"}),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	if d.baseURL != "https://example.com" {
		t.Errorf("baseURL = %q, want https://example.com", d.baseURL)
	}

	if !d.sandboxEnabled {
		t.Error("sandboxEnabled should be true")
	}

	if len(d.allowedDomains) != 1 || d.allowedDomains[0] != "example.com" {
		t.Errorf("allowedDomains = %v", d.allowedDomains)
	}
}

func TestDownloaderListAvailable(t *testing.T) {
	d, err := NewGrammarDownloader()
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	available := d.ListAvailable()
	if len(available) == 0 {
		t.Error("ListAvailable should return known grammars")
	}

	found := false
	for _, name := range available {
		if name == "go" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListAvailable should include 'go'")
	}
}

func TestDownloaderListInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "grammars")
	os.MkdirAll(cacheDir, 0755)

	d, err := NewGrammarDownloader(WithDownloadCacheDir(cacheDir))
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	installed, err := d.ListInstalled()
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}

	if len(installed) != 0 {
		t.Errorf("ListInstalled should return empty for fresh cache, got %v", installed)
	}

	fakeLib := filepath.Join(cacheDir, grammarLibName("go"))
	os.WriteFile(fakeLib, []byte("fake"), 0644)

	installed, err = d.ListInstalled()
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}

	if len(installed) != 1 || installed[0] != "go" {
		t.Errorf("ListInstalled = %v, want [go]", installed)
	}
}

func TestDownloaderPermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()

	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
		WithPermissionCallback(func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
			return PermissionResponse{Granted: false, Reason: "user denied"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	ctx := context.Background()
	_, err = d.EnsureGrammar(ctx, "go")
	if err == nil {
		t.Error("EnsureGrammar should fail when permission denied")
	}
}

func TestDownloaderNoPermissionCallback(t *testing.T) {
	tmpDir := t.TempDir()

	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	ctx := context.Background()
	_, err = d.EnsureGrammar(ctx, "go")
	if err == nil {
		t.Error("EnsureGrammar should fail when no permission callback and grammar not installed")
	}
}

func TestDownloaderAuditLogging(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "grammars")

	var entries []AuditEntry
	auditCb := func(entry AuditEntry) {
		entries = append(entries, entry)
	}

	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(cacheDir),
		WithAuditCallback(auditCb),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	fakeLib := filepath.Join(cacheDir, grammarLibName("go"))
	os.WriteFile(fakeLib, []byte("fake"), 0644)

	ctx := context.Background()
	_, err = d.EnsureGrammar(ctx, "go")
	if err != nil {
		t.Fatalf("EnsureGrammar: %v", err)
	}

	if len(entries) == 0 {
		t.Error("audit callback should have been called")
	}

	if entries[0].Action != "grammar_cache_hit" {
		t.Errorf("first audit entry action = %q, want grammar_cache_hit", entries[0].Action)
	}
}

func TestDownloaderSessionPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	callCount := 0
	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
		WithPermissionCallback(func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
			callCount++
			return PermissionResponse{Granted: true, Scope: PermissionGrantedSession}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	ctx := context.Background()

	d.EnsureGrammar(ctx, "go")
	d.EnsureGrammar(ctx, "go")

	if callCount != 1 {
		t.Errorf("permission callback called %d times, want 1 (session scope)", callCount)
	}
}

func TestDownloaderAlwaysPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	callCount := 0
	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
		WithPermissionCallback(func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
			callCount++
			return PermissionResponse{Granted: true, Scope: PermissionGrantedAlways}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	status := d.GetPermissionStatus("go")
	if status != PermissionDenied {
		t.Errorf("initial status = %d, want PermissionDenied", status)
	}

	ctx := context.Background()
	d.EnsureGrammar(ctx, "go")

	status = d.GetPermissionStatus("go")
	if status != PermissionGrantedAlways {
		t.Errorf("after grant status = %d, want PermissionGrantedAlways", status)
	}
}

func TestDownloaderRevokeSessionPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	callCount := 0
	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
		WithPermissionCallback(func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
			callCount++
			return PermissionResponse{Granted: true, Scope: PermissionGrantedSession}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	ctx := context.Background()

	d.EnsureGrammar(ctx, "go")
	firstCallCount := callCount

	d.EnsureGrammar(ctx, "go")
	if callCount != firstCallCount {
		t.Errorf("second call should use session permission, got %d calls", callCount)
	}

	d.RevokeSessionPermissions()

	status := d.GetPermissionStatus("go")
	if status != PermissionDenied {
		t.Errorf("after revoke, status = %d, want PermissionDenied", status)
	}
}

func TestDownloaderClean(t *testing.T) {
	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "grammar-build")
	os.MkdirAll(buildDir, 0755)
	os.WriteFile(filepath.Join(buildDir, "temp"), []byte("data"), 0644)

	d, err := NewGrammarDownloader(WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")))
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}
	d.buildDir = buildDir

	if err := d.Clean(); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	entries, _ := os.ReadDir(buildDir)
	if len(entries) != 0 {
		t.Error("Clean should remove build dir contents")
	}
}

func TestDownloaderChecksumVerification(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "grammars")
	os.MkdirAll(cacheDir, 0755)

	d, err := NewGrammarDownloader(WithDownloadCacheDir(cacheDir))
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	libPath := filepath.Join(cacheDir, "test.so")
	os.WriteFile(libPath, []byte("test content"), 0644)

	d.RegisterChecksum("test", "wrong_checksum")

	err = d.VerifyChecksum("test", libPath)
	if err == nil {
		t.Error("VerifyChecksum should fail with wrong checksum")
	}

	err = d.VerifyChecksum("unregistered", libPath)
	if err != nil {
		t.Errorf("VerifyChecksum should pass for unregistered grammar: %v", err)
	}
}

func TestExtractGrammarName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"libtree-sitter-go.dylib", "go"},
		{"libtree-sitter-python.so", "python"},
		{"tree-sitter-rust.dll", "rust"},
		{"libtree-sitter-typescript.dylib", "typescript"},
	}

	for _, tt := range tests {
		got := extractGrammarName(tt.input)
		if got != tt.want {
			t.Errorf("extractGrammarName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDownloaderUnknownGrammar(t *testing.T) {
	tmpDir := t.TempDir()

	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
		WithPermissionCallback(func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
			return PermissionResponse{Granted: true, Scope: PermissionGrantedOnce}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	ctx := context.Background()
	_, err = d.EnsureGrammar(ctx, "unknown_language_xyz")
	if err == nil {
		t.Error("EnsureGrammar should fail for unknown grammar")
	}
}

func TestPermissionRequestFields(t *testing.T) {
	tmpDir := t.TempDir()

	var capturedReq PermissionRequest
	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(filepath.Join(tmpDir, "grammars")),
		WithPermissionCallback(func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
			capturedReq = req
			return PermissionResponse{Granted: false, Reason: "test"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	d.RegisterChecksum("go", "expected_checksum")

	ctx := context.Background()
	d.EnsureGrammar(ctx, "go")

	if capturedReq.Grammar != "go" {
		t.Errorf("Grammar = %q, want go", capturedReq.Grammar)
	}
	if capturedReq.Action != "download_and_compile" {
		t.Errorf("Action = %q, want download_and_compile", capturedReq.Action)
	}
	if !capturedReq.RequireUser {
		t.Error("RequireUser should be true")
	}
	if capturedReq.Checksum != "expected_checksum" {
		t.Errorf("Checksum = %q, want expected_checksum", capturedReq.Checksum)
	}
}

func TestAuditEntryTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "grammars")
	os.MkdirAll(cacheDir, 0755)

	var entry AuditEntry
	d, err := NewGrammarDownloader(
		WithDownloadCacheDir(cacheDir),
		WithAuditCallback(func(e AuditEntry) { entry = e }),
	)
	if err != nil {
		t.Fatalf("NewGrammarDownloader: %v", err)
	}

	fakeLib := filepath.Join(cacheDir, grammarLibName("go"))
	os.WriteFile(fakeLib, []byte("fake"), 0644)

	before := time.Now()
	d.EnsureGrammar(context.Background(), "go")
	after := time.Now()

	if entry.Timestamp.Before(before) || entry.Timestamp.After(after) {
		t.Error("audit entry timestamp should be within test bounds")
	}
}

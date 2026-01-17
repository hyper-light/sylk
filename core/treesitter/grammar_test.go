package treesitter

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateGrammarName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"go", true},
		{"python", true},
		{"tree_sitter_go", true},
		{"cpp", true},
		{"c_sharp", true},

		{"", false},
		{"a", false},
		{"Go", false},
		{"PYTHON", false},
		{"tree-sitter", false},
		{"go.mod", false},
		{"../etc/passwd", false},
		{"a/b", false},
		{"/abs/path", false},
		{"name with spaces", false},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGrammarName(tt.name)
			if tt.want && err != nil {
				t.Errorf("validateGrammarName(%q) = %v, want nil", tt.name, err)
			}
			if !tt.want && err == nil {
				t.Errorf("validateGrammarName(%q) = nil, want error", tt.name)
			}
		})
	}
}

func TestGrammarLibName(t *testing.T) {
	name := "python"
	got := grammarLibName(name)

	switch runtime.GOOS {
	case "darwin":
		if got != "libtree-sitter-python.dylib" {
			t.Errorf("grammarLibName(%q) = %q on darwin", name, got)
		}
	case "windows":
		if got != "tree-sitter-python.dll" {
			t.Errorf("grammarLibName(%q) = %q on windows", name, got)
		}
	default:
		if got != "libtree-sitter-python.so" {
			t.Errorf("grammarLibName(%q) = %q on linux", name, got)
		}
	}
}

func TestIsSubpath(t *testing.T) {
	tests := []struct {
		child  string
		parent string
		want   bool
	}{
		{"/usr/lib/foo.so", "/usr/lib", true},
		{"/usr/lib/sub/foo.so", "/usr/lib", true},
		{"/usr/lib", "/usr/lib", true},
		{"/usr/bin/foo", "/usr/lib", false},
		{"/etc/passwd", "/usr/lib", false},
		{"/usr/lib/../bin/foo", "/usr/lib", false},
	}

	for _, tt := range tests {
		t.Run(tt.child+"_"+tt.parent, func(t *testing.T) {
			got := isSubpath(tt.child, tt.parent)
			if got != tt.want {
				t.Errorf("isSubpath(%q, %q) = %v, want %v", tt.child, tt.parent, got, tt.want)
			}
		})
	}
}

func TestValidateDirectoryRejectsWorldWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("world-writable check not applicable on Windows")
	}

	tmpDir := t.TempDir()
	worldWritable := filepath.Join(tmpDir, "public")
	if err := os.Mkdir(worldWritable, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(worldWritable, 0777); err != nil {
		t.Fatal(err)
	}

	err := validateDirectory(worldWritable)
	if err == nil {
		t.Error("validateDirectory should reject world-writable directory")
	}
}

func TestValidateDirectoryAcceptsNormal(t *testing.T) {
	tmpDir := t.TempDir()
	normalDir := filepath.Join(tmpDir, "normal")
	if err := os.Mkdir(normalDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := validateDirectory(normalDir)
	if err != nil {
		t.Errorf("validateDirectory should accept normal directory: %v", err)
	}
}

func TestValidateLibraryFileRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests unreliable on Windows")
	}

	tmpDir := t.TempDir()
	trustedDir := filepath.Join(tmpDir, "trusted")
	untrustedDir := filepath.Join(tmpDir, "untrusted")
	if err := os.Mkdir(trustedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(untrustedDir, 0755); err != nil {
		t.Fatal(err)
	}

	realFile := filepath.Join(untrustedDir, "malicious.so")
	if err := os.WriteFile(realFile, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	symlink := filepath.Join(trustedDir, "libtree-sitter-evil.dylib")
	if err := os.Symlink(realFile, symlink); err != nil {
		t.Fatal(err)
	}

	err := validateLibraryFile(symlink, trustedDir)
	if err == nil {
		t.Error("validateLibraryFile should reject symlink escaping trusted directory")
	}
}

func TestValidateLibraryFileAcceptsValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	validFile := filepath.Join(tmpDir, "libtree-sitter-go.dylib")
	if err := os.WriteFile(validFile, []byte("valid library content"), 0644); err != nil {
		t.Fatal(err)
	}

	err := validateLibraryFile(validFile, tmpDir)
	if err != nil {
		t.Errorf("validateLibraryFile should accept valid file: %v", err)
	}
}

func TestGrammarLoaderOptions(t *testing.T) {
	tmpDir := t.TempDir()

	gl := NewGrammarLoader(
		WithTrustedDir(tmpDir),
		WithChecksum("go", "abc123"),
		WithRequireVerification(true),
	)

	found := false
	for _, dir := range gl.trustedDirs {
		if dir == tmpDir {
			found = true
			break
		}
	}
	if !found {
		t.Error("WithTrustedDir should add directory to trusted list")
	}

	if gl.checksums["go"] != "abc123" {
		t.Error("WithChecksum should register checksum")
	}

	if !gl.requireVerify {
		t.Error("WithRequireVerification should set require flag")
	}
}

func TestGrammarLoaderLoadInvalidName(t *testing.T) {
	gl := NewGrammarLoader()

	_, err := gl.Load("../evil")
	if err == nil {
		t.Error("Load should reject invalid grammar name")
	}

	_, err = gl.Load("UPPERCASE")
	if err == nil {
		t.Error("Load should reject uppercase grammar name")
	}
}

func TestGrammarLoaderNotFound(t *testing.T) {
	gl := NewGrammarLoader()

	_, err := gl.Load("nonexistent_grammar_xyz")
	if err == nil {
		t.Error("Load should error when grammar not found")
	}
}

func TestGrammarLoaderChecksumMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	libName := grammarLibName("testlang")
	libPath := filepath.Join(tmpDir, libName)
	if err := os.WriteFile(libPath, []byte("fake library"), 0644); err != nil {
		t.Fatal(err)
	}

	gl := NewGrammarLoader(
		WithTrustedDir(tmpDir),
		WithChecksum("testlang", "wrong_checksum"),
	)

	_, err := gl.verifyLibrary("testlang", libPath)
	if err == nil {
		t.Error("verifyLibrary should fail on checksum mismatch")
	}
}

func TestGrammarLoaderRequireVerificationNoChecksum(t *testing.T) {
	tmpDir := t.TempDir()
	libPath := filepath.Join(tmpDir, "lib.so")
	if err := os.WriteFile(libPath, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	gl := NewGrammarLoader(
		WithRequireVerification(true),
	)

	_, err := gl.verifyLibrary("unknown", libPath)
	if err == nil {
		t.Error("verifyLibrary should fail when verification required but no checksum registered")
	}
}

func TestGrammarLoaderRegisterChecksum(t *testing.T) {
	gl := NewGrammarLoader()
	gl.RegisterChecksum("python", "sha256hash")

	gl.mu.RLock()
	defer gl.mu.RUnlock()
	if gl.checksums["python"] != "sha256hash" {
		t.Error("RegisterChecksum should store checksum")
	}
}

func TestGrammarLoaderAddTrustedDir(t *testing.T) {
	gl := NewGrammarLoader()
	tmpDir := t.TempDir()

	if err := gl.AddTrustedDir(tmpDir); err != nil {
		t.Errorf("AddTrustedDir should accept valid directory: %v", err)
	}

	gl.mu.RLock()
	found := false
	for _, dir := range gl.trustedDirs {
		if dir == tmpDir {
			found = true
			break
		}
	}
	gl.mu.RUnlock()

	if !found {
		t.Error("AddTrustedDir should add directory to list")
	}
}

func TestGrammarLoaderAddTrustedDirRejectsInvalid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("world-writable check not applicable on Windows")
	}

	gl := NewGrammarLoader()
	tmpDir := t.TempDir()
	worldWritable := filepath.Join(tmpDir, "public")
	if err := os.Mkdir(worldWritable, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(worldWritable, 0777); err != nil {
		t.Fatal(err)
	}

	err := gl.AddTrustedDir(worldWritable)
	if err == nil {
		t.Error("AddTrustedDir should reject world-writable directory")
	}
}

func TestGrammarLoaderSetRequireVerification(t *testing.T) {
	gl := NewGrammarLoader()
	if gl.requireVerify {
		t.Error("requireVerify should default to false")
	}

	gl.SetRequireVerification(true)
	if !gl.requireVerify {
		t.Error("SetRequireVerification(true) should enable verification")
	}

	gl.SetRequireVerification(false)
	if gl.requireVerify {
		t.Error("SetRequireVerification(false) should disable verification")
	}
}

func TestGrammarLoaderLoadedGrammars(t *testing.T) {
	gl := NewGrammarLoader()
	grammars := gl.LoadedGrammars()
	if len(grammars) != 0 {
		t.Error("LoadedGrammars should return empty map initially")
	}
}

func TestIsGrammarAvailableInvalidName(t *testing.T) {
	if IsGrammarAvailable("../evil") {
		t.Error("IsGrammarAvailable should return false for invalid name")
	}
}

func TestSylkDataDir(t *testing.T) {
	dir := sylkDataDir()
	if dir == "" {
		t.Error("sylkDataDir should return non-empty path")
	}
}

func TestDefaultTrustedDirs(t *testing.T) {
	dirs := defaultTrustedDirs()
	if len(dirs) == 0 {
		t.Error("defaultTrustedDirs should return at least one directory")
	}
}

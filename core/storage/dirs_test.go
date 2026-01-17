package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestResolveDirs(t *testing.T) {
	dirs, err := ResolveDirs()
	if err != nil {
		t.Fatalf("ResolveDirs failed: %v", err)
	}

	if dirs.Config == "" {
		t.Error("Config dir should not be empty")
	}
	if dirs.Data == "" {
		t.Error("Data dir should not be empty")
	}
	if dirs.Cache == "" {
		t.Error("Cache dir should not be empty")
	}
	if dirs.State == "" {
		t.Error("State dir should not be empty")
	}

	if !strings.Contains(dirs.Config, "sylk") {
		t.Errorf("Config dir should contain 'sylk': %s", dirs.Config)
	}
}

func TestResolveDirsXDGOverride(t *testing.T) {
	resetGlobalDirs()

	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	dirs, err := ResolveDirs()
	if err != nil {
		t.Fatalf("ResolveDirs failed: %v", err)
	}

	expected := filepath.Join(tmpDir, "sylk")
	if dirs.Config != expected {
		t.Errorf("XDG override failed: got %s, want %s", dirs.Config, expected)
	}
}

func TestResolveProjectDirs(t *testing.T) {
	projectRoot := "/test/project"
	dirs := ResolveProjectDirs(projectRoot)

	if dirs.Root != filepath.Join(projectRoot, ".sylk") {
		t.Errorf("Root: got %s, want %s", dirs.Root, filepath.Join(projectRoot, ".sylk"))
	}
	if dirs.Config != filepath.Join(projectRoot, ".sylk", "config.yaml") {
		t.Errorf("Config: got %s, want %s", dirs.Config, filepath.Join(projectRoot, ".sylk", "config.yaml"))
	}
	if dirs.Local != filepath.Join(projectRoot, ".sylk", "local") {
		t.Errorf("Local: got %s, want %s", dirs.Local, filepath.Join(projectRoot, ".sylk", "local"))
	}
}

func TestProjectHash(t *testing.T) {
	hash1 := ProjectHash("/project/one")
	hash2 := ProjectHash("/project/two")
	hash3 := ProjectHash("/project/one")

	if hash1 == hash2 {
		t.Error("Different projects should have different hashes")
	}
	if hash1 != hash3 {
		t.Error("Same project should have same hash")
	}
	if len(hash1) != 16 {
		t.Errorf("Hash should be 16 chars, got %d", len(hash1))
	}
}

func TestEnsureDir(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test", "nested", "dir")

	err := EnsureDir(testDir, 0755)
	if err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}

	info, err := os.Stat(testDir)
	if err != nil {
		t.Fatalf("Dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("Created path is not a directory")
	}

	err = EnsureDir(testDir, 0755)
	if err != nil {
		t.Error("EnsureDir should be idempotent")
	}
}

func TestEnsureSensitiveDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Permission test not applicable on Windows")
	}

	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "sensitive")

	err := EnsureSensitiveDir(testDir)
	if err != nil {
		t.Fatalf("EnsureSensitiveDir failed: %v", err)
	}

	info, err := os.Stat(testDir)
	if err != nil {
		t.Fatalf("Dir not created: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf("Permissions: got %o, want 0700", perm)
	}
}

func TestCleanupDir(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "toclean")

	err := os.MkdirAll(testDir, 0755)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	err = CleanupDir(testDir, []string{tmpDir})
	if err != nil {
		t.Fatalf("CleanupDir failed: %v", err)
	}

	if _, err := os.Stat(testDir); !os.IsNotExist(err) {
		t.Error("Dir should be removed")
	}
}

func TestCleanupDirRejectsOutsidePath(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := "/tmp/outside"

	err := CleanupDir(outsideDir, []string{tmpDir})
	if err == nil {
		t.Error("CleanupDir should reject paths outside allowed prefixes")
	}

	if !isPathNotAllowedError(err) {
		t.Errorf("Expected PathNotAllowedError, got %T", err)
	}
}

func TestTempDir(t *testing.T) {
	dir1, err := TempDir("")
	if err != nil {
		t.Fatalf("TempDir failed: %v", err)
	}
	defer os.RemoveAll(dir1)

	if !strings.Contains(dir1, "sylk-") {
		t.Errorf("TempDir should contain 'sylk-': %s", dir1)
	}

	dir2, err := TempDir("test")
	if err != nil {
		t.Fatalf("TempDir with pattern failed: %v", err)
	}
	defer os.RemoveAll(dir2)

	if !strings.Contains(dir2, "sylk-test-") {
		t.Errorf("TempDir should contain 'sylk-test-': %s", dir2)
	}
}

func TestDirsHelperMethods(t *testing.T) {
	dirs := &Dirs{
		Config: "/config",
		Data:   "/data",
		Cache:  "/cache",
		State:  "/state",
	}

	if got := dirs.ConfigDir("sub"); got != "/config/sub" {
		t.Errorf("ConfigDir: got %s, want /config/sub", got)
	}
	if got := dirs.DataDir("a", "b"); got != "/data/a/b" {
		t.Errorf("DataDir: got %s, want /data/a/b", got)
	}
	if got := dirs.CacheDir(); got != "/cache" {
		t.Errorf("CacheDir: got %s, want /cache", got)
	}
	if got := dirs.StateDir("logs"); got != "/state/logs" {
		t.Errorf("StateDir: got %s, want /state/logs", got)
	}
}

func TestDirsSessionDir(t *testing.T) {
	dirs := &Dirs{Data: "/data"}
	got := dirs.SessionDir("sess123")
	want := "/data/sessions/sess123"
	if got != want {
		t.Errorf("SessionDir: got %s, want %s", got, want)
	}
}

func TestDirsProjectDataDir(t *testing.T) {
	dirs := &Dirs{Data: "/data"}
	got := dirs.ProjectDataDir("/project/path")

	if !strings.HasPrefix(got, "/data/projects/") {
		t.Errorf("ProjectDataDir should start with /data/projects/: %s", got)
	}
}

func TestDirsBackupDir(t *testing.T) {
	dirs := &Dirs{Data: "/data"}
	got := dirs.BackupDir("system")
	want := "/data/backups/system"
	if got != want {
		t.Errorf("BackupDir: got %s, want %s", got, want)
	}
}

func TestDirsLogDir(t *testing.T) {
	dirs := &Dirs{State: "/state"}
	got := dirs.LogDir()
	want := "/state/logs"
	if got != want {
		t.Errorf("LogDir: got %s, want %s", got, want)
	}
}

func TestDirsLockDir(t *testing.T) {
	dirs := &Dirs{State: "/state"}
	got := dirs.LockDir()
	want := "/state/locks"
	if got != want {
		t.Errorf("LockDir: got %s, want %s", got, want)
	}
}

func TestEnsureAll(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &Dirs{
		Config: filepath.Join(tmpDir, "config"),
		Data:   filepath.Join(tmpDir, "data"),
		Cache:  filepath.Join(tmpDir, "cache"),
		State:  filepath.Join(tmpDir, "state"),
	}

	err := dirs.EnsureAll()
	if err != nil {
		t.Fatalf("EnsureAll failed: %v", err)
	}

	checkDirExists := func(path string) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Dir should exist: %s", path)
		}
	}

	checkDirExists(dirs.Config)
	checkDirExists(dirs.ConfigDir("credentials"))
	checkDirExists(dirs.Data)
	checkDirExists(dirs.DataDir("sessions"))
	checkDirExists(dirs.DataDir("projects"))
	checkDirExists(dirs.DataDir("backups"))
	checkDirExists(dirs.Cache)
	checkDirExists(dirs.CacheDir("hot"))
	checkDirExists(dirs.CacheDir("warm"))
	checkDirExists(dirs.CacheDir("cold"))
	checkDirExists(dirs.State)
	checkDirExists(dirs.LogDir())
	checkDirExists(dirs.LockDir())
}

func resetGlobalDirs() {
	globalDirs = nil
	globalDirsOnce = sync.Once{}
	globalDirsErr = nil
}

func isPathNotAllowedError(err error) bool {
	_, ok := err.(*PathNotAllowedError)
	return ok
}

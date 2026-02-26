package boot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotenv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, dotenvFile)

	content := `# Comment line
SYLK_TEST_KEY=hello
SYLK_TEST_QUOTED="world"
SYLK_TEST_SINGLE='single'

  # Indented comment
SYLK_TEST_SPACES = spaced_value
`
	if err := os.WriteFile(envFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	// Clean up env after test.
	for _, key := range []string{
		"SYLK_TEST_KEY", "SYLK_TEST_QUOTED", "SYLK_TEST_SINGLE", "SYLK_TEST_SPACES",
	} {
		t.Cleanup(func() { os.Unsetenv(key) })
	}

	if err := LoadDotenv(dir); err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}

	cases := []struct {
		key, want string
	}{
		{"SYLK_TEST_KEY", "hello"},
		{"SYLK_TEST_QUOTED", "world"},
		{"SYLK_TEST_SINGLE", "single"},
		{"SYLK_TEST_SPACES", "spaced_value"},
	}
	for _, tc := range cases {
		if got := os.Getenv(tc.key); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestLoadDotenvNoOverride(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, dotenvFile)

	if err := os.WriteFile(envFile, []byte("SYLK_TEST_EXISTING=new\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SYLK_TEST_EXISTING", "original")

	if err := LoadDotenv(dir); err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}

	if got := os.Getenv("SYLK_TEST_EXISTING"); got != "original" {
		t.Errorf("SYLK_TEST_EXISTING = %q, want %q (should not override)", got, "original")
	}
}

func TestLoadDotenvMissing(t *testing.T) {
	err := LoadDotenv(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.IsNotExist, got: %v", err)
	}
}

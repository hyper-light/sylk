package git

import "testing"

func configureGitTestRepo(t *testing.T, dir string, run func(t *testing.T, dir string, args ...string) string) {
	t.Helper()
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "Test User")
	run(t, dir, "config", "commit.gpgsign", "false")
	run(t, dir, "config", "tag.gpgsign", "false")
}

package engineer

import "testing"

func TestApplyConfigDefaults_EnablesWritesAndCommands(t *testing.T) {
	cfg := applyConfigDefaults(Config{})

	if !cfg.EngineerConfig.EnableFileWrites {
		t.Fatal("expected file writes to be enabled by default")
	}
	if !cfg.EngineerConfig.EnableCommands {
		t.Fatal("expected command execution to be enabled by default")
	}
}

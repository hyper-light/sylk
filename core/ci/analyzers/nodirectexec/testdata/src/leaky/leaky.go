// Package leaky simulates an agent-layer package that attempts to spawn
// processes directly. Every call below must be flagged by the analyzer.
package leaky

import (
	"context"
	"os/exec"
	"syscall"
)

func SpawnViaCommand() {
	cmd := exec.Command("echo", "leak") // want `direct exec.Command call is forbidden`
	_ = cmd
}

func SpawnViaCommandContext(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "pytest") // want `direct exec.CommandContext call is forbidden`
	_ = cmd
}

func SpawnViaStartProcess() {
	_, _, _ = syscall.StartProcess("/bin/true", nil, nil) // want `direct syscall.StartProcess call is forbidden`
}

// Package fakepkg simulates code inside the allowlisted core/purevfs path —
// i.e. the broker itself. Direct exec calls here are legitimate because this
// package *is* the sandbox implementation. The analyzer must not flag them.
package fakepkg

import (
	"context"
	"os/exec"
)

func SandboxSpawn(ctx context.Context) {
	// No diagnostic expected: purevfs is on the allowlist.
	_ = exec.CommandContext(ctx, "bwrap")
	_ = exec.Command("sandbox-exec")
}

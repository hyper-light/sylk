// Package brokerok simulates an ordinary agent package that runs commands
// only via the broker. No direct exec calls; no diagnostics expected.
package brokerok

import "context"

type fakeBroker interface {
	Run(ctx context.Context, argv []string) error
}

func InstallDependency(ctx context.Context, b fakeBroker) error {
	return b.Run(ctx, []string{"python", "-m", "pip", "install", "pytest"})
}

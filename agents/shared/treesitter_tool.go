package shared

import (
	"sync"

	"github.com/adalundhe/sylk/core/treesitter"
)

// SharedTreeSitterTool is the process-wide TreeSitterTool agents
// compose into their LSP backends. Grammars are cached internally by
// the tool, so a single instance serves every agent without
// reloading shared state. Lazy construction keeps cold-start cost
// off the boot path for agents that never touch treesitter.
var (
	sharedTreeSitterToolOnce sync.Once
	sharedTreeSitterTool     *treesitter.TreeSitterTool
)

// SharedTreeSitter returns the process-wide TreeSitterTool,
// constructing it on first call. Safe for concurrent callers.
func SharedTreeSitter() *treesitter.TreeSitterTool {
	sharedTreeSitterToolOnce.Do(func() {
		sharedTreeSitterTool = treesitter.NewTreeSitterTool()
	})
	return sharedTreeSitterTool
}

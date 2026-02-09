package macro

import (
	"sync"

	"github.com/adalundhe/sylk/ui/editor/register"
)

// maxRecursionDepth is the maximum nesting depth for macro playback.
// Prevents runaway recursion from self-referencing macros. Derived from
// vim's default recursion limit for mappings.
const maxRecursionDepth = 100

// Player replays macro key sequences stored in the register store.
type Player struct {
	mu           sync.Mutex
	registers    *register.Store
	lastRegister rune
	depth        int
}

// NewPlayer creates a Player backed by the given register store.
func NewPlayer(registers *register.Store) *Player {
	return &Player{
		registers: registers,
	}
}

// Play returns the key sequence stored in the given register, repeated
// count times. Returns nil if the register is empty or the recursion
// guard trips.
func (p *Player) Play(reg rune, count int) []rune {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.depth >= maxRecursionDepth {
		return nil
	}
	content, _ := p.registers.Get(reg)
	if len(content) == 0 {
		return nil
	}
	p.lastRegister = reg
	p.depth++
	keys := []rune(content)
	result := repeatKeys(keys, count)
	p.depth--
	return result
}

// PlayLast replays the most recently played register. This implements the
// @@ command. Returns nil if no previous macro has been played.
func (p *Player) PlayLast(count int) []rune {
	p.mu.Lock()
	last := p.lastRegister
	p.mu.Unlock()
	if last == 0 {
		return nil
	}
	return p.Play(last, count)
}

// LastRegister returns the register used by the most recent Play call.
func (p *Player) LastRegister() rune {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastRegister
}

// ResetDepth resets the recursion depth counter. This should be called
// at the start of each top-level macro invocation from the key handler.
func (p *Player) ResetDepth() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.depth = 0
}

// repeatKeys produces a key sequence that is keys repeated count times.
// The result is bounded to avoid unbounded growth.
func repeatKeys(keys []rune, count int) []rune {
	effectiveCount := max(count, 1)
	totalLen := len(keys) * effectiveCount
	// Bound the total to prevent excessive allocation. The bound is
	// derived from the maximum macro key buffer times a reasonable
	// repeat count.
	maxTotal := maxMacroKeyBuffer * maxRecursionDepth
	if totalLen > maxTotal {
		effectiveCount = maxTotal / max(len(keys), 1)
		totalLen = len(keys) * effectiveCount
	}
	result := make([]rune, 0, totalLen)
	for range effectiveCount {
		result = append(result, keys...)
	}
	return result
}

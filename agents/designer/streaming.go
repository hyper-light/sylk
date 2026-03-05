package designer

import (
	"sync"

	"github.com/adalundhe/sylk/core/providers"
)

// designerUsageAccumulator sums real token counts from multiple LLM calls
// within a single Designer request. Thread-safe for the tool loop.
type designerUsageAccumulator struct {
	mu              sync.Mutex
	inputTotal      int
	outputTotal     int
	reasoningTotal  int
	cacheReadTotal  int
	cacheWriteTotal int
}

func (a *designerUsageAccumulator) Add(usage *providers.Usage) {
	if usage == nil {
		return
	}
	a.mu.Lock()
	a.inputTotal += usage.InputTokens
	a.outputTotal += usage.OutputTokens
	a.reasoningTotal += usage.ReasoningTokens
	a.cacheReadTotal += usage.CacheReadTokens
	a.cacheWriteTotal += usage.CacheWriteTokens
	a.mu.Unlock()
}

func (a *designerUsageAccumulator) Total() (input, output int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inputTotal, a.outputTotal
}

// accumulateUsage adds a response's token usage to the per-request accumulator.
func (d *Designer) accumulateUsage(resp *providers.Response) {
	if resp == nil {
		return
	}
	d.usageAccum.Add(&resp.Usage)
}

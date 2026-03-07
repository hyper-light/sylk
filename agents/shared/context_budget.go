package shared

import (
	"github.com/adalundhe/sylk/core/providers"
)

// BudgetZone classifies the current context window utilization.
type BudgetZone int

const (
	ZoneGreen    BudgetZone = iota // 0–80%: no action
	ZoneYellow                     // 80–90%: compact oldest turn groups
	ZoneRed                        // 90–95%: evict oldest turn groups
	ZoneCritical                   // >95%: abort — budget exhausted
)

func (z BudgetZone) String() string {
	switch z {
	case ZoneGreen:
		return "green"
	case ZoneYellow:
		return "yellow"
	case ZoneRed:
		return "red"
	case ZoneCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ContextBudget performs token accounting using ground-truth anchoring.
//
// After every LLM call the provider reports the exact input token count.
// The budget anchors to that number and estimates only the delta — messages
// appended since the anchor (typically one assistant reply + tool results).
// This bounds the estimation error to a single turn's worth of new content
// rather than the entire context window.
//
// Before the first LLM response the budget uses a character-based estimate
// of the fixed overhead (system prompt + tool definitions) plus message
// content. This pre-anchor estimate is conservative but only used once.
type ContextBudget struct {
	modelLimit    int
	reserveTokens int
	counter       *providers.ProviderTokenCounter

	// Ground-truth anchor from the most recent provider response.
	anchorTokens   int // provider-reported InputTokens
	anchorMsgCount int // len(req.Messages) at anchor time
	anchored       bool

	// Character-based overhead estimate, used only before the first anchor.
	fixedOverhead int

	// Watermark fractions of available capacity.
	compactAt  float64 // 0.80
	evictAt    float64 // 0.90
	criticalAt float64 // 0.95
}

// NewContextBudget creates a budget tracker for the given model. Reserve is
// the sum of max output tokens and thinking budget — tokens that the model
// may produce and therefore must not be filled by input.
func NewContextBudget(model string, maxOutputTokens, thinkingBudget int) *ContextBudget {
	counter := providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig())
	return &ContextBudget{
		modelLimit:    counter.MaxContextTokens(model),
		reserveTokens: maxOutputTokens + thinkingBudget,
		counter:       counter,
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}
}

// ComputeFixedOverhead estimates the static portion of every request — system
// prompt and tool definitions — and caches it. Used only before the first
// anchor is established.
func (b *ContextBudget) ComputeFixedOverhead(systemPrompt string, tools []providers.Tool) {
	promptTokens, _ := b.counter.CountText(systemPrompt)
	msgs := []providers.Message{{Role: providers.RoleUser, Content: systemPrompt}}
	withTools, _ := b.counter.CountWithTools(msgs, tools)
	msgOnly, _ := b.counter.Count(msgs)
	toolTokens := withTools - msgOnly
	b.fixedOverhead = promptTokens + toolTokens
}

// Anchor records the provider's ground-truth input token count and the
// message count at the time of the LLM call. All subsequent utilization
// projections use this as the base, estimating only newly appended messages.
func (b *ContextBudget) Anchor(inputTokens, messageCount int) {
	b.anchorTokens = inputTokens
	b.anchorMsgCount = messageCount
	b.anchored = true
}

// InvalidateAnchor marks the anchor as stale. Called when messages are
// removed (steering rollback, compaction, eviction) so the next BeginTurn
// falls back to the character-based estimate until a new anchor arrives.
func (b *ContextBudget) InvalidateAnchor() {
	b.anchored = false
}

// Anchored returns whether a ground-truth anchor is active.
func (b *ContextBudget) Anchored() bool { return b.anchored }

// AnchorTokens returns the last anchored input token count (for telemetry).
func (b *ContextBudget) AnchorTokens() int { return b.anchorTokens }

// Capacity returns the total input token budget: model limit minus the
// output reserve. This is the denominator for utilization.
func (b *ContextBudget) Capacity() int {
	cap := b.modelLimit - b.reserveTokens
	if cap < 0 {
		return 0
	}
	return cap
}

// ProjectedInput returns the best estimate of total input tokens for the
// given message slice. When anchored, this is anchor + delta estimate.
// When unanchored, it falls back to fixedOverhead + character estimate.
func (b *ContextBudget) ProjectedInput(messages []providers.Message) int {
	if b.anchored && b.anchorMsgCount <= len(messages) {
		delta := b.estimateRaw(messages[b.anchorMsgCount:])
		return b.anchorTokens + delta
	}
	return b.fixedOverhead + b.estimateRaw(messages)
}

// Available returns the token budget remaining for new messages.
func (b *ContextBudget) Available(messages []providers.Message) int {
	avail := b.Capacity() - b.ProjectedInput(messages)
	if avail < 0 {
		return 0
	}
	return avail
}

// Utilization returns the fraction of capacity consumed by current input.
func (b *ContextBudget) Utilization(messages []providers.Message) float64 {
	cap := b.Capacity()
	if cap <= 0 {
		return 1.0
	}
	return float64(b.ProjectedInput(messages)) / float64(cap)
}

// Zone returns the watermark zone for the current message set.
func (b *ContextBudget) Zone(messages []providers.Message) BudgetZone {
	u := b.Utilization(messages)
	switch {
	case u >= b.criticalAt:
		return ZoneCritical
	case u >= b.evictAt:
		return ZoneRed
	case u >= b.compactAt:
		return ZoneYellow
	default:
		return ZoneGreen
	}
}

// PerResultBudget computes the token budget for a single tool result based
// on remaining capacity and remaining turns.
func (b *ContextBudget) PerResultBudget(projectedInput, remainingTurns int) int {
	remaining := b.Capacity() - projectedInput
	if remaining <= 0 {
		return 0
	}
	divisor := remainingTurns
	if divisor < 1 {
		divisor = 1
	}
	return remaining / divisor
}

// estimateRaw returns a character-based token estimate for messages without
// any calibration scaling. Used only for delta estimation (small, bounded
// content added since the last anchor).
func (b *ContextBudget) estimateRaw(messages []providers.Message) int {
	raw, _ := b.counter.Count(messages)
	return raw
}

// FixedOverhead returns the character-based overhead estimate (for telemetry).
func (b *ContextBudget) FixedOverhead() int { return b.fixedOverhead }

// ModelLimit returns the model's context window size.
func (b *ContextBudget) ModelLimit() int { return b.modelLimit }

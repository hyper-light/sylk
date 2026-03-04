package shared

import (
	"math"

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

// ContextBudget performs token accounting with watermark zones and adaptive
// calibration via exponential moving average of actual/estimated ratios.
type ContextBudget struct {
	modelLimit    int
	reserveTokens int
	fixedOverhead int
	counter       *providers.ProviderTokenCounter

	// Adaptive calibration: EMA of actual/estimated ratio.
	calibRatio   float64
	calibSamples int

	// Watermark fractions of available budget.
	compactAt  float64 // 0.80
	evictAt    float64 // 0.90
	criticalAt float64 // 0.95
}

const calibrationAlpha = 0.3

// NewContextBudget creates a budget tracker for the given model. Reserve is
// the sum of max output tokens and thinking budget — tokens that the model
// may produce and therefore must not be filled by input.
func NewContextBudget(model string, maxOutputTokens, thinkingBudget int) *ContextBudget {
	counter := providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig())
	return &ContextBudget{
		modelLimit:    counter.MaxContextTokens(model),
		reserveTokens: maxOutputTokens + thinkingBudget,
		counter:       counter,
		calibRatio:    1.0,
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}
}

// ComputeFixedOverhead estimates the static portion of every request — system
// prompt and tool definitions — and caches it. Called once at governor init.
func (b *ContextBudget) ComputeFixedOverhead(systemPrompt string, tools []providers.Tool) {
	promptTokens, _ := b.counter.CountText(systemPrompt)
	msgs := []providers.Message{{Role: providers.RoleUser, Content: systemPrompt}}
	withTools, _ := b.counter.CountWithTools(msgs, tools)
	msgOnly, _ := b.counter.Count(msgs)
	toolTokens := withTools - msgOnly
	b.fixedOverhead = promptTokens + toolTokens
}

// Available returns the token budget available for messages (excluding the
// fixed overhead and output reserve).
func (b *ContextBudget) Available() int {
	avail := b.modelLimit - b.reserveTokens - b.fixedOverhead
	if avail < 0 {
		return 0
	}
	return avail
}

// EstimateMessages returns the calibrated token estimate for the given messages.
func (b *ContextBudget) EstimateMessages(messages []providers.Message) int {
	raw, _ := b.counter.Count(messages)
	return int(math.Ceil(float64(raw) * b.calibRatio))
}

// Utilization returns the fraction of available budget consumed by messages.
func (b *ContextBudget) Utilization(messages []providers.Message) float64 {
	avail := b.Available()
	if avail <= 0 {
		return 1.0
	}
	return float64(b.EstimateMessages(messages)) / float64(avail)
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

// Calibrate updates the EMA calibration ratio using ground-truth input
// tokens from the provider response vs. the character-based estimate.
func (b *ContextBudget) Calibrate(estimatedTokens, actualInputTokens int) {
	if estimatedTokens <= 0 || actualInputTokens <= 0 {
		return
	}
	observed := float64(actualInputTokens) / float64(estimatedTokens)
	if b.calibSamples == 0 {
		b.calibRatio = observed
	} else {
		b.calibRatio = calibrationAlpha*observed + (1-calibrationAlpha)*b.calibRatio
	}
	b.calibSamples++
}

// PerResultBudget computes the token budget for a single tool result based
// on remaining capacity and remaining turns.
func (b *ContextBudget) PerResultBudget(currentUsed, remainingTurns int) int {
	remaining := b.Available() - currentUsed
	if remaining <= 0 {
		return 0
	}
	divisor := remainingTurns
	if divisor < 1 {
		divisor = 1
	}
	return remaining / divisor
}

// CalibRatio returns the current calibration ratio (for telemetry).
func (b *ContextBudget) CalibRatio() float64 { return b.calibRatio }

// CalibSamples returns the number of calibration samples received.
func (b *ContextBudget) CalibSamples() int { return b.calibSamples }

// ModelLimit returns the model's context window size.
func (b *ContextBudget) ModelLimit() int { return b.modelLimit }

package architect

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

// defaultCharsPerToken is derived from the provider's fallback character-to-token
// ratio. Used for continuation bound derivation.
var defaultCharsPerToken = providers.DefaultTokenCounterConfig().FallbackCharsPerToken

// continuationConfig holds all derived bounds for the continuation engine.
// Every field is computed from model parameters — no magic numbers.
type continuationConfig struct {
	maxRounds          int     // contextWindow / maxOutputTokens
	prefillCharLimit   int     // (contextWindow / 2) * charsPerToken
	maxOutputTokens    int     // from model config
	contextWindow      int     // from model config
	charsPerToken      int     // from provider config
	overlapSearchLimit int     // maxOutputTokens * charsPerToken
	progressAlpha      float64 // 2.0 / (windowSize + 1)
	progressThreshold  float64 // 1.0 / max(2, windowSize)
}

// continuationMetrics tracks operational metrics across continuation rounds.
type continuationMetrics struct {
	roundsUsed        int
	netNewBytesTotal  int
	overlapBytesTotal int
	finalLength       int
	abortReason       string // empty = completed normally
}

// continuationState is the mutable state carried through continuation rounds.
// Stack-local to the caller goroutine — never shared.
type continuationState struct {
	accumulated   *strings.Builder
	length        int
	usage         *providers.Usage
	mode          continuationMode
	progress      progressTracker
	metrics       continuationMetrics
	lastSnapshot  string            // accumulated text, set once per round after stitch
	lastStructure structuralContext // structural analysis, set once per round after stitch
}

// progressTracker implements exponential moving average (EMA) tracking of
// per-round net-new content ratio. When the EMA drops below the threshold,
// the model is repeating more than it produces — stop early.
type progressTracker struct {
	ema   float64
	alpha float64
	count int
}

// maxContinuationRounds is the hard ceiling on continuation rounds.
// 3 rounds (initial + 2 continuations) is sufficient for any plan;
// beyond this, coherence degrades regardless of context window size.
const maxContinuationRounds = 3

// deriveMaxRounds computes the tightest safe round limit from model parameters.
// Beyond (contextWindow/2)/maxOutputTokens rounds, the sliding prefill tail
// can't represent full output — coherence degrades. Capped at
// maxContinuationRounds to bound total latency.
func deriveMaxRounds(contextWindow, maxOutputTokens int) int {
	theoretical := contextWindow / maxOutputTokens
	prefillCapacity := (contextWindow / 2) / maxOutputTokens
	return min(maxContinuationRounds, max(1, min(theoretical, prefillCapacity)))
}

// deriveContinuationConfig computes all continuation bounds from model parameters.
func deriveContinuationConfig(contextWindow, maxOutputTokens, charsPerToken int) continuationConfig {
	maxRounds := deriveMaxRounds(contextWindow, maxOutputTokens)
	windowSize := max(1, maxRounds/3)
	return continuationConfig{
		maxRounds:          maxRounds,
		prefillCharLimit:   (contextWindow / 2) * charsPerToken,
		maxOutputTokens:    maxOutputTokens,
		contextWindow:      contextWindow,
		charsPerToken:      charsPerToken,
		overlapSearchLimit: maxOutputTokens * charsPerToken,
		progressAlpha:      2.0 / (float64(windowSize) + 1),
		progressThreshold:  1.0 / float64(max(2, windowSize)),
	}
}

// continuationModeFromOnChunk determines the continuation mode from the
// onChunk callback. nil = JSON (non-streaming), non-nil = text (streaming).
func continuationModeFromOnChunk(onChunk func(string)) continuationMode {
	if onChunk == nil {
		return continuationModeJSON
	}
	return continuationModeText
}

// newProgressTracker creates a tracker with the given EMA smoothing factor.
func newProgressTracker(alpha float64) progressTracker {
	return progressTracker{
		ema:   1.0,
		alpha: alpha,
	}
}

// update records a round's net-new ratio into the EMA.
func (p *progressTracker) update(received, netNew int) {
	ratio := float64(netNew) / float64(received)
	p.ema = progressEMA(p.ema, ratio, p.alpha, p.count)
	p.count++
}

// progressEMA computes the exponential moving average. On the first sample,
// the value is used directly (no history to blend with).
func progressEMA(current, value, alpha float64, count int) float64 {
	if count == 0 {
		return value
	}
	return alpha*value + (1-alpha)*current
}

// isHealthy returns true if the EMA is above the threshold, or if no
// samples have been recorded yet.
func (p *progressTracker) isHealthy(threshold float64) bool {
	return p.count == 0 || p.ema >= threshold
}

// prefillTail returns the last limit characters of accumulated, or the full
// string if shorter. This implements the sliding-window prefill that prevents
// unbounded prefill growth.
func prefillTail(accumulated string, limit int) string {
	if len(accumulated) <= limit {
		return accumulated
	}
	return accumulated[len(accumulated)-limit:]
}

// --- Continuation loop ---

// runContinuationLoop manages multi-round continuation of a truncated response.
// Called only when the initial request hit max_tokens.
func (p *anthropicPlanner) runContinuationLoop(
	lease *plannerRequestLease,
	prompt string,
	initial *streamResult,
	cfg continuationConfig,
	mode continuationMode,
	system string,
	stage string,
	onChunk func(string),
) (string, *providers.Usage, error) {
	p.logger.Info("runContinuationLoop: entry",
		"stage", stage,
		"max_rounds", cfg.maxRounds,
		"initial_len", len(initial.Text),
		"ctx_deadline", contextDeadlineString(lease.parent))
	state := initContinuationState(initial, mode, cfg)
	loopStart := time.Now()
	p.executeRounds(lease, prompt, &state, cfg, system, stage, onChunk)
	p.logger.Info("runContinuationLoop: done",
		"stage", stage,
		"rounds_used", state.metrics.roundsUsed,
		"abort_reason", state.metrics.abortReason,
		"net_new_bytes", state.metrics.netNewBytesTotal,
		"final_len", state.length,
		"elapsed", time.Since(loopStart).String())
	finalizeContinuation(&state, onChunk, stage, p.logger)
	return state.lastSnapshot, state.usage, nil
}

// executeRounds runs up to maxRounds continuation requests, stopping when
// the model completes naturally, progress decays, structure completes,
// the AIMD window is exhausted, or the context deadline approaches.
func (p *anthropicPlanner) executeRounds(
	lease *plannerRequestLease,
	prompt string,
	state *continuationState,
	cfg continuationConfig,
	system string,
	stage string,
	onChunk func(string),
) {
	aimd := newContinuationAIMD(cfg.maxRounds)

	for round := range cfg.maxRounds { // hard ceiling unchanged
		if p.continuationWindowExhausted(state, stage, round, aimd) {
			return
		}
		p.logger.Info("executeRounds: starting round",
			"stage", stage,
			"round", round,
			"max_rounds", cfg.maxRounds,
			"aimd_allowed", aimd.AllowedRounds(),
			"progress_ema", state.progress.ema,
			"accumulated_len", state.length,
			"ctx_deadline", contextDeadlineString(lease.parent))
		roundStart := time.Now()
		if !p.continuationRound(lease, prompt, state, cfg, system, stage, onChunk) {
			p.logger.Info("executeRounds: round signaled stop",
				"stage", stage, "round", round,
				"elapsed", time.Since(roundStart).String(),
				"abort_reason", state.metrics.abortReason)
			return
		}
		p.logger.Info("executeRounds: round complete",
			"stage", stage, "round", round,
			"elapsed", time.Since(roundStart).String(),
			"net_new_total", state.metrics.netNewBytesTotal,
			"progress_healthy", state.progress.isHealthy(cfg.progressThreshold))
		p.advanceContinuationWindow(state, cfg, aimd)
	}
	p.logger.Info("executeRounds: max rounds exhausted",
		"stage", stage, "max_rounds", cfg.maxRounds)
	state.metrics.abortReason = "max_rounds_exhausted"
}

func (p *anthropicPlanner) continuationWindowExhausted(
	state *continuationState,
	stage string,
	round int,
	aimd *continuationAIMD,
) bool {
	if round < aimd.AllowedRounds() {
		return false
	}
	p.logger.Info("executeRounds: AIMD window exhausted",
		"stage", stage, "round", round,
		"allowed", aimd.AllowedRounds())
	state.metrics.abortReason = "aimd_window_exhausted"
	return true
}

func (p *anthropicPlanner) advanceContinuationWindow(
	state *continuationState,
	cfg continuationConfig,
	aimd *continuationAIMD,
) {
	if state.progress.isHealthy(cfg.progressThreshold) {
		aimd.OnProgress()
		return
	}
	aimd.OnStall()
}

// continuationRound executes a single continuation request. Returns true to
// continue the loop, false to stop.
func (p *anthropicPlanner) continuationRound(
	lease *plannerRequestLease,
	prompt string,
	state *continuationState,
	cfg continuationConfig,
	system string,
	stage string,
	onChunk func(string),
) bool {
	if !hasTimeForContinuation(lease.parent) {
		p.logger.Info("continuationRound: insufficient time remaining",
			"stage", stage,
			"ctx_deadline", contextDeadlineString(lease.parent))
		state.metrics.abortReason = "context_deadline"
		return false
	}
	return p.executeContinuationRound(lease, prompt, state, cfg, system, stage, onChunk)
}

// perRoundTimeoutCap is the maximum time allowed for a single continuation
// round. Derived from the default per-request timeout (120s) × 0.75 = 90s,
// leaving headroom for overlap detection and stitching.
const perRoundTimeoutCap = 90 * time.Second

// executeContinuationRound performs the request and applies the result.
// Each round is bounded by min(p.timeout, perRoundTimeoutCap) to prevent
// a single round from consuming the entire operation budget.
func (p *anthropicPlanner) executeContinuationRound(
	lease *plannerRequestLease,
	prompt string,
	state *continuationState,
	cfg continuationConfig,
	system string,
	stage string,
	onChunk func(string),
) bool {
	state.metrics.roundsUsed++

	roundTimeout := min(p.timeout, perRoundTimeoutCap)
	p.logger.Info("executeContinuationRound: requesting continuation",
		"stage", stage,
		"round", state.metrics.roundsUsed,
		"accumulated_len", state.length,
		"round_timeout", roundTimeout.String(),
		"ctx_deadline", contextDeadlineString(lease.parent))
	reqStart := time.Now()
	var result *streamResult
	err := lease.run(roundTimeout, false, func(roundCtx context.Context, attemptTimeout time.Duration) error {
		var innerErr error
		result, innerErr = p.requestContinuation(
			roundCtx,
			prompt,
			state.lastSnapshot,
			state.lastStructure,
			state.mode,
			cfg,
			system,
			stage,
			nil,
		)
		return innerErr
	})
	if err != nil {
		p.logger.Warn("executeContinuationRound: request failed",
			"stage", stage,
			"round", state.metrics.roundsUsed,
			"elapsed", time.Since(reqStart).String(),
			"error", err)
		state.metrics.abortReason = "request_error"
		return false
	}
	p.logger.Info("executeContinuationRound: request succeeded",
		"stage", stage,
		"round", state.metrics.roundsUsed,
		"elapsed", time.Since(reqStart).String(),
		"result_len", len(result.Text))
	return applyContinuationResult(state, result, cfg, onChunk)
}

// requestContinuation builds a multi-turn prefill request with structural
// hints and a sliding-window assistant prefill.
func (p *anthropicPlanner) requestContinuation(
	ctx context.Context,
	originalPrompt string,
	accText string,
	cachedStructure structuralContext,
	mode continuationMode,
	cfg continuationConfig,
	system string,
	stage string,
	onChunk func(string),
) (*streamResult, error) {
	contPrompt := buildContinuationPrompt(mode, cachedStructure)
	prefill := prefillTail(accText, cfg.prefillCharLimit)
	resolvedSystem := p.resolveSystemPrompt(system)
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: originalPrompt},
			{Role: providers.RoleAssistant, Content: prefill},
			{Role: providers.RoleUser, Content: contPrompt},
		},
		MaxTokens:    cfg.maxOutputTokens,
		SystemPrompt: resolvedSystem,
	}
	p.applyStreamingRuntimeProfile(req, stage, 0, architectSessionIDFromContext(ctx))
	return p.streamRequest(ctx, req, stage, p.timeout, onChunk)
}

// applyContinuationResult detects overlap, stitches net-new content, caches
// the new snapshot and structural analysis, and determines whether to continue.
func applyContinuationResult(
	state *continuationState,
	result *streamResult,
	cfg continuationConfig,
	onChunk func(string),
) bool {
	overlap := detectModeOverlap(state.mode, state.lastSnapshot, result.Text, cfg.overlapSearchLimit)
	streamContinuationNetNew(onChunk, overlap.netNew)
	stitchResult(state, &overlap, result)
	// One .String() and one analyzeStructure per round — cached for next round.
	state.lastSnapshot = state.accumulated.String()
	state.lastStructure = analyzeStructure(state.mode, state.lastSnapshot)
	return shouldContinue(state, result, overlap, cfg)
}

func streamContinuationNetNew(onChunk func(string), netNew string) {
	if onChunk == nil || netNew == "" {
		return
	}
	onChunk(netNew)
}

// stitchResult appends net-new content, updates metrics, and merges usage.
func stitchResult(state *continuationState, overlap *overlapResult, result *streamResult) {
	state.metrics.overlapBytesTotal += overlap.overlapLen
	state.metrics.netNewBytesTotal += len(overlap.netNew)
	state.accumulated.WriteString(overlap.netNew)
	state.length += len(overlap.netNew)
	state.usage = mergeUsage(state.usage, result.Usage)
}

// shouldContinue returns true if another continuation round is warranted.
// False when: model completed naturally, progress has decayed, or the
// accumulated text is structurally complete.
func shouldContinue(
	state *continuationState,
	result *streamResult,
	overlap overlapResult,
	cfg continuationConfig,
) bool {
	if !isTruncatedResult(result.StopReason) {
		return false
	}
	if !checkProgress(state, overlap, cfg) {
		state.metrics.abortReason = "progress_decay"
		return false
	}
	return !checkStructuralCompleteness(state.lastStructure)
}

// checkProgress validates that enough net-new content was produced. Updates
// the EMA tracker and returns false if progress has decayed below threshold.
func checkProgress(state *continuationState, overlap overlapResult, cfg continuationConfig) bool {
	received := len(overlap.netNew) + overlap.overlapLen
	if received == 0 {
		return false
	}
	state.progress.update(received, len(overlap.netNew))
	return state.progress.isHealthy(cfg.progressThreshold)
}

// checkStructuralCompleteness returns true if the cached structural context
// indicates a complete structure (balanced JSON, no open code fences in markdown).
func checkStructuralCompleteness(sc structuralContext) bool {
	return isStructurallyComplete(sc)
}

// --- State initialization and finalization ---

// initContinuationState creates the initial state from the first (truncated)
// stream result.
func initContinuationState(initial *streamResult, mode continuationMode, cfg continuationConfig) continuationState {
	acc := &strings.Builder{}
	acc.WriteString(initial.Text)
	snapshot := acc.String()
	return continuationState{
		accumulated:   acc,
		length:        len(initial.Text),
		usage:         cloneUsage(initial.Usage),
		mode:          mode,
		progress:      newProgressTracker(cfg.progressAlpha),
		lastSnapshot:  snapshot,
		lastStructure: analyzeStructure(mode, snapshot),
		metrics: continuationMetrics{
			netNewBytesTotal: len(initial.Text),
		},
	}
}

// finalizeContinuation logs metrics and streams the truncation indicator
// to the UI when the continuation was aborted (not naturally completed).
func finalizeContinuation(state *continuationState, onChunk func(string), stage string, logger *slog.Logger) {
	state.metrics.finalLength = state.length
	logContinuationMetrics(&state.metrics, stage, logger)
	if state.metrics.abortReason != "" {
		streamTruncationIndicator(onChunk)
	}
}

// streamTruncationIndicator sends the visual truncation notice to the UI.
func streamTruncationIndicator(onChunk func(string)) {
	if onChunk != nil {
		onChunk(truncationIndicator)
	}
}

// logContinuationMetrics emits structured metrics about the continuation run.
func logContinuationMetrics(metrics *continuationMetrics, stage string, logger *slog.Logger) {
	logger.Info("continuation complete",
		"stage", stage,
		"rounds_used", metrics.roundsUsed,
		"net_new_bytes", metrics.netNewBytesTotal,
		"overlap_bytes", metrics.overlapBytesTotal,
		"final_length", metrics.finalLength,
		"abort_reason", metrics.abortReason,
	)
}

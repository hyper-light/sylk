package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/llmruntime"
	promptskill "github.com/adalundhe/sylk/core/promptskills"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/signal"
	"github.com/adalundhe/sylk/core/skills"
)

type planningLLM interface {
	AnalyzeRequirements(ctx context.Context, query string, params map[string]any) (*Requirements, error)
	DesignArchitecture(ctx context.Context, requirements *Requirements, patterns *CodebasePatterns) (*SolutionArchitecture, error)
	GenerateTasks(ctx context.Context, architecture *SolutionArchitecture, constraints *PlanConstraints) ([]*AtomicTask, error)
	ComposeUserResponse(ctx context.Context, request plannerConversationRequest) (string, error)
	CompleteForToolLoop(ctx context.Context, req *providers.Request, stage string, onChunk func(string)) (*providers.Response, error)
	ConversationSystemPrompt() string
}

// PlannerStreamProvider is the subset of provider capabilities the planner
// needs. Satisfied by *providers.AnthropicProvider, *gateway.GatewayProvider,
// and test mocks.
type PlannerStreamProvider = plannerStreamProvider

type plannerStreamProvider interface {
	StreamWithHandler(ctx context.Context, req *providers.Request, handler providers.StreamHandler) error
}

type anthropicPlanner struct {
	provider           plannerStreamProvider
	maxTokens          int
	thinkingBudget     int
	contextWindow      int
	system             string
	conversationSystem string
	timeout            time.Duration
	logger             *slog.Logger
	stagePropagator    *resources.DeadlinePropagator

	// Signal bus for broadcasting time-pressure degradation signals.
	signalBus       signal.SignalBusInterface
	operationBudget time.Duration // total operation budget, set per-operation
	pressureOnce    sync.Once     // ensures at most one TimePressure signal per operation
}

// streamResult carries the full outcome of a single streaming LLM call,
// including the stop reason so callers can detect max_tokens truncation.
type streamResult struct {
	Text       string
	Usage      *providers.Usage
	StopReason providers.StopReason
}

type plannerThoughtCallback func(stage string, thought string)

type plannerThoughtContextKey struct{}

var ErrArchitectPlannerAuthNotConfigured = errors.New("architect planner auth not configured")

const plannerJSONSystemPrompt = `You are the Sylk Architect planner.
Return strictly valid JSON with no markdown, no prose, and no extra keys.
Never wrap JSON in code fences.
Keep outputs concise and deterministic.`

func (a *Architect) initPlanner(ctx context.Context, cfg Config) error {
	if !cfg.EnableLLM {
		return nil
	}

	if a.ensurePlanner(ctx) == nil {
		a.logger.Warn("architect llm planner unavailable; using deterministic fallback")
	}
	return nil
}

func (a *Architect) ensurePlanner(ctx context.Context) planningLLM {
	if !a.config.EnableLLM {
		return nil
	}
	if planner := a.currentPlanner(); planner != nil {
		return planner
	}

	a.plannerMu.Lock()
	defer a.plannerMu.Unlock()

	if a.planner != nil {
		return a.planner
	}

	planner, err := newAnthropicPlanner(ctx, a.config, a.logger, a.skills.GetAll())
	if err != nil {
		if !errors.Is(err, ErrArchitectPlannerAuthNotConfigured) {
			a.logger.Warn("architect llm planner init failed", "error", err)
		}
		return nil
	}

	a.planner = planner
	// Wire signal bus inline — write lock is already held by this method;
	// calling wirePlannerSignalBus() would deadlock on plannerMu.RLock().
	if ap, ok := planner.(*anthropicPlanner); ok && ap != nil {
		ap.signalBus = a.signalBus
	}
	a.logger.Info("architect llm planner enabled", "model", a.config.Model)
	return a.planner
}

func (a *Architect) currentPlanner() planningLLM {
	a.plannerMu.RLock()
	planner := a.planner
	a.plannerMu.RUnlock()
	return planner
}

func (a *Architect) tryAnalyzeRequirementsWithLLM(
	ctx context.Context,
	query string,
	params map[string]any,
) (*Requirements, bool) {
	planner := a.ensurePlanner(ctx)
	if planner == nil {
		a.logInfo("architect llm requirements: planner unavailable")
		return nil, false
	}
	requirements, err := planner.AnalyzeRequirements(ctx, query, params)
	if err != nil {
		a.logWarn("architect llm requirements fallback", "error", err)
		return nil, false
	}
	if requirements == nil || len(requirements.Goals) == 0 {
		a.logWarn("architect llm requirements: empty goals, using deterministic fallback")
		return nil, false
	}
	a.logInfo("architect llm requirements ok", "goals", len(requirements.Goals))
	return requirements, true
}

func (a *Architect) tryDesignArchitectureWithLLM(
	ctx context.Context,
	requirements *Requirements,
	patterns *CodebasePatterns,
) (*SolutionArchitecture, bool) {
	planner := a.ensurePlanner(ctx)
	if planner == nil {
		a.logInfo("architect llm design: planner unavailable")
		return nil, false
	}
	architecture, err := planner.DesignArchitecture(ctx, requirements, patterns)
	if err != nil {
		a.logWarn("architect llm design fallback", "error", err)
		return nil, false
	}
	if architecture == nil || len(architecture.Components) == 0 {
		a.logWarn("architect llm design: empty components, using deterministic fallback")
		return nil, false
	}
	a.logInfo("architect llm design ok", "components", len(architecture.Components))
	return architecture, true
}

func (a *Architect) tryGenerateTasksWithLLM(
	ctx context.Context,
	architecture *SolutionArchitecture,
	constraints *PlanConstraints,
) ([]*AtomicTask, bool) {
	planner := a.ensurePlanner(ctx)
	if planner == nil {
		a.logInfo("architect llm tasks: planner unavailable")
		return nil, false
	}
	tasks, err := planner.GenerateTasks(ctx, architecture, constraints)
	if err != nil {
		a.logWarn("architect llm task fallback", "error", err)
		return nil, false
	}
	if len(tasks) == 0 {
		a.logWarn("architect llm tasks: empty result, using deterministic fallback")
		return nil, false
	}
	a.logInfo("architect llm tasks ok", "count", len(tasks))
	return normalizeTaskGraph(tasks), true
}

func newAnthropicPlanner(ctx context.Context, cfg Config, logger *slog.Logger, goSkills []*skills.Skill) (planningLLM, error) {
	defaults := providers.DefaultBaseConfig()
	providerCfg := providers.AnthropicConfig{
		BaseConfig: providers.BaseConfig{
			APIKey:         cfg.AnthropicAPIKey,
			Model:          cfg.Model,
			MaxTokens:      cfg.MaxOutputTokens,
			Temperature:    0.7,
			MaxRetries:     cfg.LLMRetryMax,
			RetryBaseDelay: defaults.RetryBaseDelay,
			RetryMaxDelay:  defaults.RetryMaxDelay,
		},
		AuthMode:         cfg.AnthropicAuthMode,
		EnableCaching:    !cfg.DisablePromptCache,
		PromptCacheTTL:   cfg.PromptCacheTTL,
		SystemPrompt:     buildPlannerSystemPrompt(cfg.SystemPrompt),
		AdaptiveThinking: true,
	}
	if providerCfg.Model == "" {
		providerCfg.Model = DefaultArchitectModel
	}
	if providerCfg.MaxTokens == 0 {
		providerCfg.MaxTokens = DefaultMaxOutputTokens
	}

	diskSkills := promptskill.DiscoverAgentSkills("architect")
	promptSkills := promptskill.MergePromptSkills(diskSkills, goSkills)
	rawProvider, err := providers.NewAnthropicProvider(ctx, providerCfg, promptSkills...)
	if err != nil {
		if strings.Contains(err.Error(), "api_key") {
			return nil, ErrArchitectPlannerAuthNotConfigured
		}
		return nil, err
	}

	var stream plannerStreamProvider = rawProvider
	if cfg.PlannerProviderWrapper != nil {
		stream = cfg.PlannerProviderWrapper(rawProvider)
	}

	counter := providers.NewCharacterBasedCounter(providers.DefaultTokenCounterConfig())
	return &anthropicPlanner{
		provider:           stream,
		maxTokens:          cfg.MaxOutputTokens,
		thinkingBudget:     0, // Adaptive thinking — model allocates dynamically.
		contextWindow:      counter.MaxContextTokens(providerCfg.Model),
		system:             buildPlannerSystemPrompt(cfg.SystemPrompt),
		conversationSystem: buildPlannerConversationSystemPrompt(cfg.SystemPrompt),
		timeout:            cfg.LLMRequestTimeout,
		logger:             logger,
		stagePropagator:    &resources.DeadlinePropagator{CleanupBuffer: 2 * time.Second},
	}, nil
}

// newPlannerFromProvider builds a planner around an externally-created,
// gateway-wrapped provider. Used by SwapModel for cross-provider swaps.
func newPlannerFromProvider(provider plannerStreamProvider, cfg Config, logger *slog.Logger) planningLLM {
	maxTokens := cfg.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxOutputTokens
	}
	counter := providers.NewCharacterBasedCounter(providers.DefaultTokenCounterConfig())
	return &anthropicPlanner{
		provider:           provider,
		maxTokens:          maxTokens,
		thinkingBudget:     0,
		contextWindow:      counter.MaxContextTokens(cfg.Model),
		system:             buildPlannerSystemPrompt(cfg.SystemPrompt),
		conversationSystem: buildPlannerConversationSystemPrompt(cfg.SystemPrompt),
		timeout:            cfg.LLMRequestTimeout,
		logger:             logger,
		stagePropagator:    &resources.DeadlinePropagator{CleanupBuffer: 2 * time.Second},
	}
}

func buildPlannerSystemPrompt(base string) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return plannerJSONSystemPrompt
	}
	return plannerJSONSystemPrompt + "\n\nContext:\n" + trimmed
}

func withPlannerThoughtCallback(ctx context.Context, cb plannerThoughtCallback) context.Context {
	if cb == nil {
		return ctx
	}
	return context.WithValue(ctx, plannerThoughtContextKey{}, cb)
}

func emitPlannerThought(ctx context.Context, stage string, thought string) {
	if ctx == nil {
		return
	}
	cb, ok := ctx.Value(plannerThoughtContextKey{}).(plannerThoughtCallback)
	if !ok || cb == nil {
		return
	}
	stage = strings.TrimSpace(stage)
	thought = strings.TrimSpace(thought)
	if thought == "" {
		return
	}
	cb(stage, thought)
}

func (p *anthropicPlanner) AnalyzeRequirements(
	ctx context.Context,
	query string,
	params map[string]any,
) (*Requirements, error) {
	prompt := buildRequirementsPrompt(query, params)
	var payload requirementsPayload
	if err := p.requestJSONWithBudgets(
		ctx,
		prompt,
		&payload,
		requirementsBudgets(p.maxTokens),
		p.systemForStage("requirements"),
		"requirements",
	); err != nil {
		return nil, err
	}
	return payload.toRequirements(query, params), nil
}

func (p *anthropicPlanner) DesignArchitecture(
	ctx context.Context,
	requirements *Requirements,
	patterns *CodebasePatterns,
) (*SolutionArchitecture, error) {
	prompt := buildDesignPrompt(requirements, patterns)
	var payload architecturePayload
	if err := p.requestJSONWithBudgets(
		ctx,
		prompt,
		&payload,
		designBudgets(p.maxTokens),
		p.systemForStage("design"),
		"design",
	); err != nil {
		return nil, err
	}
	return payload.toArchitecture(requirements), nil
}

func (p *anthropicPlanner) GenerateTasks(
	ctx context.Context,
	architecture *SolutionArchitecture,
	constraints *PlanConstraints,
) ([]*AtomicTask, error) {
	prompt := buildTaskPrompt(architecture, constraints)
	tasks, err := p.parseTasksWithBudgets(
		ctx,
		prompt,
		taskBudgets(p.maxTokens),
		p.systemForStage("tasks"),
		"tasks",
	)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("planner returned zero tasks")
	}
	return tasks, nil
}

func (p *anthropicPlanner) parseTasksWithBudgets(
	ctx context.Context,
	prompt string,
	budgets []int,
	system string,
	stage string,
) ([]*AtomicTask, error) {
	var lastErr error
	for i, budget := range budgets {
		text, err := p.requestJSONText(ctx, prompt, budget, system, stage, i == len(budgets)-1)
		if err != nil {
			lastErr = err
			continue
		}
		tasks, parseErr := parseTaskPayload(text)
		if parseErr == nil {
			return tasks, nil
		}
		lastErr = parseErr
	}
	return nil, lastErr
}

func (p *anthropicPlanner) requestJSONWithBudgets(
	ctx context.Context,
	prompt string,
	out any,
	budgets []int,
	system string,
	stage string,
) error {
	var lastErr error
	for i, budget := range budgets {
		text, err := p.requestJSONText(ctx, prompt, budget, system, stage, i == len(budgets)-1)
		if err != nil {
			lastErr = err
			continue
		}
		decodeErr := decodeJSONPayload(text, out)
		if decodeErr == nil {
			return nil
		}
		lastErr = decodeErr
	}
	return lastErr
}

// requestJSONText performs a single-shot request at the given budget.
// At non-final budgets, truncation (max_tokens) returns an error immediately
// so the caller escalates to the next budget without wasting continuation
// round trips. At the final budget, continuation is used to extend the
// response since there is no higher budget to escalate to.
func (p *anthropicPlanner) requestJSONText(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	finalBudget bool,
) (string, error) {
	p.checkDeadlinePressure(ctx, stage)
	if finalBudget {
		text, _, err := p.requestTextWithContinuation(ctx, prompt, maxTokens, system, stage)
		return text, err
	}
	// Non-final budget: single-shot, fast-escalate on truncation.
	sr, err := p.requestTextOnce(ctx, prompt, maxTokens, system, stage)
	if err != nil {
		return "", err
	}
	if sr.StopReason == providers.StopReasonMaxTokens {
		return "", fmt.Errorf("response truncated at %d token budget, escalating", maxTokens)
	}
	return sr.Text, nil
}

// requestTextOnce performs a single-shot request and returns the full
// streamResult including StopReason, allowing callers to react to truncation.
func (p *anthropicPlanner) requestTextOnce(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
) (*streamResult, error) {
	lease := newPlannerRequestLease(ctx, p, stage)
	var result *streamResult
	err := lease.run(p.timeout, false, func(reqCtx context.Context, attemptTimeout time.Duration) error {
		var innerErr error
		result, innerErr = p.requestTextStreamingOnceWithTimeout(reqCtx, prompt, maxTokens, system, stage, attemptTimeout, nil)
		return innerErr
	})
	return result, err
}

// requestTextWithContinuation performs a request with automatic continuation
// on max_tokens, used for final-budget JSON calls where no escalation remains.
func (p *anthropicPlanner) requestTextWithContinuation(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
) (string, *providers.Usage, error) {
	return p.requestTextStreamingWithLease(ctx, prompt, maxTokens, system, stage, nil)
}

func (p *anthropicPlanner) requestTextStreamingWithMaxTokens(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	onChunk func(string),
) (string, *providers.Usage, error) {
	return p.requestTextStreamingWithLease(ctx, prompt, maxTokens, system, stage, onChunk)
}

func (p *anthropicPlanner) requestTextStreamingWithLease(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	onChunk func(string),
) (string, *providers.Usage, error) {
	lease := newPlannerRequestLease(ctx, p, stage)
	initial, err := p.requestTextStreamingOnceWithLease(lease, prompt, maxTokens, system, stage, onChunk)
	if err != nil {
		return "", nil, err
	}
	if !isTruncatedResult(initial.StopReason) {
		return initial.Text, initial.Usage, nil
	}
	mode := continuationModeFromOnChunk(onChunk)
	cfg := deriveContinuationConfig(p.contextWindow, maxTokens, defaultCharsPerToken)
	return p.runContinuationLoop(lease, prompt, initial, cfg, mode, system, stage, onChunk)
}

func (p *anthropicPlanner) requestTextStreamingOnceWithLease(
	lease *plannerRequestLease,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	onChunk func(string),
) (*streamResult, error) {
	var result *streamResult
	err := lease.run(p.timeout, onChunk != nil, func(reqCtx context.Context, attemptTimeout time.Duration) error {
		var innerErr error
		result, innerErr = p.requestTextStreamingOnceWithTimeout(reqCtx, prompt, maxTokens, system, stage, attemptTimeout, onChunk)
		return innerErr
	})
	return result, err
}

func (p *anthropicPlanner) requestTextStreamingOnce(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	onChunk func(string),
) (*streamResult, error) {
	return p.requestTextStreamingOnceWithTimeout(ctx, prompt, maxTokens, system, stage, p.timeout, onChunk)
}

func (p *anthropicPlanner) requestTextStreamingOnceWithTimeout(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	timeout time.Duration,
	onChunk func(string),
) (*streamResult, error) {
	resolvedSystem := p.resolveSystemPrompt(system)
	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
		MaxTokens:    maxTokens,
		SystemPrompt: resolvedSystem,
	}
	p.applyStreamingRuntimeProfile(req, stage, p.resolveThinkingBudget(maxTokens), architectSessionIDFromContext(ctx))
	return p.streamRequest(ctx, req, stage, timeout, onChunk)
}

// streamRequest executes a single streaming request and returns the result.
// Separated from requestTextStreamingOnce so continuation can reuse it
// with custom multi-turn requests.
func (p *anthropicPlanner) streamRequest(
	ctx context.Context,
	req *providers.Request,
	stage string,
	timeout time.Duration,
	onChunk func(string),
) (*streamResult, error) {
	var text strings.Builder
	emitter := newThoughtEmitter(ctx)
	var finalUsage *providers.Usage
	var stopReason providers.StopReason
	err := p.streamWithProgressTimeout(ctx, stage, req, timeout, func(chunk *providers.StreamChunk) error {
		switch chunk.Type {
		case providers.ChunkTypeStart:
			// RetryReset is set by the provider's retryAwareHandler when a
			// stream retry replays from the beginning. Signal the UI to
			// discard prior partial content before the replayed chunks arrive.
			if chunk.RetryReset {
				emitStreamRetryReset(ctx)
			}
			text.Reset()
			emitter = newThoughtEmitter(ctx)
			if chunk.Usage != nil && chunk.Usage.InputTokens > 0 {
				emitArchitectEarlyUsage(ctx, chunk.Usage.InputTokens)
			}
		case providers.ChunkTypeText:
			text.WriteString(chunk.Text)
			if onChunk != nil {
				onChunk(chunk.Text)
			}
		case providers.ChunkTypeThought:
			if llmruntime.EmitsThoughts(req) {
				if thought := emitter.addDelta(chunk.Text); thought != "" {
					emitPlannerThought(ctx, stage, thought)
				}
			}
		case providers.ChunkTypeEnd:
			if chunk.Usage != nil {
				finalUsage = chunk.Usage
				accumulateArchitectUsage(ctx, chunk.Usage)
			}
			if chunk.StopReason != "" {
				stopReason = chunk.StopReason
			}
		}
		return nil
	})
	// Flush any remaining thought content that didn't trigger an emission.
	if llmruntime.EmitsThoughts(req) {
		if thought := emitter.flush(); thought != "" {
			emitPlannerThought(ctx, stage, thought)
		}
	}
	if err != nil {
		// If the stream was interrupted but we accumulated partial text,
		// treat it as a truncated result rather than discarding the work.
		// The continuation loop picks up StopReasonError via isTruncatedResult.
		if partial := strings.TrimSpace(text.String()); partial != "" && isRecoverableStreamError(ctx, err) {
			return &streamResult{
				Text:       partial,
				Usage:      finalUsage,
				StopReason: providers.StopReasonError,
			}, nil
		}
		return nil, err
	}
	result := strings.TrimSpace(text.String())
	if result == "" {
		return nil, fmt.Errorf("planner returned empty content")
	}
	return &streamResult{
		Text:       result,
		Usage:      finalUsage,
		StopReason: stopReason,
	}, nil
}

// streamRequestFull executes a streaming request and accumulates the full
// response including tool calls via StreamAccumulator. Side effects (text→onChunk,
// thought→emit, start→reset/earlyUsage, end→accumulateUsage) are preserved so
// the UI pipeline stays in sync.
func (p *anthropicPlanner) streamRequestFull(
	ctx context.Context,
	req *providers.Request,
	stage string,
	onChunk func(string),
) (*providers.Response, error) {
	return p.streamRequestFullWithTimeout(ctx, req, stage, p.timeout, onChunk)
}

func (p *anthropicPlanner) streamRequestFullWithTimeout(
	ctx context.Context,
	req *providers.Request,
	stage string,
	timeout time.Duration,
	onChunk func(string),
) (*providers.Response, error) {
	accumulator := providers.NewStreamAccumulator()
	emitter := newThoughtEmitter(ctx)

	streamStart := time.Now()
	var textChunks, thoughtChunks, toolChunks, otherChunks int

	streamMsgSummary := make([]string, len(req.Messages))
	for i, m := range req.Messages {
		extra := ""
		if len(m.ToolCalls) > 0 {
			extra = fmt.Sprintf("+tc:%d", len(m.ToolCalls))
		}
		if m.ToolCallID != "" {
			extra = "+result:" + m.ToolName
		}
		streamMsgSummary[i] = fmt.Sprintf("%s(%d)%s", m.Role, len(m.Content), extra)
	}
	architectDebugLog().Debug("stream_full: START",
		"stage", stage,
		"max_tokens", req.MaxTokens,
		"thinking_budget", req.ThinkingBudget,
		"tools_count", len(req.Tools),
		"messages_count", len(req.Messages),
		"messages_detail", strings.Join(streamMsgSummary, " | "))

	err := p.streamWithProgressTimeout(ctx, stage, req, timeout, func(chunk *providers.StreamChunk) error {
		accumulator.Add(chunk)

		switch chunk.Type {
		case providers.ChunkTypeStart:
			if chunk.RetryReset {
				emitStreamRetryReset(ctx)
			}
			emitter = newThoughtEmitter(ctx)
			if chunk.Usage != nil && chunk.Usage.InputTokens > 0 {
				emitArchitectEarlyUsage(ctx, chunk.Usage.InputTokens)
				architectDebugLog().Debug("stream_full: EARLY_USAGE",
					"stage", stage,
					"input_tokens", chunk.Usage.InputTokens)
			}
		case providers.ChunkTypeText:
			textChunks++
			if onChunk != nil {
				onChunk(chunk.Text)
			}
		case providers.ChunkTypeThought:
			thoughtChunks++
			if llmruntime.EmitsThoughts(req) {
				if thought := emitter.addDelta(chunk.Text); thought != "" {
					emitPlannerThought(ctx, stage, thought)
				}
			}
		case providers.ChunkTypeToolStart, providers.ChunkTypeToolDelta, providers.ChunkTypeToolEnd:
			toolChunks++
		case providers.ChunkTypeEnd:
			if chunk.Usage != nil {
				accumulateArchitectUsage(ctx, chunk.Usage)
				architectDebugLog().Debug("stream_full: END_USAGE",
					"stage", stage,
					"stop_reason", string(chunk.StopReason),
					"input_tokens", chunk.Usage.InputTokens,
					"output_tokens", chunk.Usage.OutputTokens,
					"total_tokens", chunk.Usage.TotalTokens,
					"cache_read", chunk.Usage.CacheReadTokens,
					"cache_write", chunk.Usage.CacheWriteTokens)
			}
		default:
			otherChunks++
		}
		return nil
	})
	// Flush any remaining thought content that didn't trigger an emission.
	if llmruntime.EmitsThoughts(req) {
		if thought := emitter.flush(); thought != "" {
			emitPlannerThought(ctx, stage, thought)
		}
	}

	architectDebugLog().Debug("stream_full: STREAM_DONE",
		"stage", stage,
		"elapsed", time.Since(streamStart).String(),
		"text_chunks", textChunks,
		"thought_chunks", thoughtChunks,
		"tool_chunks", toolChunks,
		"other_chunks", otherChunks,
		"err", err)

	if err != nil {
		return nil, err
	}

	resp := accumulator.Response()
	if resp == nil {
		architectDebugLog().Debug("stream_full: NIL_RESPONSE", "stage", stage)
		return nil, fmt.Errorf("planner returned nil response")
	}

	architectDebugLog().Debug("stream_full: RESPONSE",
		"stage", stage,
		"content_len", len(resp.Content),
		"thinking_len", len(resp.Thinking),
		"tool_call_count", len(resp.ToolCalls),
		"stop_reason", string(resp.StopReason),
		"model", resp.Model)

	return resp, nil
}

func (p *anthropicPlanner) streamWithProgressTimeout(
	ctx context.Context,
	stage string,
	req *providers.Request,
	timeout time.Duration,
	handler providers.StreamHandler,
) error {
	if timeout <= 0 {
		return p.provider.StreamWithHandler(ctx, req, handler)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	watchdog := newPlannerStreamWatchdog(streamCtx, cancel, timeout)
	defer watchdog.Stop()

	err := p.provider.StreamWithHandler(streamCtx, req, func(chunk *providers.StreamChunk) error {
		watchdog.Progress()
		return handler(chunk)
	})

	if watchdog.TimedOut() {
		p.logger.Warn("planner stream progress timeout",
			"stage", stage,
			"timeout", timeout.String(),
			"ctx_deadline", contextDeadlineString(ctx))
		return context.DeadlineExceeded
	}
	return err
}

type plannerStreamWatchdog struct {
	done     chan struct{}
	progress chan struct{}
	timedOut atomic.Bool
}

func newPlannerStreamWatchdog(
	ctx context.Context,
	cancel context.CancelFunc,
	timeout time.Duration,
) *plannerStreamWatchdog {
	w := &plannerStreamWatchdog{
		done:     make(chan struct{}),
		progress: make(chan struct{}, 1),
	}
	go w.run(ctx, cancel, timeout)
	return w
}

func (w *plannerStreamWatchdog) run(
	ctx context.Context,
	cancel context.CancelFunc,
	timeout time.Duration,
) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-timer.C:
			w.timedOut.Store(true)
			cancel()
			return
		case <-w.progress:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)
		}
	}
}

func (w *plannerStreamWatchdog) Progress() {
	if w == nil {
		return
	}
	select {
	case w.progress <- struct{}{}:
	default:
	}
}

func (w *plannerStreamWatchdog) Stop() {
	if w == nil {
		return
	}
	close(w.done)
}

func (w *plannerStreamWatchdog) TimedOut() bool {
	if w == nil {
		return false
	}
	return w.timedOut.Load()
}

// CompleteForToolLoop performs a streaming request that accumulates the full
// response including any tool calls. This bridges the streaming planner to the
// synchronous tool loop pattern used by all other agents.
func (p *anthropicPlanner) CompleteForToolLoop(
	ctx context.Context,
	req *providers.Request,
	stage string,
	onChunk func(string),
) (*providers.Response, error) {
	architectDebugLog().Debug("complete_for_tool_loop: ENTRY",
		"stage", stage,
		"max_tokens", req.MaxTokens,
		"thinking_budget", req.ThinkingBudget,
		"tools_count", len(req.Tools),
		"messages_count", len(req.Messages),
		"timeout", p.timeout.String(),
		"ctx_deadline", contextDeadlineString(ctx))

	lease := newPlannerRequestLease(ctx, p, stage)
	start := time.Now()
	var resp *providers.Response
	err := lease.run(p.timeout, onChunk != nil, func(reqCtx context.Context, attemptTimeout time.Duration) error {
		var innerErr error
		resp, innerErr = p.streamRequestFullWithTimeout(reqCtx, req, stage, attemptTimeout, onChunk)
		return innerErr
	})

	architectDebugLog().Debug("complete_for_tool_loop: DONE",
		"stage", stage,
		"elapsed", time.Since(start).String(),
		"err", err,
		"has_response", resp != nil)

	return resp, err
}

// propagateDeadline uses the stage-level propagator to create a child context
// whose deadline is min(p.timeout, parent_remaining - cleanupBuffer).
func (p *anthropicPlanner) propagateDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return p.propagateDeadlineWithTimeout(ctx, p.timeout)
}

func (p *anthropicPlanner) propagateDeadlineWithTimeout(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	if p.stagePropagator != nil {
		return p.stagePropagator.Propagate(ctx, timeout)
	}
	return context.WithTimeout(ctx, timeout)
}

func (p *anthropicPlanner) logRequestLeaseRefresh(
	parent context.Context,
	localTimeout time.Duration,
	stage string,
	refreshes int,
	err error,
) {
	p.logger.Info("planner request lease refreshed",
		"stage", stage,
		"refresh_count", refreshes,
		"local_timeout", localTimeout.String(),
		"parent_deadline", contextDeadlineString(parent),
		"error", err)
}

// ConversationSystemPrompt returns the conversation-mode system prompt.
func (p *anthropicPlanner) ConversationSystemPrompt() string {
	return p.conversationSystem
}

const (
	truncationIndicator = "\n\n---\n*[Response truncated due to length. Ask me to continue if you need more detail.]*"
	// contextDeadlineGuard is the minimum remaining time needed to attempt a continuation round.
	contextDeadlineGuard = 10 * time.Second
)

// requestTextStreamingWithContinuation wraps requestTextStreamingOnce with
// automatic continuation when the model hits the max output token limit.
//
// Continuation uses:
//   - Z-algorithm overlap detection (O(n) worst case) to deduplicate repeated text
//   - EMA-based progress decay to stop when the model is repeating more than producing
//   - Structural completeness detection to stop when JSON is balanced or markdown fences close
//   - Sliding-window prefill to prevent unbounded context growth
//   - All bounds derived from model parameters (contextWindow, maxOutputTokens)
func (p *anthropicPlanner) requestTextStreamingWithContinuation(
	ctx context.Context,
	prompt string,
	maxTokens int,
	system string,
	stage string,
	onChunk func(string),
) (string, *providers.Usage, error) {
	return p.requestTextStreamingWithLease(ctx, prompt, maxTokens, system, stage, onChunk)
}

// mergeUsage sums two Usage values, handling nil inputs.
func mergeUsage(a *providers.Usage, b *providers.Usage) *providers.Usage {
	if a == nil && b == nil {
		return nil
	}
	result := &providers.Usage{}
	if a != nil {
		result.InputTokens = a.InputTokens
		result.OutputTokens = a.OutputTokens
		result.TotalTokens = a.TotalTokens
		result.CacheReadTokens = a.CacheReadTokens
		result.CacheWriteTokens = a.CacheWriteTokens
	}
	if b != nil {
		result.InputTokens += b.InputTokens
		result.OutputTokens += b.OutputTokens
		result.TotalTokens += b.TotalTokens
		result.CacheReadTokens += b.CacheReadTokens
		result.CacheWriteTokens += b.CacheWriteTokens
	}
	return result
}

// cloneUsage returns a shallow copy of a Usage pointer, or nil.
func cloneUsage(u *providers.Usage) *providers.Usage {
	if u == nil {
		return nil
	}
	copy := *u
	return &copy
}

// isTruncatedResult returns true for stop reasons that indicate the response
// was cut short and continuation may recover additional content. Both
// max_tokens (model hit output budget) and error (stream interrupted by
// timeout/cancellation with partial text recovered) qualify.
func isTruncatedResult(reason providers.StopReason) bool {
	return reason == providers.StopReasonMaxTokens || reason == providers.StopReasonError
}

// isRecoverableStreamError returns true if the error indicates a stream
// interruption (timeout, cancellation) where accumulated partial text
// should be preserved rather than discarded.
func isRecoverableStreamError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// hasTimeForContinuation checks whether the context has enough remaining
// deadline to justify another API call.
func hasTimeForContinuation(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true // No deadline set — proceed.
	}
	return time.Until(deadline) >= contextDeadlineGuard
}

// timePressureFraction is the remaining-budget fraction below which
// a time pressure signal is emitted. Derived from the 80/20 rule:
// 80% of work completes in 20% of the time budget.
const timePressureFraction = 0.20

// checkDeadlinePressure broadcasts a TimePressure signal through the signal bus
// when the operation deadline is approaching (remaining < 20% of total budget).
// Uses sync.Once to ensure at most one signal per planning operation — callers
// invoke this on every stage/budget-level entry; only the first crossing fires.
func (p *anthropicPlanner) checkDeadlinePressure(ctx context.Context, stage string) {
	if p.signalBus == nil || p.operationBudget <= 0 {
		return
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}
	remaining := time.Until(deadline)
	fraction := float64(remaining) / float64(p.operationBudget)
	if fraction >= timePressureFraction {
		return
	}
	elapsed := p.operationBudget - remaining
	p.pressureOnce.Do(func() {
		msg := signal.SignalMessage{
			ID:       "tp_" + stage + "_" + time.Now().Format("150405"),
			Signal:   signal.TimePressure,
			TargetID: "architect",
			Reason:   "planning operation approaching deadline",
			Payload: signal.TimePressurePayload{
				AgentType: "architect",
				Operation: "planning",
				Elapsed:   elapsed,
				Remaining: remaining,
				Stage:     stage,
				Suggestion: fmt.Sprintf(
					"Planning is taking longer than expected (stage: %s, elapsed: %s, remaining: %s)",
					stage, elapsed.Truncate(time.Second), remaining.Truncate(time.Second)),
			},
			SentAt: time.Now(),
		}
		if err := p.signalBus.Broadcast(msg); err != nil {
			p.logger.Warn("failed to broadcast time-pressure signal",
				"stage", stage, "error", err)
		}
	})
}

// resetOperationState resets per-operation mutable state on the planner.
// Called at the start of each planning operation so pressure debouncing
// and budget tracking are fresh.
func (p *anthropicPlanner) resetOperationState(budget time.Duration) {
	p.operationBudget = budget
	p.pressureOnce = sync.Once{} // fresh Once for this operation
}

// thoughtEmitter batches thought deltas and emits snapshots only when
// warranted, avoiding the O(n^2) total allocation of returning buffer.String()
// on every delta. Emission triggers: first non-empty thought, sentence
// boundary in delta, or buffer doubled since last emit.
type thoughtEmitter struct {
	buffer      strings.Builder
	lastEmitLen int
	hasCallback bool
}

// newThoughtEmitter creates a thought emitter. When hasCallback is false,
// addDelta skips materialization entirely.
func newThoughtEmitter(ctx context.Context) thoughtEmitter {
	return thoughtEmitter{hasCallback: hasPlannerThoughtCallback(ctx)}
}

// addDelta appends delta to the buffer and returns a snapshot only when
// emission is warranted. Returns "" to skip emission.
func (e *thoughtEmitter) addDelta(delta string) string {
	if delta == "" || !e.hasCallback {
		if delta != "" {
			e.buffer.WriteString(delta)
		}
		return ""
	}
	e.buffer.WriteString(delta)
	if !e.shouldEmit(delta) {
		return ""
	}
	e.lastEmitLen = e.buffer.Len()
	return strings.TrimSpace(e.buffer.String())
}

// flush returns the final buffer snapshot if any content remains unEmitted
// since the last addDelta emission. Call after the thinking block ends to
// deliver the tail of the thought.
func (e *thoughtEmitter) flush() string {
	if !e.hasCallback || e.buffer.Len() == 0 || e.buffer.Len() == e.lastEmitLen {
		return ""
	}
	e.lastEmitLen = e.buffer.Len()
	return strings.TrimSpace(e.buffer.String())
}

// shouldEmit returns true when a snapshot should be published.
func (e *thoughtEmitter) shouldEmit(delta string) bool {
	return containsSentenceBoundary(delta)
}

// containsSentenceBoundary returns true if s contains a sentence-ending
// character that warrants a thought snapshot emission.
func containsSentenceBoundary(s string) bool {
	for _, ch := range s {
		switch ch {
		case '.', '!', '?', '\n':
			return true
		}
	}
	return false
}

// hasPlannerThoughtCallback returns true if the context carries a thought
// callback. Used as a fast-path check to skip materialization.
func hasPlannerThoughtCallback(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	cb, ok := ctx.Value(plannerThoughtContextKey{}).(plannerThoughtCallback)
	return ok && cb != nil
}

// resolveThinkingBudget returns the thinking budget for a planner request.
// Returns 0 when thinkingBudget is not explicitly configured — this is the
// normal case with adaptive thinking, where the provider handles allocation
// dynamically and the request should not carry a fixed budget.
// When a fixed budget IS set, clamps to [0, maxTokens-1].
func (p *anthropicPlanner) resolveThinkingBudget(maxTokens int) int {
	if p.thinkingBudget <= 0 {
		return 0
	}
	if maxTokens < 2048 {
		return 0
	}
	if p.thinkingBudget >= maxTokens {
		return maxTokens - 1
	}
	return p.thinkingBudget
}

func (p *anthropicPlanner) systemForStage(stage string) string {
	stagePrompt := strings.TrimSpace(ArchitectPlannerPromptForStage(stage))
	if stagePrompt == "" {
		return p.system
	}
	return buildPlannerSystemPrompt(stagePrompt)
}

func (p *anthropicPlanner) resolveSystemPrompt(system string) string {
	trimmed := strings.TrimSpace(system)
	if trimmed != "" {
		return trimmed
	}
	return p.system
}

func requirementsBudgets(base int) []int {
	return compactBudgets(base, 1536, 3072, 4096)
}

func designBudgets(base int) []int {
	return compactBudgets(base, 4096, 6144, 8192)
}

func taskBudgets(base int) []int {
	return compactBudgets(base, 3072, 6144, 8192)
}

func compactBudgets(base int, a int, b int, c int) []int {
	values := []int{maxInt(base, a), maxInt(base*2, b), maxInt(base*3, c)}
	seen := map[int]struct{}{}
	budgets := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		budgets = append(budgets, value)
	}
	return budgets
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

type requirementsPayload struct {
	Goals                   json.RawMessage `json:"goals"`
	Constraints             json.RawMessage `json:"constraints"`
	Dependencies            json.RawMessage `json:"dependencies"`
	ClarificationQuestions  json.RawMessage `json:"clarification_questions"`
	Unknowns                json.RawMessage `json:"unknowns"`
	Recommendations         json.RawMessage `json:"provisional_recommendations"`
	Tradeoffs               json.RawMessage `json:"tradeoffs"`
	RecommendationNarrative string          `json:"recommendation_narrative"`
	Scope                   string          `json:"scope"`
	Priority                string          `json:"priority"`
}

func (p requirementsPayload) toRequirements(query string, params map[string]any) *Requirements {
	requirements := &Requirements{
		Query:        query,
		Goals:        parseStringList(p.Goals),
		Constraints:  parseStringList(p.Constraints),
		Dependencies: parseStringList(p.Dependencies),
		Scope:        nonEmptyString(p.Scope, "project"),
		Priority:     strings.TrimSpace(p.Priority),
	}
	requirements.Metadata = requirementsMetadataFromPayload(p)
	if len(requirements.Goals) == 0 {
		requirements.Goals = []string{query}
	}
	if params == nil {
		return requirements
	}
	applyRequirementOverrides(requirements, params)
	return requirements
}

func requirementsMetadataFromPayload(payload requirementsPayload) map[string]any {
	questions := parseStringList(payload.ClarificationQuestions)
	unknowns := parseStringList(payload.Unknowns)
	recommendations := parseStringList(payload.Recommendations)
	tradeoffs := parseStringList(payload.Tradeoffs)
	if len(questions) == 0 && len(unknowns) == 0 && len(recommendations) == 0 && len(tradeoffs) == 0 {
		return nil
	}
	metadata := map[string]any{}
	if len(questions) > 0 {
		metadata["clarification_questions"] = questions
	}
	if len(unknowns) > 0 {
		metadata["unknowns"] = unknowns
	}
	if len(recommendations) > 0 {
		metadata["provisional_recommendations"] = recommendations
	}
	if len(tradeoffs) > 0 {
		metadata["tradeoffs"] = tradeoffs
	}
	if narrative := strings.TrimSpace(payload.RecommendationNarrative); narrative != "" {
		metadata["recommendation_narrative"] = narrative
	}
	return metadata
}

func applyRequirementOverrides(requirements *Requirements, params map[string]any) {
	if requirements == nil || params == nil {
		return
	}
	if scope, ok := params["scope"].(string); ok && scope != "" {
		requirements.Scope = scope
	}
	if goals, ok := params["goals"].([]string); ok && len(goals) > 0 {
		requirements.Goals = goals
	}
	if constraints, ok := params["constraints"].([]string); ok && len(constraints) > 0 {
		requirements.Constraints = constraints
	}
}

type architecturePayload struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Components  []ComponentSpec     `json:"components"`
	Interfaces  []InterfaceSpec     `json:"interfaces"`
	Patterns    json.RawMessage     `json:"patterns"`
	Layers      []ArchitectureLayer `json:"layers"`
}

func (p architecturePayload) toArchitecture(requirements *Requirements) *SolutionArchitecture {
	name := strings.TrimSpace(p.Name)
	if name == "" && requirements != nil {
		name = fmt.Sprintf("Architecture for: %s", truncateString(requirements.Query, 50))
	}
	desc := strings.TrimSpace(p.Description)
	if desc == "" && requirements != nil {
		desc = requirements.Query
	}
	return &SolutionArchitecture{
		Name:        name,
		Description: desc,
		Components:  p.Components,
		Interfaces:  p.Interfaces,
		Patterns:    parseStringList(p.Patterns),
		Layers:      p.Layers,
	}
}

type taskListPayload struct {
	Tasks []taskPayload `json:"tasks"`
}

type taskPayload struct {
	ID              string   `json:"id"`
	Slug            string   `json:"slug,omitempty"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	AgentType       string   `json:"agent_type"`
	SuccessCriteria []string `json:"success_criteria"`
	Dependencies    []string `json:"dependencies"`
	EstimatedTokens int      `json:"estimated_tokens"`
	Complexity      string   `json:"complexity"`

	// Co-tenancy fields for compound node dispatch.
	CoAgents          []string            `json:"co_agents,omitempty"`
	CollaborationMode string              `json:"collaboration_mode,omitempty"`
	MaxReviewRounds   int                 `json:"max_review_rounds,omitempty"`
	AgentScopes       []agentScopePayload `json:"agent_scopes,omitempty"`

	// Rich specification fields.
	AcceptanceCriteria  []acceptanceCriterionPayload `json:"acceptance_criteria"`
	Guidelines          []string                     `json:"guidelines"`
	ImplementationGuide string                       `json:"implementation_guide"`
	Examples            []taskExamplePayload         `json:"examples"`
	AffectedFiles       []taskFileTargetPayload      `json:"affected_files"`
	TestRequirements    []string                     `json:"test_requirements"`
	RiskFactors         []string                     `json:"risk_factors"`
	Workspace           taskWorkspacePayload         `json:"workspace,omitempty"`
	WorkerPackets       []workerPacketPayload        `json:"worker_packets,omitempty"`
	ExecutionContracts  []executionContractPayload   `json:"execution_contracts,omitempty"`
}

type acceptanceCriterionPayload struct {
	Given    string `json:"given"`
	When     string `json:"when"`
	Then     string `json:"then"`
	Priority string `json:"priority"`
}

type taskExamplePayload struct {
	Label       string `json:"label"`
	Code        string `json:"code"`
	Explanation string `json:"explanation"`
}

type taskFileTargetPayload struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

type agentScopePayload struct {
	AgentType           string                       `json:"agent_type"`
	Role                string                       `json:"role"`
	AcceptanceCriteria  []acceptanceCriterionPayload `json:"acceptance_criteria"`
	ImplementationGuide string                       `json:"implementation_guide"`
	AffectedFiles       []taskFileTargetPayload      `json:"affected_files"`
	Guidelines          []string                     `json:"guidelines"`
	TestRequirements    []string                     `json:"test_requirements"`
}

type taskWorkspacePayload struct {
	BaseVersion   string   `json:"base_version,omitempty"`
	ReadSet       []string `json:"read_set,omitempty"`
	WriteSet      []string `json:"write_set,omitempty"`
	TestSurface   []string `json:"test_surface,omitempty"`
	PrefetchPaths []string `json:"prefetch_paths,omitempty"`
}

type workerPacketPayload struct {
	AgentType           string                       `json:"agent_type"`
	Role                string                       `json:"role"`
	Objective           string                       `json:"objective,omitempty"`
	Responsibilities    []string                     `json:"responsibilities,omitempty"`
	AcceptanceCriteria  []acceptanceCriterionPayload `json:"acceptance_criteria,omitempty"`
	ImplementationGuide string                       `json:"implementation_guide,omitempty"`
	AffectedFiles       []taskFileTargetPayload      `json:"affected_files,omitempty"`
	ReadSet             []string                     `json:"read_set,omitempty"`
	WriteSet            []string                     `json:"write_set,omitempty"`
	Guidelines          []string                     `json:"guidelines,omitempty"`
	TestRequirements    []string                     `json:"test_requirements,omitempty"`
}

type executionContractPayload struct {
	AgentType    string   `json:"agent_type"`
	Intents      []string `json:"intents,omitempty"`
	Deliverables []string `json:"deliverables,omitempty"`
}

func (p taskPayload) toTask(index int) *AtomicTask {
	taskID := strings.TrimSpace(p.ID)
	if taskID == "" {
		taskID = fmt.Sprintf("task_%d", index+1)
	}
	task := &AtomicTask{
		ID:                  taskID,
		Slug:                strings.TrimSpace(p.Slug),
		Name:                strings.TrimSpace(p.Name),
		Description:         strings.TrimSpace(p.Description),
		AgentType:           normalizeTaskAgentType(p.AgentType),
		SuccessCriteria:     nonEmptySlice(p.SuccessCriteria),
		Dependencies:        nonEmptySlice(p.Dependencies),
		EstimatedTokens:     nonZeroInt(p.EstimatedTokens, 3000),
		Complexity:          parseComplexity(p.Complexity),
		Status:              TaskStatusPending,
		AcceptanceCriteria:  toAcceptanceCriteria(p.AcceptanceCriteria),
		Guidelines:          nonEmptySlice(p.Guidelines),
		ImplementationGuide: strings.TrimSpace(p.ImplementationGuide),
		Examples:            toTaskExamples(p.Examples),
		AffectedFiles:       toTaskFileTargets(p.AffectedFiles),
		TestRequirements:    nonEmptySlice(p.TestRequirements),
		RiskFactors:         nonEmptySlice(p.RiskFactors),
		Workspace:           toTaskWorkspaceSpec(p.Workspace),
		WorkerPackets:       toWorkerPackets(p.WorkerPackets),
		ExecutionContracts:  toExecutionContracts(p.ExecutionContracts),
	}

	// Populate co-tenancy fields when the LLM specifies them.
	if coAgents := nonEmptySlice(p.CoAgents); len(coAgents) > 0 {
		task.CoAgents = coAgents
		task.CollaborationMode = parseCollaborationMode(p.CollaborationMode)
		task.MaxReviewRounds = p.MaxReviewRounds
		task.AgentScopes = toAgentScopes(p.AgentScopes)
	}

	return task
}

func parseCollaborationMode(raw string) dag.CollaborationMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "adversarial":
		return dag.CollaborationAdversarial
	default:
		return dag.CollaborationSequential
	}
}

func toAgentScopes(payloads []agentScopePayload) []AgentScope {
	if len(payloads) == 0 {
		return nil
	}
	scopes := make([]AgentScope, 0, len(payloads))
	for _, p := range payloads {
		agentType := normalizeTaskAgentType(p.AgentType)
		role := normalizeAgentRole(p.Role)
		scope := AgentScope{
			AgentType:           agentType,
			Role:                role,
			AcceptanceCriteria:  toAcceptanceCriteria(p.AcceptanceCriteria),
			ImplementationGuide: strings.TrimSpace(p.ImplementationGuide),
			AffectedFiles:       toTaskFileTargets(p.AffectedFiles),
			Guidelines:          nonEmptySlice(p.Guidelines),
			TestRequirements:    nonEmptySlice(p.TestRequirements),
		}
		scopes = append(scopes, scope)
	}
	return scopes
}

func toTaskWorkspaceSpec(payload taskWorkspacePayload) TaskWorkspaceSpec {
	return TaskWorkspaceSpec{
		BaseVersion:   strings.TrimSpace(payload.BaseVersion),
		ReadSet:       nonEmptySlice(payload.ReadSet),
		WriteSet:      nonEmptySlice(payload.WriteSet),
		TestSurface:   nonEmptySlice(payload.TestSurface),
		PrefetchPaths: nonEmptySlice(payload.PrefetchPaths),
	}
}

func toWorkerPackets(payloads []workerPacketPayload) []WorkerPacket {
	if len(payloads) == 0 {
		return nil
	}
	packets := make([]WorkerPacket, 0, len(payloads))
	for _, p := range payloads {
		packet := WorkerPacket{
			AgentType:           normalizeTaskAgentType(p.AgentType),
			Role:                normalizeAgentRole(p.Role),
			Objective:           strings.TrimSpace(p.Objective),
			Responsibilities:    nonEmptySlice(p.Responsibilities),
			AcceptanceCriteria:  toAcceptanceCriteria(p.AcceptanceCriteria),
			ImplementationGuide: strings.TrimSpace(p.ImplementationGuide),
			AffectedFiles:       toTaskFileTargets(p.AffectedFiles),
			ReadSet:             nonEmptySlice(p.ReadSet),
			WriteSet:            nonEmptySlice(p.WriteSet),
			Guidelines:          nonEmptySlice(p.Guidelines),
			TestRequirements:    nonEmptySlice(p.TestRequirements),
		}
		packets = append(packets, packet)
	}
	return packets
}

func toExecutionContracts(payloads []executionContractPayload) []AgentExecutionContract {
	if len(payloads) == 0 {
		return nil
	}
	contracts := make([]AgentExecutionContract, 0, len(payloads))
	for _, p := range payloads {
		contract := AgentExecutionContract{
			AgentType:    normalizeExecutionContractAgentType(p.AgentType),
			Intents:      nonEmptySlice(p.Intents),
			Deliverables: nonEmptySlice(p.Deliverables),
		}
		contracts = append(contracts, contract)
	}
	return contracts
}

func normalizeAgentRole(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "primary", "co_agent":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "co_agent"
	}
}

func toAcceptanceCriteria(payloads []acceptanceCriterionPayload) []AcceptanceCriterion {
	if len(payloads) == 0 {
		return nil
	}
	criteria := make([]AcceptanceCriterion, 0, len(payloads))
	for _, p := range payloads {
		ac := AcceptanceCriterion{
			Given:    strings.TrimSpace(p.Given),
			When:     strings.TrimSpace(p.When),
			Then:     strings.TrimSpace(p.Then),
			Priority: normalizeACPriority(p.Priority),
		}
		if ac.Then == "" {
			continue
		}
		criteria = append(criteria, ac)
	}
	return criteria
}

func normalizeACPriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "must", "should", "could":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "must"
	}
}

func toTaskExamples(payloads []taskExamplePayload) []TaskExample {
	if len(payloads) == 0 {
		return nil
	}
	examples := make([]TaskExample, 0, len(payloads))
	for _, p := range payloads {
		ex := TaskExample{
			Label:       strings.TrimSpace(p.Label),
			Code:        strings.TrimSpace(p.Code),
			Explanation: strings.TrimSpace(p.Explanation),
		}
		if ex.Code == "" && ex.Label == "" {
			continue
		}
		examples = append(examples, ex)
	}
	return examples
}

func toTaskFileTargets(payloads []taskFileTargetPayload) []TaskFileTarget {
	if len(payloads) == 0 {
		return nil
	}
	targets := make([]TaskFileTarget, 0, len(payloads))
	for _, p := range payloads {
		ft := TaskFileTarget{
			Path:      strings.TrimSpace(p.Path),
			Operation: normalizeFileOp(p.Operation),
			Reason:    strings.TrimSpace(p.Reason),
		}
		if ft.Path == "" {
			continue
		}
		targets = append(targets, ft)
	}
	return targets
}

func normalizeFileOp(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "create", "modify", "delete":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "modify"
	}
}

func parseTaskPayload(text string) ([]*AtomicTask, error) {
	entries, err := decodeTaskEntries(text)
	if err != nil {
		return nil, err
	}
	tasks := make([]*AtomicTask, 0, len(entries))
	for i := range entries {
		tasks = append(tasks, entries[i].toTask(i))
	}
	return tasks, nil
}

func decodeTaskEntries(text string) ([]taskPayload, error) {
	var payload taskListPayload
	if err := decodeJSONPayload(text, &payload); err == nil && len(payload.Tasks) > 0 {
		return payload.Tasks, nil
	}
	var list []taskPayload
	if err := decodeJSONPayload(text, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func decodeJSONPayload(text string, out any) error {
	for _, candidate := range jsonCandidates(text) {
		if json.Unmarshal([]byte(candidate), out) == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to decode planner json")
}

func jsonCandidates(text string) []string {
	candidates := []string{strings.TrimSpace(text)}
	fenced := extractFencedJSON(text)
	if fenced != "" {
		candidates = append(candidates, fenced)
	}
	object := extractJSONObject(text)
	if object != "" {
		candidates = append(candidates, object)
	}
	return uniqueNonEmptyStrings(candidates)
}

func extractFencedJSON(text string) string {
	start := strings.Index(text, "```")
	if start == -1 {
		return ""
	}
	rest := text[start+3:]
	if strings.HasPrefix(rest, "json") {
		rest = rest[4:]
	}
	end := strings.Index(rest, "```")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func extractJSONObject(text string) string {
	start := strings.IndexAny(text, "{[")
	if start == -1 {
		return ""
	}
	snippet := strings.TrimSpace(text[start:])
	for i := len(snippet); i > 0; i-- {
		candidate := strings.TrimSpace(snippet[:i])
		var raw json.RawMessage
		if json.Unmarshal([]byte(candidate), &raw) == nil {
			return candidate
		}
	}
	return ""
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func buildRequirementsPrompt(query string, params map[string]any) string {
	base := fmt.Sprintf(RequirementsAnalysisPrompt, query, mustJSON(params))
	return base + `

Return JSON only, exactly:
{
  "goals": ["..."],
  "constraints": ["..."],
  "dependencies": ["..."],
  "provisional_recommendations": ["..."],
  "tradeoffs": ["..."],
  "recommendation_narrative": "...",
  "clarification_questions": ["..."],
  "unknowns": ["..."],
  "scope": "project|module|file",
  "priority": "low|medium|high|critical"
}

Hard limits:
- At most 6 goals, 6 constraints, 6 dependencies
- At most 5 clarification_questions and 5 unknowns
- Each string must be <= 20 words
- recommendation_narrative must be <= 90 words
- Only include clarification_questions when the request has GENUINE ambiguity that would lead to fundamentally different implementations. For straightforward, well-understood, or single-scope requests, set clarification_questions to an empty array
- If the user asks for recommendations or preferences, include opinionated provisional_recommendations plus concise tradeoffs
- For recommendation questions, include at least 2 provisional_recommendations and 2 tradeoffs
`
}

func buildDesignPrompt(requirements *Requirements, patterns *CodebasePatterns) string {
	base := fmt.Sprintf(ArchitectureDesignPrompt, mustJSON(requirements), mustJSON(patterns))
	return base + `

Return JSON only, exactly:
{
  "name": "...",
  "description": "...",
  "components": [
    {
      "name": "...",
      "type": "backend|frontend|data|integration|test",
      "description": "...",
      "dependencies": ["component_name"],
      "interfaces": ["interface_name"],
      "file_path": ""
    }
  ],
  "interfaces": [
    {
      "name": "...",
      "from": "...",
      "to": "...",
      "type": "api|event|internal",
      "description": "...",
      "methods": [{"name":"...","parameters":["..."],"returns":"..."}]
    }
  ],
  "patterns": ["..."],
  "layers": [{"name":"...","components":["..."],"order":1}]
}

Hard limits:
- At most 6 components, 6 interfaces, 6 patterns, 4 layers
- Keep all descriptions <= 24 words
- Use Go-style file paths when file_path is set (e.g. core/providers/token_rotation.go)
`
}

func buildTaskPrompt(architecture *SolutionArchitecture, constraints *PlanConstraints) string {
	base := fmt.Sprintf(TaskDecompositionPrompt, mustJSON(architecture))
	return base + "\n\nConstraints:\n" + mustJSON(constraints) + `

Return JSON only, exactly:
{
  "tasks": [
    {
      "id": "task_1",
      "slug": "short-kebab-case-label",
      "name": "Short imperative name",
      "description": "Detailed implementation description. Include what to build, how it fits the architecture, and key design decisions. Be specific enough that an agent can implement without follow-up questions.",
      "agent_type": "engineer|designer",
      "co_agents": ["designer"],
      "collaboration_mode": "sequential|adversarial",
      "max_review_rounds": 0,
      "agent_scopes": [
        {
          "agent_type": "designer",
          "role": "primary",
          "acceptance_criteria": [{"given": "...", "when": "...", "then": "...", "priority": "must"}],
          "implementation_guide": "Step-by-step for THIS agent only",
          "affected_files": [{"path": "...", "operation": "create", "reason": "..."}],
          "guidelines": ["Constraint specific to this agent"],
          "test_requirements": ["Test specific to this agent's output"]
        }
      ],
      "success_criteria": ["Criterion 1", "Criterion 2"],
      "dependencies": ["task_id"],
      "estimated_tokens": 3000,
      "complexity": "low|medium|high|critical",
      "acceptance_criteria": [
        {
          "given": "Precondition or initial state",
          "when": "Action or trigger",
          "then": "Expected observable outcome",
          "priority": "must|should|could"
        }
      ],
      "guidelines": [
        "Implementation constraint or convention to follow"
      ],
      "implementation_guide": "Step-by-step implementation instructions. Reference specific functions, types, and patterns from the codebase. Include the sequence of operations, error handling strategy, and integration points.",
      "examples": [
        {
          "label": "What the example demonstrates",
          "code": "func Example() { ... }",
          "explanation": "Why this pattern applies"
        }
      ],
      "affected_files": [
        {
          "path": "core/module/file.go",
          "operation": "create|modify|delete",
          "reason": "Why this file is affected"
        }
      ],
      "workspace": {
        "base_version": "session_head",
        "read_set": ["path/or/glob/needed/for/context"],
        "write_set": ["path/or/glob/the-task-may-mutate"],
        "test_surface": ["tests/or/packages/to-run"],
        "prefetch_paths": ["high-value context paths to preload"]
      },
      "worker_packets": [
        {
          "agent_type": "engineer",
          "role": "primary|co_agent",
          "objective": "Short execution objective for this worker",
          "responsibilities": ["Concrete responsibility 1", "Concrete responsibility 2"],
          "acceptance_criteria": [{"given": "...", "when": "...", "then": "...", "priority": "must"}],
          "implementation_guide": "Step-by-step for THIS worker only",
          "affected_files": [{"path": "...", "operation": "create", "reason": "..."}],
          "read_set": ["paths this worker must read"],
          "write_set": ["paths this worker may mutate"],
          "guidelines": ["Constraint specific to this worker"],
          "test_requirements": ["Tests this worker must satisfy"]
        }
      ],
      "execution_contracts": [
        {
          "agent_type": "inspector-pipeline",
          "intents": ["synthesize_contract", "inspect_scope", "record_pending_validation", "publish_handoff_contract"],
          "deliverables": ["criteria_contract", "scope_inspection", "pending_validation_state", "handoff_contract"]
        },
        {
          "agent_type": "tester-pipeline",
          "intents": ["plan_tests", "author_tests"],
          "deliverables": ["test_plan", "test_artifact"]
        },
        {
          "agent_type": "engineer",
          "intents": ["produce_requested_change"],
          "deliverables": ["requested_change"]
        }
      ],
      "test_requirements": [
        "Specific test case that must pass"
      ],
      "risk_factors": [
        "Potential blocker or failure mode"
      ]
    }
  ]
}

Pipeline model:
- Each engineer/designer task is automatically expanded into a 3-stage pipeline: inspector → tester → engineer/designer.
- Do NOT create standalone tester, inspector, or architect tasks. Testing and inspection are pipeline stages, not task types.
- agent_type MUST be "engineer" or "designer" only. Any other value is rejected.
- Include test_requirements on each task — the pipeline tester uses them to write tests BEFORE the engineer/designer executes.

Hard limits:
- At most 10 tasks
- Each task MUST have at least 2 acceptance_criteria with priority "must"
- Each task MUST have at least 1 affected_files entry
- Each task MUST have implementation_guide (minimum 2 sentences)
- Each task MUST have a unique slug in lowercase kebab-case suitable for the pipeline panel (examples: "auth-checkout", "payment-retry")
- Each task MUST have workspace with at least 1 write_set entry and enough read_set coverage for the assigned work.
- worker_packets are REQUIRED for every task. Include 1 packet for single-agent tasks, or 1 packet per participating worker for compound tasks.
- execution_contracts are REQUIRED for every task. Include explicit contracts for inspector-pipeline, tester-pipeline, the primary implementation agent, and every co-agent.
- guidelines: 1-4 items per task
- test_requirements: 1-4 items per task
- examples: 0-2 per task (include for non-trivial tasks)
- risk_factors: 0-3 per task
- success_criteria: 2-4 items per task
- co_agents: omit for single-agent tasks. When a task involves BOTH visual/UX concerns AND implementation logic, split responsibilities:
  1. Identify the primary agent (who acts first): designer for UI-first tasks, engineer for logic-first tasks
  2. Identify co-agents (who act after the primary): the complementary agent type
  3. Set collaboration_mode: "sequential" (primary acts, co-agent follows) or "adversarial" (co-agent can push back, bounded by max_review_rounds)
  4. Provide agent_scopes: REQUIRED when co_agents is non-empty. Each agent MUST have its own scoped acceptance_criteria, implementation_guide, affected_files, and guidelines.
  5. Provide worker_packets: REQUIRED for the primary agent and every co-agent. These are the execution packets consumed by inspector/tester/engineer/designer.
  Classification: tasks with styled components/layouts/theming → primary: designer, co_agents: ["engineer"]. Tasks with UI + state/API/logic → primary: engineer, co_agents: ["designer"]. Pure backend or pure design → no co_agents.
  Execution model: primary agent executes first, producing files. Co-agents execute sequentially after, receiving the primary's changed files as context.
- max_review_rounds: omit or 0 for sequential mode. Set 1-3 for adversarial mode (bounds review iterations).
- agent_scopes: REQUIRED when co_agents is non-empty. 1 scope per agent (primary + each co-agent). Each scope must have at least 1 acceptance_criteria with priority "must". Omit entirely for single-agent tasks.
- workspace: REQUIRED. read_set should cover the code and docs needed to reason locally; write_set should be the narrowest safe mutation surface; test_surface should name the packages/files/commands the tester must exercise; prefetch_paths should preload especially relevant context in large repos.
- worker_packets: REQUIRED. read_set/write_set inside each worker packet must stay within the task workspace. Use them to divide engineer/designer work cleanly and keep the task collision-free.
- execution_contracts: REQUIRED. Use only these intent values: plan_tests, author_tests, run_tests, diagnose_failures, verify_spec, prepare_harness, report_findings, synthesize_contract, inspect_scope, record_pending_validation, publish_handoff_contract, validate_implementation, run_quality_checks, publish_validation_report, grade_quality, consume_reviews, inspect_review_context, address_review, resolve_reviews, produce_requested_change. Use only these deliverable values: test_plan, test_artifact, suite_execution, failure_diagnosis, failure_report, harness_prepared, criteria_contract, scope_inspection, pending_validation_state, handoff_contract, criteria_evaluation, quality_checks, validation_report, quality_grade, review_intake, review_context, review_addressed, review_resolution, requested_change.
`
}

func mustJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func nonEmptySlice(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func nonEmptyString(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func nonZeroInt(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func parseComplexity(raw string) TaskComplexity {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return ComplexityLow
	case "high":
		return ComplexityHigh
	case "critical":
		return ComplexityCritical
	default:
		return ComplexityMedium
	}
}

func parseStringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return nonEmptySlice(list)
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return nonEmptySlice([]string{single})
	}
	var maps []map[string]any
	if json.Unmarshal(raw, &maps) == nil {
		return extractStringValues(maps)
	}
	return nil
}

func extractStringValues(items []map[string]any) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, firstNonEmptyMapString(item)...)
	}
	return nonEmptySlice(values)
}

func firstNonEmptyMapString(item map[string]any) []string {
	keys := []string{"description", "name", "id", "value", "text"}
	for _, key := range keys {
		value, ok := item[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			return []string{text}
		}
	}
	return nil
}

func normalizeTaskGraph(tasks []*AtomicTask) []*AtomicTask {
	aliases := ensureTaskIdentity(tasks)
	idSet := buildTaskIDSet(tasks)
	nameIndex := buildTaskNameIndex(tasks)
	for _, task := range tasks {
		normalizeTask(task, idSet, nameIndex, aliases)
	}
	return tasks
}

func ensureTaskIdentity(tasks []*AtomicTask) map[string]string {
	aliases := make(map[string]string, len(tasks)*2)
	idSeen := make(map[string]int, len(tasks))
	slugSeen := make(map[string]int, len(tasks))

	for i, task := range tasks {
		if task == nil {
			continue
		}

		baseSlug := taskSlugCandidate(task, i)
		task.Slug = uniqueTaskSlug(baseSlug, slugSeen)

		rawID := strings.TrimSpace(task.ID)
		baseID := canonicalTaskID(rawID)
		if baseID == "" {
			baseID = canonicalTaskID("task_" + strings.ReplaceAll(task.Slug, "-", "_"))
		}
		task.ID = uniqueTaskID(baseID, idSeen)

		if rawID != "" {
			if _, exists := aliases[rawID]; !exists {
				aliases[rawID] = task.ID
			}
		}
		if baseID != "" {
			if _, exists := aliases[baseID]; !exists {
				aliases[baseID] = task.ID
			}
		}
	}
	return aliases
}

func buildTaskIDSet(tasks []*AtomicTask) map[string]struct{} {
	idSet := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		idSet[strings.TrimSpace(task.ID)] = struct{}{}
	}
	return idSet
}

func buildTaskNameIndex(tasks []*AtomicTask) map[string]string {
	index := make(map[string]string, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		key := canonicalTaskKey(task.Name)
		if key != "" {
			if _, exists := index[key]; exists {
				continue
			}
			index[key] = task.ID
		}
	}
	return index
}

func normalizeTask(task *AtomicTask, idSet map[string]struct{}, nameIndex map[string]string, aliases map[string]string) {
	if task == nil {
		return
	}
	task.AgentType = normalizeTaskAgentType(task.AgentType)
	task.Dependencies = normalizeDependencies(task.Dependencies, idSet, nameIndex, aliases)
	if len(task.SuccessCriteria) == 0 {
		task.SuccessCriteria = []string{"Task completed"}
	}
	task.WorkerPackets = normalizeWorkerPackets(task)
	task.ExecutionContracts = normalizeExecutionContracts(task)
	task.Workspace = normalizeTaskWorkspace(task)
}

func normalizeWorkerPackets(task *AtomicTask) []WorkerPacket {
	if task == nil {
		return nil
	}

	packets := make([]WorkerPacket, 0, max(len(task.WorkerPackets), len(task.AgentScopes)+1))
	seen := make(map[string]struct{})

	appendPacket := func(packet WorkerPacket) {
		packet.AgentType = normalizeTaskAgentType(packet.AgentType)
		if packet.AgentType == "" {
			return
		}
		if _, ok := seen[packet.AgentType]; ok {
			return
		}
		seen[packet.AgentType] = struct{}{}
		packet.Role = normalizeAgentRole(packet.Role)
		packet.Responsibilities = nonEmptySlice(packet.Responsibilities)
		packet.Guidelines = nonEmptySlice(packet.Guidelines)
		packet.TestRequirements = nonEmptySlice(packet.TestRequirements)
		packet.ReadSet = nonEmptySlice(packet.ReadSet)
		packet.WriteSet = nonEmptySlice(packet.WriteSet)
		packets = append(packets, packet)
	}

	for _, packet := range task.WorkerPackets {
		appendPacket(packet)
	}
	for _, scope := range task.AgentScopes {
		appendPacket(WorkerPacket{
			AgentType:           scope.AgentType,
			Role:                scope.Role,
			AcceptanceCriteria:  scope.AcceptanceCriteria,
			ImplementationGuide: scope.ImplementationGuide,
			AffectedFiles:       scope.AffectedFiles,
			Guidelines:          scope.Guidelines,
			TestRequirements:    scope.TestRequirements,
			Objective:           task.Name,
		})
	}

	if len(packets) == 0 {
		appendPacket(WorkerPacket{
			AgentType:           task.AgentType,
			Role:                "primary",
			Objective:           task.Name,
			AcceptanceCriteria:  task.AcceptanceCriteria,
			ImplementationGuide: task.ImplementationGuide,
			AffectedFiles:       task.AffectedFiles,
			Guidelines:          task.Guidelines,
			TestRequirements:    task.TestRequirements,
			WriteSet:            taskFilePaths(task.AffectedFiles),
		})
	}

	return packets
}

func normalizeExecutionContracts(task *AtomicTask) []AgentExecutionContract {
	if task == nil {
		return nil
	}

	contracts := make([]AgentExecutionContract, 0, max(len(task.ExecutionContracts), len(requiredExecutionContractAgents(task))))
	seen := make(map[string]struct{})

	appendContract := func(contract AgentExecutionContract) {
		agentType := normalizeExecutionContractAgentType(contract.AgentType)
		if agentType == "" {
			return
		}
		if _, ok := seen[agentType]; ok {
			return
		}
		seen[agentType] = struct{}{}
		contracts = append(contracts, AgentExecutionContract{
			AgentType:    agentType,
			Intents:      nonEmptySlice(contract.Intents),
			Deliverables: nonEmptySlice(contract.Deliverables),
		})
	}

	for _, contract := range task.ExecutionContracts {
		appendContract(contract)
	}
	for _, contract := range defaultExecutionContracts(task) {
		appendContract(contract)
	}
	return contracts
}

func defaultExecutionContracts(task *AtomicTask) []AgentExecutionContract {
	if task == nil {
		return nil
	}
	contracts := []AgentExecutionContract{
		{
			AgentType:    "inspector-pipeline",
			Intents:      []string{"synthesize_contract", "inspect_scope", "record_pending_validation", "publish_handoff_contract"},
			Deliverables: []string{"criteria_contract", "scope_inspection", "pending_validation_state", "handoff_contract"},
		},
		{
			AgentType:    "tester-pipeline",
			Intents:      []string{"plan_tests", "author_tests"},
			Deliverables: []string{"test_plan", "test_artifact"},
		},
	}
	for _, agentType := range requiredExecutionContractAgents(task) {
		if agentType == "inspector-pipeline" || agentType == "tester-pipeline" {
			continue
		}
		contracts = append(contracts, AgentExecutionContract{
			AgentType:    agentType,
			Intents:      []string{"produce_requested_change"},
			Deliverables: []string{"requested_change"},
		})
	}
	return contracts
}

func requiredExecutionContractAgents(task *AtomicTask) []string {
	if task == nil {
		return nil
	}
	agents := []string{"inspector-pipeline", "tester-pipeline", normalizeTaskAgentType(task.AgentType)}
	for _, coAgent := range task.CoAgents {
		agents = append(agents, normalizeTaskAgentType(coAgent))
	}
	for _, scope := range task.AgentScopes {
		agents = append(agents, normalizeTaskAgentType(scope.AgentType))
	}
	return nonEmptySlice(appendUniqueStrings(nil, agents...))
}

func normalizeExecutionContractAgentType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "inspector-pipeline", "tester-pipeline":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return normalizeTaskAgentType(raw)
	}
}

func normalizeTaskWorkspace(task *AtomicTask) TaskWorkspaceSpec {
	if task == nil {
		return TaskWorkspaceSpec{}
	}
	readSet := append([]string(nil), task.Workspace.ReadSet...)
	writeSet := append([]string(nil), task.Workspace.WriteSet...)
	testSurface := append([]string(nil), task.Workspace.TestSurface...)
	prefetch := append([]string(nil), task.Workspace.PrefetchPaths...)

	affected := taskFilePaths(task.AffectedFiles)
	readSet = appendUniqueStrings(readSet, affected...)
	writeSet = appendUniqueStrings(writeSet, affected...)
	for _, packet := range task.WorkerPackets {
		readSet = appendUniqueStrings(readSet, packet.ReadSet...)
		writeSet = appendUniqueStrings(writeSet, packet.WriteSet...)
		readSet = appendUniqueStrings(readSet, taskFilePaths(packet.AffectedFiles)...)
		writeSet = appendUniqueStrings(writeSet, taskFilePaths(packet.AffectedFiles)...)
	}

	if len(testSurface) == 0 {
		testSurface = appendUniqueStrings(testSurface, writeSet...)
	}
	prefetch = appendUniqueStrings(prefetch, readSet...)

	return TaskWorkspaceSpec{
		BaseVersion:   firstNonEmpty(strings.TrimSpace(task.Workspace.BaseVersion), "session_head"),
		ReadSet:       nonEmptySlice(readSet),
		WriteSet:      nonEmptySlice(writeSet),
		TestSurface:   nonEmptySlice(testSurface),
		PrefetchPaths: nonEmptySlice(prefetch),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func taskFilePaths(files []TaskFileTarget) []string {
	result := make([]string, 0, len(files))
	for _, file := range files {
		if path := strings.TrimSpace(file.Path); path != "" {
			result = append(result, path)
		}
	}
	return result
}

func appendUniqueStrings(dst []string, values ...string) []string {
	if len(values) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, existing := range dst {
		if trimmed := strings.TrimSpace(existing); trimmed != "" {
			seen[trimmed] = struct{}{}
		}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func normalizeDependencies(
	dependencies []string,
	idSet map[string]struct{},
	nameIndex map[string]string,
	aliases map[string]string,
) []string {
	result := make([]string, 0, len(dependencies))
	seen := map[string]struct{}{}
	for _, dependency := range dependencies {
		mapped := mapDependency(dependency, idSet, nameIndex, aliases)
		if mapped == "" {
			continue
		}
		if _, ok := seen[mapped]; ok {
			continue
		}
		seen[mapped] = struct{}{}
		result = append(result, mapped)
	}
	return result
}

func mapDependency(
	dependency string,
	idSet map[string]struct{},
	nameIndex map[string]string,
	aliases map[string]string,
) string {
	trimmed := strings.TrimSpace(dependency)
	if trimmed == "" {
		return ""
	}
	if _, ok := idSet[trimmed]; ok {
		return trimmed
	}
	if value, ok := aliases[trimmed]; ok {
		return value
	}
	if value, ok := aliases[canonicalTaskID(trimmed)]; ok {
		return value
	}
	key := canonicalTaskKey(trimmed)
	if value, ok := nameIndex[key]; ok {
		return value
	}
	return ""
}

func taskSlugForTask(task *AtomicTask, fallbackIndex int) string {
	if task == nil {
		return uniqueTaskSlug(fmt.Sprintf("task-%d", fallbackIndex+1), map[string]int{})
	}
	if slug := slugifyTaskValue(task.Slug); slug != "" {
		return slug
	}
	return taskSlugCandidate(task, fallbackIndex)
}

func taskSlugCandidate(task *AtomicTask, index int) string {
	if task == nil {
		return fmt.Sprintf("task-%d", index+1)
	}
	candidates := []string{
		task.Slug,
		task.Name,
		task.Description,
	}
	for _, candidate := range candidates {
		if slug := slugifyTaskValue(candidate); slug != "" {
			return slug
		}
	}
	for _, file := range task.AffectedFiles {
		if slug := slugifyTaskValue(file.Path); slug != "" {
			return slug
		}
	}
	return fmt.Sprintf("task-%d", index+1)
}

func uniqueTaskSlug(base string, seen map[string]int) string {
	base = slugifyTaskValue(base)
	if base == "" {
		base = "task"
	}
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, seen[base])
}

func uniqueTaskID(base string, seen map[string]int) string {
	base = canonicalTaskID(base)
	if base == "" {
		base = "task"
	}
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, seen[base])
}

func canonicalTaskID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	lastUnderscore := false
	for _, r := range strings.ToLower(raw) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_' || r == '-' || unicode.IsSpace(r) || r == '/' || r == ':' || r == '.':
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	id := strings.Trim(b.String(), "_")
	return id
}

func slugifyTaskValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	lastDash := false
	for _, r := range strings.ToLower(raw) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r) || r == '/' || r == ':' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func canonicalTaskKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "implement ")
	return strings.TrimSpace(value)
}

func normalizeTaskAgentType(agentType string) string {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "engineer", "designer", "tester", "inspector", "architect":
		return strings.ToLower(strings.TrimSpace(agentType))
	default:
		return "engineer"
	}
}

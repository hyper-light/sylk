package shared

import (
	"context"
	"errors"
	"strings"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/tools"
)

// ErrContextBudgetExhausted is returned when the context window utilization
// exceeds the critical watermark and no further compaction or eviction can
// bring it below threshold.
var ErrContextBudgetExhausted = errors.New("context budget exhausted")

type contextGovernorKey struct{}

// ContextGovernor prevents context window overflow in agent tool loops.
//
// It uses ground-truth anchoring: after each LLM call the provider reports
// exact input tokens, which become the anchor. Between calls, only the
// delta (newly appended tool results) is estimated, bounding error to one
// turn. Three corrective layers: per-result output limiting, per-turn
// compaction/eviction, and tool stripping at Red/Critical zones.
type ContextGovernor struct {
	budget      *ContextBudget
	initialized bool

	// Turn state — set by BeginTurn, read by LimitToolOutput.
	currentTurn int
	maxTurns    int
	messages    *[]providers.Message // live pointer into req.Messages
}

const (
	preserveRecentGroups = 3
	importanceMaxLines   = 50
)

// NewContextGovernor creates a governor for the given model parameters.
func NewContextGovernor(model string, maxOutputTokens, thinkingBudget int) *ContextGovernor {
	return &ContextGovernor{
		budget: NewContextBudget(model, maxOutputTokens, thinkingBudget),
	}
}

// WithContextGovernor stores the governor in the context.
func WithContextGovernor(ctx context.Context, gov *ContextGovernor) context.Context {
	return context.WithValue(ctx, contextGovernorKey{}, gov)
}

// ContextGovernorFromContext retrieves the governor from context, or nil.
func ContextGovernorFromContext(ctx context.Context) *ContextGovernor {
	if ctx == nil {
		return nil
	}
	gov, _ := ctx.Value(contextGovernorKey{}).(*ContextGovernor)
	return gov
}

// ApplyContextBudget checks context governor zones and the safety ceiling,
// stripping tools when context pressure reaches Red or turn reaches maxRuns.
// Returns ErrContextBudgetExhausted if the context is critically full.
// Must be called before each LLM call.
func ApplyContextBudget(ctx context.Context, turn, maxRuns int, req *providers.Request) error {
	stampRequestIdentity(ctx, req)

	if gov := ContextGovernorFromContext(ctx); gov != nil {
		zone := gov.BeginTurn(ctx, turn, maxRuns, req)
		switch {
		case zone == ZoneCritical:
			return ErrContextBudgetExhausted
		case zone >= ZoneRed:
			req.Tools = nil // context pressure — force synthesis
		}
	}
	if turn == maxRuns {
		req.Tools = nil // safety ceiling — force synthesis
	}
	return nil
}

// stampRequestIdentity sets agent_id and session_id in the request Metadata
// from the LogMeta carried in ctx. Idempotent — skips if already set.
func stampRequestIdentity(ctx context.Context, req *providers.Request) {
	m := LogMetaFromContext(ctx)
	if m.AgentID == "" && m.SessionID == "" {
		return
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any, 2)
	}
	if _, ok := req.Metadata["agent_id"]; !ok {
		req.Metadata["agent_id"] = m.AgentID
	}
	if _, ok := req.Metadata["session_id"]; !ok {
		req.Metadata["session_id"] = m.SessionID
	}
}

// Initialize computes the fixed overhead (system prompt + tool definitions)
// and marks the governor as ready. Called once per request.
func (g *ContextGovernor) Initialize(req *providers.Request) {
	g.budget.ComputeFixedOverhead(req.SystemPrompt, req.Tools)
	g.initialized = true
}

// BeginTurn checks context utilization and takes corrective action if needed.
// It must be called after the steering checkpoint and before each LLM call.
// Returns the resulting budget zone. The caller should abort if Critical.
// Auto-initializes on first call using the request's system prompt and tools.
func (g *ContextGovernor) BeginTurn(ctx context.Context, turn, maxRuns int, req *providers.Request) BudgetZone {
	if !g.initialized {
		g.Initialize(req)
	}

	g.currentTurn = turn
	g.maxTurns = maxRuns
	g.messages = &req.Messages

	zone := g.budget.Zone(req.Messages)
	g.logBudgetCheck(ctx, zone, req.Messages)

	switch zone {
	case ZoneYellow:
		g.compact(ctx, req, preserveRecentGroups)
	case ZoneRed:
		g.compact(ctx, req, preserveRecentGroups)
		g.evict(ctx, req)
	case ZoneCritical:
		g.compact(ctx, req, preserveRecentGroups)
		g.evict(ctx, req)
		if g.budget.Zone(req.Messages) >= ZoneCritical {
			g.compact(ctx, req, 0)
		}
		zone = g.budget.Zone(req.Messages)
		if zone >= ZoneCritical {
			return ZoneCritical
		}
	}

	return g.budget.Zone(req.Messages)
}

// Calibrate anchors the budget to the provider's ground-truth input token
// count. This replaces all character-based estimation for the base context;
// only the delta (messages appended after this call) is estimated.
func (g *ContextGovernor) Calibrate(ctx context.Context, resp *providers.Response, messages []providers.Message) {
	if !g.initialized || resp == nil || resp.Usage.InputTokens <= 0 {
		return
	}

	projected := g.budget.ProjectedInput(messages)
	g.budget.Anchor(resp.Usage.InputTokens, len(messages))

	g.logAnchor(ctx, projected, resp.Usage.InputTokens)
}

// LimitToolOutput truncates a tool result if it exceeds the per-result
// budget. Error results (isError=true) should NOT be passed through this —
// they must be preserved verbatim.
func (g *ContextGovernor) LimitToolOutput(ctx context.Context, result, toolName string) string {
	if !g.initialized {
		return result
	}

	resultTokens, _ := g.budget.counter.CountText(result)
	projected := g.budget.ProjectedInput(*g.messages)
	remaining := g.maxTurns - g.currentTurn
	perResult := g.budget.PerResultBudget(projected, remaining)

	if perResult <= 0 || resultTokens <= perResult {
		return result
	}

	limited := truncateWithImportance(result, toolName, perResult)
	g.logOutputLimited(ctx, toolName, len(result), len(limited), perResult)
	return limited
}

func (g *ContextGovernor) compact(ctx context.Context, req *providers.Request, preserve int) {
	counter := g.budget.counter
	groups := IdentifyTurnGroups(req.Messages, counter)
	freed := CompactTurnGroups(req.Messages, groups, preserve, counter)
	if freed > 0 {
		g.budget.InvalidateAnchor()
		g.logCompaction(ctx, len(groups), freed)
	}
}

func (g *ContextGovernor) evict(ctx context.Context, req *providers.Request) {
	counter := g.budget.counter
	groups := IdentifyTurnGroups(req.Messages, counter)

	evictable := len(groups) - preserveRecentGroups
	if evictable <= 0 {
		return
	}

	evicted := 0
	for evicted < evictable {
		freshGroups := IdentifyTurnGroups(req.Messages, counter)
		if len(freshGroups) <= preserveRecentGroups {
			break
		}
		req.Messages = EvictTurnGroup(req.Messages, freshGroups[0])
		evicted++
		if g.budget.Zone(req.Messages) < ZoneRed {
			break
		}
	}
	if evicted > 0 {
		g.budget.InvalidateAnchor()
		g.logEviction(ctx, evicted)
	}
}

// truncateWithImportance performs tiered truncation: first keeps important
// lines (errors, file:line references), then hard-truncates if still over.
func truncateWithImportance(content, toolName string, budgetTokens int) string {
	detector := tools.NewImportanceDetector(tools.DefaultImportancePatterns())
	lines := strings.Split(content, "\n")

	var important []string
	for _, line := range lines {
		if detector.IsImportant(line) {
			important = append(important, line)
		}
	}
	if len(important) > importanceMaxLines {
		important = important[:importanceMaxLines]
	}

	charBudget := budgetTokens * 4
	summary := SummarizeToolResult(toolName, content)

	if len(summary) <= charBudget {
		return summary
	}

	truncLen := charBudget - 20
	if truncLen > len(summary) {
		truncLen = len(summary)
	}
	if truncLen > 0 {
		return summary[:truncLen] + "\n[...truncated]"
	}
	return "[truncated]"
}

// Telemetry helpers — emit via LogContextEvent using ctx for session logger.

func (g *ContextGovernor) logBudgetCheck(ctx context.Context, zone BudgetZone, messages []providers.Message) {
	LogContextEvent(ctx, agentlog.EventContextBudgetCheck, agentlog.ContextBudgetPayload{
		EstimatedTokens: g.budget.ProjectedInput(messages),
		AvailableBudget: g.budget.Available(messages),
		Utilization:     g.budget.Utilization(messages),
		Zone:            zone.String(),
	})
}

func (g *ContextGovernor) logCompaction(ctx context.Context, groups, freed int) {
	LogContextEvent(ctx, agentlog.EventContextCompaction, agentlog.CompactionPayload{
		GroupsCompacted: groups,
		TokensFreed:     freed,
	})
}

func (g *ContextGovernor) logEviction(ctx context.Context, groups int) {
	LogContextEvent(ctx, agentlog.EventContextEviction, agentlog.CompactionPayload{
		GroupsCompacted: groups,
	})
}

func (g *ContextGovernor) logAnchor(ctx context.Context, projected, actual int) {
	LogContextEvent(ctx, agentlog.EventContextCalibration, agentlog.CalibrationPayload{
		EstimatedInputTokens: projected,
		ActualInputTokens:    actual,
	})
}

func (g *ContextGovernor) logOutputLimited(ctx context.Context, toolName string, origLen, limitedLen, budget int) {
	LogContextEvent(ctx, agentlog.EventContextOutputLimited, agentlog.OutputLimitedPayload{
		ToolName:     toolName,
		OriginalLen:  origLen,
		LimitedLen:   limitedLen,
		BudgetTokens: budget,
	})
}

// LogContextEvent emits a context budget telemetry event to the JSONL session
// logger. Uses the LogMeta stored in ctx to find the session logger.
func LogContextEvent(ctx context.Context, eventType agentlog.EventType, data any) {
	m := LogMetaFromContext(ctx)
	if m.EventLogger == nil {
		return
	}
	LogAgentEvent(m.EventLogger, eventType, m.AgentID, m.SessionID, m.CorrID, "info", data)
}

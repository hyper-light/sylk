package handoff

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

// ContextBrief is an LLM-generated summary of an agent's current context,
// designed for seamless handoff to a fresh instance.
type ContextBrief struct {
	TaskSummary  string    `json:"task_summary"`
	KeyDecisions string    `json:"key_decisions"`
	ActiveState  string    `json:"active_state"`
	NextSteps    string    `json:"next_steps"`
	Blockers     string    `json:"blockers"`
	GeneratedAt  time.Time `json:"generated_at"`
	ContextSize  int       `json:"context_size"`
	TurnNumber   int       `json:"turn_number"`
}

// BriefSource produces a ContextBrief. Implementations may use direct LLM
// calls or route through the Archivalist.
type BriefSource interface {
	RequestBrief(ctx context.Context, agentType string, contextSize, turnNumber int) (*ContextBrief, error)
}

// BriefGenerator produces handoff summaries asynchronously.
// Generation is guarded by an atomic flag to prevent concurrent requests.
type BriefGenerator struct {
	source     BriefSource
	mu         sync.Mutex
	latest     atomic.Pointer[ContextBrief]
	generating atomic.Bool
}

// NewBriefGenerator creates a BriefGenerator using the given source.
func NewBriefGenerator(source BriefSource) *BriefGenerator {
	return &BriefGenerator{source: source}
}

// briefPrompt builds the summarization prompt for the given agent type.
func briefPrompt(agentType string) string {
	return fmt.Sprintf(
		`You are a %s agent summarizing your current context for a handoff to a fresh instance of yourself. The recipient has no prior context. Provide a JSON object with these fields:
- task_summary: What you're working on and current progress
- key_decisions: Important decisions made and their rationale
- active_state: Active files, variables, in-flight operations
- next_steps: What needs to happen next
- blockers: Known issues or open questions
Be specific and concise. Focus on what the recipient needs for seamless continuity.`,
		agentType,
	)
}

// briefMaxRecentMessages is the maximum number of recent messages to include
// in the summarization request.
const briefMaxRecentMessages = 10

// Generate fires an async goroutine to produce a ContextBrief.
// Only one generation runs at a time; concurrent calls are no-ops.
func (bg *BriefGenerator) Generate(ctx context.Context, _ []Message, agentType string, contextSize, turnNumber int) {
	if !bg.generating.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer bg.generating.Store(false)

		brief, err := bg.source.RequestBrief(ctx, agentType, contextSize, turnNumber)
		if err != nil {
			return
		}
		bg.latest.Store(brief)
	}()
}

// snapshotMessages returns the last n messages as a copied slice.
func snapshotMessages(msgs []Message, n int) []Message {
	if len(msgs) <= n {
		out := make([]Message, len(msgs))
		copy(out, msgs)
		return out
	}
	out := make([]Message, n)
	copy(out, msgs[len(msgs)-n:])
	return out
}

// Latest returns the most recently generated brief, or nil.
func (bg *BriefGenerator) Latest() *ContextBrief {
	return bg.latest.Load()
}

// SetOnPreparedContext serializes the latest brief as JSON and stores it
// in the PreparedContext metadata under "context_brief".
func (bg *BriefGenerator) SetOnPreparedContext(pc *PreparedContext) {
	brief := bg.latest.Load()
	if brief == nil {
		return
	}

	data, err := json.Marshal(brief)
	if err != nil {
		return
	}
	pc.SetMetadata("context_brief", string(data))
}

// --------------------------------------------------------------------------
// DirectBriefSource — backwards-compatible LLM-based brief generation
// --------------------------------------------------------------------------

// DirectBriefSource generates briefs by calling an LLM provider directly.
// This is the legacy path, used when no Archivalist is available.
type DirectBriefSource struct {
	provider providers.Provider
	model    string
	messages func() []Message // returns recent messages for context
}

// NewDirectBriefSource creates a BriefSource that calls the LLM directly.
func NewDirectBriefSource(provider providers.Provider, model string, messagesFn func() []Message) *DirectBriefSource {
	return &DirectBriefSource{
		provider: provider,
		model:    model,
		messages: messagesFn,
	}
}

// RequestBrief generates a brief by calling the LLM provider directly.
func (d *DirectBriefSource) RequestBrief(ctx context.Context, agentType string, contextSize, turnNumber int) (*ContextBrief, error) {
	msgs := d.messages()
	recent := snapshotMessages(msgs, briefMaxRecentMessages)

	var sb strings.Builder
	for _, m := range recent {
		fmt.Fprintf(&sb, "[%s]: %s\n", m.Role, m.Content)
	}

	req := &providers.Request{
		Model:        d.model,
		MaxTokens:    1024,
		SystemPrompt: briefPrompt(agentType),
		Messages: []providers.Message{
			{
				Role:    providers.RoleUser,
				Content: sb.String(),
			},
		},
	}

	resp, err := d.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("brief generation: %w", err)
	}

	brief := &ContextBrief{
		GeneratedAt: time.Now(),
		ContextSize: contextSize,
		TurnNumber:  turnNumber,
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), brief); err != nil {
		brief.TaskSummary = strings.TrimSpace(resp.Content)
	}

	return brief, nil
}

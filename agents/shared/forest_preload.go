package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/forest"
)

// MEM-01: pre-LLM forest preload.
//
// The skills registry already lets the LLM call into the forest
// mid-loop (ResolveIntent, Retrieve, etc.). That is reactive — the
// model must think "I should look at memory" before it reaches for a
// skill. MEM-01 closes the other half: surface the relevant family
// projections in the system prompt *before* the LLM call, so memory
// context is framing rather than a lookup.

// ForestPreloadInput carries everything needed to issue family
// projections for a single LLM turn.
type ForestPreloadInput struct {
	AgentType string
	Query     string
	SessionID string
	TaskID    string
	AgentID   string
	IntentID  string
	Horizon   forest.CanopyHorizon
	Limit     int
}

// ForestPreload is the assembled pre-LLM memory bundle. A nil or
// empty preload means "no memory context available" and callers
// should degrade silently — memory is an assist, not a gate.
type ForestPreload struct {
	Projection *forest.ForestRoleProjection
	Cursor     *forest.ForestCursor
	Packets    []*forest.ForestPacket
}

// PreloadFor assembles a phase-9 role projection from ForestPacket and
// ForestCursor state for every role that participates in emergent agency.
// Returns (nil, nil) when the service is nil; memory preload is best-effort,
// never a gate.
func PreloadFor(
	ctx context.Context,
	svc MemoryForestService,
	input ForestPreloadInput,
) (*ForestPreload, error) {
	if svc == nil {
		return nil, nil
	}
	role := normalizedForestPreloadRole(input.AgentType)
	if !forestPreloadRoleSupported(role) {
		return nil, nil
	}
	limit := input.Limit
	packets, err := svc.RetrieveForest(ctx, forest.Query{
		Query:                  input.Query,
		SessionID:              input.SessionID,
		TaskID:                 input.TaskID,
		AgentID:                input.AgentID,
		AgentType:              input.AgentType,
		IntentID:               input.IntentID,
		Horizon:                input.Horizon,
		Limit:                  limit,
		IncludeCounterEvidence: true,
	})
	if err != nil {
		return nil, fmt.Errorf("preload retrieve forest packets: %w", err)
	}
	cursor, err := svc.CreateForestCursor(ctx, forest.ForestCursorInput{
		SessionID: input.SessionID,
		TaskID:    input.TaskID,
		AgentID:   input.AgentID,
		Packets:   packets,
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("preload create forest cursor: %w", err)
	}
	projection, err := forest.BuildRoleForestProjection(role, packets, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("preload role projection: %w", err)
	}
	return &ForestPreload{Projection: projection, Cursor: cursor, Packets: packets}, nil
}

func forestPreloadRoleSupported(role string) bool {
	switch normalizedForestPreloadRole(role) {
	case "architect", "engineer", "tester", "guardian", "inspector", "librarian", "academic", "designer", "orchestrator", "scribe", "archivalist", "guide":
		return true
	default:
		return false
	}
}

func normalizedForestPreloadRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch {
	case strings.HasPrefix(role, "scribe"):
		return "scribe"
	case role == "inspector-pipeline":
		return "inspector"
	case role == "tester-pipeline":
		return "tester"
	default:
		return role
	}
}

// -----------------------------------------------------------------------------
// Rendering: preload → prompt text.
// -----------------------------------------------------------------------------

// Render turns the preload into a compact, skimmable block suitable
// for prepending to a system prompt. Returns "" when the preload is
// empty so callers can always unconditionally concatenate.
//
// Layout is one header line per family followed by up to five
// top-ranked branches per bucket. We stay aggressively short — a
// 40-branch memory dump competes with actual instructions for the
// model's attention. The specific bucket we surface from each
// projection is the one the consumer acts on most often (Active
// intents, Enforced constraints, Chosen decisions, Current evidence,
// Successes for outcomes, Active preferences, Proven capabilities).
func (p *ForestPreload) Render() string {
	if p.IsEmpty() {
		return ""
	}
	if p.Projection != nil {
		return p.Projection.Text
	}
	return ""
}

// IsEmpty reports whether the preload carries any usable content. A
// preload with every projection set but every bucket empty still
// counts as empty — rendering it would produce a pure header.
func (p *ForestPreload) IsEmpty() bool {
	if p == nil {
		return true
	}
	if p.Projection != nil && strings.TrimSpace(p.Projection.Text) != "" {
		return false
	}
	return true
}

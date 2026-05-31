package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/providers"
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

// ApplyForestPreload injects the rendered role projection into an LLM request
// and propagates the cursor through request metadata and fabric baggage. It is
// best-effort by design: callers should continue when the forest is absent.
func ApplyForestPreload(
	ctx context.Context,
	req *providers.Request,
	svc MemoryForestService,
	input ForestPreloadInput,
) (context.Context, *ForestPreload, error) {
	if req == nil {
		return ctx, nil, nil
	}
	preload, err := PreloadFor(ctx, svc, input)
	if err != nil || preload == nil {
		return ctx, preload, err
	}
	rendered := preload.Render()
	if rendered != "" {
		req.SystemPrompt = rendered + "\n\n---\n\n" + req.SystemPrompt
	}
	if preload.Cursor != nil {
		req.Metadata = forestPreloadMetadata(req.Metadata, preload.Cursor)
		ctx = activity.WithBaggage(ctx, "forest_cursor_id", preload.Cursor.ID)
		ctx = activity.WithBaggage(ctx, "forest_active_nodes", strings.Join(preload.Cursor.ActiveNodeIDs, ","))
		ctx = activity.WithBaggage(ctx, "forest_active_clusters", strings.Join(preload.Cursor.ActiveClusterIDs, ","))
	}
	return ctx, preload, nil
}

func forestPreloadMetadata(metadata map[string]any, cursor *forest.ForestCursor) map[string]any {
	if metadata == nil {
		metadata = make(map[string]any)
	}
	if cursor == nil {
		metadata["forest_no_cursor_reason"] = "no_cursor"
		return metadata
	}
	metadata["forest_cursor_id"] = cursor.ID
	metadata["forest_active_node_ids"] = append([]string(nil), cursor.ActiveNodeIDs...)
	metadata["forest_active_cluster_ids"] = append([]string(nil), cursor.ActiveClusterIDs...)
	metadata["forest_risk_flags"] = append([]string(nil), cursor.RiskFlags...)
	if cursor.NoCursorReason != "" {
		metadata["forest_no_cursor_reason"] = cursor.NoCursorReason
	}
	return metadata
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

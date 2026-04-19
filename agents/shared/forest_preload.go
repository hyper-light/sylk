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
	Intents       *forest.IntentProjection
	Constraints   *forest.ConstraintProjection
	Evidence      *forest.EvidenceProjection
	Decisions     *forest.DecisionProjection
	Outcomes      *forest.OutcomeProjection
	Preferences   *forest.PreferenceProjection
	Capabilities  *forest.CapabilityProjection
	Opportunities *forest.OpportunityProjection
}

// PreloadFor dispatches to the agent-specific projection set.
//
// Per MEMORY_FOREST.md the "specialized trees" are consumer-shaped:
// Architect cares about Intent / Constraint / Decision; Librarian
// wants Preference / Capability (plus Intent to ground retrievals);
// Academic wants Evidence / Outcome. Asking every agent for every
// family would inflate prompt size and dilute ranking signal, so we
// keep these lanes narrow on purpose.
//
// Returns (nil, nil) when the service is nil or the agent type is
// unknown — memory preload is best-effort, never a gate.
func PreloadFor(
	ctx context.Context,
	svc MemoryForestService,
	input ForestPreloadInput,
) (*ForestPreload, error) {
	if svc == nil {
		return nil, nil
	}
	projInput := forest.ProjectionInput{
		Query:     input.Query,
		SessionID: input.SessionID,
		TaskID:    input.TaskID,
		AgentID:   input.AgentID,
		AgentType: input.AgentType,
		IntentID:  input.IntentID,
		Horizon:   input.Horizon,
		Limit:     input.Limit,
	}
	switch strings.ToLower(strings.TrimSpace(input.AgentType)) {
	case "architect":
		return preloadArchitect(ctx, svc, projInput)
	case "librarian":
		return preloadLibrarian(ctx, svc, projInput)
	case "academic":
		return preloadAcademic(ctx, svc, projInput)
	default:
		return nil, nil
	}
}

// preloadArchitect pulls Intent + Constraint + Decision. The architect
// is a planner: it needs the current intent envelope (what does the
// user want), the constraint set (what are we forbidden from doing),
// and the decision lineage (what has already been tried / chosen /
// rejected). Evidence is intentionally left out here — it is the
// Academic's lane, and the architect should source factual claims via
// consultation rather than inline prompt.
func preloadArchitect(ctx context.Context, p MemoryForestService, in forest.ProjectionInput) (*ForestPreload, error) {
	intents, err := p.ProjectIntent(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload architect intents: %w", err)
	}
	constraints, err := p.ProjectConstraints(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload architect constraints: %w", err)
	}
	decisions, err := p.ProjectDecisions(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload architect decisions: %w", err)
	}
	return &ForestPreload{Intents: intents, Constraints: constraints, Decisions: decisions}, nil
}

// preloadLibrarian pulls Preference + Capability + Intent. The
// librarian acts on behalf of the user's preferences (explanation
// style, risk tolerance) and needs to know which retrieval / reading
// strategies have worked before (capabilities). Intent is included so
// it can bias lexical/semantic search toward the active goal.
func preloadLibrarian(ctx context.Context, p MemoryForestService, in forest.ProjectionInput) (*ForestPreload, error) {
	preferences, err := p.ProjectPreferences(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload librarian preferences: %w", err)
	}
	capabilities, err := p.ProjectCapabilities(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload librarian capabilities: %w", err)
	}
	intents, err := p.ProjectIntent(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload librarian intents: %w", err)
	}
	return &ForestPreload{Preferences: preferences, Capabilities: capabilities, Intents: intents}, nil
}

// preloadAcademic pulls Evidence + Outcome + Intent. The academic is
// the epistemic anchor of the fabric: it needs the current evidence
// graph (what do we already know), past outcomes (what have we
// validated or contradicted), and the active intent (so its
// hypotheses aim at the current question, not an abstract survey).
func preloadAcademic(ctx context.Context, p MemoryForestService, in forest.ProjectionInput) (*ForestPreload, error) {
	evidence, err := p.ProjectEvidence(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload academic evidence: %w", err)
	}
	outcomes, err := p.ProjectOutcomes(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload academic outcomes: %w", err)
	}
	intents, err := p.ProjectIntent(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("preload academic intents: %w", err)
	}
	return &ForestPreload{Evidence: evidence, Outcomes: outcomes, Intents: intents}, nil
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
	var b strings.Builder
	b.WriteString("MEMORY CONTEXT (pre-LLM forest preload):\n")
	renderIntent(&b, p.Intents)
	renderConstraints(&b, p.Constraints)
	renderDecisions(&b, p.Decisions)
	renderEvidence(&b, p.Evidence)
	renderOutcomes(&b, p.Outcomes)
	renderPreferences(&b, p.Preferences)
	renderCapabilities(&b, p.Capabilities)
	return b.String()
}

// IsEmpty reports whether the preload carries any usable content. A
// preload with every projection set but every bucket empty still
// counts as empty — rendering it would produce a pure header.
func (p *ForestPreload) IsEmpty() bool {
	if p == nil {
		return true
	}
	return intentEmpty(p.Intents) &&
		constraintEmpty(p.Constraints) &&
		decisionEmpty(p.Decisions) &&
		evidenceEmpty(p.Evidence) &&
		outcomeEmpty(p.Outcomes) &&
		preferenceEmpty(p.Preferences) &&
		capabilityEmpty(p.Capabilities)
}

const preloadBucketMax = 5

func renderIntent(b *strings.Builder, p *forest.IntentProjection) {
	if intentEmpty(p) {
		return
	}
	b.WriteString("\n- Active intents:")
	if p.PrimaryIntent != "" {
		fmt.Fprintf(b, " primary=%q", p.PrimaryIntent)
	}
	writeBranchSummaries(b, p.Active, preloadBucketMax)
}

func renderConstraints(b *strings.Builder, p *forest.ConstraintProjection) {
	if constraintEmpty(p) {
		return
	}
	b.WriteString("\n- Enforced constraints:")
	writeBranchSummaries(b, p.Enforced, preloadBucketMax)
}

func renderDecisions(b *strings.Builder, p *forest.DecisionProjection) {
	if decisionEmpty(p) {
		return
	}
	b.WriteString("\n- Chosen decisions:")
	writeBranchSummaries(b, p.Chosen, preloadBucketMax)
}

func renderEvidence(b *strings.Builder, p *forest.EvidenceProjection) {
	if evidenceEmpty(p) {
		return
	}
	b.WriteString("\n- Current evidence:")
	writeBranchSummaries(b, p.Current, preloadBucketMax)
}

func renderOutcomes(b *strings.Builder, p *forest.OutcomeProjection) {
	if outcomeEmpty(p) {
		return
	}
	b.WriteString("\n- Validated outcomes:")
	writeBranchSummaries(b, p.Successes, preloadBucketMax)
}

func renderPreferences(b *strings.Builder, p *forest.PreferenceProjection) {
	if preferenceEmpty(p) {
		return
	}
	b.WriteString("\n- Active preferences:")
	writeBranchSummaries(b, p.Active, preloadBucketMax)
}

func renderCapabilities(b *strings.Builder, p *forest.CapabilityProjection) {
	if capabilityEmpty(p) {
		return
	}
	b.WriteString("\n- Proven capabilities:")
	writeBranchSummaries(b, p.Proven, preloadBucketMax)
}

func writeBranchSummaries(b *strings.Builder, packets []forest.BranchPacket, max int) {
	if len(packets) == 0 {
		b.WriteString(" (none)")
		return
	}
	for i, packet := range packets {
		if i >= max {
			fmt.Fprintf(b, "\n  …+%d more", len(packets)-max)
			return
		}
		summary := packetSummary(&packet)
		if summary == "" {
			continue
		}
		fmt.Fprintf(b, "\n  • %s", summary)
	}
}

func packetSummary(p *forest.BranchPacket) string {
	if p == nil || p.Branch == nil {
		return ""
	}
	title := strings.TrimSpace(p.Branch.Title)
	summary := strings.TrimSpace(p.Branch.Summary)
	switch {
	case title != "" && summary != "":
		return title + " — " + summary
	case title != "":
		return title
	default:
		return summary
	}
}

func intentEmpty(p *forest.IntentProjection) bool {
	return p == nil || (len(p.Active) == 0 && p.PrimaryIntent == "")
}

func constraintEmpty(p *forest.ConstraintProjection) bool {
	return p == nil || len(p.Enforced) == 0
}

func decisionEmpty(p *forest.DecisionProjection) bool {
	return p == nil || len(p.Chosen) == 0
}

func evidenceEmpty(p *forest.EvidenceProjection) bool {
	return p == nil || len(p.Current) == 0
}

func outcomeEmpty(p *forest.OutcomeProjection) bool {
	return p == nil || len(p.Successes) == 0
}

func preferenceEmpty(p *forest.PreferenceProjection) bool {
	return p == nil || len(p.Active) == 0
}

func capabilityEmpty(p *forest.CapabilityProjection) bool {
	return p == nil || len(p.Proven) == 0
}

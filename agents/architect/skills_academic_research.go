// Phase 2.6 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// academic_research absorbs route_requirements_research and
// read_research_paper into one verb-typed primitive. The original
// skill handlers (submitRequirementsResearchHandoff,
// executePlanningProtocol-from-paper) stay; only the outer LLM-facing
// surface collapses.
package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

type academicResearchInput struct {
	Action string `json:"action"`

	// request (previously route_requirements_research) fields.
	PlanID              string   `json:"plan_id,omitempty"`
	OriginalInput       string   `json:"original_input,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	ResearchGoal        string   `json:"research_goal,omitempty"`
	KnownContext        []string `json:"known_context,omitempty"`
	MissingRequirements []string `json:"missing_requirements,omitempty"`

	// read (previously read_research_paper) fields.
	ResearchSlug         string   `json:"research_slug,omitempty"`
	PaperPath            string   `json:"paper_path,omitempty"`
	Version              int      `json:"version,omitempty"`
	Summary              string   `json:"summary,omitempty"`
	RecommendedOptionID  string   `json:"recommended_option_id,omitempty"`
	PrototypeSketch      string   `json:"prototype_sketch,omitempty"`
	SystemDesignNotes    []string `json:"system_design_notes,omitempty"`
	SessionID            string   `json:"session_id,omitempty"`
}

// academicResearchSkill merges route_requirements_research and
// read_research_paper into one skill with action ∈ {request, read}.
func academicResearchSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("academic_research").
		Description("Engage the Academic for requirements clarification or to read a research artifact into the planning protocol. Actions:\n" +
			"- request: Hand the conversation to the Academic when the request is too vague for reliable planning (params: reason [required], original_input, research_goal, known_context, missing_requirements, plan_id).\n" +
			"- read: Read a research paper artifact and drive the full planning protocol from it (params: paper_path [required], research_slug, version, summary, recommended_option_id, prototype_sketch, system_design_notes, session_id).").
		Domain("research").
		Keywords("academic", "research", "requirements", "clarify", "paper", "proposal", "underspecified").
		Priority(95).
		Usage("request: the user's implementation request is underspecified; hand off to Academic for exploratory clarification before planning continues. read: the user supplied a substantial research paper or proposal; convert it into an executable plan via the full protocol (including an Academic consultation).").
		Example(`{"action": "request", "original_input": "Build a production-ready observability system.", "reason": "Scope/compliance/success metrics not defined.", "missing_requirements": ["Target services", "Retention constraints"]}`).
		Example(`{"action": "read", "research_slug": "distributed-cache-v2", "paper_path": "docs/proposals/distributed_cache.md", "version": 1, "summary": "Two-tier caching with Redis L1 and disk L2"}`).
		BestPractice("Use request for problem-space clarification, read for real research artifacts.").
		BestPractice("Do NOT use request when the blocker is codebase or change-history evidence — consult Librarian/Archivalist instead via consult_peer.").
		EnumParam("action", "Research action", []string{"request", "read"}, true).
		StringParam("plan_id", "Current plan identifier when the action is mid-protocol (request)", false).
		StringParam("original_input", "The user's implementation request or latest relevant message (request)", false).
		StringParam("reason", "Why the Architect cannot safely plan yet (request)", false).
		StringParam("research_goal", "What the Academic should help clarify or research (request)", false).
		ArrayParam("known_context", "Facts or constraints already established (request)", "string", false).
		ArrayParam("missing_requirements", "Concrete gaps that must be clarified before planning (request)", "string", false).
		StringParam("research_slug", "Research identifier slug (read)", false).
		StringParam("paper_path", "Path to research paper markdown (read)", false).
		IntParam("version", "Research version number (read)", false).
		StringParam("summary", "Optional proposal summary (read)", false).
		StringParam("recommended_option_id", "Optional preferred architecture option identifier (read)", false).
		StringParam("prototype_sketch", "Optional proof-of-concept sketch from the research paper (read)", false).
		ArrayParam("system_design_notes", "Optional system design implications carried from the research paper (read)", "string", false).
		StringParam("session_id", "Session identifier (read)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params academicResearchInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(params.Action) {
			case "request":
				return academicResearchRequest(ctx, a, &params)
			case "read":
				return academicResearchRead(ctx, a, &params)
			default:
				return nil, fmt.Errorf("unknown academic_research action: %q (expected request|read)", params.Action)
			}
		}).
		Build()
}

func academicResearchRequest(ctx context.Context, a *Architect, p *academicResearchInput) (any, error) {
	if strings.TrimSpace(p.Reason) == "" {
		return nil, fmt.Errorf("reason is required for action=request")
	}
	return a.submitRequirementsResearchHandoff(ctx, &routeRequirementsResearchParams{
		PlanID:              strings.TrimSpace(p.PlanID),
		OriginalInput:       strings.TrimSpace(p.OriginalInput),
		Reason:              strings.TrimSpace(p.Reason),
		ResearchGoal:        strings.TrimSpace(p.ResearchGoal),
		KnownContext:        dedupeNonEmptyStrings(p.KnownContext),
		MissingRequirements: dedupeNonEmptyStrings(p.MissingRequirements),
	})
}

func academicResearchRead(ctx context.Context, a *Architect, p *academicResearchInput) (any, error) {
	if strings.TrimSpace(p.PaperPath) == "" {
		return nil, fmt.Errorf("paper_path is required for action=read")
	}
	content, err := os.ReadFile(p.PaperPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read research paper: %w", err)
	}
	readParams := &readResearchPaperParams{
		ResearchSlug:        p.ResearchSlug,
		PaperPath:           p.PaperPath,
		Version:             p.Version,
		Summary:             p.Summary,
		RecommendedOptionID: p.RecommendedOptionID,
		PrototypeSketch:     p.PrototypeSketch,
		SystemDesignNotes:   p.SystemDesignNotes,
		SessionID:           p.SessionID,
	}
	query := buildResearchPlanningQuery(readParams, string(content))
	req := &ArchitectRequest{
		ID:        uuid.NewString(),
		Intent:    IntentPlan,
		Query:     query,
		SessionID: readParams.SessionID,
		Timestamp: time.Now(),
		Params: map[string]any{
			"scope":            "research",
			"include_academic": true,
			"research_slug":    readParams.ResearchSlug,
			"paper_path":       readParams.PaperPath,
			"version":          readParams.Version,
		},
	}
	plan, err := a.executePlanningProtocol(ctx, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"research_slug": readParams.ResearchSlug,
		"paper_path":    readParams.PaperPath,
		"plan_id":       plan.ID,
		"status":        plan.Status.String(),
	}, nil
}

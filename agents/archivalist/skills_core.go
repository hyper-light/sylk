package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
)

func (a *Archivalist) registerExtendedSkills() {
	a.skills.Register(crossSessionQuerySkill(a))
	a.skills.Register(workflowHistorySkill(a))
	a.skills.Register(tokenSavingsSkill(a))
	a.skills.Register(sessionTimelineSkill(a))
	a.skills.Register(storeResearchPaperSkill(a))
	a.skills.Register(researchPapersSkill(a))
	a.skills.Register(researchPlanningBriefSkill(a))
	a.skills.Register(patternSearchSkill(a))
	a.skills.Register(failureSearchSkill(a))
	a.skills.Register(decisionSearchSkill(a))
	a.skills.Register(knowledgeMemorySkill(a))
	a.skills.Register(knowledgeQuerySkill(a))
	a.skills.Register(knowledgeInsightsSkill(a))
}

func (a *Archivalist) registerCoreSkills() {
	a.skills.Register(storeSkill(a))
	a.skills.Register(querySkill(a))
	a.skills.Register(briefingSkill(a))
	a.skills.Register(getBriefingToolSkill(a))
	a.skills.Register(queryPatternsToolSkill(a))
	a.skills.Register(queryFailuresToolSkill(a))
	a.skills.Register(queryContextToolSkill(a))
	a.skills.Register(queryFileStateToolSkill(a))
	a.skills.Register(recordPatternToolSkill(a))
	a.skills.Register(recordFailureToolSkill(a))
	a.skills.Register(updateFileStateToolSkill(a))
	a.skills.Register(declareIntentToolSkill(a))
	a.skills.Register(completeIntentToolSkill(a))
	a.skills.Register(getConflictsToolSkill(a))
	// Façades: the 10 verbose per-verb tools above collapse into three
	// polymorphic primitives for the LLM catalog — archivalist_query
	// (6 read kinds → 1), archivalist_record (2 write kinds → 1),
	// archivalist_intent (2 lifecycle actions → 1). The per-verb
	// skills stay registered for internal callers (handlers.go, the
	// legacy protocol endpoints, and tests); only the visible surface
	// collapses. See skills_facade.go for the dispatch handlers.
	a.skills.Register(archivalistQueryFacadeSkill(a))
	a.skills.Register(archivalistRecordFacadeSkill(a))
	a.skills.Register(archivalistIntentFacadeSkill(a))
	a.skills.Register(routeToSkill(a))
	a.skills.Register(replyToSkill(a))
	a.skills.Register(consultSkill(a))
	a.skills.Register(shared.NewSelfDiagnosticSkill(&archivalistDiag{a: a}))
	if a.logIngest != nil {
		a.skills.Register(queryAgentLogsSkill(a.logIngest))
	}
	a.skills.Register(skills.NewRerouteSkill(skills.RerouteConfig{
		AgentID:   "archivalist",
		SessionID: func() string { return a.defaultSessionID },
		Publish:   a.publishRerouteRequest,
	}))

	// Activity Fabric: uniform awareness skills + recall.
	a.registerFabricSkills()
}

func (a *Archivalist) registerFabricSkills() {
	sessionID := func() string { return strings.TrimSpace(a.defaultSessionID) }
	agentID := func() string { return a.id }
	agentType := func() string { return "archivalist" }

	for _, skill := range fabric.AwarenessSkills(fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      sessionID,
		AgentID:        agentID,
		AgentType:      agentType,
	}) {
		a.skills.Register(skill)
	}
	for _, skill := range fabric.RecallSkills(fabric.RecallSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      sessionID,
		AgentID:        agentID,
		AgentType:      agentType,
	}) {
		a.skills.Register(skill)
	}
	// Claims skills.
	boardProvider := func() *claims.ClaimsBoard {
		return claims.DefaultSessionBoardRegistry().Lookup(a.defaultSessionID)
	}
	inboxProvider := func() *claims.ClaimsInbox { return a.claimsInbox }
	a.skills.Register(claims.QueryClaimsBoardSkill(boardProvider))
	a.skills.Register(claims.PostActionSkill(boardProvider, inboxProvider))
	a.skills.Register(claims.SubmitTestamentsSkill(boardProvider))
	a.skills.Register(claims.EvaluateValidationSkill(boardProvider))
	a.skills.Register(claims.UpdateClaimProgressSkill(boardProvider))
	a.skills.Register(claims.InspectClaimConflictsSkill(boardProvider))
	a.skills.Register(claims.TraverseSkill(boardProvider))

	fabricCfg := fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      sessionID,
		AgentID:        agentID,
		AgentType:      agentType,
	}
	for _, skill := range fabric.ClaimsAwarenessSkills(fabricCfg) {
		a.skills.Register(skill)
	}
}

type archivalistDiag struct{ a *Archivalist }

func (d *archivalistDiag) AgentName() string                         { return "archivalist" }
func (d *archivalistDiag) SessionID() string                         { return d.a.defaultSessionID }
func (d *archivalistDiag) LogsDir() string                           { return "" }
func (d *archivalistDiag) EventLogger() *agentlog.SessionEventLogger { return nil }
func (d *archivalistDiag) PeerLogsDirs() map[string]string           { return nil }
func (d *archivalistDiag) RecoveryHints() []string                   { return nil }

func (d *archivalistDiag) AgentSpecificDiagnostics() map[string]any {
	result := map[string]any{}
	if d.a.logIngest != nil {
		result["ingest_store_stats"] = d.a.logIngest.Stats()
	}
	return result
}

func (a *Archivalist) publishRerouteRequest(reason, originalInput, suggestedTarget string) error {
	if a.bus == nil {
		return fmt.Errorf("archivalist bus not available")
	}
	reroute := &guide.RerouteRequest{
		OriginalInput:   originalInput,
		Reason:          reason,
		SourceAgentID:   "archivalist",
		SuggestedTarget: suggestedTarget,
		SessionID:       a.defaultSessionID,
		ExcludeAgents:   []string{"archivalist"},
	}
	return a.bus.Publish(guide.TopicGuideRequests, guide.NewRerouteMessage("", reroute))
}

func storeSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("store").
		Description("Store information in the chronicle.").
		Domain("chronicle").
		Keywords("store", "save", "record", "remember", "log").
		Priority(100).
		StringParam("content", "The content to store", true).
		EnumParam("category", "Category of the entry", []string{
			"decision", "insight", "pattern", "failure", "task_state",
			"timeline", "user_voice", "hypothesis", "open_thread", "general",
		}, true).
		StringParam("title", "Optional title for the entry", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Content  string `json:"content"`
				Category string `json:"category"`
				Title    string `json:"title"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			entry := &Entry{
				Content:  params.Content,
				Title:    params.Title,
				Category: Category(params.Category),
				Source:   SourceModelArchivalist,
			}
			result := a.StoreEntry(ctx, entry)
			return result, result.Error
		}).
		Build()
}

func querySkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("query").
		Description("Query the chronicle for stored information.").
		Domain("chronicle").
		Keywords("query", "find", "search", "recall", "retrieve", "what", "when", "how").
		Priority(100).
		StringParam("search", "Text to search for", false).
		EnumParam("category", "Filter by category", []string{
			"decision", "insight", "pattern", "failure", "task_state",
			"timeline", "user_voice", "hypothesis", "open_thread", "general", "all",
		}, false).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Search   string `json:"search"`
				Category string `json:"category"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.Limit == 0 {
				params.Limit = 10
			}

			query := ArchiveQuery{
				SearchText: params.Search,
				Limit:      params.Limit,
			}

			if params.Category != "" && params.Category != "all" {
				query.Categories = []Category{Category(params.Category)}
			}

			return a.Query(ctx, query)
		}).
		Build()
}

func briefingSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("briefing").
		Description("Get a briefing of the current session state.").
		Domain("memory").
		Keywords("briefing", "status", "context", "state", "progress", "current").
		Priority(90).
		EnumParam("tier", "Level of detail", []string{"micro", "standard", "full"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Tier string `json:"tier"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			switch params.Tier {
			case "micro":
				return a.getMicroBriefing().Data, nil
			case "full":
				return a.getFullBriefing().Data, nil
			default:
				return a.getStandardBriefing().Data, nil
			}
		}).
		Build()
}

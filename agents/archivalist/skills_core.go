package archivalist

import (
	"context"
	"encoding/json"

	"github.com/adalundhe/sylk/core/skills"
)

func (a *Archivalist) registerExtendedSkills() {
	a.skills.Register(crossSessionQuerySkill(a))
	a.skills.Register(workflowHistorySkill(a))
	a.skills.Register(tokenSavingsSkill(a))
	a.skills.Register(sessionTimelineSkill(a))
	a.skills.Register(patternSearchSkill(a))
	a.skills.Register(failureSearchSkill(a))
	a.skills.Register(decisionSearchSkill(a))
}

func (a *Archivalist) registerCoreSkills() {
	a.skills.Register(storeSkill(a))
	a.skills.Register(querySkill(a))
	a.skills.Register(briefingSkill(a))
	a.skills.Register(routeToSkill(a))
	a.skills.Register(replyToSkill(a))

	a.skills.Load("store")
	a.skills.Load("query")
	a.skills.Load("briefing")
	a.skills.Load("route_to")
	a.skills.Load("reply_to")
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

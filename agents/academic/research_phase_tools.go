package academic

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
)

func academicToolNames(tools []providers.Tool) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func academicFilterToolDefinitions(tools []providers.Tool, allowed map[string]struct{}) []providers.Tool {
	if len(tools) == 0 || len(allowed) == 0 {
		return nil
	}
	filtered := make([]providers.Tool, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if _, ok := allowed[name]; !ok {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func academicValidateToolCallsAgainstRequest(calls []providers.ToolCall, tools []providers.Tool) error {
	if len(calls) == 0 || len(tools) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		allowed[name] = struct{}{}
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		return fmt.Errorf("academic phase policy rejected tool %q for the current research phase", name)
	}
	return nil
}

func academicNeedsConsultEvidence(contract *academicCompletionContract, tracker *searchEvidenceTracker) bool {
	if contract == nil {
		return false
	}
	for _, class := range contract.missingRequiredEvidence(tracker) {
		switch class {
		case academicEvidenceCodebaseFit, academicEvidenceHistoricalPrecedent:
			return true
		}
	}
	return false
}

func academicNeedsReferenceRepoEvidence(contract *academicCompletionContract, tracker *searchEvidenceTracker) bool {
	if contract == nil {
		return false
	}
	for _, class := range contract.missingRequiredEvidence(tracker) {
		if class == academicEvidenceReferenceRepos {
			return true
		}
	}
	for _, class := range contract.missingPreferredEvidence(tracker) {
		if class == academicEvidenceReferenceRepos {
			return true
		}
	}
	return false
}

func academicAllowedToolNamesForPhase(
	ctx context.Context,
	tools []providers.Tool,
	tracker *searchEvidenceTracker,
) map[string]struct{} {
	allowed := make(map[string]struct{}, len(tools))
	if len(tools) == 0 {
		return allowed
	}

	phase := researchPhaseDiscover
	if tracker != nil {
		phase = tracker.currentPhase()
	}
	contract := AcademicCompletionContractFromContext(ctx)
	requiredState := AcademicTurnStateFromContext(ctx)
	requiredAction, _ := requiredState.RequiredAction()
	requiredActionReady := requiredAction != "" && (contract == nil || tracker == nil || contract.requiredEvidenceSatisfied(tracker))
	hasGrounding := tracker != nil && tracker.hasGroundedEvidence()
	needsConsult := academicNeedsConsultEvidence(contract, tracker) && hasGrounding
	needsReferenceRepos := academicNeedsReferenceRepoEvidence(contract, tracker) && hasGrounding

	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		switch phase {
		case researchPhaseDiscover:
			switch name {
			case "web_search", "web_fetch", "fetch_document", "crawl_links", "search_skills":
				allowed[name] = struct{}{}
			}
		case researchPhaseGround:
			switch name {
			case "web_fetch", "fetch_document", "crawl_links":
				allowed[name] = struct{}{}
			}
		case researchPhaseCorroborate:
			switch name {
			case "web_fetch", "fetch_document", "crawl_links":
				allowed[name] = struct{}{}
			case "consult":
				if needsConsult {
					allowed[name] = struct{}{}
				}
			case "clone_via_librarian":
				if needsReferenceRepos {
					allowed[name] = struct{}{}
				}
			}
		case researchPhaseSynthesize:
			if requiredAction == academicTurnActionResearchPaper && requiredActionReady && name == string(academicTurnActionResearchPaper) {
				allowed[name] = struct{}{}
				continue
			}
			switch name {
			case "web_fetch", "fetch_document", "crawl_links":
				if contract != nil && !contract.requiredEvidenceSatisfied(tracker) {
					allowed[name] = struct{}{}
				}
			case "consult":
				if needsConsult {
					allowed[name] = struct{}{}
				}
			case "clone_via_librarian":
				if needsReferenceRepos {
					allowed[name] = struct{}{}
				}
			}
		default:
			allowed[name] = struct{}{}
		}
	}

	if len(allowed) == 0 && requiredAction == academicTurnActionResearchPaper {
		for _, tool := range tools {
			if strings.TrimSpace(tool.Name) == string(academicTurnActionResearchPaper) && requiredActionReady {
				allowed[tool.Name] = struct{}{}
				break
			}
		}
	}

	return allowed
}

func academicApplyPhaseToolPolicy(
	ctx context.Context,
	tools []providers.Tool,
	tracker *searchEvidenceTracker,
) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	filtered := academicFilterToolDefinitions(tools, academicAllowedToolNamesForPhase(ctx, tools, tracker))
	academicLogPhaseToolSurface(ctx, tracker, tools, filtered)
	return filtered
}

func academicShouldUsePhaseToolPolicy(ctx context.Context, discipline searchDiscipline) bool {
	if discipline.RequireWebSearch {
		return true
	}
	if AcademicCompletionContractFromContext(ctx) != nil {
		return true
	}
	if state := AcademicTurnStateFromContext(ctx); state != nil {
		if action, _ := state.RequiredAction(); action != "" {
			return true
		}
	}
	return false
}

func academicLogPhaseToolSurface(ctx context.Context, tracker *searchEvidenceTracker, before, after []providers.Tool) {
	meta := shared.LogMetaFromContext(ctx)
	if meta.EventLogger == nil {
		return
	}
	phase := researchPhaseDiscover
	scores := researchProgressScores{}
	if tracker != nil {
		phase = tracker.currentPhase()
		scores = tracker.currentScores()
	}
	requiredAction := ""
	if state := AcademicTurnStateFromContext(ctx); state != nil {
		action, _ := state.RequiredAction()
		requiredAction = string(action)
	}
	contract := AcademicCompletionContractFromContext(ctx)
	shared.LogAgentEvent(
		meta.EventLogger,
		agentlog.EventResearchQuery,
		meta.AgentID,
		meta.SessionID,
		meta.CorrID,
		"debug",
		map[string]any{
			"decision":            "phase_tool_surface",
			"phase":               string(phase),
			"tools_before":        academicToolNames(before),
			"tools_after":         academicToolNames(after),
			"required_action":     requiredAction,
			"missing_required":    academicMissingEvidenceLabels(contract, tracker, true),
			"missing_preferred":   academicMissingEvidenceLabels(contract, tracker, false),
			"saw_search":          tracker != nil && tracker.sawSearch,
			"native_search_calls": trackerCountMap(tracker, func(t *searchEvidenceTracker) int { return len(t.seenCallKeys) }),
			"grounded_urls":       trackerCountMap(tracker, func(t *searchEvidenceTracker) int { return len(t.fetchedURLs) }),
			"consult_targets":     trackerConsultTargets(tracker),
			"breadth":             scores.Breadth,
			"depth":               scores.Depth,
			"grounding":           scores.Grounding,
			"corroboration":       scores.Corroboration,
			"confidence":          scores.Confidence,
			"readiness":           scores.Readiness,
			"total":               scores.Total,
		},
	)
}

func academicLogLoopDecision(ctx context.Context, tracker *searchEvidenceTracker, decision string, extra map[string]any) {
	meta := shared.LogMetaFromContext(ctx)
	if meta.EventLogger == nil {
		return
	}
	phase := researchPhaseDiscover
	scores := researchProgressScores{}
	if tracker != nil {
		phase = tracker.currentPhase()
		scores = tracker.currentScores()
	}
	data := map[string]any{
		"decision":            strings.TrimSpace(decision),
		"phase":               string(phase),
		"saw_search":          tracker != nil && tracker.sawSearch,
		"native_search_calls": trackerCountMap(tracker, func(t *searchEvidenceTracker) int { return len(t.seenCallKeys) }),
		"grounded_urls":       trackerCountMap(tracker, func(t *searchEvidenceTracker) int { return len(t.fetchedURLs) }),
		"consult_targets":     trackerConsultTargets(tracker),
		"breadth":             scores.Breadth,
		"depth":               scores.Depth,
		"grounding":           scores.Grounding,
		"corroboration":       scores.Corroboration,
		"confidence":          scores.Confidence,
		"readiness":           scores.Readiness,
		"total":               scores.Total,
	}
	for key, value := range extra {
		data[key] = value
	}
	shared.LogAgentEvent(
		meta.EventLogger,
		agentlog.EventResearchResult,
		meta.AgentID,
		meta.SessionID,
		meta.CorrID,
		"debug",
		data,
	)
}

func academicMissingEvidenceLabels(contract *academicCompletionContract, tracker *searchEvidenceTracker, required bool) []string {
	if contract == nil {
		return nil
	}
	var classes []academicEvidenceClass
	if required {
		classes = contract.missingRequiredEvidence(tracker)
	} else {
		classes = contract.missingPreferredEvidence(tracker)
	}
	out := make([]string, 0, len(classes))
	for _, class := range classes {
		out = append(out, evidenceClassLabel(class))
	}
	return out
}

func trackerCountMap(tracker *searchEvidenceTracker, fn func(*searchEvidenceTracker) int) int {
	if tracker == nil || fn == nil {
		return 0
	}
	return fn(tracker)
}

func trackerConsultTargets(tracker *searchEvidenceTracker) []string {
	if tracker == nil || len(tracker.consultTargets) == 0 {
		return nil
	}
	targets := make([]string, 0, len(tracker.consultTargets))
	for target := range tracker.consultTargets {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

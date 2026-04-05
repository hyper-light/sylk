package global

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

const determineAuditDepthToolName = "determine_audit_depth"

type globalAuditRequestState struct {
	Input    string
	Contract *agentShared.GlobalExecutionContract
	Metadata map[string]any
}

type globalAuditRequestStateKey struct{}

type auditDepthDecision struct {
	Depth              agentShared.ResearchDepth
	Rationale          string
	ReconsiderIf       []string
	AssessmentGuidance string
}

func withGlobalAuditRequestState(ctx context.Context, input string, contract *agentShared.GlobalExecutionContract, metadata map[string]any) context.Context {
	state := globalAuditRequestState{
		Input:    strings.TrimSpace(input),
		Contract: contract,
		Metadata: agentShared.CloneMetadataMap(metadata),
	}
	return context.WithValue(ctx, globalAuditRequestStateKey{}, state)
}

func globalAuditRequestStateFromContext(ctx context.Context) globalAuditRequestState {
	if ctx == nil {
		return globalAuditRequestState{}
	}
	state, _ := ctx.Value(globalAuditRequestStateKey{}).(globalAuditRequestState)
	return state
}

func determineAuditDepthSkill(_ *GlobalInspector) *skills.Skill {
	return skills.NewSkill(determineAuditDepthToolName).
		Description("Determine the audit depth contract for this global inspector branch before any other audit work begins.").
		Domain("audit").
		Keywords("audit", "depth", "scope", "review", "research").
		Priority(100).
		Usage("Use this as the first tool call on a new global audit branch. It chooses the branch-wide audit depth for your own assessment and for any knowledge consults that honor research depth.").
		Requirement("Call this before other audit, consultation, or global-review work on a fresh branch. The returned depth must govern subsequent audit behavior until the branch changes materially.").
		Satisfies("Creates the branch-level audit depth contract used for local audit intensity and compatible knowledge-agent consult depth.").
		Avoid("Do not skip this on a fresh global audit branch. Do not invent a depth outside the shared Academic depth enum.").
		BestPractice("Use the returned depth as the default depth for your own assessment and for any knowledge consults that support depth unless the branch changes materially.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				CurrentFocus string `json:"current_focus"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, fmt.Errorf("invalid parameters: %w", err)
				}
			}
			decision := determineAuditDepthDecision(ctx, params.CurrentFocus)
			contract, err := agentShared.PersistAuditDepthContract(ctx, agentShared.AuditDepthContract{
				Depth:        string(decision.Depth),
				Rationale:    decision.Rationale,
				ReconsiderIf: decision.ReconsiderIf,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"determined":            true,
				"depth":                 contract.Depth,
				"rationale":             contract.Rationale,
				"reconsider_if":         contract.ReconsiderIf,
				"assessment_guidance":   decision.AssessmentGuidance,
				"applies_to":            []string{"global_inspector_local_assessment", "knowledge_consults_with_depth"},
				"academic_depth_compat": true,
			}, nil
		}).
		Build()
}

func determineAuditDepthDecision(ctx context.Context, currentFocus string) auditDepthDecision {
	state := globalAuditRequestStateFromContext(ctx)
	metadata := agentShared.CloneMetadataMap(state.Metadata)
	if review := agentShared.GlobalReviewStateFromContext(ctx); review != nil {
		metadata = review.BaseMetadata()
	}

	rank := 1 // quick by default for fresh global audit branches
	reasons := []string{"defaulting to a quick global audit posture until the branch proves it needs broader corroboration"}
	reconsider := []string{
		"New contradictory evidence appears",
		"The review surface expands materially",
		"A consult returns a blocker or design contradiction",
	}

	if state.Contract != nil && state.Contract.Mode == agentShared.GlobalExecutionModeRecall {
		rank = 0
		reasons = []string{"this request is recall-oriented rather than adversarial validation"}
		reconsider = append(reconsider, "The request shifts from recall into audit or validation")
	}

	stage := strings.TrimSpace(fmt.Sprint(metadata["global_review_stage"]))
	switch stage {
	case "final":
		if rank < 2 {
			rank = 2
		}
		reasons = append(reasons, "this is the final whole-plan review stage")
	case "checkpoint":
		reasons = append(reasons, "this is a progressive checkpoint review, so future work may still be pending")
	}

	fileCount := auditDepthAffectedFileCount(metadata)
	switch {
	case fileCount >= 15:
		if rank < 4 {
			rank = 4
		}
		reasons = append(reasons, fmt.Sprintf("the review touches %d affected files, which is a broad integration surface", fileCount))
	case fileCount >= 6:
		if rank < 3 {
			rank = 3
		}
		reasons = append(reasons, fmt.Sprintf("the review touches %d affected files, which is larger than a narrow checkpoint", fileCount))
	case fileCount > 0:
		reasons = append(reasons, fmt.Sprintf("the current merged surface is %d affected file(s)", fileCount))
	}

	combinedText := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		state.Input,
		currentFocus,
		fmt.Sprint(metadata["current_request"]),
		fmt.Sprint(metadata["review_reason"]),
	}, "\n")))
	if containsAnyAuditDepthTerm(combinedText,
		"security", "race", "deadlock", "concurrency", "corruption",
		"performance", "latency", "throughput", "regression",
		"migration", "compatibility", "backward", "packaging", "versioning",
		"architecture", "cross-file", "whole-plan", "root cause",
	) {
		if rank < 3 {
			rank = 3
		}
		reasons = append(reasons, "the review request carries architectural or high-blast-radius risk signals")
	}
	if containsAnyAuditDepthTerm(combinedText, "comprehensive", "research-grade", "durable synthesis", "exhaustive") {
		if rank < 4 {
			rank = 4
		}
		reasons = append(reasons, "the request explicitly calls for unusually deep corroboration")
	}
	if containsAnyAuditDepthTerm(combinedText, "toy", "small", "single file", "zero-dependency", "stdlib-only", "boilerplate") && rank > 1 {
		rank--
		reasons = append(reasons, "the request also reads as a small or low-complexity surface")
	}

	depth := auditDepthRankToEnum(rank)
	return auditDepthDecision{
		Depth:              depth,
		Rationale:          strings.Join(uniqueTrimmedStrings(reasons), "; "),
		ReconsiderIf:       uniqueTrimmedStrings(reconsider),
		AssessmentGuidance: auditDepthAssessmentGuidance(depth),
	}
}

func appendAuditDepthGuidance(base string, ctx context.Context) string {
	contract, ok := agentShared.AuditDepthContractFromContext(ctx)
	if !ok {
		return base
	}
	lines := []string{
		"# Audit Depth Contract",
		"",
		fmt.Sprintf("- Branch audit depth: %s", contract.Depth),
		"- Use this depth as the default intensity for your own local assessment and for any knowledge consult that supports research depth.",
	}
	if rationale := strings.TrimSpace(contract.Rationale); rationale != "" {
		lines = append(lines, "- Rationale: "+rationale)
	}
	if guidance := strings.TrimSpace(auditDepthAssessmentGuidance(agentShared.EffectiveResearchDepth(contract.Depth))); guidance != "" {
		lines = append(lines, "- Assessment guidance: "+guidance)
	}
	if len(contract.ReconsiderIf) > 0 {
		lines = append(lines, "- Reconsider only if: "+strings.Join(contract.ReconsiderIf, "; "))
	}
	if strings.TrimSpace(base) == "" {
		return strings.Join(lines, "\n")
	}
	return strings.TrimSpace(base) + "\n\n---\n\n" + strings.Join(lines, "\n")
}

func auditDepthRequired(ctx context.Context) bool {
	_, ok := agentShared.AuditDepthContractFromContext(ctx)
	return !ok
}

func auditDepthGateViolation(resp *providers.Response) error {
	first := ""
	if resp != nil && len(resp.ToolCalls) > 0 {
		first = strings.TrimSpace(resp.ToolCalls[0].Name)
	}
	if first == "" {
		return fmt.Errorf("before any other global audit work, call `%s` first to choose one of: %s", determineAuditDepthToolName, strings.Join(agentShared.ResearchDepthEnumValues(), ", "))
	}
	return fmt.Errorf("before `%s`, call `%s` first to choose one of: %s", first, determineAuditDepthToolName, strings.Join(agentShared.ResearchDepthEnumValues(), ", "))
}

func auditDepthAffectedFileCount(metadata map[string]any) int {
	switch typed := metadata["affected_files"].(type) {
	case []string:
		return len(typed)
	case []any:
		return len(typed)
	default:
		return 0
	}
}

func containsAnyAuditDepthTerm(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(term))) {
			return true
		}
	}
	return false
}

func auditDepthRankToEnum(rank int) agentShared.ResearchDepth {
	switch {
	case rank <= 0:
		return agentShared.ResearchDepthMinimal
	case rank == 1:
		return agentShared.ResearchDepthQuick
	case rank == 2:
		return agentShared.ResearchDepthStandard
	case rank == 3:
		return agentShared.ResearchDepthDeep
	default:
		return agentShared.ResearchDepthComprehensive
	}
}

func auditDepthAssessmentGuidance(depth agentShared.ResearchDepth) string {
	switch depth {
	case agentShared.ResearchDepthMinimal:
		return "Keep the audit extremely narrow. Prefer direct file inspection and only escalate if a concrete authority gap blocks a safe verdict."
	case agentShared.ResearchDepthQuick:
		return "Favor the shortest evidence path to a safe verdict. Avoid broad corroboration unless a real contradiction or plan-level risk appears."
	case agentShared.ResearchDepthStandard:
		return "Run the normal global-inspector audit depth: enough corroboration to challenge weak choices without drifting into a research memo."
	case agentShared.ResearchDepthDeep:
		return "Broaden corroboration and compare stronger alternatives. This branch justifies more than the default checkpoint challenge."
	case agentShared.ResearchDepthComprehensive:
		return "Use a research-grade audit posture. Widen corroboration, probe strongest alternatives and counterevidence, and only stop once the branch is decision-grade."
	default:
		return ""
	}
}

func effectiveAuditResearchDepth(ctx context.Context, explicitDepth string) agentShared.ResearchDepth {
	if normalized := agentShared.NormalizeResearchDepth(explicitDepth); normalized != agentShared.ResearchDepthUnset {
		return normalized
	}
	if inherited := agentShared.EffectiveAuditDepthFromContext(ctx); inherited != agentShared.ResearchDepthUnset {
		return inherited
	}
	return agentShared.ResearchDepthStandard
}

func consultationMetadataWithAuditDepth(ctx context.Context, metadata map[string]any, explicitDepth string) map[string]any {
	depth := effectiveAuditResearchDepth(ctx, explicitDepth)
	metadata = agentShared.ConsultationMetadataWithResearchDepth(metadata, string(depth))
	if contract, ok := agentShared.AuditDepthContractFromContext(ctx); ok {
		metadata = agentShared.MetadataWithAuditDepthContract(metadata, contract)
	}
	return metadata
}

func uniqueTrimmedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

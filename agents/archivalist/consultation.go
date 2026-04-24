package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

var archivalistConsultTargets = map[string]string{
	"academic":  "External research, tradeoffs, and evidence-backed alternatives",
	"librarian": "Current codebase patterns, implementation reality, and repository context",
}

func consultSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("consult").
		Description("Consult another knowledge agent. Targets: academic (external research and tradeoffs), librarian (current codebase patterns and implementation reality).").
		Domain("consultation").
		Keywords("consult", "academic", "librarian", "research", "patterns", "context").
		Priority(85).
		EnumParam("target", "Agent to consult", []string{"academic", "librarian"}, true).
		StringParam("query", "Consultation question", true).
		StringParam("scope", "Optional scope for the consultation", false).
		EnumParam("depth", "Research depth for Academic consultations", shared.ResearchDepthEnumValues(), false).
		StringParam("session_id", "Session identifier", false).
		Usage("Use when Archivalist needs current codebase reality or external research that memory and history alone cannot resolve. Prefer targeted follow-up consults as the unresolved question changes. Consultation is synchronous — you receive the result before proceeding.").
		BestPractice("Consult only for the missing current-state or external question, not the whole problem statement. Prefer repeated targeted consults over one broad request.").
		BestPractice("When consulting Academic, re-evaluate depth each time: `minimal` for a fast plausibility check, `quick` for a narrow evidence-backed answer, `standard` for the default research consult, `deep` for broader corroboration on important design or policy questions, and `comprehensive` for high-stakes or reusable research artifacts.").
		BestPractice("Use `standard` as the default for Academic. Escalate to `deep` or `comprehensive` only when broader external evidence could materially change what history should preserve or recommend.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Target    string `json:"target"`
				Query     string `json:"query"`
				Scope     string `json:"scope"`
				Depth     string `json:"depth"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if _, ok := archivalistConsultTargets[params.Target]; !ok {
				return nil, fmt.Errorf("invalid target %q: must be academic or librarian", params.Target)
			}
			if strings.TrimSpace(params.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			metadata := map[string]any(nil)
			if strings.TrimSpace(params.Target) == "academic" {
				metadata = shared.ConsultationMetadataWithResearchDepth(nil, params.Depth)
			}
			a.archivalistPostClaim(ctx,
				claims.Action{AgentID: "archivalist", Type: claims.ActionTypeConsultation},
				archivalistConsultClaim(
					"Consult "+params.Target+": "+truncateArchivalist(params.Query, 60),
					"Knowledge consultation",
					params.Target,
					[]claims.ClaimScopeEntry{{Kind: "consultation", Key: params.Target}},
					[]*claims.Validation{
						archivalistValidation(claims.ValidationTypeReceipt, true, "Consultation succeeded", "evidence.Success == true"),
					},
				),
			)
			evidence, err := a.requestConsultationWithMetadata(ctx, params.Target, params.Query, params.Scope, params.SessionID, metadata)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"target":  params.Target,
				"success": evidence.Success,
				"data":    evidence.Data,
			}, nil
		}).
		Build()
}

func (a *Archivalist) requestConsultation(
	ctx context.Context,
	target, query, scope, sessionID string,
) (*shared.ConsultationEvidence, error) {
	return a.requestConsultationWithMetadata(ctx, target, query, scope, sessionID, nil)
}

func (a *Archivalist) requestConsultationWithMetadata(
	ctx context.Context,
	target, query, scope, sessionID string,
	metadata map[string]any,
) (*shared.ConsultationEvidence, error) {
	if a.bus == nil || !a.running {
		return archivalistFailedConsultEvidence(target, query, scope, "", fmt.Errorf("archivalist bus is unavailable")), fmt.Errorf("archivalist bus is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = versioning.SessionIDFromContext(ctx)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(a.defaultSessionID)
	}
	req := &guide.RouteRequest{
		Input:           strings.TrimSpace(query),
		TargetAgentID:   strings.TrimSpace(target),
		SourceAgentID:   strings.TrimSpace(a.id),
		SourceAgentName: "archivalist",
		SessionID:       sessionID,
		ExplicitTarget:  true,
		Metadata:        shared.CloneMetadataMap(metadata),
	}
	admission := shared.AdmitConsultation(ctx, target, query, req.Metadata)
	if !admission.Allowed {
		return archivalistFailedConsultEvidence(target, query, scope, "", shared.ConsultationAdmissionError(admission)), nil
	}
	req.Metadata = admission.Metadata
	response, err := shared.RequestGuideRouteSync(ctx, shared.GuideRouteSyncRequest{
		Bus:           a.bus,
		ResponseTopic: archivalistResponseTopic(a),
		Request:       req,
	})
	shared.RecordConsultationOutcome(ctx, admission.AttemptID, err == nil, shared.ConsultationDataFromMessage(response), err)
	if err != nil {
		return archivalistFailedConsultEvidence(target, query, scope, req.CorrelationID, err), err
	}
	return archivalistBuildConsultEvidence(target, query, scope, req.CorrelationID, response), nil
}

func archivalistResponseTopic(a *Archivalist) string {
	if a != nil && a.channels != nil && strings.TrimSpace(a.channels.Responses) != "" {
		return a.channels.Responses
	}
	agentID := "archivalist"
	if a != nil && strings.TrimSpace(a.id) != "" {
		agentID = strings.TrimSpace(a.id)
	}
	return guide.TopicResponses("archivalist", agentID)
}

func archivalistBuildConsultEvidence(
	target, query, scope, correlationID string,
	msg *guide.Message,
) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: correlationID,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if msg == nil {
		evidence.Success = false
		evidence.Error = "empty consultation response"
		return evidence
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		evidence.Success = resp.Success
		evidence.Data = resp.Data
		evidence.Error = resp.Error
		return evidence
	}
	if errStr, ok := msg.GetError(); ok {
		evidence.Success = false
		evidence.Error = errStr
		return evidence
	}
	evidence.Success = false
	evidence.Error = "unsupported consultation payload"
	return evidence
}

func archivalistFailedConsultEvidence(target, query, scope, corr string, err error) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: corr,
		Success:     false,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if err != nil {
		evidence.Error = err.Error()
	}
	return evidence
}

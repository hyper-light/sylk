package librarian

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

var librarianConsultTargets = map[string]string{
	"academic":    "External research, alternatives, and evidence-backed tradeoffs",
	"archivalist": "Historical context, prior failures, and earlier decisions",
}

func consultSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("consult").
		Description("Consult another knowledge agent. Targets: academic (external research and tradeoffs), archivalist (historical context and precedent).").
		Domain("consultation").
		Keywords("consult", "academic", "archivalist", "research", "history", "precedent").
		Priority(85).
		EnumParam("target", "Agent to consult", []string{"academic", "archivalist"}, true).
		StringParam("query", "Consultation question", true).
		StringParam("scope", "Optional scope for the consultation", false).
		EnumParam("depth", "Research depth for Academic consultations", shared.ResearchDepthEnumValues(), false).
		StringParam("session_id", "Session identifier", false).
		Usage("Use when Librarian needs outside evidence or historical context that the local codebase alone cannot provide. Consultation is synchronous — you receive the result before proceeding.").
		BestPractice("When consulting Academic, choose depth deliberately: `minimal` for a fast plausibility check, `quick` for a narrow evidence-backed answer, `standard` for the default research consult, `deep` for broader corroboration on important design choices, and `comprehensive` for high-stakes or reusable research artifacts.").
		BestPractice("Do not request `comprehensive` depth for ordinary codebase-fit checks; reserve it for questions where stronger external evidence could materially change the conclusion.").
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
			if _, ok := librarianConsultTargets[params.Target]; !ok {
				return nil, fmt.Errorf("invalid target %q: must be academic or archivalist", params.Target)
			}
			if strings.TrimSpace(params.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			metadata := map[string]any(nil)
			if strings.TrimSpace(params.Target) == "academic" {
				metadata = shared.ConsultationMetadataWithResearchDepth(nil, params.Depth)
			}
			evidence, err := l.requestConsultationWithMetadata(ctx, params.Target, params.Query, params.Scope, params.SessionID, metadata)
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

func (l *Librarian) requestConsultation(
	ctx context.Context,
	target, query, scope, sessionID string,
) (*shared.ConsultationEvidence, error) {
	return l.requestConsultationWithMetadata(ctx, target, query, scope, sessionID, nil)
}

func (l *Librarian) requestConsultationWithMetadata(
	ctx context.Context,
	target, query, scope, sessionID string,
	metadata map[string]any,
) (*shared.ConsultationEvidence, error) {
	if l.bus == nil || !l.running {
		return failedConsultEvidence(target, query, scope, "", fmt.Errorf("librarian bus is unavailable")), fmt.Errorf("librarian bus is unavailable")
	}
	if !l.isAgentRegistered(target) {
		err := fmt.Errorf("agent %q is not registered", target)
		return failedConsultEvidence(target, query, scope, "", err), err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = versioning.SessionIDFromContext(ctx)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(l.config.SessionID)
	}
	req := &guide.RouteRequest{
		Input:           strings.TrimSpace(query),
		TargetAgentID:   strings.TrimSpace(target),
		SourceAgentID:   strings.TrimSpace(l.id),
		SourceAgentName: "librarian",
		SessionID:       sessionID,
		ExplicitTarget:  true,
		Metadata:        shared.CloneMetadataMap(metadata),
	}
	response, err := shared.RequestGuideRouteSync(ctx, shared.GuideRouteSyncRequest{
		Bus:           l.bus,
		ResponseTopic: librarianResponseTopic(l),
		Request:       req,
	})
	if err != nil {
		return failedConsultEvidence(target, query, scope, req.CorrelationID, err), err
	}
	return buildConsultEvidence(target, query, scope, req.CorrelationID, response), nil
}

func librarianResponseTopic(l *Librarian) string {
	if l != nil && l.channels != nil && strings.TrimSpace(l.channels.Responses) != "" {
		return l.channels.Responses
	}
	agentID := "librarian"
	if l != nil && strings.TrimSpace(l.id) != "" {
		agentID = strings.TrimSpace(l.id)
	}
	return guide.TopicResponses("librarian", agentID)
}

func (l *Librarian) isAgentRegistered(target string) bool {
	normalized := strings.ToLower(strings.TrimSpace(target))
	if normalized == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ann := range l.knownAgents {
		if ann == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(ann.AgentID), normalized) || strings.EqualFold(strings.TrimSpace(ann.AgentType), normalized) {
			return true
		}
	}
	return false
}

func buildConsultEvidence(
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

func failedConsultEvidence(target, query, scope, corr string, err error) *shared.ConsultationEvidence {
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

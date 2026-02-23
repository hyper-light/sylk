package architect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (a *Architect) enforceConsultationGate(
	ctx context.Context,
	plan *DesignPlan,
	req *ArchitectRequest,
) error {
	if !a.config.MandatoryConsultation {
		return nil
	}
	if a.bus == nil || !a.running {
		appendRiskSummary(plan, "consultation warning: architect bus unavailable; continuing without mandatory consultation results")
		return nil
	}
	return a.collectMandatoryConsultations(ctx, plan, req)
}

func (a *Architect) collectMandatoryConsultations(
	ctx context.Context,
	plan *DesignPlan,
	req *ArchitectRequest,
) error {
	targets := mandatoryConsultationTargets(req)
	for _, target := range targets {
		if err := a.captureConsultationResult(ctx, plan, req, target); err != nil {
			return err
		}
	}
	return nil
}

func (a *Architect) captureConsultationResult(
	ctx context.Context,
	plan *DesignPlan,
	req *ArchitectRequest,
	target string,
) error {
	evidence, err := a.runConsultation(ctx, target, req, plan)
	if shouldAbortConsultationGate(ctx, err) {
		return ctx.Err()
	}
	recordConsultationEvidence(plan, target, ensureConsultationEvidence(target, req, plan, evidence, err))
	if shouldWarnConsultation(err, evidence) {
		appendRiskSummary(plan, consultationWarningMessage(target, evidence, err))
	}
	return nil
}

func shouldAbortConsultationGate(ctx context.Context, err error) bool {
	if ctx == nil || err == nil {
		return false
	}
	if ctx.Err() == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func shouldWarnConsultation(err error, evidence *ConsultationEvidence) bool {
	if err != nil {
		return true
	}
	if evidence == nil {
		return true
	}
	return !evidence.Success
}

func ensureConsultationEvidence(
	target string,
	req *ArchitectRequest,
	plan *DesignPlan,
	evidence *ConsultationEvidence,
	err error,
) *ConsultationEvidence {
	if evidence != nil {
		return evidence
	}
	scope := planScope(plan)
	query := consultationPrompt(target, requestQuery(req), scope)
	return failedConsultation(target, query, scope, "", err)
}

func requestQuery(req *ArchitectRequest) string {
	if req == nil {
		return ""
	}
	return req.Query
}

func recordConsultationEvidence(plan *DesignPlan, target string, evidence *ConsultationEvidence) {
	if plan == nil || target == "" {
		return
	}
	if plan.Consultations == nil {
		plan.Consultations = map[string]*ConsultationEvidence{}
	}
	plan.Consultations[target] = evidence
}

func appendRiskSummary(plan *DesignPlan, warning string) {
	if plan == nil {
		return
	}
	trimmed := strings.TrimSpace(warning)
	if trimmed == "" {
		return
	}
	plan.RiskSummary = append(plan.RiskSummary, trimmed)
}

func consultationWarningMessage(target string, evidence *ConsultationEvidence, err error) string {
	if err != nil {
		return fmt.Sprintf("consultation warning (%s): %s", target, err.Error())
	}
	if evidence == nil {
		return fmt.Sprintf("consultation warning (%s): no consultation evidence returned", target)
	}
	if strings.TrimSpace(evidence.Error) != "" {
		return fmt.Sprintf("consultation warning (%s): %s", target, strings.TrimSpace(evidence.Error))
	}
	return fmt.Sprintf("consultation warning (%s): consultation unsuccessful", target)
}

func mandatoryConsultationTargets(req *ArchitectRequest) []string {
	targets := []string{"librarian", "archivalist"}
	if shouldConsultAcademic(req) {
		targets = append(targets, "academic")
	}
	return targets
}

func shouldConsultAcademic(req *ArchitectRequest) bool {
	if req == nil {
		return false
	}
	if req.Params != nil {
		if value, ok := req.Params["include_academic"].(bool); ok {
			return value
		}
	}
	return containsAny(req.Query, []string{
		"best practice",
		"research",
		"benchmark",
		"tradeoff",
		"paper",
	})
}

func containsAny(input string, terms []string) bool {
	lowered := strings.ToLower(input)
	for _, term := range terms {
		if strings.Contains(lowered, term) {
			return true
		}
	}
	return false
}

func (a *Architect) runConsultation(
	ctx context.Context,
	target string,
	req *ArchitectRequest,
	plan *DesignPlan,
) (*ConsultationEvidence, error) {
	scope := planScope(plan)
	query := consultationPrompt(target, req.Query, scope)
	consultCtx, cancel := context.WithTimeout(ctx, a.config.ConsultationTimeout)
	defer cancel()
	evidence, err := a.requestConsultation(consultCtx, target, query, scope, req.SessionID)
	if evidence != nil && evidence.RequestedAt.IsZero() {
		evidence.RequestedAt = time.Now()
	}
	if evidence != nil && evidence.ReceivedAt.IsZero() {
		evidence.ReceivedAt = time.Now()
	}
	return evidence, err
}

func planScope(plan *DesignPlan) string {
	if plan == nil || plan.Constraints == nil {
		return ""
	}
	return plan.Constraints.Scope
}

func consultationPrompt(target string, query string, scope string) string {
	if scope == "" {
		return fmt.Sprintf("Consultation for %s: %s", target, query)
	}
	return fmt.Sprintf("Consultation for %s (scope: %s): %s", target, scope, query)
}

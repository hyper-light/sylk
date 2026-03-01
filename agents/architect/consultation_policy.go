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
	a.logInfo("enforceConsultationGate: entry",
		"mandatory", a.config.MandatoryConsultation,
		"bus_available", a.bus != nil && a.running,
		"ctx_deadline", contextDeadlineString(ctx))
	if !a.config.MandatoryConsultation {
		a.logInfo("enforceConsultationGate: skipped (not mandatory)")
		return nil
	}
	if a.bus == nil || !a.running {
		a.logWarn("enforceConsultationGate: bus unavailable, skipping consultations")
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
	allTargets := mandatoryConsultationTargets(req)
	targets := a.filterRegisteredTargets(allTargets, plan)
	a.logInfo("collectMandatoryConsultations: starting",
		"requested", strings.Join(allTargets, ","),
		"registered", strings.Join(targets, ","),
		"skipped", len(allTargets)-len(targets),
		"ctx_deadline", contextDeadlineString(ctx))
	for i, target := range targets {
		a.logInfo("collectMandatoryConsultations: consulting",
			"target", target,
			"index", i,
			"total", len(targets),
			"ctx_deadline", contextDeadlineString(ctx))
		start := time.Now()
		if err := a.captureConsultationResult(ctx, plan, req, target); err != nil {
			a.logWarn("collectMandatoryConsultations: consultation aborted",
				"target", target,
				"elapsed", time.Since(start).String(),
				"err", err)
			return err
		}
		a.logInfo("collectMandatoryConsultations: consultation complete",
			"target", target,
			"elapsed", time.Since(start).String())
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
		a.logWarn("captureConsultationResult: context cancelled during consultation",
			"target", target,
			"ctx_err", ctx.Err())
		return ctx.Err()
	}
	recordConsultationEvidence(plan, target, ensureConsultationEvidence(target, req, plan, evidence, err))
	if shouldWarnConsultation(err, evidence) {
		warning := consultationWarningMessage(target, evidence, err)
		a.logInfo("captureConsultationResult: consultation warning",
			"target", target,
			"warning", warning)
		appendRiskSummary(plan, warning)
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

// filterRegisteredTargets returns only those targets that are currently
// registered on the bus. Unregistered targets are recorded as skipped
// consultations with a risk warning so the plan reflects reduced coverage.
func (a *Architect) filterRegisteredTargets(targets []string, plan *DesignPlan) []string {
	registered := make([]string, 0, len(targets))
	for _, target := range targets {
		if a.isAgentRegistered(target) {
			registered = append(registered, target)
			continue
		}
		a.logInfo("filterRegisteredTargets: skipping unregistered agent",
			"target", target)
		appendRiskSummary(plan, fmt.Sprintf("consultation skipped (%s): agent not registered", target))
	}
	return registered
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
	a.logInfo("runConsultation: creating timeout context",
		"target", target,
		"consultation_timeout", a.config.ConsultationTimeout.String(),
		"parent_ctx_deadline", contextDeadlineString(ctx))
	consultCtx, cancel := context.WithTimeout(ctx, a.config.ConsultationTimeout)
	defer cancel()
	a.logInfo("runConsultation: calling requestConsultation",
		"target", target,
		"consult_ctx_deadline", contextDeadlineString(consultCtx))
	start := time.Now()
	evidence, err := a.requestConsultation(consultCtx, target, query, scope, req.SessionID)
	a.logInfo("runConsultation: requestConsultation returned",
		"target", target,
		"elapsed", time.Since(start).String(),
		"success", evidence != nil && evidence.Success,
		"err", err)
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

package architect

import (
	"context"
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
		return fmt.Errorf("mandatory consultation requires architect bus connectivity")
	}
	targets := mandatoryConsultationTargets(req)
	for _, target := range targets {
		evidence, err := a.runConsultation(ctx, target, req, plan)
		if plan.Consultations == nil {
			plan.Consultations = map[string]*ConsultationEvidence{}
		}
		plan.Consultations[target] = evidence
		if err != nil {
			return fmt.Errorf("mandatory consultation failed for %s: %w", target, err)
		}
		if !evidence.Success {
			return fmt.Errorf("mandatory consultation failed for %s: %s", target, evidence.Error)
		}
	}
	return nil
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


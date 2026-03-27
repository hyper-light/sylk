package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/versioning"
)

func newAcademicFetchConsentGate() *fetch.ConsentGate {
	return fetch.NewConsentGate(fetch.ConsentGateConfig{
		Callback: func(ctx context.Context, proposal *fetch.FetchProposal) (*fetch.ConsentResult, error) {
			return requestAcademicFetchConsent(ctx, proposal)
		},
	})
}

func requestAcademicFetchConsent(ctx context.Context, proposal *fetch.FetchProposal) (*fetch.ConsentResult, error) {
	if proposal == nil {
		return nil, fmt.Errorf("fetch proposal is required")
	}
	gate := commandapproval.GateFromContext(ctx)
	if gate == nil {
		return nil, fmt.Errorf("guardian approval flow is not configured for external fetch")
	}

	req := commandapproval.Request{
		Command:        strings.TrimSpace(proposal.URL),
		ToolName:       firstNonEmptyFetchToolName(proposal.ToolName),
		Domain:         strings.TrimSpace(proposal.Domain),
		Justification:  strings.TrimSpace(proposal.Reason),
		SessionID:      versioning.SessionIDFromContext(ctx),
		ApprovalPolicy: commandapproval.ApprovalPolicyExact,
	}
	shared.PopulateCommandApprovalScope(ctx, &req)

	// Academic fetches should use the same Guardian command-approval path as
	// command-executing agents. The shared gate owns the route, keepalive, and
	// completion semantics; this fetch adapter only shapes the request and
	// translates explicit denials into a rejected consent result.
	eval, err := commandapproval.Authorize(ctx, commandapproval.NewEvaluator(nil), req)
	if err != nil {
		if !errors.Is(err, commandapproval.ErrApprovalDenied) {
			return nil, err
		}
	}
	if eval.Decision != commandapproval.DecisionAllow {
		return &fetch.ConsentResult{
			Granted: false,
			Reason:  firstNonEmptyFetchConsentReason(eval.Reason, "fetch approval denied"),
		}, nil
	}

	result := &fetch.ConsentResult{
		Granted:     true,
		Reason:      strings.TrimSpace(eval.Reason),
		GrantDomain: strings.TrimSpace(req.Domain),
	}
	switch strings.TrimSpace(eval.UserDecision) {
	case "allow_always":
		result.GrantScope = "domain"
	case "allow_once":
		result.GrantScope = "once"
	default:
		switch eval.Source {
		case commandapproval.MatchSourceStoredAllow, commandapproval.MatchSourceBuiltinAllow:
			result.GrantScope = "domain"
		default:
			result.GrantScope = "once"
		}
	}
	return result, nil
}

func firstNonEmptyFetchToolName(toolName string) string {
	if trimmed := strings.TrimSpace(toolName); trimmed != "" {
		return trimmed
	}
	return "web_fetch"
}

func firstNonEmptyFetchConsentReason(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

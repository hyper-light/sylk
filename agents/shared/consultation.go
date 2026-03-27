package shared

import (
	"context"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/deadlinelease"
)

// DefaultConsultationTimeout is the per-attempt synchronous consultation
// timeout shared by all agents while waiting for another agent via the event
// bus.
const DefaultConsultationTimeout = 60 * time.Second

// DefaultResearchConsultationTimeout extends the inactivity budget for
// research-heavy consultations like Academic, which may need to search,
// fetch, and synthesize external sources before responding.
const DefaultResearchConsultationTimeout = 3 * DefaultConsultationTimeout

// DefaultConsultationLeaseRefreshes bounds how many times a synchronous bus
// wait can renew its child deadline while the parent operation still has
// budget.
const DefaultConsultationLeaseRefreshes = 2

// DefaultContextLeaseDeadlineGuard is the minimum remaining parent budget
// needed to justify one more renewed child attempt.
const DefaultContextLeaseDeadlineGuard = deadlinelease.DefaultDeadlineGuard

type ContextLeaseConfig = deadlinelease.Config
type ContextLeaseRefresh = deadlinelease.Refresh
type ContextLeaseTimeoutError = deadlinelease.TimeoutError

func RunWithContextLease(
	parent context.Context,
	cfg ContextLeaseConfig,
	run func(context.Context) error,
) error {
	return deadlinelease.Run(parent, cfg, run)
}

func WrapLeaseTimeoutError(subject string, fallback time.Duration, err error) error {
	return deadlinelease.WrapTimeoutError(subject, fallback, err)
}

func IsContextLeaseError(attemptErr error, err error) bool {
	return deadlinelease.IsRefreshableDeadline(attemptErr, err)
}

func HasContextLeaseBudget(ctx context.Context, guard time.Duration) bool {
	return deadlinelease.HasBudget(ctx, guard)
}

// ConsultationInactivityTimeout returns the default inactivity timeout for a
// synchronous consultation target. Research-oriented agents need a larger
// budget than lightweight local knowledge lookups.
func ConsultationInactivityTimeout(target string) time.Duration {
	switch normalizeConsultationTarget(target) {
	case "academic":
		return DefaultResearchConsultationTimeout
	default:
		return DefaultConsultationTimeout
	}
}

func normalizeConsultationTarget(target string) string {
	trimmed := strings.ToLower(strings.TrimSpace(target))
	if trimmed == "" {
		return ""
	}
	if idx := strings.LastIndex(trimmed, ":"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	return trimmed
}

// ConsultationEvidence records the result of a cross-agent consultation request.
// It captures the query sent to a target agent, the response data, and timing
// information for observability and correlation tracking.
type ConsultationEvidence struct {
	Target      string    `json:"target"`
	Query       string    `json:"query"`
	Scope       string    `json:"scope"`
	Correlation string    `json:"correlation"`
	Success     bool      `json:"success"`
	Data        any       `json:"data,omitempty"`
	Error       string    `json:"error,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
	ReceivedAt  time.Time `json:"received_at"`
}

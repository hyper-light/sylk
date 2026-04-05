package shared

import (
	"context"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/deadlinelease"
)

// DefaultConsultationTimeout is the per-attempt synchronous consultation
// timeout shared by all agents while waiting for another agent via the event
// bus.
const DefaultConsultationTimeout = 60 * time.Second

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

// ConsultationInactivityTimeout returns the silence budget for a synchronous
// consultation before the child route has started. Once the child starts
// streaming, the wait path defers lifetime to the parent context instead of
// treating long quiet thinking periods as failure.
func ConsultationInactivityTimeout(string) time.Duration {
	return DefaultConsultationTimeout
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

func ConsultationDataFromMessage(msg *guide.Message) any {
	if msg == nil {
		return nil
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		return resp.Data
	}
	if errText, ok := msg.GetError(); ok && strings.TrimSpace(errText) != "" {
		return errText
	}
	return nil
}

package handoff

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
)

const transportRetryHandoffTimeout = 30 * time.Second

// ScribeFeedSink is the minimal interface needed to preserve in-flight work in
// an agent-side scribe before a forced handoff.
type ScribeFeedSink interface {
	FeedScribe(parentAgentType, userRequest, agentResponse, parentCorrelationID string)
}

// TransportRetryBridge is the minimal handoff surface required by the
// transport-retry recovery helper.
type TransportRetryBridge interface {
	PreserveInFlightWork(rec TurnRecord, messages ...Message)
	ForceHandoff(ctx context.Context, reason string) error
}

// TransportRetryHandoffConfig describes the request-scoped state needed to
// force a fresh agent handoff when transport retries are exhausted.
type TransportRetryHandoffConfig struct {
	Enabled       bool
	Bridge        TransportRetryBridge
	AgentID       string
	AgentType     string
	UserRequest   string
	CorrelationID string
	SessionID     string
	EventLogger   *agentlog.SessionEventLogger
	Scribe        ScribeFeedSink
}

type transportRetryHandoffState struct {
	cfg       TransportRetryHandoffConfig
	triggered atomic.Bool
}

// WithTransportRetryHandoff registers a request-scoped provider callback that
// preserves the active work and forces a fresh agent handoff when transient
// transport retries are exhausted.
func WithTransportRetryHandoff(ctx context.Context, cfg TransportRetryHandoffConfig) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !cfg.Enabled || cfg.Bridge == nil || strings.TrimSpace(cfg.AgentType) == "" {
		return ctx
	}

	cfg.AgentID = strings.TrimSpace(cfg.AgentID)
	cfg.AgentType = strings.TrimSpace(cfg.AgentType)
	cfg.UserRequest = strings.TrimSpace(cfg.UserRequest)
	cfg.CorrelationID = strings.TrimSpace(cfg.CorrelationID)
	cfg.SessionID = strings.TrimSpace(cfg.SessionID)

	state := &transportRetryHandoffState{cfg: cfg}
	existing := providers.RetryExhaustedObserverFromContext(ctx)
	return providers.WithRetryExhaustedObserver(ctx, func(event providers.RetryExhaustedEvent) {
		if existing != nil {
			existing(event)
		}
		state.handle(ctx, event)
	})
}

func (s *transportRetryHandoffState) handle(ctx context.Context, event providers.RetryExhaustedEvent) {
	if s == nil || s.cfg.Bridge == nil || !event.Transport || event.RetriesUsed <= 0 {
		return
	}
	if !s.triggered.CompareAndSwap(false, true) {
		return
	}

	requestText := transportRetryRequestSummary(s.cfg)
	responseText := transportRetryFailureSummary(event)
	reason := transportRetryHandoffReason(event)

	if s.cfg.Scribe != nil {
		s.cfg.Scribe.FeedScribe(s.cfg.AgentType, requestText, responseText, s.cfg.CorrelationID)
	}

	s.cfg.Bridge.PreserveInFlightWork(TurnRecord{
		CorrelationID: s.cfg.CorrelationID,
		SessionID:     s.cfg.SessionID,
		EventLogger:   s.cfg.EventLogger,
	}, NewMessage("user", requestText), NewMessage("assistant", responseText))

	handoffCtx := context.Background()
	if ctx != nil {
		handoffCtx = context.WithoutCancel(ctx)
	}
	handoffCtx, cancel := context.WithTimeout(handoffCtx, transportRetryHandoffTimeout)
	defer cancel()

	if err := s.cfg.Bridge.ForceHandoff(handoffCtx, reason); err != nil {
		s.triggered.Store(false)
		s.logFailure(reason, err, event)
		return
	}

	s.logSuccess(reason, event)
}

func (s *transportRetryHandoffState) logSuccess(reason string, event providers.RetryExhaustedEvent) {
	if s == nil || s.cfg.EventLogger == nil {
		return
	}
	s.cfg.EventLogger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "info",
		Agent:     transportRetryAgentName(s.cfg),
		SessionID: s.cfg.SessionID,
		Event:     "transport_retry_handoff_triggered",
		EventCode: agentlog.EventHandoffTriggered,
		CorrID:    s.cfg.CorrelationID,
		Data: map[string]any{
			"agent_type":     s.cfg.AgentType,
			"retries_used":   event.RetriesUsed,
			"total_attempts": event.TotalAttempts,
			"reason":         reason,
		},
	})
}

func (s *transportRetryHandoffState) logFailure(reason string, err error, event providers.RetryExhaustedEvent) {
	if s == nil || s.cfg.EventLogger == nil || err == nil {
		return
	}
	s.cfg.EventLogger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "error",
		Agent:     transportRetryAgentName(s.cfg),
		SessionID: s.cfg.SessionID,
		Event:     "transport_retry_handoff_failed",
		EventCode: agentlog.EventError,
		CorrID:    s.cfg.CorrelationID,
		Error:     err.Error(),
		Data: map[string]any{
			"agent_type":     s.cfg.AgentType,
			"retries_used":   event.RetriesUsed,
			"total_attempts": event.TotalAttempts,
			"reason":         reason,
		},
	})
}

func transportRetryAgentName(cfg TransportRetryHandoffConfig) string {
	if cfg.AgentID != "" {
		return cfg.AgentID
	}
	return cfg.AgentType
}

func transportRetryRequestSummary(cfg TransportRetryHandoffConfig) string {
	if cfg.UserRequest != "" {
		return compactTransportRetryText(cfg.UserRequest, 4000)
	}
	return fmt.Sprintf("Resume the in-flight %s request that failed during provider transport recovery.", cfg.AgentType)
}

func transportRetryFailureSummary(event providers.RetryExhaustedEvent) string {
	return compactTransportRetryText(fmt.Sprintf(
		"Provider transport retries were exhausted after %d attempts (%d retries). Last error: %s. Continue from this preserved work context on a fresh instance.",
		event.TotalAttempts,
		event.RetriesUsed,
		strings.TrimSpace(event.Err.Error()),
	), 4000)
}

func transportRetryHandoffReason(event providers.RetryExhaustedEvent) string {
	return compactTransportRetryText(fmt.Sprintf(
		"provider transport retries exhausted after %d attempts: %s",
		event.TotalAttempts,
		strings.TrimSpace(event.Err.Error()),
	), 512)
}

func compactTransportRetryText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

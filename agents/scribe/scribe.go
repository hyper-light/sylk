package scribe

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/google/uuid"
)

// scribeProvider is the minimal LLM interface the Scribe needs.
type scribeProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// Config configures a Scribe sidecar.
type Config struct {
	ParentAgentType string
	Provider        scribeProvider
	Model           string
	Bus             guide.EventBus
	Scope           *concurrency.GoroutineScope
	Logger          *slog.Logger
	MaxMessages     int // 0 = default (50 turn pairs)
}

// defaultMaxMessages is the default ring buffer capacity in turn pairs.
const defaultMaxMessages = 50

// feedBufferSize is the bounded feed channel capacity.
const feedBufferSize = 32

// Scribe is a per-agent summarization sidecar that maintains running
// commentary on a parent agent's work and forwards it to the Archivalist
// for long-term memory.
type Scribe struct {
	id              string
	parentAgentType string
	provider        scribeProvider
	model           string
	bus             guide.EventBus
	channels        *guide.AgentChannels
	logger          *slog.Logger
	scope           *concurrency.GoroutineScope

	// Conversation state: running context for the Scribe's LLM.
	messagesMu  sync.RWMutex
	messages    []providers.Message
	maxMessages int

	// Handoff bridge (GP-based quality detection).
	handoffBridge *handoff.HandoffBridge

	// Bus subscriptions.
	requestSub  guide.Subscription
	responseSub guide.Subscription

	// Lifecycle.
	runCtx    context.Context
	runCancel context.CancelFunc
	running   atomic.Bool

	// Feed channel: bounded, non-blocking ingest from parent agent.
	feedCh chan shared.ScribeFeed
}

// compile-time assertion
var _ shared.Scribe = (*Scribe)(nil)

// New creates a Scribe for the given parent agent type.
func New(cfg Config) *Scribe {
	id := fmt.Sprintf("scribe-%s-%s", cfg.ParentAgentType, uuid.New().String()[:8])
	maxMsgs := cfg.MaxMessages
	if maxMsgs == 0 {
		maxMsgs = defaultMaxMessages
	}
	return &Scribe{
		id:              id,
		parentAgentType: cfg.ParentAgentType,
		provider:        cfg.Provider,
		model:           cfg.Model,
		bus:             cfg.Bus,
		logger:          cfg.Logger,
		scope:           cfg.Scope,
		maxMessages:     maxMsgs,
		messages:        make([]providers.Message, 0, maxMsgs*2),
		feedCh:          make(chan shared.ScribeFeed, feedBufferSize),
	}
}

// Start begins the Scribe's feed processing loop and bus subscriptions.
func (s *Scribe) Start() error {
	if s.running.Swap(true) {
		return nil
	}
	s.runCtx, s.runCancel = context.WithCancel(context.Background())
	s.channels = guide.NewAgentChannels("scribe-"+s.parentAgentType, s.id)

	var err error
	s.requestSub, err = s.bus.SubscribeAsync(s.channels.Requests, s.handleBusRequest)
	if err != nil {
		s.running.Store(false)
		return fmt.Errorf("scribe %s: subscribe requests: %w", s.id, err)
	}
	s.responseSub, err = s.bus.SubscribeAsync(s.channels.Responses, s.handleBusResponse)
	if err != nil {
		s.requestSub.Unsubscribe()
		s.running.Store(false)
		return fmt.Errorf("scribe %s: subscribe responses: %w", s.id, err)
	}

	_ = s.scope.Go("scribe-"+s.parentAgentType+"-feed", 0, func(ctx context.Context) error {
		s.feedLoop(ctx)
		return nil
	})

	return nil
}

// Stop shuts down the Scribe, closing the feed channel and unsubscribing.
func (s *Scribe) Stop() error {
	if !s.running.Swap(false) {
		return nil
	}
	s.runCancel()
	close(s.feedCh)
	if s.requestSub != nil {
		s.requestSub.Unsubscribe()
	}
	if s.responseSub != nil {
		s.responseSub.Unsubscribe()
	}
	return nil
}

// Feed sends turn data from the parent agent. Non-blocking; drops the
// oldest entry if the buffer is full.
func (s *Scribe) Feed(feed shared.ScribeFeed) {
	select {
	case s.feedCh <- feed:
	default:
		// Buffer full — drain one and retry.
		select {
		case <-s.feedCh:
		default:
		}
		select {
		case s.feedCh <- feed:
		default:
		}
	}
}

// feedLoop drains the feed channel and processes each turn.
func (s *Scribe) feedLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case feed, ok := <-s.feedCh:
			if !ok {
				return
			}
			s.processFeed(ctx, feed)
		}
	}
}

// processFeed appends messages, generates commentary, and forwards to
// the Archivalist.
func (s *Scribe) processFeed(ctx context.Context, feed shared.ScribeFeed) {
	s.appendMessage(providers.RoleUser,
		fmt.Sprintf("[%s request] %s", s.parentAgentType, feed.UserRequest))
	s.appendMessage(providers.RoleAssistant,
		fmt.Sprintf("[%s response] %s", s.parentAgentType, feed.AgentResponse))

	commentary, err := s.generateCommentary(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("scribe commentary generation failed",
				"parent", s.parentAgentType, "error", err)
		}
		return
	}

	s.forwardToArchivalist(commentary, feed.CorrelationID)
}

// scribeSystemPrompt is the system prompt template for commentary generation.
const scribeSystemPrompt = `You are a Scribe — a silent observer maintaining running notes on a %s agent's work. Your output goes to the Archivalist for long-term memory.

For each turn, provide a concise JSON commentary:
{
  "summary": "What happened this turn",
  "progress": "Overall task progress",
  "decisions": "Key decisions or trade-offs made",
  "state": "Current working state (files, variables, blockers)",
  "risk": "Any risks, regressions, or concerns"
}

Be specific, factual, and concise. No opinions or suggestions — just observational notes.`

// generateCommentary calls the LLM to produce commentary on recent turns.
func (s *Scribe) generateCommentary(ctx context.Context) (string, error) {
	s.messagesMu.RLock()
	msgs := make([]providers.Message, len(s.messages))
	copy(msgs, s.messages)
	s.messagesMu.RUnlock()

	req := &providers.Request{
		Model:        s.model,
		MaxTokens:    512,
		SystemPrompt: fmt.Sprintf(scribeSystemPrompt, s.parentAgentType),
		Messages:     msgs,
	}
	s.applyCommentaryRuntimeProfile(req)

	resp, err := s.provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("scribe %s: commentary: %w", s.id, err)
	}
	return strings.TrimSpace(resp.Content), nil
}

// forwardToArchivalist sends commentary to the Archivalist via bus
// using fire-and-forget semantics.
func (s *Scribe) forwardToArchivalist(commentary, correlationID string) {
	msgID := fmt.Sprintf("scribe_%s_msg_%s", s.parentAgentType, uuid.New().String()[:8])
	msg := guide.NewForwardMessage(msgID, &guide.ForwardedRequest{
		CorrelationID: correlationID,
		SourceAgentID: s.id,
		Input:         commentary,
		Intent:        guide.IntentStore,
		Metadata: map[string]any{
			"source_type":  "scribe",
			"parent_agent": s.parentAgentType,
			"scribe_id":    s.id,
		},
		FireAndForget: true,
	})
	_ = s.bus.Publish(guide.TopicRequests("archivalist", "archivalist"), msg)
}

// appendMessage adds a message to the ring buffer, evicting the oldest
// pair when capacity is exceeded.
func (s *Scribe) appendMessage(role providers.Role, content string) {
	s.messagesMu.Lock()
	defer s.messagesMu.Unlock()

	s.messages = append(s.messages, providers.Message{
		Role:    role,
		Content: content,
	})

	if len(s.messages) > s.maxMessages*2 {
		s.messages = s.messages[2:]
	}
}

// handleBusRequest processes incoming bus messages (bus subscription handler).
func (s *Scribe) handleBusRequest(_ *guide.Message) error {
	return nil
}

// handleBusResponse processes response bus messages (bus subscription handler).
func (s *Scribe) handleBusResponse(_ *guide.Message) error {
	return nil
}

// --------------------------------------------------------------------------
// HandoffableAgent implementation
// --------------------------------------------------------------------------

// AgentID returns the Scribe's unique identifier.
func (s *Scribe) AgentID() string { return s.id }

// AgentType returns the Scribe's agent type.
func (s *Scribe) AgentType() string { return "scribe-" + s.parentAgentType }

// Descriptor returns the Scribe's agent descriptor for handoff.
func (s *Scribe) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:     "scribe-" + s.parentAgentType,
		ModelID:       s.model,
		ContextWindow: 1_000_000, // Gemini 3 Flash context window
		Category:      handoff.CategoryStandalone,
	}
}

// ExtractArchivableState returns the Scribe's archivable state.
func (s *Scribe) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   s.id,
		AgentType: "scribe-" + s.parentAgentType,
		Timestamp: time.Now(),
	}
}

// Terminate stops the Scribe.
func (s *Scribe) Terminate(_ context.Context) error { return s.Stop() }

// SetHandoffBridge sets the handoff bridge for GP-based quality detection.
func (s *Scribe) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	s.handoffBridge = bridge
}

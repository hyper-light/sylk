package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/planapproval"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	proposalBridgeName   = "bridge.proposals"
	proposalEventBuffer  = 64
	proposalDrainTimeout = 0
)

// ProposalBridge forwards Guardian approval proposals from the bus to the
// Bubble Tea app. The AppModel already knows how to render command and plan
// approvals; this bridge is the production delivery path from
// responses.tui.tui to those typed UI messages.
type ProposalBridge struct {
	id          string
	scope       *concurrency.GoroutineScope
	bus         guide.EventBus
	proposals   chan any
	dropped     atomic.Int64
	done        chan struct{}
	stopOnce    sync.Once
	proposalSub guide.Subscription
}

func NewProposalBridge(id string, bus guide.EventBus, scope *concurrency.GoroutineScope) *ProposalBridge {
	if id == "" {
		id = proposalBridgeName
	}
	return &ProposalBridge{
		id:        id,
		bus:       bus,
		scope:     scope,
		proposals: make(chan any, proposalEventBuffer),
		done:      make(chan struct{}),
	}
}

func (b *ProposalBridge) Start(program TeaProgram) error {
	if b.bus != nil {
		sub, err := b.bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), b.onBusMessage)
		if err != nil {
			return err
		}
		b.proposalSub = sub
	}
	return b.scope.Go(proposalBridgeName, proposalDrainTimeout, b.drainFunc(program))
}

func (b *ProposalBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.proposalSub != nil {
			_ = b.proposalSub.Unsubscribe()
		}
		close(b.done)
	})
}

func (b *ProposalBridge) Name() string { return proposalBridgeName }

func (b *ProposalBridge) DroppedCount() int64 { return b.dropped.Load() }

func (b *ProposalBridge) onBusMessage(m *guide.Message) error {
	if m == nil || m.Type != guide.MessageTypeProposal {
		return nil
	}
	out, ok := proposalUIMessage(m.Payload)
	if !ok {
		return nil
	}
	if planMsg, ok := out.(msg.PlanApprovalRequestMsg); ok {
		planMsg.Proposal = planApprovalProposalWithSource(planMsg.Proposal, m.SourceAgentID)
		out = planMsg
	}
	select {
	case b.proposals <- out:
	default:
		b.dropped.Add(1)
	}
	return nil
}

func (b *ProposalBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case proposal := <-b.proposals:
				program.Send(proposal)
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func proposalUIMessage(payload any) (any, bool) {
	switch p := payload.(type) {
	case *planapproval.Proposal:
		if p == nil {
			return nil, false
		}
		return msg.PlanApprovalRequestMsg{Proposal: p}, true
	case planapproval.Proposal:
		pp := p
		return msg.PlanApprovalRequestMsg{Proposal: &pp}, true
	case *commandapproval.Proposal:
		if p == nil {
			return nil, false
		}
		return msg.CommandApprovalRequestMsg{Proposal: p}, true
	case commandapproval.Proposal:
		pp := p
		return msg.CommandApprovalRequestMsg{Proposal: &pp}, true
	case map[string]any:
		return proposalUIMessageFromMap(p)
	case json.RawMessage:
		return proposalUIMessageFromJSON([]byte(p))
	case []byte:
		return proposalUIMessageFromJSON(p)
	case string:
		return proposalUIMessageFromJSON([]byte(p))
	default:
		return nil, false
	}
}

func proposalUIMessageFromMap(payload map[string]any) (any, bool) {
	if _, ok := payload["plan_id"]; ok {
		var proposal planapproval.Proposal
		if decodeProposalPayload(payload, &proposal) {
			return msg.PlanApprovalRequestMsg{Proposal: &proposal}, true
		}
	}
	if _, ok := payload["plan_text"]; ok {
		var proposal planapproval.Proposal
		if decodeProposalPayload(payload, &proposal) {
			return msg.PlanApprovalRequestMsg{Proposal: &proposal}, true
		}
	}
	if _, ok := payload["command"]; ok {
		var proposal commandapproval.Proposal
		if decodeProposalPayload(payload, &proposal) {
			return msg.CommandApprovalRequestMsg{Proposal: &proposal}, true
		}
	}
	if _, ok := payload["tool_name"]; ok {
		var proposal commandapproval.Proposal
		if decodeProposalPayload(payload, &proposal) {
			return msg.CommandApprovalRequestMsg{Proposal: &proposal}, true
		}
	}
	return nil, false
}

func proposalUIMessageFromJSON(raw []byte) (any, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	return proposalUIMessageFromMap(payload)
}

func decodeProposalPayload(payload any, out any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return json.Unmarshal(encoded, out) == nil
}

func planApprovalProposalWithSource(proposal *planapproval.Proposal, sourceAgentID string) *planapproval.Proposal {
	sourceAgentID = strings.TrimSpace(sourceAgentID)
	if proposal == nil || sourceAgentID == "" {
		return proposal
	}
	clone := *proposal
	clone.Metadata = make(map[string]any, len(proposal.Metadata)+2)
	for k, v := range proposal.Metadata {
		clone.Metadata[k] = v
	}
	if strings.TrimSpace(stringFromAny(clone.Metadata["source_agent_id"])) == "" {
		clone.Metadata["source_agent_id"] = sourceAgentID
	}
	if strings.TrimSpace(stringFromAny(clone.Metadata["target_agent_id"])) == "" {
		clone.Metadata["target_agent_id"] = sourceAgentID
	}
	return &clone
}

func stringFromAny(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

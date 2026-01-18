package resources

import (
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/google/uuid"
)

type ContextCompactor struct {
	bus      *signal.SignalBus
	registry *UsageRegistry
}

func NewContextCompactor(bus *signal.SignalBus, registry *UsageRegistry) *ContextCompactor {
	return &ContextCompactor{
		bus:      bus,
		registry: registry,
	}
}

func (cc *ContextCompactor) CompactAll() {
	msg := signal.SignalMessage{
		ID:       uuid.New().String(),
		Signal:   signal.CompactContexts,
		TargetID: "",
		Payload: signal.CompactContextsPayload{
			All:    true,
			Reason: "Memory pressure",
		},
		RequiresAck: false,
		SentAt:      time.Now(),
	}
	_ = cc.bus.Broadcast(msg)
}

func (cc *ContextCompactor) CompactLargest(n int) {
	largest := cc.registry.LargestComponents(n, UsageCategoryLLMBuffers)

	for _, comp := range largest {
		cc.sendCompactSignal(comp.ID)
	}
}

func (cc *ContextCompactor) sendCompactSignal(targetID string) {
	msg := signal.SignalMessage{
		ID:       uuid.New().String(),
		Signal:   signal.CompactContexts,
		TargetID: targetID,
		Payload: signal.CompactContextsPayload{
			TargetID: targetID,
			All:      false,
			Reason:   "Memory pressure - large context",
		},
		RequiresAck: false,
		SentAt:      time.Now(),
	}
	_ = cc.bus.Broadcast(msg)
}

package resources

import (
	"github.com/adalundhe/sylk/core/signal"
	"github.com/google/uuid"
)

// SignalBusPublisher adapts MemoryMonitor's SignalPublisher interface to the SignalBus.
type SignalBusPublisher struct {
	bus *signal.SignalBus
}

// NewSignalBusPublisher creates a publisher that broadcasts memory signals via SignalBus.
func NewSignalBusPublisher(bus *signal.SignalBus) *SignalBusPublisher {
	return &SignalBusPublisher{bus: bus}
}

// PublishMemorySignal publishes a memory signal through the SignalBus.
// This method is safe for concurrent use and non-blocking.
func (p *SignalBusPublisher) PublishMemorySignal(memSig MemorySignal, payload MemorySignalPayload) error {
	sig := translateMemorySignal(memSig)
	busPayload := translatePayload(memSig, payload)

	msg := signal.SignalMessage{
		ID:          uuid.New().String(),
		Signal:      sig,
		TargetID:    string(payload.Component),
		Payload:     busPayload,
		RequiresAck: false,
		SentAt:      payload.Timestamp,
	}

	return p.bus.Broadcast(msg)
}

var memorySignalMap = map[MemorySignal]signal.Signal{
	SignalComponentLRU:        signal.EvictCaches,
	SignalComponentAggressive: signal.EvictCaches,
	SignalGlobalEmergency:     signal.MemoryPressureChanged,
	SignalGlobalPause:         signal.MemoryPressureChanged,
	SignalGlobalResume:        signal.MemoryPressureChanged,
}

func translateMemorySignal(memSig MemorySignal) signal.Signal {
	if sig, ok := memorySignalMap[memSig]; ok {
		return sig
	}
	return signal.MemoryPressureChanged
}

func translatePayload(memSig MemorySignal, payload MemorySignalPayload) any {
	switch memSig {
	case SignalComponentLRU, SignalComponentAggressive:
		return signal.EvictCachesPayload{
			Percent:     calculateEvictPercent(memSig, payload),
			TargetBytes: payload.BudgetBytes - payload.CurrentBytes,
			Reason:      string(memSig),
		}
	default:
		return signal.MemoryPressurePayload{
			From:      "",
			To:        pressureLevelString(payload.Level),
			Usage:     usageRatio(payload),
			Timestamp: payload.Timestamp,
		}
	}
}

var memoryPressureLevelNames = map[MemoryPressureLevel]string{
	MemoryPressureNone:      "none",
	MemoryPressureLow:       "low",
	MemoryPressureHigh:      "high",
	MemoryPressureCritical:  "critical",
	MemoryPressureEmergency: "emergency",
}

func pressureLevelString(level MemoryPressureLevel) string {
	if name, ok := memoryPressureLevelNames[level]; ok {
		return name
	}
	return "unknown"
}

// calculateEvictPercent determines the eviction percentage based on signal type.
func calculateEvictPercent(memSig MemorySignal, payload MemorySignalPayload) float64 {
	if memSig == SignalComponentAggressive {
		return 0.50
	}
	return 0.25
}

// usageRatio calculates the global memory usage ratio.
func usageRatio(payload MemorySignalPayload) float64 {
	if payload.GlobalCeiling == 0 {
		return 0
	}
	return float64(payload.GlobalBytes) / float64(payload.GlobalCeiling)
}

// Compile-time interface check
var _ SignalPublisher = (*SignalBusPublisher)(nil)

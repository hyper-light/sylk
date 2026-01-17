package session

import (
	"time"
)

type SignalType string

const (
	SignalPreempt   SignalType = "preempt"
	SignalPressure  SignalType = "pressure"
	SignalRebalance SignalType = "rebalance"
	SignalShutdown  SignalType = "shutdown"
)

type CrossSessionSignal struct {
	Type        SignalType `json:"type"`
	FromSession string     `json:"from_session"`
	ToSession   string     `json:"to_session"`
	Timestamp   time.Time  `json:"timestamp"`
	Payload     string     `json:"payload"`
}

type SignalHandler func(signal CrossSessionSignal)

func NewPreemptSignal(from, to, payload string) CrossSessionSignal {
	return CrossSessionSignal{
		Type:        SignalPreempt,
		FromSession: from,
		ToSession:   to,
		Timestamp:   time.Now(),
		Payload:     payload,
	}
}

func NewPressureSignal(from, payload string) CrossSessionSignal {
	return CrossSessionSignal{
		Type:        SignalPressure,
		FromSession: from,
		Timestamp:   time.Now(),
		Payload:     payload,
	}
}

func NewRebalanceSignal(from, payload string) CrossSessionSignal {
	return CrossSessionSignal{
		Type:        SignalRebalance,
		FromSession: from,
		Timestamp:   time.Now(),
		Payload:     payload,
	}
}

func NewShutdownSignal(from string) CrossSessionSignal {
	return CrossSessionSignal{
		Type:        SignalShutdown,
		FromSession: from,
		Timestamp:   time.Now(),
	}
}

func (s CrossSessionSignal) IsBroadcast() bool {
	return s.ToSession == ""
}

func (s CrossSessionSignal) IsTargeted() bool {
	return s.ToSession != ""
}

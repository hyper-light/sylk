package clean

import "time"

type Artifact struct {
	Data     []byte
	DataType string
}

type HandlerDeterminism string

const HandlerDeterminismPure HandlerDeterminism = "pure"

type Bus interface {
	SubscribeDelta(pattern string, handler any) (any, error)
}

func NewParticipantRegistration(category string, routeKey string, scope map[string]string, queueCapacity int, concurrencyBudget int, timeout time.Duration, determinism HandlerDeterminism, actions []string) {
}

func Clean(bus Bus) {
	done := make(chan struct{})
	jobs := make(chan int, 1)
	_, _ = bus.SubscribeDelta("claims.session.canonical.agent.participant-agent-guide.claim.posted", nil)
	NewParticipantRegistration("service", "knowledge", map[string]string{"session": "s"}, 8, 1, time.Second, HandlerDeterminismPure, []string{"task"})
	_ = Artifact{Data: []byte("payload"), DataType: "typed.payload.v1"}
	_ = done
	_ = jobs
}

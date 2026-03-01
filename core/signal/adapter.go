package signal

import "time"

// SignalBusInterface abstracts the SignalBus so consumers can be wired
// to either the standalone SignalBus or the ChannelBusSignalAdapter.
type SignalBusInterface interface {
	Subscribe(sub SignalSubscriber) error
	Unsubscribe(subscriberID string) error
	Broadcast(msg SignalMessage) error
	Acknowledge(ack SignalAck) error
	WaitForAcks(signalID string, timeout time.Duration) ([]SignalAck, error)
	ChannelBufferSize() int
	Close()
}

// Compile-time check: SignalBus satisfies SignalBusInterface.
var _ SignalBusInterface = (*SignalBus)(nil)

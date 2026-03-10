package designer

import "github.com/adalundhe/sylk/agents/guide"

func (d *Designer) registerPendingWait(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	d.pendingMu.Lock()
	d.pendingBus[correlationID] = ch
	d.pendingMu.Unlock()
	return ch
}

func (d *Designer) clearPendingWait(correlationID string) {
	d.pendingMu.Lock()
	delete(d.pendingBus, correlationID)
	d.pendingMu.Unlock()
}

func (d *Designer) deliverPendingMessage(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		return
	}
	d.pendingMu.Lock()
	ch := d.pendingBus[msg.CorrelationID]
	d.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

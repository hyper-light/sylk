package pipeline

import "github.com/adalundhe/sylk/agents/guide"

func (pt *PipelineTester) registerPendingWait(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	pt.pendingMu.Lock()
	pt.pendingBus[correlationID] = ch
	pt.pendingMu.Unlock()
	return ch
}

func (pt *PipelineTester) clearPendingWait(correlationID string) {
	pt.pendingMu.Lock()
	delete(pt.pendingBus, correlationID)
	pt.pendingMu.Unlock()
}

func (pt *PipelineTester) deliverPendingMessage(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		return
	}
	pt.pendingMu.Lock()
	ch := pt.pendingBus[msg.CorrelationID]
	pt.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

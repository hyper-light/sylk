package handoff

import (
	"fmt"
	"sync/atomic"
	"time"
)

// ParallelBufferConfig is derived from AgentDescriptor.
type ParallelBufferConfig struct {
	ContextWindow int
	AgentType     string
	ModelID       string
}

// ParallelBuffer maintains a continuously-updated snapshot of agent context
// ready for immediate handoff. A single processLoop goroutine owns all
// mutable state, eliminating write-write races.
//
// The buffer ingests turn records via a bounded channel, accumulates
// messages into segments, drives the elastic sizer from GP predictions,
// and publishes COW snapshots atomically for lock-free reads.
type ParallelBuffer struct {
	config ParallelBufferConfig

	// Owned by processLoop (single writer).
	ring          *BufferRing
	elastic       *ElasticSizer
	accumulator   []Message
	accumTokens   int
	segmentTarget int

	// Communication channels.
	ingestCh   chan bufferIngest
	gpUpdateCh chan *GPPrediction
	stopCh     chan struct{}
	doneCh     chan struct{}

	// Pending stress signal for the next GP update.
	pendingStress atomic.Pointer[StressSignal]

	started atomic.Bool
}

// bufferIngest is the message sent from RecordTurn to processLoop.
type bufferIngest struct {
	Message    Message
	TurnRecord TurnRecord
}

// NewParallelBuffer creates a buffer derived from the descriptor.
// Channel sizes are derived from the context window.
//
//	ingestCh:   max(16, contextWindow / 10_000)
//	gpUpdateCh: 4
func NewParallelBuffer(desc AgentDescriptor) *ParallelBuffer {
	cw := desc.ContextWindow
	if cw < 20_000 {
		cw = 20_000
	}

	ingestSize := cw / 10_000
	if ingestSize < 16 {
		ingestSize = 16
	}

	return &ParallelBuffer{
		config: ParallelBufferConfig{
			ContextWindow: cw,
			AgentType:     desc.AgentType,
			ModelID:       desc.ModelID,
		},
		ring:          NewBufferRing(cw),
		elastic:       NewElasticSizer(desc),
		accumulator:   make([]Message, 0, 32),
		segmentTarget: SegmentTokenTarget(cw),
		ingestCh:      make(chan bufferIngest, ingestSize),
		gpUpdateCh:    make(chan *GPPrediction, 4),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// Start launches the processLoop goroutine.
func (pb *ParallelBuffer) Start() error {
	if pb.started.Swap(true) {
		return fmt.Errorf("parallel buffer already started")
	}
	go pb.processLoop()
	return nil
}

// Stop shuts down the processLoop and waits for completion.
func (pb *ParallelBuffer) Stop() error {
	if !pb.started.Swap(false) {
		return nil
	}
	close(pb.stopCh)
	<-pb.doneCh
	return nil
}

// Ingest sends a turn record to the processLoop. Non-blocking: drops
// oldest if the channel is full (back-pressure from slow processLoop).
func (pb *ParallelBuffer) Ingest(msg Message, rec TurnRecord) {
	item := bufferIngest{Message: msg, TurnRecord: rec}

	select {
	case pb.ingestCh <- item:
	default:
		// Channel full — drop oldest, then send.
		select {
		case <-pb.ingestCh:
		default:
		}
		select {
		case pb.ingestCh <- item:
		default:
		}
	}
}

// UpdateGP sends a GP prediction to the processLoop for elastic sizing.
// Non-blocking: drops oldest if the channel is full.
func (pb *ParallelBuffer) UpdateGP(pred *GPPrediction) {
	if pred == nil {
		return
	}
	select {
	case pb.gpUpdateCh <- pred:
	default:
		// Channel full — drop oldest, then send.
		select {
		case <-pb.gpUpdateCh:
		default:
		}
		select {
		case pb.gpUpdateCh <- pred:
		default:
		}
	}
}

// UpdateGPWithStress stores a stress signal atomically, then sends the
// GP prediction. The processLoop picks up the stress on the next GP update.
func (pb *ParallelBuffer) UpdateGPWithStress(pred *GPPrediction, stress *StressSignal) {
	if stress != nil {
		pb.pendingStress.Store(stress)
	}
	pb.UpdateGP(pred)
}

// Snapshot returns the latest COW snapshot (lock-free, zero-copy).
func (pb *ParallelBuffer) Snapshot() *COWSnapshot {
	return pb.ring.Snapshot()
}

// ElasticState returns the current elastic sizer state.
func (pb *ParallelBuffer) ElasticState() ElasticState {
	return pb.elastic.State()
}

// TokenBudget returns the current buffer token budget.
func (pb *ParallelBuffer) TokenBudget() int {
	return pb.elastic.TokenBudget()
}

// Reset clears the buffer and resets elastic state (after handoff).
func (pb *ParallelBuffer) Reset() {
	// Send a reset signal through the ingest channel so the processLoop
	// handles it on its own goroutine. We achieve this by clearing from
	// outside since the ring and elastic are thread-safe for reads,
	// and the processLoop is the only writer.
	//
	// Actually, we must be careful — Reset is called from the bridge
	// goroutine, not from processLoop. The ring.Clear and elastic.Reset
	// must happen on the processLoop to maintain the single-writer invariant.
	// We use a sentinel message to signal reset.
	pb.Ingest(Message{Role: resetSentinel}, TurnRecord{})
}

// resetSentinel is a special role value used to signal buffer reset.
const resetSentinel = "__parallel_buffer_reset__"

// processLoop is the single-writer goroutine that owns all ring mutations.
func (pb *ParallelBuffer) processLoop() {
	defer close(pb.doneCh)

	for {
		select {
		case <-pb.stopCh:
			return
		case ingest := <-pb.ingestCh:
			if ingest.Message.Role == resetSentinel {
				pb.handleReset()
				continue
			}
			pb.handleIngest(ingest)
		case pred := <-pb.gpUpdateCh:
			pb.handleGPUpdate(pred)
		}
	}
}

// handleIngest processes an incoming turn record:
// 1. Appends message to accumulator
// 2. Seals a segment when accumulator reaches token target
// 3. Publishes updated snapshot
func (pb *ParallelBuffer) handleIngest(ingest bufferIngest) {
	pb.accumulator = append(pb.accumulator, ingest.Message)
	pb.accumTokens += ingest.Message.TokenCount

	if pb.accumTokens >= pb.segmentTarget {
		pb.sealSegment()
	}

	pb.ring.PublishSnapshot(pb.elastic.TokenBudget())
}

// handleGPUpdate processes a GP prediction for elastic sizing,
// incorporating any pending stress signal.
func (pb *ParallelBuffer) handleGPUpdate(pred *GPPrediction) {
	// Swap pending stress (atomic, returns nil if none).
	stress := pb.pendingStress.Swap(nil)

	var newBudget int
	var changed bool
	if stress != nil {
		newBudget, changed = pb.elastic.EvaluateWithStress(pred, stress)
	} else {
		newBudget, changed = pb.elastic.Evaluate(pred)
	}

	if changed && pb.elastic.State() == ElasticShrinking {
		pb.ring.TrimToBudget(newBudget)
	}
	pb.ring.PublishSnapshot(newBudget)
}

// handleReset clears all buffer state and returns elastic to nominal.
func (pb *ParallelBuffer) handleReset() {
	pb.ring.Clear()
	pb.elastic.Reset()
	pb.accumulator = pb.accumulator[:0]
	pb.accumTokens = 0
}

// sealSegment creates an immutable BufferSegment from the accumulator
// and appends it to the ring.
func (pb *ParallelBuffer) sealSegment() {
	msgs := make([]Message, len(pb.accumulator))
	copy(msgs, pb.accumulator)

	seg := &BufferSegment{
		Messages:   msgs,
		TokenCount: pb.accumTokens,
		SealedAt:   time.Now(),
	}

	pb.ring.Append(seg, pb.elastic.TokenBudget())

	// Reset accumulator.
	pb.accumulator = pb.accumulator[:0]
	pb.accumTokens = 0
}

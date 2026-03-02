package steering

import (
	"context"
	"log/slog"
	"sync"

	"github.com/adalundhe/sylk/core/events"
)

// SteeringLedger is the per-request coordination structure for live steering.
// Created when an agent begins handling a forwarded request, destroyed when
// the request completes.
type SteeringLedger struct {
	CorrelationID string
	AgentID       string
	SessionID     string

	Mailbox     *SteeringMailbox
	Checkpoints *CheckpointStore

	// Pace control.
	paceMu   sync.Mutex
	pace     Pace
	resumeCh chan struct{} // cap=1, signaled on Resume or any new command.

	// WAL durability (nil-safe — ledger works without a journal).
	journal *SteeringJournal

	// Archivalist observation (nil-safe).
	activityPub events.ActivityPublisher
}

// NewSteeringLedger creates a new ledger for the given request.
// Journal and activityPub may be nil (ledger degrades gracefully).
func NewSteeringLedger(
	correlationID, agentID, sessionID string,
	activityPub events.ActivityPublisher,
	journal *SteeringJournal,
) *SteeringLedger {
	l := &SteeringLedger{
		CorrelationID: correlationID,
		AgentID:       agentID,
		SessionID:     sessionID,
		Mailbox:       &SteeringMailbox{},
		Checkpoints:   &CheckpointStore{},
		resumeCh:      make(chan struct{}, 1),
		journal:       journal,
		activityPub:   activityPub,
	}

	if journal != nil {
		journal.LogBegin(correlationID, agentID, sessionID)
	}

	return l
}

// DepositCommand adds a command to the mailbox, WAL-logs it, and signals
// resume (unblocking any WaitForResume call).
func (l *SteeringLedger) DepositCommand(cmd Command) {
	l.Mailbox.Deposit(cmd)

	if l.journal != nil {
		l.journal.LogCommand(cmd)
	}

	l.signalResume()
}

// RecordCheckpoint captures a checkpoint, WAL-logs it, and publishes an
// activity event for archivalist observation.
func (l *SteeringLedger) RecordCheckpoint(turn, msgCount int, phase string, snapshotter StateSnapshotter) Checkpoint {
	cp := l.Checkpoints.Capture(turn, msgCount, phase, snapshotter)

	if l.journal != nil {
		l.journal.LogCheckpoint(cp)
	}

	l.publishCheckpointEvent(cp)
	return cp
}

// SetPace changes the execution pace. Setting PaceAuto signals resume.
func (l *SteeringLedger) SetPace(p Pace) {
	l.paceMu.Lock()
	l.pace = p
	l.paceMu.Unlock()

	if p == PaceAuto {
		l.signalResume()
	}
}

// Pace returns the current execution pace.
func (l *SteeringLedger) Pace() Pace {
	l.paceMu.Lock()
	defer l.paceMu.Unlock()
	return l.pace
}

// WaitForResume blocks until either the resume channel is signaled or
// the context is cancelled. Returns ctx.Err() on cancellation.
func (l *SteeringLedger) WaitForResume(ctx context.Context) error {
	select {
	case <-l.resumeCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close writes the closing WAL bracket and cleans up.
// interrupted=true writes OpSteeringInterrupted, otherwise OpSteeringComplete.
func (l *SteeringLedger) Close(interrupted bool) {
	if l.journal == nil {
		return
	}

	if interrupted {
		var lastID, lastPhase string
		var lastTurn int
		if cp := l.Checkpoints.Latest(); cp != nil {
			lastID = cp.ID
			lastTurn = cp.Turn
			lastPhase = cp.Phase
		}
		l.journal.LogInterrupted(l.CorrelationID, lastID, lastTurn, lastPhase)
		return
	}

	var finalTurn int
	if cp := l.Checkpoints.Latest(); cp != nil {
		finalTurn = cp.Turn
	}
	l.journal.LogComplete(l.CorrelationID, finalTurn, l.Checkpoints.Count())
}

func (l *SteeringLedger) signalResume() {
	select {
	case l.resumeCh <- struct{}{}:
	default:
	}
}

func (l *SteeringLedger) publishCheckpointEvent(cp Checkpoint) {
	if l.activityPub == nil {
		return
	}

	event := events.NewActivityEvent(
		events.EventTypeSteeringCheckpoint,
		l.SessionID,
		"checkpoint "+cp.Phase,
	)
	event.AgentID = l.AgentID
	event.Category = "steering"
	event.Visibility = events.VisibilityAgent
	event.Data = map[string]any{
		"correlation_id": l.CorrelationID,
		"agent_id":       l.AgentID,
		"checkpoint_id":  cp.ID,
		"turn":           cp.Turn,
		"phase":          cp.Phase,
		"message_count":  cp.MessageCount,
	}

	l.activityPub.PublishActivity(event)
}

// PublishSteeringEvent publishes a steering inject/edit/rollback event
// to the activity bus.
func (l *SteeringLedger) PublishSteeringEvent(eventType events.EventType, cmd Command) {
	if l.activityPub == nil {
		return
	}

	event := events.NewActivityEvent(eventType, l.SessionID, "steering "+cmd.Type.String())
	event.AgentID = l.AgentID
	event.Category = "steering"
	event.Visibility = events.VisibilityAgent
	event.Data = map[string]any{
		"correlation_id": l.CorrelationID,
		"agent_id":       l.AgentID,
		"command_type":   cmd.Type.String(),
		"text_len":       len(cmd.Text) + len(cmd.NewText),
		"checkpoint_id":  cmd.CheckpointID,
	}

	l.activityPub.PublishActivity(event)
	slog.Debug("steering: published event",
		"type", eventType.String(),
		"cmd", cmd.Type.String(),
		"agent", l.AgentID,
		"corr", l.CorrelationID)
}

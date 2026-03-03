package shared

import (
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
)

// SteeringResult is the outcome of DrainAndCheckpoint, consumed by the
// tool loop to decide whether to inject messages, rollback, or pause.
type SteeringResult struct {
	// Steered is true when user messages were injected into req.Messages.
	Steered bool

	// Rollback is non-nil when the tool loop should truncate messages,
	// restore agent state, and reset the turn counter.
	Rollback *steering.Checkpoint

	// EditReplay is non-nil when the tool loop should truncate messages,
	// inject an edited message, and restart from that checkpoint.
	EditReplay *steering.Checkpoint

	// EditText is the replacement text for an edit command.
	EditText string

	// ShouldPause is true when the pace is Step or Paused and the tool
	// loop should call WaitForResume before continuing.
	ShouldPause bool
}

// DrainAndCheckpoint is the single integration point for steering in all
// agent tool loops. Called once per turn, before the LLM call.
//
// Nil-safe: if ledger is nil, returns a zero SteeringResult (all fields
// false/nil), meaning agents can be called without a ledger with zero
// behavioral change.
//
// Processing order: Rollback > Edit > Steer > Pace > Resume.
// If both a rollback and steering messages are pending, the rollback wins
// (steering messages are stale if we're rolling back).
func DrainAndCheckpoint(
	ledger *steering.SteeringLedger,
	req *providers.Request,
	turn int,
	phase string,
	snap steering.StateSnapshotter,
) SteeringResult {
	if ledger == nil {
		return SteeringResult{}
	}

	// Record checkpoint at this turn boundary.
	ledger.RecordCheckpoint(turn, len(req.Messages), phase, snap)

	// Drain all pending commands.
	cmds := ledger.Mailbox.Drain()
	if len(cmds) == 0 {
		return checkPace(ledger)
	}

	return processCommands(ledger, req, cmds)
}

func processCommands(
	ledger *steering.SteeringLedger,
	req *providers.Request,
	cmds []steering.Command,
) SteeringResult {
	var result SteeringResult

	// Collect steering texts for compaction.
	var steerTexts []string

	for _, cmd := range cmds {
		switch cmd.Type {
		case steering.CommandRollback:
			cp := ledger.Checkpoints.FindByID(cmd.CheckpointID)
			if cp != nil {
				result.Rollback = cp
				ledger.PublishSteeringEvent(events.EventTypeSteeringRollback, cmd)
				return result // Rollback takes priority, exit immediately.
			}

		case steering.CommandEdit:
			cp := ledger.Checkpoints.FindBefore(cmd.MessageIndex)
			if cp != nil {
				result.EditReplay = cp
				result.EditText = cmd.NewText
				ledger.PublishSteeringEvent(events.EventTypeSteeringEdit, cmd)
				return result // Edit takes priority over steer.
			}

		case steering.CommandSteer:
			steerTexts = append(steerTexts, cmd.Text)
			ledger.PublishSteeringEvent(events.EventTypeSteeringInject, cmd)

		case steering.CommandPace:
			ledger.SetPace(cmd.Pace)

		case steering.CommandResume:
			ledger.SetPace(steering.PaceAuto)
		}
	}

	// Compact steering messages into a single user message.
	if len(steerTexts) > 0 {
		msg := steering.FormatSteeringMessages(steerTexts)
		req.Messages = append(req.Messages, msg)
		result.Steered = true
	}

	// Check pace after processing all commands.
	paceResult := checkPace(ledger)
	result.ShouldPause = paceResult.ShouldPause
	return result
}

func checkPace(ledger *steering.SteeringLedger) SteeringResult {
	pace := ledger.Pace()
	return SteeringResult{
		ShouldPause: pace == steering.PaceStep || pace == steering.PacePaused,
	}
}

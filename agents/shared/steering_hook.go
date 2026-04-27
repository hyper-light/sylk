package shared

import (
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
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

// DrainOption configures optional behavior for DrainAndCheckpoint.
type DrainOption func(*drainConfig)

type drainConfig struct {
	boardProvider func() *claims.ClaimsBoard
	agentID       string
}

// WithBoardProvider configures DrainAndCheckpoint to check the claims
// board for pending corrective claims directed at this agent and convert
// them to steering commands.
func WithBoardProvider(bp func() *claims.ClaimsBoard, agentID string) DrainOption {
	return func(cfg *drainConfig) {
		cfg.boardProvider = bp
		cfg.agentID = agentID
	}
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
	opts ...DrainOption,
) SteeringResult {
	if ledger == nil {
		return SteeringResult{}
	}

	// Record checkpoint at this turn boundary.
	ledger.RecordCheckpoint(turn, len(req.Messages), phase, snap)

	// Drain all pending commands.
	cmds := ledger.Mailbox.Drain()

	// If a board provider is set, check for pending corrective claims
	// and convert them to steering commands.
	var cfg drainConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.boardProvider != nil && cfg.agentID != "" {
		if boardCmds := drainCorrectiveClaimsAsCommands(cfg.boardProvider, cfg.agentID); len(boardCmds) > 0 {
			cmds = append(cmds, boardCmds...)
		}
	}

	if len(cmds) == 0 {
		return checkPace(ledger)
	}

	return processCommands(ledger, req, cmds)
}

// drainCorrectiveClaimsAsCommands checks the board for pending
// corrective claims directed at the given agent and converts them
// to steering steer commands.
func drainCorrectiveClaimsAsCommands(bp func() *claims.ClaimsBoard, agentID string) []steering.Command {
	if bp == nil {
		return nil
	}
	board := bp()
	if board == nil {
		return nil
	}
	// Find corrective claims where this agent is the subject.
	ids := board.ObjectIDsWithRelation(claims.RelatedTypeAgent, claims.RelationshipSubject, agentID)
	if len(ids) == 0 {
		return nil
	}
	var cmds []steering.Command
	for _, id := range ids {
		c, ok := board.ClaimByID(id)
		if !ok || c == nil {
			continue
		}
		if c.ActionType != claims.ActionTypeCorrective {
			continue
		}
		if c.Status != claims.ClaimStatusPending {
			continue
		}
		text := strings.TrimSpace(c.Title)
		if desc := strings.TrimSpace(c.Description); desc != "" {
			text += ": " + desc
		}
		if text == "" {
			continue
		}
		cmds = append(cmds, steering.Command{
			Type: steering.CommandSteer,
			Text: text,
		})
		// Mark the claim as in-progress so it's not re-consumed.
		if err := board.UpdateClaimProgress(nil, c.ID, claims.ClaimProgressUpdate{}, "steering_drain"); err != nil {
			slog.Debug("steering_drain_claim_progress_failed", "claim_id", c.ID, "error", err.Error())
		}
	}
	return cmds
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

// DrainFinalSteering performs an atomic drain of the steering mailbox when the
// tool loop is about to exit (LLM returned no tool calls). If actionable
// commands (steer/rollback/edit) are present, it prepares req.Messages for
// re-entry and returns true. Pace/resume commands are applied without re-entry.
//
// For steers: appends the assistant's current response followed by the
// formatted steer text as a user message. Events are published.
//
// For rollback/edit: re-deposits the command so the next DrainAndCheckpoint
// handles truncation and state restoration. The assistant response is appended
// so the checkpoint's MessageCount is correct on re-entry.
//
// The caller must decrement the turn counter before continuing to avoid
// consuming turn budget on steering re-entries.
//
// Nil-safe: returns false when ledger is nil.
func DrainFinalSteering(
	ledger *steering.SteeringLedger,
	req *providers.Request,
	assistantContent string,
) bool {
	if ledger == nil {
		return false
	}
	cmds := ledger.Mailbox.Drain()
	if len(cmds) == 0 {
		return false
	}

	reenter := false
	var steerTexts []string

	for _, cmd := range cmds {
		switch cmd.Type {
		case steering.CommandSteer:
			steerTexts = append(steerTexts, cmd.Text)
			ledger.PublishSteeringEvent(events.EventTypeSteeringInject, cmd)
			reenter = true
		case steering.CommandRollback, steering.CommandEdit:
			// Re-deposit so DrainAndCheckpoint handles truncation + state restore.
			ledger.Mailbox.Deposit(cmd)
			reenter = true
		case steering.CommandPace:
			ledger.SetPace(cmd.Pace)
		case steering.CommandResume:
			ledger.SetPace(steering.PaceAuto)
		}
	}

	if !reenter {
		return false
	}

	// Append the assistant's in-progress response so the LLM sees context.
	// For rollback/edit, DrainAndCheckpoint truncates past the checkpoint.
	req.Messages = append(req.Messages, providers.Message{
		Role:    providers.RoleAssistant,
		Content: assistantContent,
	})

	if len(steerTexts) > 0 {
		req.Messages = append(req.Messages, steering.FormatSteeringMessages(steerTexts))
	}

	return true
}

// MaxSteerReentries bounds how many times the outer turn loop can re-enter
// for steering. Derived from mailbox capacity — a single drain cannot yield
// more commands than the ring buffer holds.
const MaxSteerReentries = 16

// ExecuteTurnLoop wraps an inner tool loop with Codex-style outer turn
// boundary handling. After the inner loop returns successfully (err == nil),
// it drains the steering mailbox for pending commands:
//
//   - Steer: appends assistant content + steer text to req.Messages, re-enters.
//   - Rollback/Edit: re-deposits the command for the inner loop's
//     DrainAndCheckpoint to handle on re-entry.
//   - Pace/Resume: applied without re-entry.
//
// Bounded by MaxSteerReentries. Nil-safe: when ledger is nil, calls
// innerLoop exactly once.
func ExecuteTurnLoop(
	ledger *steering.SteeringLedger,
	req *providers.Request,
	innerLoop func() (string, error),
) (string, error) {
	if ledger == nil {
		return innerLoop()
	}

	var lastContent string
	for range MaxSteerReentries + 1 {
		content, err := innerLoop()
		if err != nil {
			return content, err
		}

		lastContent = content

		if !DrainFinalSteering(ledger, req, content) {
			return content, nil
		}
		// DrainFinalSteering mutated req.Messages:
		//   steer   → appended assistant + steer text
		//   rb/edit → appended assistant, re-deposited command
		// Inner loop's DrainAndCheckpoint handles the rest on re-entry.
	}

	// Exhausted steering re-entries. Return last successful content.
	return lastContent, nil
}

func checkPace(ledger *steering.SteeringLedger) SteeringResult {
	pace := ledger.Pace()
	return SteeringResult{
		ShouldPause: pace == steering.PaceStep || pace == steering.PacePaused,
	}
}

package shared

import "context"

// RequiredProtocolGraceTurns reserves a small number of extra completion turns
// when a protocol tool forces an immediate follow-up action such as
// `handoff_to_green` or `commit_to_disk`. One turn lets the model issue the
// required tool call; the second covers a single narrated/non-tool retry.
const RequiredProtocolGraceTurns = 2

// PendingRequiredProtocolAction reports whether the current turn has a forced
// protocol follow-up that has not yet been satisfied by a terminal action.
func PendingRequiredProtocolAction(ctx context.Context) bool {
	if state := PipelineProtocolStateFromContext(ctx); state != nil {
		if action, _ := state.RequiredAction(); action != "" && state.TerminalAction() == nil {
			return true
		}
	}
	if state := GlobalReviewStateFromContext(ctx); state != nil {
		if action, _ := state.RequiredAction(); action != "" && state.TerminalAction() == nil {
			return true
		}
	}
	return false
}

// ExtendRequiredProtocolGrace grants the bounded grace budget when a forced
// protocol action is still pending.
func ExtendRequiredProtocolGrace(ctx context.Context, current int) int {
	if current >= RequiredProtocolGraceTurns {
		return current
	}
	if PendingRequiredProtocolAction(ctx) {
		return RequiredProtocolGraceTurns
	}
	return current
}

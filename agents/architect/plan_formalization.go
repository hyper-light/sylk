package architect

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/google/uuid"
)

// tryFormalizePlan checks whether the user is confirming a plan formalization
// offered by the architect in prior conversation. When triggered, it runs the
// full planning protocol using the original query extracted from conversation
// history. Returns (response, true) when formalization was triggered.
func (a *Architect) tryFormalizePlan(ctx context.Context, fwd *guide.ForwardedRequest) (*DesignPlan, bool) {
	if fwd == nil || len(fwd.ConversationHistory) == 0 {
		return nil, false
	}
	// If a ready plan already exists, tryExecutePlan handles it.
	if a.latestReadyPlan() != nil {
		return nil, false
	}
	input := strings.TrimSpace(fwd.Input)
	if input == "" {
		return nil, false
	}
	triggered := isExplicitFormalizationRequest(input) ||
		(isAffirmativeResponse(input) && lastReplyOfferedFormalization(fwd.ConversationHistory))
	if !triggered {
		return nil, false
	}

	query := extractOriginalQuery(fwd.ConversationHistory, input)
	req := &ArchitectRequest{
		ID:                  uuid.NewString(),
		Intent:              IntentPlan,
		Query:               query,
		SessionID:           sessionIDFromForwarded(fwd),
		Params:              forwardedRequestParams(fwd),
		ConversationHistory: fwd.ConversationHistory,
	}

	plan, err := a.executePlanningProtocol(ctx, req)
	if err != nil {
		a.logger.Warn("plan formalization failed", "error", err)
		return nil, false
	}
	return plan, true
}

// formalizationPhrases are user phrases that explicitly request plan creation.
var formalizationPhrases = []string{
	"create the plan",
	"create a plan",
	"formalize",
	"generate tasks",
	"build the plan",
	"make the plan",
	"make a plan",
	"plan it",
	"plan this",
}

// isExplicitFormalizationRequest returns true if the user input explicitly
// requests plan creation.
func isExplicitFormalizationRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	for _, phrase := range formalizationPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// affirmativePhrases are short user confirmations.
var affirmativePhrases = []string{
	"yes",
	"sure",
	"go ahead",
	"do it",
	"sounds good",
	"looks good",
	"approved",
	"lgtm",
	"yep",
	"yeah",
	"ok",
	"okay",
	"please",
	"absolutely",
}

// isAffirmativeResponse returns true if the input is a short affirmative reply.
func isAffirmativeResponse(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	for _, phrase := range affirmativePhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// formalizationOfferPhrases are phrases the architect uses when offering to
// create an actionable plan. Matched case-insensitively.
var formalizationOfferPhrases = []string{
	"actionable plan",
	"create a plan",
	"formalize",
	"ready to plan",
	"create an actionable",
	"generate a plan",
}

// lastReplyOfferedFormalization checks whether the architect's most recent
// reply in the conversation offered to create a formalized plan.
func lastReplyOfferedFormalization(history []guide.ConversationTurn) bool {
	if len(history) == 0 {
		return false
	}
	lastReply := strings.ToLower(strings.TrimSpace(history[len(history)-1].AgentReply))
	if lastReply == "" {
		return false
	}
	for _, phrase := range formalizationOfferPhrases {
		if strings.Contains(lastReply, phrase) {
			return true
		}
	}
	return false
}

// extractOriginalQuery pulls the original substantive user request from
// conversation history. Uses the first user input as the canonical query,
// falling back to the current input if history is empty.
func extractOriginalQuery(history []guide.ConversationTurn, currentInput string) string {
	if len(history) > 0 {
		first := strings.TrimSpace(history[0].UserInput)
		if first != "" {
			return first
		}
	}
	return currentInput
}

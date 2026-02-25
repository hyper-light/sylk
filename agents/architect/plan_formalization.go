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
	// If a ready plan already exists, the phase gate handles approval/feedback.
	if a.latestReadyPlan() != nil {
		return nil, false
	}
	input := strings.TrimSpace(fwd.Input)
	if input == "" {
		return nil, false
	}
	triggered := isExplicitFormalizationRequest(input) ||
		(isAffirmativeResponse(input) && conversationDiscussedPlanning(fwd.ConversationHistory)) ||
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

// affirmativeExact are single-word confirmations that must match the entire
// input (after lowering + stripping trailing punctuation). Using exact-match
// prevents false positives like "yes, but can we change the approach?" from
// triggering plan formalization when the user intended feedback.
var affirmativeExact = map[string]struct{}{
	"yes": {}, "y": {}, "yep": {}, "yeah": {}, "yup": {},
	"ok": {}, "okay": {}, "sure": {}, "right": {},
	"great": {}, "perfect": {}, "awesome": {}, "absolutely": {},
	"approved": {}, "lgtm": {}, "please": {},
}

// affirmativeContains are multi-word phrases that signal confirmation when
// found anywhere in the input. Only multi-word phrases are safe for substring
// matching because they carry enough specificity to avoid false positives.
var affirmativeContains = []string{
	"go ahead", "go for it", "do it", "sounds good", "looks good",
	"let's go", "lets go", "let's do it", "lets do it",
	"ship it", "kick it off", "make it so",
}

// isAffirmativeResponse returns true if the input is a short affirmative reply.
// Single-word affirmatives use exact-match (stripped of trailing punctuation)
// to avoid matching feedback like "yes, but change the database layer".
// Multi-word action phrases use substring matching.
func isAffirmativeResponse(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	stripped := strings.TrimRight(lower, ".!?,;:")
	if _, ok := affirmativeExact[stripped]; ok {
		return true
	}
	for _, phrase := range affirmativeContains {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// planDiscussionSignals are phrases that indicate the architect was
// discussing building, implementing, or planning something. Broader than
// formalizationOfferPhrases — used with execution requests (not bare
// affirmatives) so the false-positive risk is low.
var planDiscussionSignals = []string{
	"execution plan",
	"implementation plan",
	"layer 0",
	"layer 1",
	"task",
	"scaffold",
	"break this down",
	"here's how",
	"here's what",
	"i'd recommend",
	"i'd suggest",
	"i'd build",
	"i'd approach",
	"i'd implement",
	"we can build",
	"we could build",
	"let me plan",
	"plan this out",
	"plan would",
	"plan it out",
	"components",
	"architecture",
	"design system",
}

// conversationDiscussedPlanning checks whether any architect reply in the
// conversation history contains plan-discussion signals. This is broader
// than lastReplyOfferedFormalization and is used with affirmative responses
// when the conversation context indicates planning was discussed.
func conversationDiscussedPlanning(history []guide.ConversationTurn) bool {
	for i := len(history) - 1; i >= 0; i-- {
		reply := strings.ToLower(strings.TrimSpace(history[i].AgentReply))
		if reply == "" {
			continue
		}
		for _, signal := range planDiscussionSignals {
			if strings.Contains(reply, signal) {
				return true
			}
		}
		for _, phrase := range formalizationOfferPhrases {
			if strings.Contains(reply, phrase) {
				return true
			}
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

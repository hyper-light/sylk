package shared

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

// recalledContextHeader opens the system-prompt history block.
const recalledContextHeader = "\n\n## Prior Conversation Context\n\n"

// recalledContextFooter closes the block with an independent-evaluation cue.
const recalledContextFooter = "\nUse this context to inform your current task. Evaluate the current request on its own merits.\n"

// ConversationTurnsToMessages converts Guide conversation turns into
// alternating User/Assistant provider message pairs. Turns with empty
// AgentReply produce only a User message. Returns nil when history is empty.
//
// Use this for reconstructing an agent's own local conversation state
// (e.g. Guardian cold-start seeding). For injecting forwarded history
// into an LLM request, use PrependHistoryMessages instead — it appends
// a recalled-context transcript to the system prompt.
func ConversationTurnsToMessages(history []guide.ConversationTurn) []providers.Message {
	if len(history) == 0 {
		return nil
	}

	msgs := make([]providers.Message, 0, len(history)*2)
	for _, turn := range history {
		msgs = append(msgs, providers.Message{
			Role:    providers.RoleUser,
			Content: turn.UserInput,
		})
		if turn.AgentReply != "" {
			msgs = append(msgs, providers.Message{
				Role:    providers.RoleAssistant,
				Content: turn.AgentReply,
			})
		}
	}
	return msgs
}

// PrependHistoryMessages appends a second-person recalled transcript of
// conversation history to req.SystemPrompt. This gives the model ownership
// of its prior responses ("Your response:") for recall and coherence while
// keeping the history in the system prompt — background knowledge that
// informs without creating continuation bias. The request messages are
// untouched, so the current task is always evaluated independently.
//
// No-op when history is empty.
func PrependHistoryMessages(req *providers.Request, history []guide.ConversationTurn) {
	if len(history) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString(recalledContextHeader)
	for i, turn := range history {
		fmt.Fprintf(&b, "Turn %d — User: %s\n", i+1, turn.UserInput)
		if turn.AgentReply != "" {
			fmt.Fprintf(&b, "Your response: %s\n", turn.AgentReply)
		}
		b.WriteByte('\n')
	}
	b.WriteString(recalledContextFooter)

	req.SystemPrompt += b.String()
}

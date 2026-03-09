package academic

import "github.com/adalundhe/sylk/agents/guide"

// ConversationResult carries a user-facing Academic response while preserving
// clean conversation history text for the Guide.
type ConversationResult struct {
	Response string
	Intent   guide.Intent
}

func (r *ConversationResult) ResponseText() string {
	if r == nil {
		return ""
	}
	return r.Response
}

package architect

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestConversationDiscussedPlanning(t *testing.T) {
	tests := []struct {
		name    string
		history []guide.ConversationTurn
		want    bool
	}{
		{
			name:    "empty history",
			history: nil,
			want:    false,
		},
		{
			name: "reply mentions execution plan",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "Here's the execution plan for the landing page with 7 tasks."},
			},
			want: true,
		},
		{
			name: "reply mentions tasks",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "I'd break this into several task groups across multiple layers."},
			},
			want: true,
		},
		{
			name: "reply mentions architecture",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "The architecture would use Next.js with a component library."},
			},
			want: true,
		},
		{
			name: "reply mentions scaffold",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "First we scaffold the project structure."},
			},
			want: true,
		},
		{
			name: "reply mentions design system",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "We'll start with the design system and core components."},
			},
			want: true,
		},
		{
			name: "reply uses formalization offer phrase",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "I can create a plan for this."},
			},
			want: true,
		},
		{
			name: "reply with no plan signals",
			history: []guide.ConversationTurn{
				{UserInput: "what is Go?", AgentReply: "Go is a statically typed programming language."},
			},
			want: false,
		},
		{
			name: "earlier turn has signal even if latest does not",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "Here's the implementation plan with several components."},
				{UserInput: "what about styling?", AgentReply: "We could use Tailwind CSS for that."},
			},
			want: true,
		},
		{
			name: "empty agent replies",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: ""},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := conversationDiscussedPlanning(tt.history)
			if got != tt.want {
				t.Errorf("conversationDiscussedPlanning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAffirmativeResponse(t *testing.T) {
	positives := []string{
		"yes", "Yes", "YES", "y", "yep", "yeah", "yup",
		"ok", "okay", "sure", "right", "great", "perfect",
		"awesome", "absolutely", "approved", "lgtm", "please",
		// With trailing punctuation (stripped before exact match).
		"yes!", "sure.", "ok!",
		// Multi-word contains matches.
		"go ahead", "do it", "sounds good", "looks good",
		"let's go", "ship it", "kick it off",
		"yes, go ahead and create it",
	}
	for _, input := range positives {
		if !isAffirmativeResponse(input) {
			t.Errorf("expected affirmative for %q", input)
		}
	}

	negatives := []string{
		"", "   ",
		// Embedded single-word affirmative in longer feedback must NOT match.
		"yes, but can we change the database layer?",
		"ok so what about the auth flow?",
		"sure, but I have some concerns about scaling",
		"yeah I don't think that's the right approach",
		// Pure feedback with no affirmative signal.
		"change the auth to JWT",
		"what about caching?",
		"I think we should use Redis instead",
	}
	for _, input := range negatives {
		if isAffirmativeResponse(input) {
			t.Errorf("expected non-affirmative for %q", input)
		}
	}
}

func TestTryFormalizeTriggerWithAffirmativeAndPlanDiscussion(t *testing.T) {
	// Verify that isAffirmativeResponse + conversationDiscussedPlanning
	// together produce the expected trigger behavior. Plan approval is now
	// handled by the Guide's phase gate, but plan formalization from
	// conversation (no ready plan yet) still uses this path.
	tests := []struct {
		name    string
		input   string
		history []guide.ConversationTurn
		want    bool
	}{
		{
			name:  "yes with plan discussion",
			input: "yes",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "The architecture would involve three layers."},
			},
			want: true,
		},
		{
			name:  "ok with plan discussion",
			input: "ok",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "I'd recommend breaking this into task groups."},
			},
			want: true,
		},
		{
			name:  "yes but no plan discussion",
			input: "yes",
			history: []guide.ConversationTurn{
				{UserInput: "what is Go?", AgentReply: "Go is a programming language developed at Google."},
			},
			want: false,
		},
		{
			name:    "yes but no history",
			input:   "yes",
			history: nil,
			want:    false,
		},
		{
			name:  "go ahead with plan discussion",
			input: "go ahead",
			history: []guide.ConversationTurn{
				{UserInput: "build a landing page", AgentReply: "Here's the execution plan for a Next.js landing page."},
			},
			want: true,
		},
		{
			name:  "yes-but feedback should NOT trigger formalization",
			input: "yes, but can we use GraphQL instead of REST?",
			history: []guide.ConversationTurn{
				{UserInput: "build a REST API", AgentReply: "The architecture would use a 3-layer design."},
			},
			want: false,
		},
		{
			name:  "ok-but feedback should NOT trigger formalization",
			input: "ok so what about the auth flow?",
			history: []guide.ConversationTurn{
				{UserInput: "build auth", AgentReply: "I'd recommend breaking this into task groups."},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAffirmativeResponse(tt.input) && conversationDiscussedPlanning(tt.history)
			if got != tt.want {
				t.Errorf("trigger = %v, want %v", got, tt.want)
			}
		})
	}
}

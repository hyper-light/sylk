package orchestrator

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrchestratorHandle_HelpIntent(t *testing.T) {
	o, err := New(Config{
		AgentID:   "orchestrator",
		SessionID: "test-session",
	}, nil, nil, nil)
	require.NoError(t, err)

	// IntentHelp routes to conversation; empty input hits the static
	// online reply fast-path (no LLM required).
	result, err := o.Handle(context.Background(), &guide.ForwardedRequest{
		Intent: guide.IntentHelp,
	})
	require.NoError(t, err)

	cr, ok := result.(*ConversationResult)
	require.True(t, ok)
	assert.Equal(t, "chat", cr.Intent)
	assert.NotEmpty(t, cr.Response)
}

func TestOrchestratorHandle_ChatIntent(t *testing.T) {
	o, err := New(Config{
		AgentID:   "orchestrator",
		SessionID: "test-session",
	}, nil, nil, nil)
	require.NoError(t, err)

	result, err := o.Handle(context.Background(), &guide.ForwardedRequest{
		Intent: guide.IntentChat,
		Input:  "Howdy!",
	})
	require.NoError(t, err)

	cr, ok := result.(*ConversationResult)
	require.True(t, ok)
	assert.Equal(t, "chat", cr.Intent)
	assert.NotEmpty(t, cr.Response)
}

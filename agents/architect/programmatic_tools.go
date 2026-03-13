package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/google/uuid"
)

func (a *Architect) invokeConversationTool(
	ctx context.Context,
	name string,
	input any,
	intent ArchitectIntent,
) (*ConversationResult, error) {
	if a == nil || a.toolRuntime() == nil {
		return nil, fmt.Errorf("architect tool runtime is not configured")
	}
	if _, err := a.toolRuntime().Activate(name); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal tool input for %s: %w", name, err)
	}
	correlationID := originalCIDFromContext(ctx)
	if correlationID == "" {
		correlationID = "corr_" + uuid.NewString()
	}
	execResult, execErr := a.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        "invoke_" + uuid.NewString(),
			Name:      name,
			Arguments: string(raw),
		},
		AgentID:         a.toolRuntime().AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: a.toolRuntime().CapabilityScope(),
	})
	if execErr != nil &&
		!errors.Is(execErr, skills.ErrDelegatedRequested) &&
		strings.TrimSpace(execResult.Output) == "" {
		return nil, execErr
	}
	message := toolOutputUserMessage(execResult.Output)
	if message == "" {
		message = strings.TrimSpace(execResult.Output)
	}
	if message == "" {
		message = skills.DelegatedMessage(execErr)
	}
	if message == "" {
		message = fmt.Sprintf("%s queued.", strings.ReplaceAll(name, "_", " "))
	}
	if errors.Is(execErr, skills.ErrDelegatedRequested) {
		execErr = nil
	}
	return &ConversationResult{Response: message, Intent: intent}, execErr
}

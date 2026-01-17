package signal

type ResumeDecision string

const (
	ResumeContinue ResumeDecision = "continue"
	ResumeRetry    ResumeDecision = "retry"
	ResumeAbort    ResumeDecision = "abort"
)

type ResumeContext struct {
	Checkpoint      *AgentCheckpoint
	CurrentMessages []Message
	AbortReason     string
}

type ResumeResult struct {
	Decision       ResumeDecision
	Reason         string
	PendingActions []Action
	LastOperation  *Operation
	OperationState OperationState
}

func EvaluateResume(ctx *ResumeContext) *ResumeResult {
	if ctx.Checkpoint == nil {
		return continueResult("no checkpoint")
	}

	if shouldAbort(ctx) {
		return abortResult(ctx.AbortReason)
	}

	if result := checkRetryConditions(ctx); result != nil {
		return result
	}

	return continueResultWithState(ctx.Checkpoint)
}

func checkRetryConditions(ctx *ResumeContext) *ResumeResult {
	if shouldRetryForMessagesChange(ctx) {
		return retryResult("context changed while paused", ctx.Checkpoint)
	}

	if shouldRetryForInProgressOperation(ctx.Checkpoint) {
		return retryResult("operation was interrupted", ctx.Checkpoint)
	}

	if shouldRetryForInProgressTools(ctx.Checkpoint) {
		return retryResult("tools were in progress", ctx.Checkpoint)
	}

	return nil
}

func shouldAbort(ctx *ResumeContext) bool {
	return ctx.AbortReason != ""
}

func shouldRetryForMessagesChange(ctx *ResumeContext) bool {
	if len(ctx.CurrentMessages) == 0 {
		return false
	}
	return !ctx.Checkpoint.VerifyMessagesHash(ctx.CurrentMessages)
}

func shouldRetryForInProgressOperation(cp *AgentCheckpoint) bool {
	return cp.IsOperationInProgress()
}

func shouldRetryForInProgressTools(cp *AgentCheckpoint) bool {
	return cp.HasInProgressTools()
}

func continueResult(reason string) *ResumeResult {
	return &ResumeResult{
		Decision: ResumeContinue,
		Reason:   reason,
	}
}

func continueResultWithState(cp *AgentCheckpoint) *ResumeResult {
	return &ResumeResult{
		Decision:       ResumeContinue,
		Reason:         "clean state",
		PendingActions: cp.PendingActions,
	}
}

func retryResult(reason string, cp *AgentCheckpoint) *ResumeResult {
	return &ResumeResult{
		Decision:       ResumeRetry,
		Reason:         reason,
		LastOperation:  cp.LastOperation,
		OperationState: cp.OperationState,
	}
}

func abortResult(reason string) *ResumeResult {
	return &ResumeResult{
		Decision: ResumeAbort,
		Reason:   reason,
	}
}

type ResumeExecutor interface {
	ExecuteContinue(actions []Action) error
	ExecuteRetry(operation *Operation) error
	ExecuteAbort(reason string) error
}

func ExecuteResume(result *ResumeResult, executor ResumeExecutor) error {
	switch result.Decision {
	case ResumeContinue:
		return executor.ExecuteContinue(result.PendingActions)
	case ResumeRetry:
		return executor.ExecuteRetry(result.LastOperation)
	case ResumeAbort:
		return executor.ExecuteAbort(result.Reason)
	}
	return nil
}

package shared

import (
	"context"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

// PendingSyncWait tracks a synchronous inter-agent request, including
// descendant child-stream activity that should keep the parent wait alive.
type PendingSyncWait struct {
	Response chan *guide.Message
	Activity chan struct{}
}

func NewPendingSyncWait() *PendingSyncWait {
	return &PendingSyncWait{
		Response: make(chan *guide.Message, 1),
		Activity: make(chan struct{}, 1),
	}
}

// PendingSyncActivityCorrelations returns the correlation IDs that should be
// treated as activity for a synchronous wait. A nested child stream counts as
// activity for both the child correlation and the parent branch correlation.
func PendingSyncActivityCorrelations(msg *guide.Message) []string {
	if msg == nil || msg.Type != guide.MessageTypeStream {
		return nil
	}

	correlations := make([]string, 0, 2)
	if corr := normalizeInlineString(msg.CorrelationID); corr != "" {
		correlations = append(correlations, corr)
	}

	streamResp, ok := msg.GetStreamResponse()
	if !ok || streamResp == nil {
		return correlations
	}

	if branch, ok := interAgentBranchMetadataFromMetadata(streamResp.Metadata); ok {
		parent := normalizeInlineString(branch.ParentCorrelationID)
		if parent != "" && parent != normalizeInlineString(msg.CorrelationID) {
			correlations = append(correlations, parent)
		}
	}

	return correlations
}

func WaitForPendingSyncResponse(
	ctx context.Context,
	subject string,
	inactivityTimeout time.Duration,
	wait *PendingSyncWait,
) (*guide.Message, error) {
	if wait == nil {
		return nil, fmt.Errorf("%s waiter is missing", subject)
	}
	if inactivityTimeout <= 0 {
		inactivityTimeout = DefaultConsultationTimeout
	}

	response, started, err := waitForPendingSyncStart(ctx, subject, inactivityTimeout, wait)
	if err != nil {
		return nil, err
	}
	if response != nil || !started {
		return response, nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case response := <-wait.Response:
			return response, nil
		case <-wait.Activity:
			// Once the child route has started, trust the parent context to own
			// lifetime. Long quiet periods should not be treated as failure.
		}
	}
}

func waitForPendingSyncStart(
	ctx context.Context,
	subject string,
	inactivityTimeout time.Duration,
	wait *PendingSyncWait,
) (*guide.Message, bool, error) {
	var (
		response *guide.Message
		started  bool
	)

	err := RunWithContextLease(ctx, ContextLeaseConfig{
		AttemptTimeout: inactivityTimeout,
		MaxRefreshes:   DefaultConsultationLeaseRefreshes,
		DeadlineGuard:  DefaultContextLeaseDeadlineGuard,
	}, func(waitCtx context.Context) error {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case response = <-wait.Response:
			return nil
		case <-wait.Activity:
			started = true
			return nil
		}
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, WrapLeaseTimeoutError(subject, inactivityTimeout, err)
	}
	return response, started, nil
}

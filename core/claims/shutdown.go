package claims

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

const shutdownActorID = "sys:claims_shutdown"

//go:generate mockery --name=ShutdownScope --output=./mocks --outpkg=mocks
type ShutdownScope interface {
	SignalShutdown()
	Shutdown(gracePeriod, hardDeadline time.Duration) error
}

//go:generate mockery --name=ShutdownInbox --output=./mocks --outpkg=mocks
type ShutdownInbox interface {
	Close() error
}

//go:generate mockery --name=ShutdownContinuationStore --output=./mocks --outpkg=mocks
type ShutdownContinuationStore interface {
	Stop(reason string)
}

type SessionShutdownOptions struct {
	SessionID    string
	Board        *ClaimsBoard
	DurableBoard *DurableBoard

	ServiceDispatchers  *ServiceDispatcherRegistry
	ValidatorDispatcher *BoardValidatorDispatcher
	SessionBoards       *SessionBoardRegistry
	SessionInboxes      *SessionInboxRegistry

	Scopes             []ShutdownScope
	Inboxes            []ShutdownInbox
	Continuations      []ShutdownContinuationStore
	ActiveRootClaimIDs []string

	GracePeriod      time.Duration
	HardDeadline     time.Duration
	OutboxDrainLimit int
	ActorID          string
	Reason           string
}

type SessionShutdownResult struct {
	SessionID      string               `json:"session_id,omitempty"`
	Steps          []string             `json:"steps,omitempty"`
	Cancelled      []string             `json:"cancelled,omitempty"`
	Terminal       []string             `json:"terminal,omitempty"`
	Errors         []string             `json:"errors,omitempty"`
	OutboxDrained  int                  `json:"outbox_drained"`
	SnapshotSaved  bool                 `json:"snapshot_saved"`
	BoardClosed    bool                 `json:"board_closed"`
	AlreadyStopped bool                 `json:"already_stopped"`
	LeakReports    []ShutdownLeakReport `json:"leak_reports,omitempty"`
}

type ShutdownLeakReport struct {
	AgentID     string   `json:"agent_id,omitempty"`
	LeakedCount int      `json:"leaked_count"`
	Workers     []string `json:"workers,omitempty"`
	StackDump   string   `json:"stack_dump,omitempty"`
}

type SessionShutdownCoordinator struct {
	mu      sync.Mutex
	started bool
	done    chan struct{}
	result  SessionShutdownResult
	err     error
}

func NewSessionShutdownCoordinator() *SessionShutdownCoordinator {
	return &SessionShutdownCoordinator{}
}

func (c *SessionShutdownCoordinator) Shutdown(ctx context.Context, opts SessionShutdownOptions) (SessionShutdownResult, error) {
	if c == nil {
		return SessionShutdownResult{}, fmt.Errorf("shutdown coordinator is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if done, stopped := c.begin(); stopped {
		<-done
		c.mu.Lock()
		result := c.result
		err := c.err
		c.mu.Unlock()
		result.AlreadyStopped = true
		return result, err
	}
	result, err := runSessionShutdown(ctx, normalizeSessionShutdownOptions(opts))
	c.finish(result, err)
	return result, err
}

func (c *SessionShutdownCoordinator) begin() (<-chan struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return c.done, true
	}
	c.started = true
	c.done = make(chan struct{})
	return c.done, false
}

func (c *SessionShutdownCoordinator) finish(result SessionShutdownResult, err error) {
	c.mu.Lock()
	c.result = result
	c.err = err
	close(c.done)
	c.mu.Unlock()
}

func runSessionShutdown(ctx context.Context, opts SessionShutdownOptions) (SessionShutdownResult, error) {
	result := SessionShutdownResult{SessionID: opts.SessionID}
	var joined error
	record := func(step string, err error) {
		result.Steps = append(result.Steps, step)
		if err != nil {
			result.Errors = append(result.Errors, step+": "+err.Error())
			joined = errors.Join(joined, err)
		}
	}
	for _, inbox := range opts.Inboxes {
		record("close_inbox", closeShutdownInbox(inbox))
	}
	for _, scope := range opts.Scopes {
		if scope != nil {
			scope.SignalShutdown()
		}
	}
	result.Steps = append(result.Steps, "signal_scopes")
	cancelResult, cancelErr := cancelShutdownClaims(ctx, opts)
	result.Cancelled = cancelResult.CancelledClaimIDs
	result.Terminal = cancelResult.TerminalClaimIDs
	record("cancel_claims", cancelErr)
	record("close_service_dispatchers", closeServiceDispatchers(opts))
	for _, store := range opts.Continuations {
		if store != nil {
			store.Stop(firstNonEmpty(opts.Reason, "session shutdown"))
		}
	}
	result.Steps = append(result.Steps, "stop_continuations")
	for _, scope := range opts.Scopes {
		err := shutdownScope(scope, opts)
		if report, ok := shutdownLeakReport(err); ok {
			result.LeakReports = append(result.LeakReports, report)
		}
		record("shutdown_scope", err)
	}
	if opts.DurableBoard != nil {
		result.OutboxDrained = opts.DurableBoard.DrainOutbox(ctx, opts.OutboxDrainLimit)
		result.Steps = append(result.Steps, "drain_outbox")
		if err := opts.DurableBoard.SaveSnapshot(); err == nil {
			result.SnapshotSaved = true
			result.Steps = append(result.Steps, "save_snapshot")
		} else {
			record("save_snapshot", err)
		}
		if err := opts.DurableBoard.Close(); err == nil {
			result.BoardClosed = true
			result.Steps = append(result.Steps, "close_board")
		} else {
			record("close_board", err)
		}
	}
	removeRegistries(opts)
	result.Steps = append(result.Steps, "remove_registries")
	return result, joined
}

func normalizeSessionShutdownOptions(opts SessionShutdownOptions) SessionShutdownOptions {
	opts.SessionID = strings.TrimSpace(opts.SessionID)
	if opts.Board == nil && opts.DurableBoard != nil {
		opts.Board = opts.DurableBoard.Board()
	}
	if opts.SessionID == "" && opts.Board != nil {
		opts.SessionID = opts.Board.SessionID()
	}
	if opts.GracePeriod <= 0 {
		opts.GracePeriod = DefaultClaimsOperationsConfig().Budgets.ShutdownGracePeriod
	}
	if opts.HardDeadline <= 0 {
		opts.HardDeadline = DefaultClaimsOperationsConfig().Budgets.ShutdownHardDeadline
	}
	if opts.OutboxDrainLimit <= 0 {
		opts.OutboxDrainLimit = DefaultClaimsOperationsConfig().Budgets.OutboxProjectionBatchLimit
	}
	opts.ActorID = firstNonEmpty(strings.TrimSpace(opts.ActorID), shutdownActorID)
	return opts
}

func closeShutdownInbox(inbox ShutdownInbox) error {
	if inbox == nil {
		return nil
	}
	return inbox.Close()
}

func closeServiceDispatchers(opts SessionShutdownOptions) error {
	if opts.ServiceDispatchers == nil || strings.TrimSpace(opts.SessionID) == "" {
		return nil
	}
	return opts.ServiceDispatchers.CloseSession(opts.SessionID)
}

func cancelShutdownClaims(ctx context.Context, opts SessionShutdownOptions) (ClaimCancellationResult, error) {
	if opts.Board == nil || len(opts.ActiveRootClaimIDs) == 0 {
		return ClaimCancellationResult{}, nil
	}
	return CancelClaimTree(ctx, ClaimCancellationOptions{
		Board:               opts.Board,
		Dispatchers:         opts.ServiceDispatchers,
		ValidatorDispatcher: opts.ValidatorDispatcher,
		RootClaimIDs:        opts.ActiveRootClaimIDs,
		OriginClaimID:       strings.Join(normalizeStringList(opts.ActiveRootClaimIDs), ","),
		ActorID:             opts.ActorID,
		Reason:              firstNonEmpty(opts.Reason, "session shutdown"),
	})
}

func shutdownScope(scope ShutdownScope, opts SessionShutdownOptions) error {
	if scope == nil {
		return nil
	}
	return scope.Shutdown(opts.GracePeriod, opts.HardDeadline)
}

func removeRegistries(opts SessionShutdownOptions) {
	if strings.TrimSpace(opts.SessionID) == "" {
		return
	}
	if opts.SessionBoards != nil {
		opts.SessionBoards.Remove(opts.SessionID)
	}
	if opts.SessionInboxes != nil {
		for _, agentID := range shutdownInboxAgentIDs(opts.Board) {
			opts.SessionInboxes.Remove(opts.SessionID, agentID)
		}
	}
}

func shutdownLeakReport(err error) (ShutdownLeakReport, bool) {
	var leak *concurrency.GoroutineLeakError
	if !errors.As(err, &leak) || leak == nil {
		return ShutdownLeakReport{}, false
	}
	return ShutdownLeakReport{
		AgentID:     leak.AgentID,
		LeakedCount: leak.LeakedCount,
		Workers:     append([]string(nil), leak.Workers...),
		StackDump:   leak.StackDump,
	}, true
}

func shutdownInboxAgentIDs(board *ClaimsBoard) []string {
	if board == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, claim := range board.Projection().Claims {
		for _, relation := range claim.Relations {
			if relation.RelatedType == RelatedTypeAgent {
				seen[strings.TrimSpace(relation.Related)] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for agentID := range seen {
		if agentID != "" {
			out = append(out, agentID)
		}
	}
	sortStrings(out)
	return out
}

func sortStrings(values []string) {
	sort.Strings(values)
}

package versioning

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// CommitResolver drives the commit queue head forward in FIFO arrival
// order, flushing each committed changeset to disk via the DiskFlusher
// and advancing the water line. See docs/PARALLEL_GLOBAL_VFS.md §3.4 /
// §3.7.
//
// Design points:
//   - Strictly serializes disk writes: at most one flush in flight.
//   - Head entry's disposition is determined by its CommitState:
//     Accepted → flush its diff; MarkCommitted; pop.
//     Superseded → flush the supersedor's diff in this slot; pop.
//     Rejected + supersedor set → Superseded handling.
//     Rejected + no supersedor → block; wait for supersession or abandonment.
//     Abandoned → pop without flushing.
//     Auditing → wait for audit decision.
//
// Stage 3 implements the resolver with a polling loop. Stage 5 will
// replace polling with event-driven wake (new descriptor enqueued,
// audit decision recorded, supersession/abandonment signaled).
type CommitResolver struct {
	session      *SessionVFS
	logger       *slog.Logger
	pollInterval time.Duration

	mu     sync.Mutex
	stopCh chan struct{}
	doneCh chan struct{}
}

// CommitResolverConfig configures a CommitResolver.
type CommitResolverConfig struct {
	// Session is the SessionVFS whose CommitQueue + DiskFlusher the
	// resolver operates on. Required.
	Session *SessionVFS

	// Logger is the slog logger for structured events. Optional; if nil,
	// slog.Default is used.
	Logger *slog.Logger

	// PollInterval sets the busy-wait interval between head inspections.
	// Default: 100ms. Short enough to be responsive, long enough to
	// avoid CPU burn.
	PollInterval time.Duration
}

// NewCommitResolver returns a new resolver bound to the given session's
// queue. Call Start to begin processing.
func NewCommitResolver(cfg CommitResolverConfig) *CommitResolver {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &CommitResolver{
		session:      cfg.Session,
		logger:       logger,
		pollInterval: interval,
	}
}

// Start begins the resolver's background loop. Idempotent; subsequent
// calls are no-ops while the loop is running.
func (r *CommitResolver) Start(ctx context.Context) {
	r.mu.Lock()
	if r.stopCh != nil {
		r.mu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	r.stopCh = stopCh
	r.doneCh = doneCh
	r.mu.Unlock()

	go r.loop(ctx, stopCh, doneCh)
}

// Stop halts the loop and waits for it to finish. Idempotent.
func (r *CommitResolver) Stop() {
	r.mu.Lock()
	stopCh := r.stopCh
	doneCh := r.doneCh
	r.stopCh = nil
	r.doneCh = nil
	r.mu.Unlock()

	if stopCh == nil {
		return
	}
	close(stopCh)
	if doneCh != nil {
		<-doneCh
	}
}

func (r *CommitResolver) loop(ctx context.Context, stopCh, doneCh chan struct{}) {
	defer close(doneCh)

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// tick performs a single commit-resolver step: inspect the queue head,
// act on its state if terminal-resolvable.
func (r *CommitResolver) tick(ctx context.Context) {
	if r.session == nil || r.session.commitQueue == nil {
		return
	}
	queue := r.session.commitQueue

	for i := 0; i < 16; i++ {
		head := queue.Head()
		if head == nil {
			return
		}
		acted, err := r.processHead(ctx, queue, head)
		if err != nil {
			r.logger.Warn("commit resolver: processHead error",
				"version", head.Descriptor.MergedVersion.String(),
				"state", string(head.State),
				"error", err.Error(),
			)
			return
		}
		if !acted {
			return
		}
		// Loop to process the next head in the same tick, bounded by
		// the outer for-loop cap so we don't starve the poll interval.
	}
}

// processHead acts on the current queue head according to its state.
// Returns (acted, err):
//   - acted=true when the head was popped (caller may process next head).
//   - acted=false when the head is not yet terminal-resolvable.
func (r *CommitResolver) processHead(ctx context.Context, queue *CommitQueue, head *CommitEntry) (bool, error) {
	switch head.State {
	case CommitStateAuditing:
		// Wait for audit decision.
		return false, nil

	case CommitStateAccepted:
		if err := r.flushToDisk(ctx, head.Descriptor); err != nil {
			return false, fmt.Errorf("flush accepted %s: %w", head.Descriptor.MergedVersion.String(), err)
		}
		if err := queue.MarkCommitted(head.Descriptor.MergedVersion); err != nil {
			return false, err
		}
		popped, ok := queue.Advance()
		if ok {
			r.logger.Info("parallel_global_vfs.disk_commit",
				"version", popped.Descriptor.MergedVersion.String(),
				"pipeline_id", popped.Descriptor.PipelineID,
				"path_count", popped.Descriptor.PathCount,
			)
		}
		return ok, nil

	case CommitStateRejected:
		// Blocked; wait for supersession or abandonment.
		return false, nil

	case CommitStateSuperseded:
		supersedor := queue.Lookup(head.SupersededBy)
		if supersedor == nil {
			return false, fmt.Errorf("superseded head %s references missing supersedor %s", head.Descriptor.MergedVersion.String(), head.SupersededBy.String())
		}
		if supersedor.State != CommitStateAccepted {
			// Wait until the supersedor is accepted (its audit may
			// still be in flight). The resolver retries on next tick.
			return false, nil
		}
		if err := r.flushToDisk(ctx, supersedor.Descriptor); err != nil {
			return false, fmt.Errorf("flush supersedor %s: %w", supersedor.Descriptor.MergedVersion.String(), err)
		}
		// Mark the supersedor committed (its work has been flushed).
		if err := queue.MarkCommitted(supersedor.Descriptor.MergedVersion); err != nil {
			return false, err
		}
		popped, ok := queue.Advance()
		if ok {
			r.logger.Info("parallel_global_vfs.disk_commit_via_supersession",
				"rejected_version", popped.Descriptor.MergedVersion.String(),
				"supersedor_version", supersedor.Descriptor.MergedVersion.String(),
				"rejected_pipeline", popped.Descriptor.PipelineID,
				"supersedor_pipeline", supersedor.Descriptor.PipelineID,
			)
		}
		return ok, nil

	case CommitStateAbandoned:
		popped, ok := queue.Advance()
		if ok {
			r.logger.Info("parallel_global_vfs.abandoned",
				"version", popped.Descriptor.MergedVersion.String(),
				"pipeline_id", popped.Descriptor.PipelineID,
				"reason", popped.RejectionReason,
			)
		}
		return ok, nil

	case CommitStateCommitted:
		// Should be popped by Advance already; safety net.
		_, ok := queue.Advance()
		return ok, nil
	}
	return false, nil
}

// flushToDisk invokes the session's DiskFlusher for the descriptor's
// changeset. Under the current DiskFlusher API, flushing commits the
// current global VFS overlay deltas to disk — NOT just this merge's
// diff. Stage 3 therefore uses a whole-overlay flush, which is
// equivalent to flushing the union of all accepted merges since the
// last disk commit. Stage 4 will refine this to per-descriptor diff
// isolation (so that a blocked rejection does not hold up the whole
// overlay's flush cadence).
func (r *CommitResolver) flushToDisk(ctx context.Context, desc MergeDescriptor) error {
	if r.session == nil || r.session.diskFlusher == nil {
		return fmt.Errorf("commit resolver: disk flusher unavailable")
	}
	_, err := r.session.diskFlusher.Flush(ctx)
	if err != nil {
		return err
	}
	return nil
}

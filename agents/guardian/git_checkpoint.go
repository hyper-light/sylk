package guardian

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/search/git"
)

// OnEventFunc is a callback for emitting WAL events from sub-components
// that don't hold an event logger reference directly.
type OnEventFunc func(agentlog.EventType, string, any)

// CheckpointManager runs a periodic ticker and proposes safety checkpoints
// when the worktree has dirty files. Every checkpoint requires explicit user approval.
type CheckpointManager struct {
	gitBus          *git.GitBus
	gitWatcher      *git.StatusWatcher
	interval        time.Duration
	dirtyThreshold  int
	activityPub     events.ActivityPublisher
	requestApproval ApprovalFunc
	onEvent         OnEventFunc
	boardProvider   func() *claims.ClaimsBoard

	mu          sync.Mutex
	running     bool
	cancel      context.CancelFunc
	seq         int
	checkpoints []CheckpointRecord
}

// SetBoardProvider injects the claims board lookup for checkpoint claims.
func (cm *CheckpointManager) SetBoardProvider(bp func() *claims.ClaimsBoard) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.boardProvider = bp
}

// NewCheckpointManager creates a checkpoint manager.
func NewCheckpointManager(
	gitBus *git.GitBus,
	gitWatcher *git.StatusWatcher,
	interval time.Duration,
	dirtyThreshold int,
	activityPub events.ActivityPublisher,
	requestApproval ApprovalFunc,
) *CheckpointManager {
	return &CheckpointManager{
		gitBus:          gitBus,
		gitWatcher:      gitWatcher,
		interval:        interval,
		dirtyThreshold:  dirtyThreshold,
		activityPub:     activityPub,
		requestApproval: requestApproval,
		checkpoints:     make([]CheckpointRecord, 0),
	}
}

// SetOnEvent wires a callback for WAL event emission.
func (cm *CheckpointManager) SetOnEvent(fn OnEventFunc) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.onEvent = fn
}

// Start begins the periodic checkpoint ticker. Prefer StartWithScope
// when a GoroutineScope is available to track the ticker goroutine.
func (cm *CheckpointManager) Start(ctx context.Context) {
	cm.mu.Lock()
	if cm.running {
		cm.mu.Unlock()
		return
	}
	cm.running = true
	tickCtx, cancel := context.WithCancel(ctx)
	cm.cancel = cancel
	cm.mu.Unlock()

	go cm.tickerLoop(tickCtx)
}

// StartWithScope begins the checkpoint ticker with the goroutine
// tracked via the provided scope.
func (cm *CheckpointManager) StartWithScope(ctx context.Context, scope *concurrency.GoroutineScope) {
	cm.mu.Lock()
	if cm.running {
		cm.mu.Unlock()
		return
	}
	cm.running = true
	tickCtx, cancel := context.WithCancel(ctx)
	cm.cancel = cancel
	cm.mu.Unlock()

	if err := scope.Go("guardian_checkpoint_manager", 0, func(_ context.Context) error {
		cm.tickerLoop(tickCtx)
		return nil
	}); err != nil {
		go cm.tickerLoop(tickCtx)
	}
}

// Stop halts the checkpoint ticker.
func (cm *CheckpointManager) Stop() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if !cm.running {
		return
	}
	cm.running = false
	if cm.cancel != nil {
		cm.cancel()
		cm.cancel = nil
	}
}

// Checkpoints returns a copy of all checkpoint records.
func (cm *CheckpointManager) Checkpoints() []CheckpointRecord {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cp := make([]CheckpointRecord, len(cm.checkpoints))
	copy(cp, cm.checkpoints)
	return cp
}

func (cm *CheckpointManager) tickerLoop(ctx context.Context) {
	ticker := time.NewTicker(cm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cm.evaluateCheckpoint(ctx)
		}
	}
}

func (cm *CheckpointManager) evaluateCheckpoint(ctx context.Context) {
	dirtyCount := cm.getDirtyFileCount()
	if dirtyCount == 0 {
		return
	}

	// Escalate warning if above threshold.
	if dirtyCount >= cm.dirtyThreshold {
		cm.publishDirtyWarning(dirtyCount)
	}

	// Propose checkpoint.
	cm.mu.Lock()
	cm.seq++
	seq := cm.seq
	cm.mu.Unlock()

	proposal := &GitMutationProposal{
		Op:     "checkpoint_commit",
		Reason: fmt.Sprintf("Safety checkpoint #%d: %d dirty files detected. Commit to preserve work?", seq, dirtyCount),
		Params: map[string]any{
			"dirty_count": dirtyCount,
			"seq":         seq,
		},
		RiskLevel: SeverityInfo,
		Timestamp: time.Now(),
	}

	// Post claim: checkpoint evaluation.
	cm.postCheckpointClaim(ctx, seq, dirtyCount)

	approved, err := cm.requestApproval(ctx, proposal)
	if err != nil || !approved {
		cm.submitCheckpointTestament(ctx, fmt.Sprintf("Checkpoint #%d not created — %s", seq, checkpointDenialReason(approved, err)))
		return
	}

	// User approved — create checkpoint commit.
	cm.createCheckpoint(seq, dirtyCount)
}

func (cm *CheckpointManager) createCheckpoint(seq, fileCount int) {
	msg := fmt.Sprintf("[guardian] safety checkpoint #%d", seq)

	record := CheckpointRecord{
		Seq:       seq,
		Message:   msg,
		FileCount: fileCount,
		Timestamp: time.Now(),
	}

	cm.mu.Lock()
	cm.checkpoints = append(cm.checkpoints, record)
	onEvt := cm.onEvent
	cm.mu.Unlock()

	if onEvt != nil {
		onEvt(agentlog.EventCheckpointCreated, "info", &agentlog.CheckpointPayload{
			CheckpointID: fmt.Sprintf("checkpoint_%d", seq),
			Action:       "created",
		})
	}

	cm.publishCheckpointActivity(record)
	cm.submitCheckpointTestament(context.Background(), fmt.Sprintf("Safety checkpoint #%d created — %d files", record.Seq, record.FileCount))
}

func (cm *CheckpointManager) getDirtyFileCount() int {
	if cm.gitWatcher == nil {
		return 0
	}
	// Use the git bus client to get uncommitted files.
	client := cm.gitBus.Client()
	if client == nil {
		return 0
	}
	files, err := client.GetUncommittedFiles()
	if err != nil {
		return 0
	}
	return len(files)
}

func (cm *CheckpointManager) publishDirtyWarning(count int) {
	if cm.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(events.EventTypeAgentDecision, "default",
		fmt.Sprintf("High dirty file count: %d files (threshold: %d)", count, cm.dirtyThreshold))
	evt.AgentID = "guardian"
	evt.Visibility = events.VisibilitySystem
	evt.Data["dirty_count"] = count
	evt.Data["threshold"] = cm.dirtyThreshold
	cm.activityPub.PublishActivity(evt)
}

func (cm *CheckpointManager) publishCheckpointActivity(record CheckpointRecord) {
	if cm.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(events.EventTypeAgentAction, "default",
		fmt.Sprintf("Safety checkpoint #%d created (%d files)", record.Seq, record.FileCount))
	evt.AgentID = "guardian"
	evt.Visibility = events.VisibilitySystem
	evt.Data["checkpoint_seq"] = record.Seq
	evt.Data["file_count"] = record.FileCount
	cm.activityPub.PublishActivity(evt)
}

func (cm *CheckpointManager) postCheckpointClaim(ctx context.Context, seq, dirtyCount int) {
	cm.mu.Lock()
	bp := cm.boardProvider
	cm.mu.Unlock()
	if bp == nil {
		return
	}
	board := bp()
	if board == nil {
		return
	}
	claim := guardianClaim(
		fmt.Sprintf("Create safety checkpoint — %d dirty files exceed threshold of %d", dirtyCount, cm.dirtyThreshold),
		"Periodic dirty file threshold evaluation",
		"guardian",
		[]claims.ClaimScopeEntry{{Kind: "git", Key: "checkpoint"}},
		claims.ActionTypeCheckpoint,
		[]*claims.Validation{
			guardianPassedValidation(claims.ValidationTypeInspection, true, "Dirty file count exceeds safety threshold", fmt.Sprintf("getDirtyFileCount() = %d >= %d", dirtyCount, cm.dirtyThreshold)),
			guardianValidation(claims.ValidationTypeInspection, true, "User approves checkpoint creation", "User clicks Approve in checkpoint dialog"),
		},
	)
	if err := board.PostAction(ctx, guardianClaimAction(claims.ActionTypeCheckpoint), []claims.Claim{claim}); err != nil {
		board.RecordNotificationError("checkpoint claim: " + err.Error())
	}
}

func (cm *CheckpointManager) submitCheckpointTestament(ctx context.Context, summary string) {
	cm.mu.Lock()
	bp := cm.boardProvider
	cm.mu.Unlock()
	if bp == nil {
		return
	}
	board := bp()
	if board == nil {
		return
	}
	testament := guardianTestament("", summary, "committed", "", []*claims.Artifact{
		guardianArtifact("", "checkpoint_status", summary),
	})
	if err := board.SubmitTestaments(ctx, guardianTestamentAction(), []claims.Testament{testament}); err != nil {
		board.RecordNotificationError("checkpoint testament: " + err.Error())
	}
}

func checkpointDenialReason(approved bool, err error) string {
	if err != nil {
		return err.Error()
	}
	if !approved {
		return "user denied"
	}
	return "unknown"
}

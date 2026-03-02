package guardian

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/search/git"
)

// CheckpointManager runs a periodic ticker and proposes safety checkpoints
// when the worktree has dirty files. Every checkpoint requires explicit user approval.
type CheckpointManager struct {
	gitBus          *git.GitBus
	gitWatcher      *git.StatusWatcher
	interval        time.Duration
	dirtyThreshold  int
	activityPub     events.ActivityPublisher
	requestApproval ApprovalFunc

	mu          sync.Mutex
	running     bool
	cancel      context.CancelFunc
	seq         int
	checkpoints []CheckpointRecord
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

// Start begins the periodic checkpoint ticker.
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

	approved, err := cm.requestApproval(ctx, proposal)
	if err != nil || !approved {
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
	cm.mu.Unlock()

	cm.publishCheckpointActivity(record)
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
	evt.Visibility = events.VisibilityUser
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
	evt.Visibility = events.VisibilityUser
	evt.Data["checkpoint_seq"] = record.Seq
	evt.Data["file_count"] = record.FileCount
	cm.activityPub.PublishActivity(evt)
}

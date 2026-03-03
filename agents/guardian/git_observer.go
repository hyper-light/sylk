package guardian

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/search/git"
)

// ApprovalFunc is the callback signature for requesting user approval.
type ApprovalFunc func(ctx context.Context, proposal *GitMutationProposal) (bool, error)

// GitObserver subscribes to the GitBus as a wildcard observer and intercepts
// mutating operations on protected branches.
type GitObserver struct {
	gitBus            *git.GitBus
	protectedBranches []string
	activityPub       events.ActivityPublisher
	requestApproval   ApprovalFunc

	unsubscribe func()
	mu          sync.Mutex
	running     bool
	stats       observerStats
	onEvent     OnEventFunc
}

// SetOnEvent wires a callback for WAL event emission.
func (o *GitObserver) SetOnEvent(fn OnEventFunc) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onEvent = fn
}

type observerStats struct {
	mutationsObserved int
	mutationsBlocked  int
	mutationsApproved int
}

// NewGitObserver creates a new git observer that watches for mutating operations.
func NewGitObserver(
	gitBus *git.GitBus,
	protectedBranches []string,
	activityPub events.ActivityPublisher,
	requestApproval ApprovalFunc,
) *GitObserver {
	return &GitObserver{
		gitBus:            gitBus,
		protectedBranches: protectedBranches,
		activityPub:       activityPub,
		requestApproval:   requestApproval,
	}
}

// Start begins observing git events.
func (o *GitObserver) Start() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.running {
		return
	}
	o.running = true
	// Subscribe to all git operations (wildcard).
	o.unsubscribe = o.gitBus.Subscribe(nil, o.handleGitEvent)
}

// Stop ceases observation.
func (o *GitObserver) Stop() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.running {
		return
	}
	o.running = false
	if o.unsubscribe != nil {
		o.unsubscribe()
		o.unsubscribe = nil
	}
}

// Stats returns a snapshot of observer statistics.
func (o *GitObserver) Stats() observerStats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stats
}

func (o *GitObserver) handleGitEvent(event *git.GitEvent) {
	if event == nil {
		return
	}

	// Only process pre-phase mutating operations.
	if event.Phase != git.PhasePre || !git.IsMutating(event.Op) {
		return
	}

	o.mu.Lock()
	o.stats.mutationsObserved++
	o.mu.Unlock()

	// Emit activity event for the mutation attempt.
	o.publishMutationActivity(event)

	// Check if the target branch is protected.
	branch := extractBranchFromEvent(event)
	if branch == "" || !o.isBranchProtected(branch) {
		return
	}

	// Protected branch mutation detected — this is logged.
	// The actual gating happens via PreMutationGate on the GitBus.
	o.mu.Lock()
	onEvt := o.onEvent
	o.mu.Unlock()
	if onEvt != nil {
		onEvt(agentlog.EventDiffReviewed, "warn", &agentlog.DiffPayload{
			Verdict: fmt.Sprintf("protected branch mutation: %s on %q", opName(event.Op), branch),
		})
	}
	o.publishProtectedBranchWarning(event, branch)
}

// GateCheck is called by the PreMutationGate on the GitBus to determine
// whether a mutating operation should be allowed to proceed.
func (o *GitObserver) GateCheck(ctx context.Context, op git.GitOp, params any) error {
	if !git.IsMutating(op) {
		return nil
	}

	branch := extractBranchFromParams(params)
	if branch == "" || !o.isBranchProtected(branch) {
		return nil
	}

	proposal := &GitMutationProposal{
		Op:           opName(op),
		TargetBranch: branch,
		Reason:       fmt.Sprintf("Mutating operation %s on protected branch %q requires approval", opName(op), branch),
		RiskLevel:    SeverityHigh,
		Timestamp:    time.Now(),
	}

	approved, err := o.requestApproval(ctx, proposal)
	o.mu.Lock()
	onEvt := o.onEvent
	o.mu.Unlock()
	if err != nil {
		o.mu.Lock()
		o.stats.mutationsBlocked++
		o.mu.Unlock()
		if onEvt != nil {
			onEvt(agentlog.EventDiffRejected, "error", &agentlog.DiffPayload{
				Verdict: "approval_failed", Reason: err.Error(),
			})
		}
		return fmt.Errorf("guardian: approval request failed: %w", err)
	}
	if !approved {
		o.mu.Lock()
		o.stats.mutationsBlocked++
		o.mu.Unlock()
		if onEvt != nil {
			onEvt(agentlog.EventDiffRejected, "warn", &agentlog.DiffPayload{
				Verdict: "denied", Reason: fmt.Sprintf("%s on %q", opName(op), branch),
			})
		}
		return fmt.Errorf("guardian: user denied mutation %s on protected branch %q", opName(op), branch)
	}

	o.mu.Lock()
	o.stats.mutationsApproved++
	o.mu.Unlock()
	if onEvt != nil {
		onEvt(agentlog.EventDiffApproved, "info", &agentlog.DiffPayload{
			Verdict: "approved", Reason: fmt.Sprintf("%s on %q", opName(op), branch),
		})
	}
	return nil
}

func (o *GitObserver) isBranchProtected(branch string) bool {
	for _, pattern := range o.protectedBranches {
		if matchBranchPattern(pattern, branch) {
			return true
		}
	}
	return false
}

// matchBranchPattern matches a branch name against a glob-like pattern.
func matchBranchPattern(pattern, branch string) bool {
	if pattern == branch {
		return true
	}
	matched, err := filepath.Match(pattern, branch)
	return err == nil && matched
}

func (o *GitObserver) publishMutationActivity(event *git.GitEvent) {
	if o.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(events.EventTypeAgentAction, "default",
		fmt.Sprintf("Git mutation observed: %s", opName(event.Op)))
	evt.AgentID = "guardian"
	evt.Visibility = events.VisibilityAgent
	evt.Data["git_op"] = opName(event.Op)
	o.activityPub.PublishActivity(evt)
}

func (o *GitObserver) publishProtectedBranchWarning(event *git.GitEvent, branch string) {
	if o.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(events.EventTypeAgentDecision, "default",
		fmt.Sprintf("Protected branch mutation: %s on %q", opName(event.Op), branch))
	evt.AgentID = "guardian"
	evt.Visibility = events.VisibilityUser
	evt.Data["git_op"] = opName(event.Op)
	evt.Data["branch"] = branch
	evt.Data["protected"] = true
	o.activityPub.PublishActivity(evt)
}

// extractBranchFromEvent attempts to extract the target branch from a git event.
func extractBranchFromEvent(event *git.GitEvent) string {
	if event == nil || event.Params == nil {
		return ""
	}
	return extractBranchFromParams(event.Params)
}

// extractBranchFromParams attempts to extract a branch name from operation params.
func extractBranchFromParams(params any) string {
	if params == nil {
		return ""
	}
	// Try common param types.
	if s, ok := params.(string); ok {
		return s
	}
	if m, ok := params.(map[string]any); ok {
		for _, key := range []string{"branch", "target_branch", "name", "ref"} {
			if v, found := m[key]; found {
				if s, sOk := v.(string); sOk {
					return s
				}
			}
		}
	}
	return ""
}

func opName(op git.GitOp) string {
	return strings.ReplaceAll(fmt.Sprintf("%d", op), " ", "_")
}

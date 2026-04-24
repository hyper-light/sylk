package guardian

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// git_safety — Domain: safety, Priority: 100
// ---------------------------------------------------------------------------

type gitSafetyInput struct {
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

func gitSafetySkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *gitSafetyInput) (any, error)
	dispatch := map[string]handler{
		"checkpoint": func(ctx context.Context, p *gitSafetyInput) (any, error) {
			if g.checkpointMgr == nil {
				return nil, fmt.Errorf("checkpoint manager not available (no git watcher configured)")
			}
			dirtyCount := g.checkpointMgr.getDirtyFileCount()
			if dirtyCount == 0 {
				return map[string]any{
					"status":      "clean",
					"dirty_count": 0,
					"message":     "Worktree is clean — no checkpoint needed.",
				}, nil
			}
			// Request user approval.
			proposal := &GitMutationProposal{
				Op:     "checkpoint_commit",
				Reason: fmt.Sprintf("Safety checkpoint requested: %d dirty files. Approve?", dirtyCount),
				Params: map[string]any{"dirty_count": dirtyCount},
			}
			if p.Message != "" {
				proposal.Reason = p.Message
			}
			approved, err := g.requestApproval(ctx, proposal)
			if err != nil {
				return nil, fmt.Errorf("approval request failed: %w", err)
			}
			if !approved {
				return map[string]any{
					"status":  "denied",
					"message": "User denied checkpoint.",
				}, nil
			}
			g.checkpointMgr.createCheckpoint(g.checkpointMgr.seq+1, dirtyCount)
			return map[string]any{
				"status":      "created",
				"dirty_count": dirtyCount,
				"message":     "Safety checkpoint created.",
			}, nil
		},
		"dirty_status": func(_ context.Context, _ *gitSafetyInput) (any, error) {
			if g.checkpointMgr == nil {
				return map[string]any{"dirty_count": 0, "available": false}, nil
			}
			count := g.checkpointMgr.getDirtyFileCount()
			return map[string]any{
				"dirty_count": count,
				"threshold":   g.config.DirtyThreshold,
				"above":       count >= g.config.DirtyThreshold,
				"available":   true,
			}, nil
		},
		"protect_check": func(_ context.Context, p *gitSafetyInput) (any, error) {
			if p.Branch == "" {
				return nil, fmt.Errorf("branch is required for protect_check")
			}
			protected := false
			if g.gitObserver != nil {
				protected = g.gitObserver.isBranchProtected(p.Branch)
			}
			return map[string]any{
				"branch":    p.Branch,
				"protected": protected,
				"patterns":  g.config.ProtectedBranches,
			}, nil
		},
		"list_checkpoints": func(_ context.Context, _ *gitSafetyInput) (any, error) {
			if g.checkpointMgr == nil {
				return map[string]any{"checkpoints": []CheckpointRecord{}}, nil
			}
			return map[string]any{
				"checkpoints": g.checkpointMgr.Checkpoints(),
			}, nil
		},
	}

	return skills.NewSkill("git_safety").
		Description("Git safety operations: checkpoint commits, dirty status, branch protection checks.\n\n"+
			"Actions:\n"+
			"- checkpoint: Propose a safety checkpoint commit (requires user approval)\n"+
			"- dirty_status: Check worktree dirty file count and threshold\n"+
			"- protect_check: Check if a branch is protected (params: branch [required])\n"+
			"- list_checkpoints: List all safety checkpoints created this session").
		Domain("safety").
		Keywords("checkpoint", "commit", "safety", "dirty", "protect", "branch", "save").
		Priority(100).
		TokenEstimate(500).
		EnumParam("action", "Safety action", []string{
			"checkpoint", "dirty_status", "protect_check", "list_checkpoints",
		}, true).
		StringParam("message", "Custom checkpoint message (for checkpoint)", false).
		StringParam("branch", "Branch name to check (for protect_check)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params gitSafetyInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown git_safety action: %q", params.Action)
			}

			sessionID := g.activeSessionID
			g.guardianPostClaim(ctx,
				guardianClaimAction(claims.ActionTypeArchival),
				guardianClaim("Git safety: "+params.Action, "Git safety operation", "guardian",
					[]claims.ClaimScopeEntry{{Kind: "git", Key: params.Action}},
					claims.ActionTypeArchival, nil),
			)

			result, err := fn(ctx, &params)
			if err != nil {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, "Git safety "+params.Action+" failed: "+err.Error(), "committed", "",
					[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
				))
			} else {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, "Git safety "+params.Action+" complete", "committed", "",
					[]*claims.Artifact{guardianJSONArtifact(sessionID, "safety_result", result)},
				))
			}
			return result, err
		}).
		Build()
}

// ---------------------------------------------------------------------------
// rollback — Domain: safety, Priority: 90
// ---------------------------------------------------------------------------

type rollbackInput struct {
	Action      string `json:"action"`
	SnapshotRef string `json:"snapshot_ref,omitempty"`
}

func rollbackSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *rollbackInput) (any, error)
	dispatch := map[string]handler{
		"list_snapshots": func(_ context.Context, _ *rollbackInput) (any, error) {
			if g.checkpointMgr == nil {
				return map[string]any{"snapshots": []CheckpointRecord{}}, nil
			}
			return map[string]any{
				"snapshots": g.checkpointMgr.Checkpoints(),
			}, nil
		},
		"rollback": func(ctx context.Context, p *rollbackInput) (any, error) {
			if p.SnapshotRef == "" {
				return nil, fmt.Errorf("snapshot_ref is required for rollback")
			}
			// Rollback requires user approval.
			proposal := &GitMutationProposal{
				Op:     "rollback",
				Reason: fmt.Sprintf("Rollback to snapshot %q requested. This will reset uncommitted changes. Approve?", p.SnapshotRef),
				Params: map[string]any{"snapshot_ref": p.SnapshotRef},
				RiskLevel: SeverityHigh,
			}
			approved, err := g.requestApproval(ctx, proposal)
			if err != nil {
				return nil, fmt.Errorf("approval request failed: %w", err)
			}
			if !approved {
				return map[string]any{
					"status":  "denied",
					"message": "User denied rollback.",
				}, nil
			}
			return map[string]any{
				"status":       "approved",
				"snapshot_ref": p.SnapshotRef,
				"message":      "Rollback approved. Execution delegated to git subsystem.",
			}, nil
		},
	}

	return skills.NewSkill("rollback").
		Description("Rollback operations: list snapshots and initiate rollbacks.\n\n"+
			"Actions:\n"+
			"- list_snapshots: List available snapshots for rollback\n"+
			"- rollback: Initiate rollback to a snapshot (requires user approval)").
		Domain("safety").
		Keywords("rollback", "undo", "revert", "snapshot", "restore").
		Priority(90).
		TokenEstimate(400).
		EnumParam("action", "Rollback action", []string{"list_snapshots", "rollback"}, true).
		StringParam("snapshot_ref", "Snapshot reference for rollback (for rollback)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params rollbackInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown rollback action: %q", params.Action)
			}

			sessionID := g.activeSessionID
			g.guardianPostClaim(ctx,
				guardianClaimAction(claims.ActionTypeCorrective),
				guardianClaim("Rollback: "+params.Action, "Rollback operation", "guardian",
					[]claims.ClaimScopeEntry{{Kind: "git", Key: "rollback"}},
					claims.ActionTypeCorrective,
					[]*claims.Validation{
						guardianValidation(claims.ValidationTypeInspection, true, "User authorizes rollback", "User clicks Approve in rollback dialog"),
					}),
			)

			result, err := fn(ctx, &params)
			if err != nil {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, "Rollback "+params.Action+" failed: "+err.Error(), "committed", "",
					[]*claims.Artifact{guardianArtifact(sessionID, "error", err.Error())},
				))
			} else {
				g.guardianSubmitTestament(ctx, guardianTestamentAction(), guardianTestament(
					sessionID, "Rollback "+params.Action+" complete", "committed", "",
					[]*claims.Artifact{guardianJSONArtifact(sessionID, "rollback_result", result)},
				))
			}
			return result, err
		}).
		Build()
}

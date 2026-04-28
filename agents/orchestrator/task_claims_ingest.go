// Architect-authored task claim ingest at pod spin-up.
//
// Each PlanHandoff task carries a []TaskClaim list, populated by the
// architect during plan generation. These claims must land on the
// session's claims board before the task's pipeline pod begins
// executing — otherwise agents have nothing to validate against and
// progress counters stay at 0/0 because no claims are tracked for
// the work.
//
// Ingest happens deterministically inside ensureTaskPod (after the
// pod struct is built, before it's published to the TaskPods cache).
// One PostAction call per task, bundling every TaskClaim that task
// carried in the handoff. Idempotent via meta.claimsPostedForTask —
// even if ensureTaskPod is invoked multiple times for a task across
// re-prepare/re-execute cycles, we never double-post.
package orchestrator

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/core/claims"
)

// collectTaskClaimsFromHandoff projects PlanHandoff.Tasks into the
// taskID → []TaskClaim shape the bridge stages with
// RegisterTaskClaims. Empty / unkeyed entries are skipped — only
// tasks with a stable ID and at least one architect-authored claim
// produce an entry.
func collectTaskClaimsFromHandoff(h *architect.PlanHandoff) map[string][]architect.TaskClaim {
	if h == nil {
		return nil
	}
	out := make(map[string][]architect.TaskClaim, len(h.Tasks))
	for _, t := range h.Tasks {
		if t == nil {
			continue
		}
		taskID := strings.TrimSpace(t.ID)
		if taskID == "" || len(t.Claims) == 0 {
			continue
		}
		out[taskID] = append([]architect.TaskClaim(nil), t.Claims...)
	}
	return out
}

// postTaskClaimsForPod converts the architect's TaskClaim list into
// the claims-package Claim shape and posts a single PostAction to
// the session board on behalf of the task. Returns nil on success
// and on idempotent skip (already posted); non-nil only when the
// board lookup or post itself failed.
func (b *DAGBridge) postTaskClaimsForPod(ctx context.Context, meta *ActiveDAGMeta, taskID string) error {
	if meta == nil || taskID == "" {
		return nil
	}
	meta.slotMu.Lock()
	if meta.claimsPostedForTask == nil {
		meta.claimsPostedForTask = make(map[string]struct{})
	}
	if _, alreadyPosted := meta.claimsPostedForTask[taskID]; alreadyPosted {
		meta.slotMu.Unlock()
		return nil
	}
	var taskClaims []architect.TaskClaim
	if meta.taskClaims != nil {
		taskClaims = append([]architect.TaskClaim(nil), meta.taskClaims[taskID]...)
	}
	if len(taskClaims) == 0 {
		// No architect-authored claims for this task. Mark as posted
		// so future ensureTaskPod calls don't keep checking.
		meta.claimsPostedForTask[taskID] = struct{}{}
		meta.slotMu.Unlock()
		return nil
	}
	// Mark as in-flight under the lock so concurrent ensure calls
	// don't double-post. We drop the lock before the board call so
	// a slow PostAction doesn't block other ensureTaskPod work.
	meta.claimsPostedForTask[taskID] = struct{}{}
	meta.slotMu.Unlock()

	board := claims.DefaultSessionBoardRegistry().Lookup(meta.SessionID)
	if board == nil {
		// No board for this session — roll back the marker so a
		// subsequent attempt can retry once the board is wired up.
		// In practice the board is always registered before the
		// architect ever dispatches, so this branch is rare.
		meta.slotMu.Lock()
		delete(meta.claimsPostedForTask, taskID)
		meta.slotMu.Unlock()
		return nil
	}

	posted := convertTaskClaimsToBoardClaims(taskClaims, taskID, meta.PlanID)
	action := claims.Action{
		AgentID: "orchestrator",
		Type:    claims.ActionTypeTask,
	}
	if err := board.PostAction(ctx, action, posted); err != nil {
		// Roll back so the next ensureTaskPod call retries.
		meta.slotMu.Lock()
		delete(meta.claimsPostedForTask, taskID)
		meta.slotMu.Unlock()
		return err
	}
	return nil
}

// convertTaskClaimsToBoardClaims projects architect TaskClaim entries
// into the claims package Claim shape consumed by the session board.
// Each TaskClaim becomes one Claim with its scope and validations
// preserved verbatim (status defaults to Pending). Task ID and plan
// ID are added to the Scope so cross-task queries can find every
// claim associated with a given task or plan.
func convertTaskClaimsToBoardClaims(src []architect.TaskClaim, taskID, planID string) []claims.Claim {
	out := make([]claims.Claim, 0, len(src))
	for _, tc := range src {
		scope := make([]claims.ClaimScopeEntry, 0, len(tc.Scope)+2)
		scope = append(scope, claims.ClaimScopeEntry{Kind: "task", Key: taskID})
		if planID != "" {
			scope = append(scope, claims.ClaimScopeEntry{Kind: "plan", Key: planID})
		}
		for _, s := range tc.Scope {
			kind := strings.TrimSpace(s.Kind)
			key := strings.TrimSpace(s.Key)
			if kind == "" || key == "" {
				continue
			}
			scope = append(scope, claims.ClaimScopeEntry{Kind: kind, Key: key})
		}

		validations := make([]*claims.Validation, 0, len(tc.Validations))
		for _, v := range tc.Validations {
			validations = append(validations, &claims.Validation{
				Type:        validationTypeFromString(v.Type),
				Required:    true,
				Description: v.Description,
				QualityBar:  v.QualityBar,
				Status:      claims.ValidationStatusPending,
			})
		}

		relations := []claims.Relation{
			{Related: "orchestrator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		}
		if subject := strings.TrimSpace(tc.Subject); subject != "" {
			relations = append(relations, claims.Relation{
				Related:      subject,
				RelatedType:  claims.RelatedTypeAgent,
				Relationship: claims.RelationshipSubject,
			})
		}

		out = append(out, claims.Claim{
			Title:       tc.Title,
			Description: tc.Description,
			ActionType:  claims.ActionTypeTask,
			Scope:       scope,
			Relations:   relations,
			Validations: validations,
		})
	}
	return out
}

// validationTypeFromString maps the architect's free-form validation
// type string to the claims package enum. Unknown strings fall
// through to Inspection — consistent with treating any validation as
// at least an inspection-class check.
func validationTypeFromString(t string) claims.ValidationType {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "test":
		return claims.ValidationTypeTest
	case "inspection":
		return claims.ValidationTypeInspection
	case "integration":
		return claims.ValidationTypeIntegration
	case "contract":
		return claims.ValidationTypeContract
	case "design":
		return claims.ValidationTypeDesign
	case "regression":
		return claims.ValidationTypeRegression
	case "receipt":
		return claims.ValidationTypeReceipt
	default:
		return claims.ValidationTypeInspection
	}
}

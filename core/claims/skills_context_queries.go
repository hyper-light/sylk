package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// Priority range for context-query skills. All lower than the core
// mutation skills so the LLM surfaces them only after the primary
// intake/emission affordances.
const (
	priorityTraceAncestry      = 80
	priorityListActionClaims   = 79
	priorityTraceCausality     = 78
	priorityFindOverlapping    = 77
	priorityShowValidationHist = 76
	priorityListTestaments     = 75
	priorityRecallArtifact     = 74
	priorityShowPhaseHistory   = 73
)

// ────────────────────────────────────────────────────────────────────
// trace_claim_ancestry
// ────────────────────────────────────────────────────────────────────

// TraceClaimAncestrySkill walks the supersedes / amends / refines
// chain rooted at a claim, returning the chain in oldest-to-newest
// order. Implemented via Traverse with unlimited depth.
func TraceClaimAncestrySkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("trace_claim_ancestry").
		Description("Walk the supersedes / amends / refines chain of a claim. Returns the chain from oldest ancestor to the queried claim.").
		Domain("claims").
		Keywords("ancestry", "supersedes", "amends", "history").
		Priority(priorityTraceAncestry).
		StringParam("claim_id", "ID of the claim to root the walk at", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			var params struct {
				ClaimID string `json:"claim_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			claimID := strings.TrimSpace(params.ClaimID)
			nodes := Traverse(board, claimID, "supersedes|amends|refines", 100)
			chain := extractClaims(nodes)
			return map[string]any{
				"claim_id": claimID,
				"chain":    chain,
				"depth":    len(chain),
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// list_action_claims
// ────────────────────────────────────────────────────────────────────

func ListActionClaimsSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("list_action_claims").
		Description("List every claim belonging to the given action (siblings under the same claim_action Relation).").
		Domain("claims").
		Keywords("siblings", "action", "claims").
		Priority(priorityListActionClaims).
		StringParam("action_id", "ID of the action to expand", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			var params struct {
				ActionID string `json:"action_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			actionID := strings.TrimSpace(params.ActionID)
			nodes := Traverse(board, actionID, RelationshipClaimAction, 1)
			claims := extractClaims(nodes)
			return map[string]any{
				"action_id": actionID,
				"claims":    claims,
				"count":     len(claims),
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// trace_action_causality
// ────────────────────────────────────────────────────────────────────

func TraceActionCausalitySkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("trace_action_causality").
		Description("Walk the caused_by chain back to the originating action. Returns actions from root cause to the queried action.").
		Domain("claims").
		Keywords("causality", "caused_by", "lineage", "action").
		Priority(priorityTraceCausality).
		StringParam("action_id", "ID of the action to root the walk at", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			var params struct {
				ActionID string `json:"action_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			actionID := strings.TrimSpace(params.ActionID)
			nodes := Traverse(board, actionID, RelationshipCausedBy, 100)
			actions := extractActions(nodes)
			return map[string]any{
				"action_id": actionID,
				"chain":     actions,
				"depth":     len(actions),
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// find_overlapping_claims
// ────────────────────────────────────────────────────────────────────

func FindOverlappingClaimsSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("find_overlapping_claims").
		Description("Find claims whose scope contains the queried (kind, key). Backed by the board's scope index (O(1) lookup).").
		Domain("claims").
		Keywords("overlap", "scope", "conflict", "file", "symbol").
		Priority(priorityFindOverlapping).
		StringParam("scope_kind", "Scope kind: file, symbol, api, test_surface, component, ux_surface", true).
		StringParam("scope_key", "Scope key: file path, symbol name, endpoint, etc.", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			var params struct {
				Kind string `json:"scope_kind"`
				Key  string `json:"scope_key"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			ids := board.ClaimIDsWithScope(strings.TrimSpace(params.Kind), strings.TrimSpace(params.Key))
			claims := collectClaimsByIDs(board, ids)
			return map[string]any{
				"scope_kind": params.Kind,
				"scope_key":  params.Key,
				"claims":     claims,
				"count":      len(claims),
			}, nil
		}).
		Build()
}

func collectClaimsByIDs(board *ClaimsBoard, ids []string) []Claim {
	if len(ids) == 0 {
		return nil
	}
	out := make([]Claim, 0, len(ids))
	for _, id := range ids {
		if c, ok := board.CloneClaim(id); ok {
			out = append(out, *c)
		}
	}
	return out
}

// ────────────────────────────────────────────────────────────────────
// show_validation_history
// ────────────────────────────────────────────────────────────────────

func ShowValidationHistorySkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("show_validation_history").
		Description("List every StatusChange on every validation attached to the claim.").
		Domain("claims").
		Keywords("validation", "history", "audit", "status").
		Priority(priorityShowValidationHist).
		StringParam("claim_id", "ID of the claim whose validations to inspect", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			var params struct {
				ClaimID string `json:"claim_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			claimID := strings.TrimSpace(params.ClaimID)
			c, ok := board.CloneClaim(claimID)
			if !ok {
				return nil, fmt.Errorf("claim %q not found", claimID)
			}
			entries := collectValidationHistory(c)
			return map[string]any{
				"claim_id": claimID,
				"entries":  entries,
				"count":    len(entries),
			}, nil
		}).
		Build()
}

type validationHistoryEntry struct {
	ValidationID  string           `json:"validation_id"`
	Description   string           `json:"description"`
	History       []StatusChange   `json:"history"`
	CurrentStatus ValidationStatus `json:"current_status"`
}

func collectValidationHistory(c *Claim) []validationHistoryEntry {
	out := make([]validationHistoryEntry, 0, len(c.Validations))
	for _, v := range c.Validations {
		out = append(out, validationHistoryEntry{
			ValidationID:  v.ID,
			Description:   v.Description,
			History:       v.StatusHistory,
			CurrentStatus: v.Status,
		})
	}
	return out
}

// ────────────────────────────────────────────────────────────────────
// list_testaments_for_claim
// ────────────────────────────────────────────────────────────────────

func ListTestamentsForClaimSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("list_testaments_for_claim").
		Description("List every testament submitted against the claim (including superseded ones) in submission order.").
		Domain("claims").
		Keywords("testament", "history", "proof", "claim").
		Priority(priorityListTestaments).
		StringParam("claim_id", "ID of the claim whose testaments to list", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			var params struct {
				ClaimID string `json:"claim_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			claimID := strings.TrimSpace(params.ClaimID)
			nodes := Traverse(board, claimID, RelationshipTestament, 1)
			testaments := extractTestaments(nodes)
			return map[string]any{
				"claim_id":   claimID,
				"testaments": testaments,
				"count":      len(testaments),
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// recall_artifact
// ────────────────────────────────────────────────────────────────────

func RecallArtifactSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("recall_artifact").
		Description("Return the full Artifact record (Reference, ContentHash, Metadata) for an artifact ID previously reported in a TestamentDelta.").
		Domain("claims").
		Keywords("artifact", "evidence", "recall", "content").
		Priority(priorityRecallArtifact).
		StringParam("artifact_id", "ID of the artifact to recall", true).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			var params struct {
				ArtifactID string `json:"artifact_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			artifactID := strings.TrimSpace(params.ArtifactID)
			artifact := findArtifactByID(board, artifactID)
			if artifact == nil {
				return nil, fmt.Errorf("artifact %q not found", artifactID)
			}
			return artifact, nil
		}).
		Build()
}

func findArtifactByID(board *ClaimsBoard, artifactID string) *Artifact {
	proj := board.Projection()
	for i := range proj.Testaments {
		for _, a := range proj.Testaments[i].Artifacts {
			if a != nil && a.ID == artifactID {
				clone := *a
				return &clone
			}
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────
// show_phase_history
// ────────────────────────────────────────────────────────────────────

func ShowPhaseHistorySkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("show_phase_history").
		Description("List every phase transition on this board with reason and timestamp.").
		Domain("claims").
		Keywords("phase", "history", "transitions").
		Priority(priorityShowPhaseHistory).
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) {
			board, err := requireBoard(bp)
			if err != nil {
				return nil, err
			}
			history := board.PhaseHistory()
			return map[string]any{
				"entries": history,
				"count":   len(history),
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

// requireBoard returns the active board or a uniform error.
func requireBoard(bp BoardProvider) (*ClaimsBoard, error) {
	if bp == nil {
		return nil, fmt.Errorf("claims board provider not configured")
	}
	board := bp()
	if board == nil {
		return nil, fmt.Errorf("claims board not available")
	}
	return board, nil
}

// extractClaims collects Claim pointers from traversal results.
func extractClaims(nodes []GraphNode) []Claim {
	out := make([]Claim, 0, len(nodes))
	for _, n := range nodes {
		if n.Claim != nil {
			out = append(out, *n.Claim)
		}
	}
	return out
}

// extractActions collects Action pointers from traversal results.
func extractActions(nodes []GraphNode) []Action {
	out := make([]Action, 0, len(nodes))
	for _, n := range nodes {
		if n.Action != nil {
			out = append(out, *n.Action)
		}
	}
	return out
}

// extractTestaments collects Testament pointers from traversal results.
func extractTestaments(nodes []GraphNode) []Testament {
	out := make([]Testament, 0, len(nodes))
	for _, n := range nodes {
		if n.Testament != nil {
			out = append(out, *n.Testament)
		}
	}
	return out
}

// AllContextQuerySkills returns every named board-context query
// skill plus the universal traverse skill.
func AllContextQuerySkills(bp BoardProvider) []*skills.Skill {
	return []*skills.Skill{
		TraverseSkill(bp),
		TraceClaimAncestrySkill(bp),
		ListActionClaimsSkill(bp),
		TraceActionCausalitySkill(bp),
		FindOverlappingClaimsSkill(bp),
		ShowValidationHistorySkill(bp),
		ListTestamentsForClaimSkill(bp),
		RecallArtifactSkill(bp),
		ShowPhaseHistorySkill(bp),
	}
}

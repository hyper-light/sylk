// Architect-side caller for Guardian's batched preflight_plan skill.
//
// Replaces the orchestrator's post-approval guardianPreflightPlan loop
// (N synchronous bus RPCs, one per task) with a single batched call
// during plan finalization. Guardian remains the canonical gate: it
// authors the verdict, the architect just orchestrates when Guardian
// is consulted (before approval, not after) and what the verdict
// travels with (PlanHandoff, not orchestrator metadata).
//
// Failure mode: if Guardian is unreachable, plan presentation is
// blocked. The architect refuses to ship un-vetted plans rather than
// presenting one and discovering Guardian's verdict only at dispatch.
package architect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/google/uuid"
)

// preflightPlanSkillName / preflightPlanAction stay in lock-step with
// guardian's review_gate enum. A typo here surfaces as Guardian
// returning "unknown review_gate action" rather than a silent miss.
const (
	preflightPlanSkillName = "review_gate"
	preflightPlanAction    = "preflight_plan"
)

// guardianPreflightTimeout bounds the wait on Guardian's response.
// Guardian's classifier is deterministic and in-memory (no LLM, no
// I/O) and the review_gate skill runs on Guardian's fast-path
// (concurrent with serialized requests, see runFastPathDirectSkill),
// so even when Guardian is busy with other gates the round-trip
// should be milliseconds.
//
// 10s is a generous ceiling that catches real failures (bus dead,
// Guardian crashed) without false-positiving on transient slowness.
// On expiry we fail the plan rather than ship un-vetted: Guardian
// is the canonical gate authority and bypassing it silently is
// worse than surfacing the problem to the user.
const guardianPreflightTimeout = 10 * time.Second

// computePlanPreflightInput builds the request payload Guardian sees,
// using the same fields the orchestrator's per-task preflight loop
// used to send. Preserves classifier semantics — the only thing that
// changes is the call site (architect, not orchestrator) and the
// batching (one RPC, not N).
func computePlanPreflightInput(plan *DesignPlan) (request map[string]any, contentHash string) {
	type taskInput struct {
		TaskID              string   `json:"task_id"`
		AgentType           string   `json:"agent_type,omitempty"`
		Complexity          string   `json:"complexity,omitempty"`
		AffectedFiles       []string `json:"affected_files,omitempty"`
		RiskFactors         []string `json:"risk_factors,omitempty"`
		TestRequirements    []string `json:"test_requirements,omitempty"`
		Guidelines          []string `json:"guidelines,omitempty"`
		ImplementationGuide string   `json:"implementation_guide,omitempty"`
	}
	tasks := make([]taskInput, 0, len(plan.Tasks))
	for _, t := range plan.Tasks {
		if t == nil || strings.TrimSpace(t.ID) == "" {
			continue
		}
		tasks = append(tasks, taskInput{
			TaskID:              t.ID,
			AgentType:           t.AgentType,
			Complexity:          t.Complexity.String(),
			AffectedFiles:       affectedFilePaths(t.AffectedFiles),
			RiskFactors:         t.RiskFactors,
			TestRequirements:    t.TestRequirements,
			Guidelines:          t.Guidelines,
			ImplementationGuide: t.ImplementationGuide,
		})
	}
	// Stable order so the content hash is deterministic. The
	// orchestrator recomputes the hash from the received handoff so
	// any divergence here would surface immediately.
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskID < tasks[j].TaskID })

	hashPayload := struct {
		PlanID   string      `json:"plan_id"`
		Revision int         `json:"revision"`
		Tasks    []taskInput `json:"tasks"`
	}{
		PlanID:   plan.ID,
		Revision: plan.Revision,
		Tasks:    tasks,
	}
	raw, _ := json.Marshal(hashPayload)
	sum := sha256.Sum256(raw)
	contentHash = hex.EncodeToString(sum[:])

	request = map[string]any{
		"action":            preflightPlanAction,
		"plan_id":           plan.ID,
		"plan_revision":     plan.Revision,
		"plan_content_hash": contentHash,
		"tasks":             tasks,
	}
	return request, contentHash
}

// affectedFilePaths flattens TaskFileTarget entries to a string slice
// for Guardian's per-path classifier. Mirrors the orchestrator helper
// of the same name (kept private because the call site here is a
// different package).
func affectedFilePaths(targets []TaskFileTarget) []string {
	if len(targets) == 0 {
		return nil
	}
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		path := strings.TrimSpace(t.Path)
		if path == "" {
			continue
		}
		out = append(out, path)
	}
	return out
}

// requestPlanPreflight issues the batched preflight to Guardian and
// decodes the response. Returns the Guardian-issued attestation and
// the content hash the architect computed for this plan revision.
//
// On error, returns nil — callers must refuse to present an
// un-vetted plan. The cost is paid during plan generation (where
// the user is already waiting on the LLM); a Guardian failure surfaces
// before the user sees the dialog rather than after they click approve.
func (a *Architect) requestPlanPreflight(ctx context.Context, plan *DesignPlan) (*agentshared.PlanPreflightAttestation, string, error) {
	if a == nil || a.bus == nil || !a.running {
		return nil, "", fmt.Errorf("architect: bus unavailable for guardian preflight")
	}
	if plan == nil {
		return nil, "", fmt.Errorf("architect: plan required for guardian preflight")
	}
	guardianID := a.knownAgentIDByType("guardian", "")
	if strings.TrimSpace(guardianID) == "" {
		return nil, "", fmt.Errorf("architect: no registered guardian for preflight")
	}

	request, contentHash := computePlanPreflightInput(plan)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("architect: marshal preflight request: %w", err)
	}

	preflightCtx, cancel := context.WithTimeout(ctx, guardianPreflightTimeout)
	defer cancel()

	req := &guide.RouteRequest{
		CorrelationID: "plan_preflight_" + uuid.NewString(),
		Input:         string(payload),
		TargetAgentID: guardianID,
		SessionID:     plan.SessionID,
		Metadata: map[string]any{
			"direct_skill": preflightPlanSkillName,
			"plan_id":      plan.ID,
		},
	}
	resp, err := a.requestRouteSync(preflightCtx, req)
	if err != nil || resp == nil {
		return nil, contentHash, fmt.Errorf("architect: guardian preflight RPC failed: %w", err)
	}
	att, err := decodePlanPreflightAttestation(resp)
	if err != nil {
		return nil, contentHash, fmt.Errorf("architect: decode guardian preflight response: %w", err)
	}
	if att == nil {
		return nil, contentHash, fmt.Errorf("architect: guardian preflight returned nil attestation")
	}
	if att.PlanContentHash != contentHash {
		return nil, contentHash, fmt.Errorf("architect: guardian preflight hash mismatch (got %q, expected %q)", att.PlanContentHash, contentHash)
	}
	return att, contentHash, nil
}

// preflightErrString returns "" for nil errors so log lines don't
// carry literal "<nil>" markers when Guardian merely returned a nil
// attestation without a transport error.
func preflightErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// decodePlanPreflightAttestation extracts the typed attestation from
// Guardian's response. Tolerant of multiple delivery shapes (typed
// pointer vs map[string]any vs canonical JSON) so transport variations
// don't surface as decode failures.
func decodePlanPreflightAttestation(msg *guide.Message) (*agentshared.PlanPreflightAttestation, error) {
	resp, _, err := routeResponseFromMessage(msg)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("guardian preflight failed: %s", resp.Error)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("guardian preflight returned nil data")
	}
	switch typed := resp.Data.(type) {
	case *agentshared.PlanPreflightAttestation:
		return typed, nil
	case agentshared.PlanPreflightAttestation:
		return &typed, nil
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("encode preflight data for fallback decode: %w", err)
	}
	var att agentshared.PlanPreflightAttestation
	if err := json.Unmarshal(raw, &att); err != nil {
		return nil, fmt.Errorf("decode preflight data: %w", err)
	}
	return &att, nil
}

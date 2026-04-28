// Orchestrator-side verifier for Guardian's batched plan preflight
// attestation. Replaces the post-approval N-sequential-RPC loop in
// guardianPreflightPlan with a single content-hash + coverage check.
//
// Guardian remains the gate authority — this code does NOT re-derive
// risk classification. It verifies that Guardian issued a current
// verdict for THIS plan (matching content hash, current classifier
// version, full per-task coverage) and converts the attestation to
// the existing guardianPlanPreflight metadata shape so downstream
// code (createWorkflowAndTasks) sees the same per-task verdicts it
// would have seen from the old RPC loop.
package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guardian"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

// verifyAndAdoptAttestation validates a Guardian-issued attestation
// against the received handoff and converts it to the orchestrator's
// internal preflight metadata shape.
//
// Returns:
//   - non-nil preflight on success (caller skips the RPC loop)
//   - error on verification failure (caller fails ingest — a
//     mismatched attestation is more dangerous than no attestation,
//     since it suggests tampering or wire-level corruption)
//   - (nil, nil) when the attestation is absent (caller falls back to
//     the legacy RPC loop)
func verifyAndAdoptAttestation(handoff *architect.PlanHandoff) (*guardianPlanPreflight, error) {
	if handoff == nil || handoff.GuardianAttestation == nil {
		return nil, nil
	}
	att := handoff.GuardianAttestation

	// Recompute the content hash from the received handoff and
	// compare. Any divergence here means the attestation was issued
	// for a different plan revision than what arrived — refuse.
	expectedHash := computeHandoffPreflightHash(handoff)
	if !strings.EqualFold(att.PlanContentHash, expectedHash) {
		return nil, fmt.Errorf("guardian attestation hash mismatch: attestation=%q recomputed=%q", att.PlanContentHash, expectedHash)
	}

	// ClassifierVersion gate — reject verdicts produced under an
	// older rule set. Within a single binary this is a no-op (both
	// sides see the same constant); across upgrades it forces
	// re-issuance.
	if att.ClassifierVersion != guardian.GuardianPreflightClassifierVersion {
		return nil, fmt.Errorf("guardian attestation classifier_version=%q (expected %q) — re-issue required",
			att.ClassifierVersion, guardian.GuardianPreflightClassifierVersion)
	}

	// PerTask coverage — every task in the handoff must have a
	// verdict. Absent verdicts mean the architect skipped a task or
	// the wire payload is incomplete; either way, the dispatch would
	// be ungated for those tasks.
	preflight := &guardianPlanPreflight{
		PlanID: att.PlanID,
		Tasks:  make(map[string]*guardianTaskPreflight, len(att.PerTask)),
	}
	for _, task := range handoff.Tasks {
		if task == nil || strings.TrimSpace(task.ID) == "" {
			continue
		}
		v, ok := att.PerTask[task.ID]
		if !ok || v == nil {
			return nil, fmt.Errorf("guardian attestation missing verdict for task %q", task.ID)
		}
		preflight.Tasks[task.ID] = adaptVerdictToPreflight(v)
	}
	return preflight, nil
}

// adaptVerdictToPreflight converts the typed shared verdict into the
// orchestrator's existing guardianTaskPreflight shape so downstream
// code (createWorkflowAndTasks task metadata) sees an identical
// payload to the old RPC-loop output.
func adaptVerdictToPreflight(v *agentshared.PlanPreflightTaskVerdict) *guardianTaskPreflight {
	return &guardianTaskPreflight{
		TaskID:           v.TaskID,
		AgentType:        v.AgentType,
		Clean:            v.Clean,
		RequiresApproval: v.RequiresApproval,
		Warnings:         append([]string(nil), v.Warnings...),
		BlockingFindings: append([]string(nil), v.BlockingFindings...),
		UserMessage:      v.UserMessage,
	}
}

// computeHandoffPreflightHash reconstructs the canonical-JSON hash of
// the preflight inputs the architect computed in
// computePlanPreflightInput. MUST stay byte-identical to the
// architect's hashing or verification will reject every attestation.
func computeHandoffPreflightHash(handoff *architect.PlanHandoff) string {
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
	tasks := make([]taskInput, 0, len(handoff.Tasks))
	for _, t := range handoff.Tasks {
		if t == nil || strings.TrimSpace(t.ID) == "" {
			continue
		}
		tasks = append(tasks, taskInput{
			TaskID:              t.ID,
			AgentType:           t.AgentType,
			Complexity:          t.Complexity,
			AffectedFiles:       handoffAffectedPaths(t.AffectedFiles),
			RiskFactors:         t.RiskFactors,
			TestRequirements:    t.TestRequirements,
			Guidelines:          t.Guidelines,
			ImplementationGuide: t.ImplementationGuide,
		})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskID < tasks[j].TaskID })

	hashPayload := struct {
		PlanID   string      `json:"plan_id"`
		Revision int         `json:"revision"`
		Tasks    []taskInput `json:"tasks"`
	}{
		PlanID:   handoff.PlanID,
		Revision: handoff.Revision,
		Tasks:    tasks,
	}
	raw, _ := json.Marshal(hashPayload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// handoffAffectedPaths flattens HandoffTask.AffectedFiles to plain
// path strings — same projection the architect uses (affectedFilePaths
// in agents/architect/preflight_plan.go).
func handoffAffectedPaths(targets []architect.TaskFileTarget) []string {
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

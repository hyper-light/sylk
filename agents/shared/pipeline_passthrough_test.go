package shared

import (
	"context"
	"testing"
)

// TestBuildPipelineHandoffTasks_AttachesInheritedArtifactsForNonRecipient
// pins the agentic passthrough contract: when the tester finalizes for
// engineer and hands off to inspector (a non-recipient), the dispatched
// inspector task must carry the engineer-targeted artifact as
// inherited_artifacts so inspector can route it forward. Direct routing
// (target == recipient) still attaches as verification_artifact_ref.
func TestBuildPipelineHandoffTasks_AttachesInheritedArtifactsForNonRecipient(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{Iteration: 1})
	state.queuedArtifacts = map[string]PipelineHandoffArtifactRef{
		PipelineAgentEngineer: {
			ArtifactID:        "art-eng-1",
			Target:            PipelineAgentEngineer,
			SuiteID:           "suite-1",
			Summary:           "red-phase test for hello_cli",
			QueuedAtIteration: 1,
		},
	}
	task := &PipelineTaskInput{TaskID: "task-1", SessionID: "ses-1"}
	action := &PipelineTurnAction{
		Type:         PipelineProtocolActionHandoff,
		AgentType:    PipelineAgentTester,
		TargetAgents: []string{PipelineAgentInspector},
		Mode:         PipelineTurnModeSingle,
	}

	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		t.Fatalf("buildPipelineHandoffTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 dispatched task, got %d", len(tasks))
	}
	dispatched := tasks[0]
	if dispatched.AgentType != PipelineAgentInspector {
		t.Fatalf("dispatched.AgentType = %q, want inspector-pipeline", dispatched.AgentType)
	}
	if _, ok := dispatched.Context["verification_artifact_ref"]; ok {
		t.Fatal("inspector should NOT receive verification_artifact_ref — it's not the recipient")
	}
	inherited, ok := dispatched.Context["inherited_artifacts"].([]map[string]any)
	if !ok || len(inherited) != 1 {
		t.Fatalf("expected exactly 1 inherited_artifact on inspector task, got %#v", dispatched.Context["inherited_artifacts"])
	}
	if inherited[0]["artifact_id"] != "art-eng-1" {
		t.Fatalf("inherited[0].artifact_id = %v, want art-eng-1", inherited[0]["artifact_id"])
	}
	if inherited[0]["target"] != PipelineAgentEngineer {
		t.Fatalf("inherited[0].target = %v, want engineer", inherited[0]["target"])
	}
}

// TestBuildPipelineHandoffTasks_DirectRoutingStillAttachesAsRef pins
// backwards compat: when the handoff target IS the recipient, the
// artifact attaches as verification_artifact_ref (the existing direct-
// delivery shape) and is not also duplicated into inherited_artifacts.
func TestBuildPipelineHandoffTasks_DirectRoutingStillAttachesAsRef(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{Iteration: 1})
	state.queuedArtifacts = map[string]PipelineHandoffArtifactRef{
		PipelineAgentEngineer: {
			ArtifactID:        "art-eng-direct",
			Target:            PipelineAgentEngineer,
			SuiteID:           "suite-1",
			QueuedAtIteration: 1,
		},
	}
	task := &PipelineTaskInput{TaskID: "task-1", SessionID: "ses-1"}
	action := &PipelineTurnAction{
		Type:         PipelineProtocolActionHandoff,
		AgentType:    PipelineAgentTester,
		TargetAgents: []string{PipelineAgentEngineer},
		Mode:         PipelineTurnModeSingle,
	}

	tasks, err := buildPipelineHandoffTasks(state, task, action)
	if err != nil {
		t.Fatalf("buildPipelineHandoffTasks: %v", err)
	}
	dispatched := tasks[0]
	ref, ok := dispatched.Context["verification_artifact_ref"].(map[string]any)
	if !ok {
		t.Fatalf("expected verification_artifact_ref on direct routing, got %#v", dispatched.Context["verification_artifact_ref"])
	}
	if ref["artifact_id"] != "art-eng-direct" {
		t.Fatalf("verification_artifact_ref.artifact_id = %v, want art-eng-direct", ref["artifact_id"])
	}
	if _, hasInherited := dispatched.Context["inherited_artifacts"]; hasInherited {
		t.Fatal("direct routing should not also populate inherited_artifacts (no other queued targets)")
	}
}

// TestBuildQueueStateAdvisory_ReportsPassthroughAndAge pins the advisory
// builder contract: when artifacts ride along to non-recipient targets,
// queue_state.inherited_passthrough lists them with age_iterations and
// (at age ≥2) an explicit advisory string the LLM can act on.
func TestBuildQueueStateAdvisory_ReportsPassthroughAndAge(t *testing.T) {
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{Iteration: 4})
	state.queuedArtifacts = map[string]PipelineHandoffArtifactRef{
		PipelineAgentEngineer: {
			ArtifactID:        "art-aged",
			Target:            PipelineAgentEngineer,
			SuiteID:           "suite-1",
			QueuedAtIteration: 1, // age = 3
		},
	}
	action := &PipelineTurnAction{
		Type:         PipelineProtocolActionHandoff,
		TargetAgents: []string{PipelineAgentInspector},
	}
	dispatch := &pipelineDispatchSelection{
		CorrelationIDs: []string{"corr-1"},
		TargetAgentIDs: []string{"inspector-pipeline-task-1"},
	}

	advisory := buildQueueStateAdvisory(state, action, dispatch)
	if advisory == nil {
		t.Fatal("expected non-nil queue_state advisory")
	}
	if advisory["current_iteration"] != 4 {
		t.Fatalf("current_iteration = %v, want 4", advisory["current_iteration"])
	}
	passthrough, ok := advisory["inherited_passthrough"].([]map[string]any)
	if !ok || len(passthrough) != 1 {
		t.Fatalf("expected 1 inherited_passthrough entry, got %#v", advisory["inherited_passthrough"])
	}
	entry := passthrough[0]
	if entry["age_iterations"] != 3 {
		t.Fatalf("age_iterations = %v, want 3", entry["age_iterations"])
	}
	if _, hasAdvisory := entry["advisory"]; !hasAdvisory {
		t.Fatal("expected advisory string at age ≥ 2")
	}
}

// TestSweepAgedArtifacts_DiscardsBeyondThreshold pins the bounded-loss
// convergence guard: artifacts whose age exceeds
// pipelineArtifactMaxIterations are auto-discarded on the next dispatch.
// The LLM sees the post-sweep state via its next queue_state advisory
// and can re-finalize if the artifact is still relevant.
func TestSweepAgedArtifacts_DiscardsBeyondThreshold(t *testing.T) {
	tmpDir := t.TempDir()
	state := NewPipelineProtocolState(&PipelineProtocolSnapshot{Iteration: 10})
	state.sessionDir = tmpDir
	state.scopeID = "scope-aged-discard"
	store, err := openDurableProtocolLog(tmpDir, pipelineProtocolNamespace, state.scopeID)
	if err != nil {
		t.Fatalf("openDurableProtocolLog: %v", err)
	}
	state.store = store
	state.queuedArtifacts = map[string]PipelineHandoffArtifactRef{
		PipelineAgentEngineer: {
			ArtifactID:        "art-too-old",
			Target:            PipelineAgentEngineer,
			QueuedAtIteration: 2, // age = 8, exceeds threshold of 5
		},
		PipelineAgentDesigner: {
			ArtifactID:        "art-fresh",
			Target:            PipelineAgentDesigner,
			QueuedAtIteration: 8, // age = 2, under threshold
		},
	}

	if err := state.sweepAgedArtifacts(context.Background()); err != nil {
		t.Fatalf("sweepAgedArtifacts: %v", err)
	}

	remaining := state.QueuedArtifacts()
	if _, stillPresent := remaining[PipelineAgentEngineer]; stillPresent {
		t.Fatal("aged artifact for engineer should have been swept")
	}
	if _, fresh := remaining[PipelineAgentDesigner]; !fresh {
		t.Fatal("fresh artifact for designer should remain")
	}
}

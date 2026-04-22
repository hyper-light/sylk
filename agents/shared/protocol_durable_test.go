package shared

import (
	"context"
	"testing"
)

func TestPipelineProtocolDurableProjectionAndMailbox(t *testing.T) {
	sessionDir := t.TempDir()
	task := &PipelineTaskInput{
		TaskID:    "task-durable",
		AgentType: PipelineAgentInspector,
		SessionID: "sess-durable",
		Context: map[string]any{
			"session_dir": sessionDir,
			"pipeline_protocol": PipelineProtocolSnapshotMap(&PipelineProtocolSnapshot{
				Roster: []PipelineProtocolAgent{
					{AgentType: PipelineAgentInspector},
					{AgentType: PipelineAgentTester},
				},
				ActiveAgents: []string{PipelineAgentTester},
				PendingChallenge: &PipelineProtocolChallenge{
					ID:                "challenge-validate",
					RequestingAgent:   PipelineAgentInspector,
					RequestingAgentID: "inspector-runtime-1",
					TargetAgents:      []string{PipelineAgentTester},
					Request:           "Validate the implementation.",
					References:        []string{finalizePipelineVerificationReference},
				},
			}),
		},
	}

	state, err := newPipelineProtocolStateForTask(task)
	if err != nil {
		t.Fatalf("newPipelineProtocolStateForTask() error = %v", err)
	}
	defer state.Close()

	testerMailbox, err := openDurableAgentMailbox(sessionDir, PipelineAgentTester)
	if err != nil {
		t.Fatalf("open tester mailbox: %v", err)
	}
	defer testerMailbox.Close()
	testerItems, err := testerMailbox.Pending(pipelineProtocolNamespace, task.TaskID)
	if err != nil {
		t.Fatalf("tester mailbox pending: %v", err)
	}
	if len(testerItems) != 1 || testerItems[0].Action != "validate_work" {
		t.Fatalf("tester mailbox = %#v, want single validate_work obligation", testerItems)
	}

	testerCtx := WithPipelineTask(context.Background(), task)
	testerCtx = WithTaskExecutionContract(testerCtx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentTester})
	if err := state.recordValidation(testerCtx, &PipelineValidationRecord{
		ChallengeID:         "challenge-validate",
		RequestingAgent:     PipelineAgentInspector,
		RequestingAgentID:   "inspector-runtime-1",
		RespondingAgent:     PipelineAgentTester,
		RespondingAgentID:   "tester-runtime-1",
		Status:              string(PipelineValidationPartial),
		Summary:             "Validation is accepted, but the remaining execution caveat is environmental.",
		ChallengeReferences: []string{finalizePipelineVerificationReference},
		EvidenceRefs:        []string{"tests/auth_test.go"},
	}); err != nil {
		t.Fatalf("recordValidation() error = %v", err)
	}

	inspectorMailbox, err := openDurableAgentMailbox(sessionDir, PipelineAgentInspector)
	if err != nil {
		t.Fatalf("open inspector mailbox: %v", err)
	}
	defer inspectorMailbox.Close()
	inspectorItems, err := inspectorMailbox.Pending(pipelineProtocolNamespace, task.TaskID)
	if err != nil {
		t.Fatalf("inspector mailbox pending after validation: %v", err)
	}
	if len(inspectorItems) != 1 || inspectorItems[0].Action != "process_validation" {
		t.Fatalf("inspector mailbox after validation = %#v, want process_validation", inspectorItems)
	}

	inspectorCtx := WithPipelineTask(context.Background(), task)
	inspectorCtx = WithTaskExecutionContract(inspectorCtx, &TaskExecutionContract{RuntimeAgentType: PipelineAgentInspector})
	if err := state.recordValidationProcessing(inspectorCtx, PipelineValidationProcessing{
		ChallengeID: "challenge-validate",
		AgentType:   PipelineAgentInspector,
		Decision:    PipelineValidationDecisionAccept,
		Summary:     "Accepted tester validation.",
		Validation: &PipelineValidationRecord{
			ChallengeID:         "challenge-validate",
			RequestingAgent:     PipelineAgentInspector,
			RequestingAgentID:   "inspector-runtime-1",
			RespondingAgent:     PipelineAgentTester,
			RespondingAgentID:   "tester-runtime-1",
			Status:              string(PipelineValidationPartial),
			Summary:             "Validation is accepted, but the remaining execution caveat is environmental.",
			ChallengeReferences: []string{finalizePipelineVerificationReference},
			EvidenceRefs:        []string{"tests/auth_test.go"},
		},
	}); err != nil {
		t.Fatalf("recordValidationProcessing() error = %v", err)
	}
	if snapshot := state.Snapshot(); snapshot == nil || snapshot.PendingValidation != nil {
		t.Fatalf("snapshot pending_validation after processing = %#v, want nil", snapshot)
	} else if snapshot.CurrentRequest != "Choose the next pipeline action after processing challenge challenge-validate." {
		t.Fatalf("current_request after processing = %q", snapshot.CurrentRequest)
	}

	inspectorItems, err = inspectorMailbox.Pending(pipelineProtocolNamespace, task.TaskID)
	if err != nil {
		t.Fatalf("inspector mailbox pending after processing: %v", err)
	}
	if len(inspectorItems) != 1 || inspectorItems[0].Action != "finalize_pipeline" {
		t.Fatalf("inspector mailbox after processing = %#v, want finalize_pipeline", inspectorItems)
	}

	if err := state.recordReadyForOT(inspectorCtx, "Pipeline ready for OT.", []string{"tests/auth_test.go"}, &PipelineValidationRecord{
		ChallengeID: "challenge-validate",
	}); err != nil {
		t.Fatalf("recordReadyForOT() error = %v", err)
	}

	inspectorItems, err = inspectorMailbox.Pending(pipelineProtocolNamespace, task.TaskID)
	if err != nil {
		t.Fatalf("inspector mailbox pending after ready_for_ot: %v", err)
	}
	if len(inspectorItems) != 1 || inspectorItems[0].Action != string(PipelineProtocolActionOT) {
		t.Fatalf("inspector mailbox after ready_for_ot = %#v, want handoff_to_green obligation", inspectorItems)
	}

	if err := state.Close(); err != nil {
		t.Fatalf("state.Close() error = %v", err)
	}
	reopened, err := newPipelineProtocolStateForTask(task)
	if err != nil {
		t.Fatalf("reopen state: %v", err)
	}
	defer reopened.Close()
	if required, _ := reopened.RequiredAction(); required != PipelineProtocolActionOT {
		t.Fatalf("required action after reopen = %q, want %q", required, PipelineProtocolActionOT)
	}
}

func TestGlobalReviewDurableProjectionAndMailbox(t *testing.T) {
	sessionDir := t.TempDir()
	metadata := map[string]any{
		"session_id":    "sess-global",
		"session_dir":   sessionDir,
		"review_id":     "review-durable",
		"global_review": true,
		"global_review_protocol": GlobalReviewSnapshotMap(&GlobalReviewSnapshot{
			ReviewID:       "review-durable",
			RequestedBy:    GlobalReviewAgentInspector,
			CurrentRequest: "Validate the merged state.",
			PendingChallenge: &GlobalReviewChallenge{
				ID:              "global-challenge-1",
				RequestingAgent: GlobalReviewAgentInspector,
				TargetAgent:     GlobalReviewAgentTester,
				Request:         "Audit the merged work.",
			},
		}),
	}

	state := NewGlobalReviewStateFromMetadata(metadata)
	if state == nil {
		t.Fatal("NewGlobalReviewStateFromMetadata() returned nil")
	}
	defer state.Close()

	testerMailbox, err := openDurableAgentMailbox(sessionDir, GlobalReviewAgentTester)
	if err != nil {
		t.Fatalf("open tester mailbox: %v", err)
	}
	defer testerMailbox.Close()
	items, err := testerMailbox.Pending(globalReviewNamespace, "review-durable")
	if err != nil {
		t.Fatalf("tester mailbox pending: %v", err)
	}
	if len(items) != 1 || items[0].Action != "validate_work" {
		t.Fatalf("tester mailbox = %#v, want validate_work", items)
	}

	if err := state.recordValidation(context.Background(), &GlobalReviewValidationRecord{
		ChallengeID:     "global-challenge-1",
		RequestingAgent: GlobalReviewAgentInspector,
		RespondingAgent: GlobalReviewAgentTester,
		Status:          string(GlobalReviewValidationPassed),
		Summary:         "Merged work passed.",
		EvidenceRefs:    []string{"go test ./..."},
	}); err != nil {
		t.Fatalf("recordValidation() error = %v", err)
	}
	if err := state.recordValidationProcessing(context.Background(), GlobalReviewValidationProcessing{
		ChallengeID: "global-challenge-1",
		AgentType:   GlobalReviewAgentInspector,
		Decision:    GlobalReviewValidationDecisionAccept,
		Summary:     "Accepted tester validation.",
		Validation: &GlobalReviewValidationRecord{
			ChallengeID:     "global-challenge-1",
			RequestingAgent: GlobalReviewAgentInspector,
			RespondingAgent: GlobalReviewAgentTester,
			Status:          string(GlobalReviewValidationPassed),
			Summary:         "Merged work passed.",
			EvidenceRefs:    []string{"go test ./..."},
		},
	}); err != nil {
		t.Fatalf("recordValidationProcessing() error = %v", err)
	}
	if err := state.recordReadyForCommit(context.Background(), "Ready to commit merged work.", []string{"go test ./..."}, &GlobalReviewValidationRecord{
		ChallengeID: "global-challenge-1",
	}); err != nil {
		t.Fatalf("recordReadyForCommit() error = %v", err)
	}

	inspectorMailbox, err := openDurableAgentMailbox(sessionDir, GlobalReviewAgentInspector)
	if err != nil {
		t.Fatalf("open inspector mailbox: %v", err)
	}
	defer inspectorMailbox.Close()
	items, err = inspectorMailbox.Pending(globalReviewNamespace, "review-durable")
	if err != nil {
		t.Fatalf("inspector mailbox pending: %v", err)
	}
	if len(items) != 1 || items[0].Action != "commit_to_disk" {
		t.Fatalf("inspector mailbox = %#v, want commit_to_disk", items)
	}

	reopened := NewGlobalReviewStateFromMetadata(metadata)
	if reopened == nil {
		t.Fatal("reopened global review state is nil")
	}
	defer reopened.Close()
	if action, _ := reopened.RequiredAction(); action != GlobalReviewActionCommit {
		t.Fatalf("required action after reopen = %q, want %q", action, GlobalReviewActionCommit)
	}
}

func TestGlobalReviewDurableProjection_CheckpointAcceptMailbox(t *testing.T) {
	sessionDir := t.TempDir()
	metadata := map[string]any{
		"session_id":    "sess-global",
		"session_dir":   sessionDir,
		"review_id":     "review-checkpoint",
		"global_review": true,
		"global_review_protocol": GlobalReviewSnapshotMap(&GlobalReviewSnapshot{
			ReviewID: "review-checkpoint",
		}),
	}

	state := NewGlobalReviewStateFromMetadata(metadata)
	if state == nil {
		t.Fatal("NewGlobalReviewStateFromMetadata() returned nil")
	}
	defer state.Close()

	record := &GlobalReviewValidationRecord{
		ChallengeID:     "checkpoint-challenge-1",
		RequestingAgent: GlobalReviewAgentInspector,
		RespondingAgent: GlobalReviewAgentTester,
		Status:          string(GlobalReviewValidationPassed),
		Summary:         "Checkpoint review passed.",
		EvidenceRefs:    []string{"go test ./..."},
	}
	if err := state.recordValidationProcessing(context.Background(), GlobalReviewValidationProcessing{
		ChallengeID: record.ChallengeID,
		AgentType:   GlobalReviewAgentInspector,
		Decision:    GlobalReviewValidationDecisionAccept,
		Summary:     "Accepted tester validation.",
		Validation:  record,
	}); err != nil {
		t.Fatalf("recordValidationProcessing() error = %v", err)
	}
	if err := state.recordReadyForCheckpoint(context.Background(), "Ready to accept checkpoint.", record.EvidenceRefs, record); err != nil {
		t.Fatalf("recordReadyForCheckpoint() error = %v", err)
	}

	inspectorMailbox, err := openDurableAgentMailbox(sessionDir, GlobalReviewAgentInspector)
	if err != nil {
		t.Fatalf("open inspector mailbox: %v", err)
	}
	defer inspectorMailbox.Close()
	items, err := inspectorMailbox.Pending(globalReviewNamespace, "review-checkpoint")
	if err != nil {
		t.Fatalf("inspector mailbox pending: %v", err)
	}
	if len(items) != 1 || items[0].Action != "accept_checkpoint" {
		t.Fatalf("inspector mailbox = %#v, want accept_checkpoint", items)
	}

	reopened := NewGlobalReviewStateFromMetadata(metadata)
	if reopened == nil {
		t.Fatal("reopened global review state is nil")
	}
	defer reopened.Close()
	if action, _ := reopened.RequiredAction(); action != GlobalReviewActionAccept {
		t.Fatalf("required action after reopen = %q, want %q", action, GlobalReviewActionAccept)
	}
}

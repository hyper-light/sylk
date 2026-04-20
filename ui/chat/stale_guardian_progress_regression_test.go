package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

// TestStaleGuardianProgress_DoesNotKillParentSpinner pins the fix for
// the class of bugs where parent agents' thinking indicators vanished
// after every guardian approval.
//
// Root cause: staleGuardianInterAgentProgress is intended to SUPPRESS
// redundant "Guardian approval received" progress text (which is
// already rendered on the inline guardian approval row). Its previous
// implementation also called clearSlotThinkingDisplay, which zeros
// slot.thinkingStart and ThinkingText — effectively killing the
// parent's spinner whenever the parent had a resolved guardian inter-
// agent tool-call and no other active tool call at the moment the
// "Guardian approval received" progress message landed.
//
// Fix: the stale-guard now suppresses the MESSAGE only; the spinner
// lifecycle is owned by the stream-complete path, not by progress
// text.
//
// The sequence below is the real one the tester runs; if the spinner
// vanishes at any step while the parent stream is still open, this
// test fails with the offending step identified.
func TestStaleGuardianProgress_DoesNotKillParentSpinner(t *testing.T) {
	m := New(theme.DefaultDark(), 16)

	parentCID := "parent-corr"

	checkAlive := func(step string) {
		t.Helper()
		slot, ok := m.streams[parentCID]
		if !ok || slot == nil {
			t.Fatalf("[%s] parent slot missing", step)
		}
		if slot.thinkingIdx < 0 {
			t.Fatalf("[%s] thinkingIdx detached", step)
		}
		if slot.thinkingStart.IsZero() {
			t.Fatalf("[%s] thinkingStart zeroed — spinner will NOT render. THIS IS THE BUG.", step)
		}
		entry := m.history.Get(slot.thinkingIdx)
		if entry == nil {
			t.Fatalf("[%s] parent entry missing", step)
		}
		if strings.TrimSpace(entry.ThinkingText) == "" {
			t.Fatalf("[%s] ThinkingText empty — spinner frame not drawn (status=%q)",
				step, entry.ThinkingStatus)
		}
		t.Logf("[%s] alive: text=%q status=%q", step, entry.ThinkingText, entry.ThinkingStatus)
	}

	comp, _ := m.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: parentCID,
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
	})
	m = comp.(*Model)
	// Simulate the stream already having emitted reasoning + a progress
	// message so the slot's thinking indicator is active and visible.
	slot := m.streams[parentCID]
	if slot == nil {
		t.Fatal("parent slot missing after StreamStart")
	}
	if slot.thinkingIdx < 0 && slot.accumulator != nil {
		slot.thinkingIdx = slot.accumulator.EntryIndex()
	}
	slot.thinkingStart = time.Now()
	m.history.UpdateAt(slot.thinkingIdx, func(e *ChatEntry) {
		e.ThinkingText = "⠋  0.0s"
		e.ThinkingStatus = "Reasoning..."
	})
	checkAlive("baseline")

	// Step 2: parent emits run_test_suite tool-call.
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: parentCID,
		Phase:         0,
		ToolCallKey:   "run-test-suite-1",
		ToolName:      "run_test_suite",
		StartedAt:     time.Now(),
	})
	m = comp.(*Model)
	checkAlive("after run_test_suite start")

	// Parent emits run_test_suite Complete so it's no longer an active
	// regular tool call. (In the real flow run_test_suite isn't
	// regular — it's the outer dispatch — but the stale-guard only
	// cares about "any active visual". We model the post-run state
	// where the only remaining active thing was the guardian approval.)
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: parentCID,
		Phase:         1,
		ToolCallKey:   "run-test-suite-1",
		ToolName:      "run_test_suite",
		StartedAt:     time.Now(),
		Duration:      71 * time.Millisecond,
		Success:       true,
	})
	m = comp.(*Model)
	checkAlive("after run_test_suite complete")

	// Step 3: parent emits an InterAgent Start event targeting guardian
	// (approval_guardian). This lands as a ToolCallRecord with
	// InterAgent populated on the parent's entry.
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: parentCID,
		Phase:         0,
		ToolCallKey:   "guardian-approval-1",
		ToolName:      "approval_guardian",
		ArgsSummary:   "target=guardian",
		StartedAt:     time.Now(),
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			AgentTypes: []string{"guardian"},
			Summary:    "Approve command execution",
			ThreadKey:  "approval-1",
			Status:     "pending",
		},
	})
	m = comp.(*Model)
	checkAlive("after guardian approval start (interagent)")

	// Step 4: approval resolves (InterAgent Complete on the parent's
	// entry — guardian says allowed).
	comp, _ = m.Update(msg.ToolCallEventMsg{
		CorrelationID: parentCID,
		Phase:         1,
		ToolCallKey:   "guardian-approval-1",
		ToolName:      "approval_guardian",
		StartedAt:     time.Now(),
		Duration:      2 * time.Millisecond,
		Success:       true,
		InterAgent: &msg.InterAgentToolEventMsg{
			Kind:       "approval",
			AgentTypes: []string{"guardian"},
			Summary:    "Command approval allowed",
			ThreadKey:  "approval-1",
			Status:     "done",
		},
	})
	m = comp.(*Model)
	checkAlive("after guardian approval complete (interagent resolved)")

	// Step 5: command_approval_gate publishes the post-resolution
	// progress message. This is the exact event that triggers the
	// stale-guard to clear the parent's spinner.
	comp, _ = m.Update(msg.StreamProgressMsg{
		SessionID:     "s1",
		CorrelationID: parentCID,
		AgentID:       "tester-pipeline",
		AgentType:     "tester-pipeline",
		Message:       "Guardian approval received for go test -json -count=1 -v -timeout=120s",
		Sequence:      1,
	})
	m = comp.(*Model)
	checkAlive("after Guardian approval received progress (THIS IS THE FAILURE POINT)")
}

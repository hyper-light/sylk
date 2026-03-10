package architect

import "testing"

func TestFormatPlanThoughtMessage_AppendsEllipsisForIncompleteThought(t *testing.T) {
	got := formatPlanThoughtMessage("tasks", "Let me")
	if got != "Tasks: Let me..." {
		t.Fatalf("formatPlanThoughtMessage() = %q, want %q", got, "Tasks: Let me...")
	}
}

func TestFormatPlanThoughtMessage_LeavesCompleteThoughtUntouched(t *testing.T) {
	got := formatPlanThoughtMessage("design", "I should compare the main options.")
	if got != "Design: I should compare the main options." {
		t.Fatalf("formatPlanThoughtMessage() = %q, want %q", got, "Design: I should compare the main options.")
	}
}

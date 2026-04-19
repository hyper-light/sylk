package planapproval

import "testing"

// TestVerdict_IsValid pins the verdict enum: only the three canonical
// values are accepted. The plan acceptance dialog has exactly three
// buttons (Approve, Modify, Reject) and no free-form decision path —
// guarding the enum here prevents a future "maybe", "needs-more-info",
// or other ambiguous verdict from sneaking into the protocol without
// a corresponding architect handler branch.
func TestVerdict_IsValid(t *testing.T) {
	for _, valid := range []Verdict{VerdictApprove, VerdictModify, VerdictReject} {
		if !valid.IsValid() {
			t.Fatalf("%q should be valid", valid)
		}
	}
	for _, invalid := range []Verdict{"", "maybe", "approve_with_changes", "unknown"} {
		if invalid.IsValid() {
			t.Fatalf("%q must not be valid — adding new verdicts requires a corresponding architect handler", invalid)
		}
	}
}

package guardian

import "testing"

func TestGuardianDirectSkillPublishesStream(t *testing.T) {
	if guardianDirectSkillPublishesStream("tool_execution_control") {
		t.Fatal("expected tool_execution_control stream publishing to be suppressed")
	}
	if guardianDirectSkillPublishesStream("command_execution_control") {
		t.Fatal("expected command_execution_control stream publishing to be suppressed")
	}
	if !guardianDirectSkillPublishesStream("other_skill") {
		t.Fatal("expected other guardian direct skills to keep stream publishing")
	}
}

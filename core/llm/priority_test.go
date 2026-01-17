package llm

import "testing"

func TestRequestPriorityString(t *testing.T) {
	tests := []struct {
		priority RequestPriority
		expected string
	}{
		{PriorityUserInteractive, "user_interactive"},
		{PriorityPlanning, "planning"},
		{PriorityExecution, "execution"},
		{PriorityValidation, "validation"},
		{PriorityBackground, "background"},
		{RequestPriority(50), "custom"},
		{RequestPriority(0), "custom"},
		{RequestPriority(150), "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.priority.String()
			if got != tt.expected {
				t.Errorf("RequestPriority.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRequestPriorityValues(t *testing.T) {
	// Verify priority ordering
	if PriorityBackground >= PriorityValidation {
		t.Error("Background should be lower priority than Validation")
	}
	if PriorityValidation >= PriorityExecution {
		t.Error("Validation should be lower priority than Execution")
	}
	if PriorityExecution >= PriorityPlanning {
		t.Error("Execution should be lower priority than Planning")
	}
	if PriorityPlanning >= PriorityUserInteractive {
		t.Error("Planning should be lower priority than UserInteractive")
	}
}

func TestDeterminePriority(t *testing.T) {
	tests := []struct {
		name          string
		agentType     string
		isUserInvoked bool
		expected      RequestPriority
	}{
		// User-invoked always gets highest priority
		{"user invoked architect", "architect", true, PriorityUserInteractive},
		{"user invoked engineer", "engineer", true, PriorityUserInteractive},
		{"user invoked archivalist", "archivalist", true, PriorityUserInteractive},
		{"user invoked unknown", "unknown", true, PriorityUserInteractive},

		// Agent-based priorities
		{"architect", "architect", false, PriorityPlanning},
		{"engineer", "engineer", false, PriorityExecution},
		{"designer", "designer", false, PriorityExecution},
		{"inspector", "inspector", false, PriorityValidation},
		{"tester", "tester", false, PriorityValidation},
		{"archivalist", "archivalist", false, PriorityBackground},
		{"librarian", "librarian", false, PriorityBackground},

		// Unknown agent types default to execution
		{"unknown agent", "custom_agent", false, PriorityExecution},
		{"empty agent", "", false, PriorityExecution},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeterminePriority(tt.agentType, tt.isUserInvoked)
			if got != tt.expected {
				t.Errorf("DeterminePriority(%q, %v) = %v (%d), want %v (%d)",
					tt.agentType, tt.isUserInvoked, got.String(), got, tt.expected.String(), tt.expected)
			}
		})
	}
}

func TestDeterminePriorityUserAlwaysHighest(t *testing.T) {
	// Test that user-invoked requests always get top priority regardless of agent type
	agentTypes := []string{
		"architect",
		"engineer",
		"designer",
		"inspector",
		"tester",
		"archivalist",
		"librarian",
		"custom",
		"",
	}

	for _, agentType := range agentTypes {
		got := DeterminePriority(agentType, true)
		if got != PriorityUserInteractive {
			t.Errorf("DeterminePriority(%q, true) = %v, want PriorityUserInteractive", agentType, got)
		}
	}
}

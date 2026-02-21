package input

import "testing"

func TestDetectAgentPrefix(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantAgent   string
		wantRemaind string
	}{
		{
			name:        "simple prefix",
			input:       "@architect plan this",
			wantAgent:   "architect",
			wantRemaind: "plan this",
		},
		{
			name:        "mixed case prefix normalized",
			input:       "@ArChItEcT plan this",
			wantAgent:   "architect",
			wantRemaind: "plan this",
		},
		{
			name:        "empty remainder allowed",
			input:       "@guide",
			wantAgent:   "guide",
			wantRemaind: "",
		},
		{
			name:        "invalid dsl command preserved",
			input:       "@to:architect implement login",
			wantAgent:   "",
			wantRemaind: "@to:architect implement login",
		},
		{
			name:        "invalid multi segment dsl preserved",
			input:       "@archivalist:recall:patterns",
			wantAgent:   "",
			wantRemaind: "@archivalist:recall:patterns",
		},
		{
			name:        "no prefix",
			input:       "hello @architect",
			wantAgent:   "",
			wantRemaind: "hello @architect",
		},
	}

	for _, tt := range tests {
		agent, remainder := detectAgentPrefix(tt.input)
		if agent != tt.wantAgent {
			t.Fatalf("%s: agent = %q, want %q", tt.name, agent, tt.wantAgent)
		}
		if remainder != tt.wantRemaind {
			t.Fatalf("%s: remainder = %q, want %q", tt.name, remainder, tt.wantRemaind)
		}
	}
}

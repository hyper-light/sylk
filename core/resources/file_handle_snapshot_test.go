package resources

import (
	"encoding/json"
	"testing"
)

func TestFileHandleSnapshot_JSONSerialization(t *testing.T) {
	snap := FileHandleSnapshot{
		Global: GlobalSnapshot{
			Limit:       1024,
			Reserved:    204,
			Allocated:   100,
			Used:        50,
			Unallocated: 720,
		},
		Sessions: []SessionSnapshot{
			{
				SessionID:   "session-1",
				Allocated:   50,
				Used:        25,
				Unallocated: 25,
				Agents: []AgentSnapshot{
					{
						AgentID:   "agent-1",
						AgentType: "editor",
						Allocated: 20,
						Used:      10,
						Waiting:   false,
					},
					{
						AgentID:   "agent-2",
						AgentType: "terminal",
						Allocated: 15,
						Used:      5,
						Waiting:   true,
					},
				},
			},
		},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded FileHandleSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Global.Limit != 1024 {
		t.Errorf("expected limit 1024, got %d", decoded.Global.Limit)
	}
	if decoded.Global.Reserved != 204 {
		t.Errorf("expected reserved 204, got %d", decoded.Global.Reserved)
	}
	if len(decoded.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(decoded.Sessions))
	}
	if decoded.Sessions[0].SessionID != "session-1" {
		t.Errorf("expected session-1, got %s", decoded.Sessions[0].SessionID)
	}
	if len(decoded.Sessions[0].Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(decoded.Sessions[0].Agents))
	}
	if !decoded.Sessions[0].Agents[1].Waiting {
		t.Error("expected agent-2 to be waiting")
	}
}

func TestAgentSnapshot_JSONFields(t *testing.T) {
	snap := AgentSnapshot{
		AgentID:   "a1",
		AgentType: "editor",
		Allocated: 10,
		Used:      5,
		Waiting:   true,
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	str := string(data)
	expectedFields := []string{"agent_id", "agent_type", "allocated", "used", "waiting"}
	for _, field := range expectedFields {
		if !contains(str, field) {
			t.Errorf("expected JSON to contain field %q, got: %s", field, str)
		}
	}
}

func TestSessionSnapshot_JSONFields(t *testing.T) {
	snap := SessionSnapshot{
		SessionID:   "s1",
		Allocated:   100,
		Used:        50,
		Unallocated: 50,
		Agents:      []AgentSnapshot{},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	str := string(data)
	expectedFields := []string{"session_id", "allocated", "used", "unallocated", "agents"}
	for _, field := range expectedFields {
		if !contains(str, field) {
			t.Errorf("expected JSON to contain field %q, got: %s", field, str)
		}
	}
}

func TestGlobalSnapshot_JSONFields(t *testing.T) {
	snap := GlobalSnapshot{
		Limit:       1024,
		Reserved:    200,
		Allocated:   500,
		Used:        300,
		Unallocated: 324,
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	str := string(data)
	expectedFields := []string{"limit", "reserved", "allocated", "used", "unallocated"}
	for _, field := range expectedFields {
		if !contains(str, field) {
			t.Errorf("expected JSON to contain field %q, got: %s", field, str)
		}
	}
}

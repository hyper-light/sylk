package recovery

import (
	"testing"
	"time"
)

func TestDefaultRecoveryConfig(t *testing.T) {
	cfg := DefaultRecoveryConfig()

	if cfg.SoftInterventionDelay != 30*time.Second {
		t.Errorf("SoftInterventionDelay = %v, want 30s", cfg.SoftInterventionDelay)
	}
	if cfg.UserEscalationDelay != 60*time.Second {
		t.Errorf("UserEscalationDelay = %v, want 60s", cfg.UserEscalationDelay)
	}
	if cfg.ForceKillDelay != 120*time.Second {
		t.Errorf("ForceKillDelay = %v, want 120s", cfg.ForceKillDelay)
	}
	if cfg.MaxSoftAttempts != 2 {
		t.Errorf("MaxSoftAttempts = %d, want 2", cfg.MaxSoftAttempts)
	}
	if cfg.MonitorInterval != 5*time.Second {
		t.Errorf("MonitorInterval = %v, want 5s", cfg.MonitorInterval)
	}
}

func TestNewRecoveryState(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	if state.AgentID != "agent-1" {
		t.Errorf("AgentID = %s, want agent-1", state.AgentID)
	}
	if state.SessionID != "session-1" {
		t.Errorf("SessionID = %s, want session-1", state.SessionID)
	}
	if state.Level != RecoveryNone {
		t.Errorf("Level = %d, want RecoveryNone", state.Level)
	}
}

func TestRecoveryState_MarkSoftIntervention(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	state.MarkSoftIntervention()

	if state.SoftAttempts != 1 {
		t.Errorf("SoftAttempts = %d, want 1", state.SoftAttempts)
	}
	if state.Level != RecoverySoftIntervention {
		t.Errorf("Level = %d, want RecoverySoftIntervention", state.Level)
	}
	if state.LastSoftIntervention.IsZero() {
		t.Error("LastSoftIntervention should be set")
	}

	state.MarkSoftIntervention()
	if state.SoftAttempts != 2 {
		t.Errorf("SoftAttempts after second mark = %d, want 2", state.SoftAttempts)
	}
}

func TestRecoveryState_MarkUserEscalated(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	state.MarkUserEscalated()

	if !state.UserEscalated {
		t.Error("UserEscalated should be true")
	}
	if state.Level != RecoveryUserEscalation {
		t.Errorf("Level = %d, want RecoveryUserEscalation", state.Level)
	}
	if state.UserEscalatedAt.IsZero() {
		t.Error("UserEscalatedAt should be set")
	}
}

func TestRecoveryState_SetUserResponse(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	response := &UserRecoveryResponse{
		Action:    UserActionKill,
		Timestamp: time.Now(),
	}
	state.SetUserResponse(response)

	if state.UserResponse == nil {
		t.Fatal("UserResponse should not be nil")
	}
	if state.UserResponse.Action != UserActionKill {
		t.Errorf("UserResponse.Action = %d, want UserActionKill", state.UserResponse.Action)
	}
}

func TestRecoveryState_MarkForceKill(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	state.MarkForceKill()

	if state.Level != RecoveryForceKill {
		t.Errorf("Level = %d, want RecoveryForceKill", state.Level)
	}
}

func TestRecoveryState_AddReleasedResources(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	state.AddReleasedResources([]string{"r1", "r2"})
	state.AddReleasedResources([]string{"r3"})

	if len(state.ResourcesReleased) != 3 {
		t.Errorf("ResourcesReleased len = %d, want 3", len(state.ResourcesReleased))
	}
}

func TestRecoveryState_Reset(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	state.MarkSoftIntervention()
	state.MarkUserEscalated()
	state.SetUserResponse(&UserRecoveryResponse{Action: UserActionWait})
	state.AddReleasedResources([]string{"r1"})

	state.Reset()

	if state.Level != RecoveryNone {
		t.Errorf("Level after reset = %d, want RecoveryNone", state.Level)
	}
	if state.SoftAttempts != 0 {
		t.Errorf("SoftAttempts after reset = %d, want 0", state.SoftAttempts)
	}
	if state.UserEscalated {
		t.Error("UserEscalated after reset should be false")
	}
	if state.UserResponse != nil {
		t.Error("UserResponse after reset should be nil")
	}
	if state.ResourcesReleased != nil {
		t.Error("ResourcesReleased after reset should be nil")
	}
}

func TestRecoveryState_LockUnlock(t *testing.T) {
	state := NewRecoveryState("agent-1", "session-1")

	state.Lock()
	state.SoftAttempts = 5
	state.Unlock()

	if state.SoftAttempts != 5 {
		t.Errorf("SoftAttempts = %d, want 5", state.SoftAttempts)
	}
}

func TestRecoveryLevel_Values(t *testing.T) {
	levels := []RecoveryLevel{
		RecoveryNone,
		RecoverySoftIntervention,
		RecoveryUserEscalation,
		RecoveryForceKill,
	}

	for i, level := range levels {
		if int(level) != i {
			t.Errorf("RecoveryLevel %d has value %d, want %d", i, level, i)
		}
	}
}

func TestUserRecoveryAction_Values(t *testing.T) {
	actions := []UserRecoveryAction{
		UserActionWait,
		UserActionKill,
		UserActionInspect,
	}

	for i, action := range actions {
		if int(action) != i {
			t.Errorf("UserRecoveryAction %d has value %d, want %d", i, action, i)
		}
	}
}

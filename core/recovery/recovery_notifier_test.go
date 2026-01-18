package recovery

import (
	"testing"
	"time"
)

func TestHealthAssessment_Fields(t *testing.T) {
	now := time.Now()
	stuckTime := now.Add(-30 * time.Second)

	assessment := HealthAssessment{
		AgentID:           "agent-1",
		SessionID:         "session-1",
		OverallScore:      0.5,
		HeartbeatScore:    0.8,
		ProgressScore:     0.6,
		RepetitionScore:   0.4,
		ResourceScore:     0.7,
		RepetitionConcern: true,
		Status:            StatusWarning,
		LastProgress:      now,
		StuckSince:        &stuckTime,
		AssessedAt:        now,
	}

	if assessment.AgentID != "agent-1" {
		t.Errorf("AgentID = %s, want agent-1", assessment.AgentID)
	}
	if assessment.OverallScore != 0.5 {
		t.Errorf("OverallScore = %v, want 0.5", assessment.OverallScore)
	}
	if !assessment.RepetitionConcern {
		t.Error("RepetitionConcern should be true")
	}
	if assessment.Status != StatusWarning {
		t.Errorf("Status = %d, want StatusWarning", assessment.Status)
	}
	if assessment.StuckSince == nil {
		t.Error("StuckSince should not be nil")
	}
}

func TestAgentHealthStatus_Values(t *testing.T) {
	statuses := []AgentHealthStatus{
		StatusHealthy,
		StatusWarning,
		StatusStuck,
		StatusCritical,
		StatusDeadlocked,
	}

	for i, status := range statuses {
		if int(status) != i {
			t.Errorf("AgentHealthStatus %d has value %d, want %d", i, status, i)
		}
	}
}

func TestHealthAssessment_StuckSinceNil(t *testing.T) {
	assessment := HealthAssessment{
		AgentID: "agent-1",
		Status:  StatusHealthy,
	}

	if assessment.StuckSince != nil {
		t.Error("StuckSince should be nil for healthy agent")
	}
}

type mockNotifier struct {
	breakoutPrompts     []string
	escalations         []string
	forceKills          []string
	reacquireNotifs     []string
	userResponseAgentID string
}

func (m *mockNotifier) InjectBreakoutPrompt(agentID string, prompt string) error {
	m.breakoutPrompts = append(m.breakoutPrompts, agentID+":"+prompt)
	return nil
}

func (m *mockNotifier) EscalateToUser(sessionID, agentID string, _ HealthAssessment) error {
	m.escalations = append(m.escalations, sessionID+":"+agentID)
	return nil
}

func (m *mockNotifier) OnUserResponse(agentID string, _ UserRecoveryResponse) {
	m.userResponseAgentID = agentID
}

func (m *mockNotifier) NotifyForceKill(sessionID, agentID string, reason string) {
	m.forceKills = append(m.forceKills, sessionID+":"+agentID+":"+reason)
}

func (m *mockNotifier) NotifyReacquireResources(agentID string, resources []string) {
	m.reacquireNotifs = append(m.reacquireNotifs, agentID)
}

func TestMockNotifier_ImplementsInterface(t *testing.T) {
	var _ RecoveryNotifier = (*mockNotifier)(nil)
}

func TestMockNotifier_Methods(t *testing.T) {
	m := &mockNotifier{}

	_ = m.InjectBreakoutPrompt("agent-1", "test prompt")
	if len(m.breakoutPrompts) != 1 {
		t.Error("InjectBreakoutPrompt not recorded")
	}

	_ = m.EscalateToUser("session-1", "agent-1", HealthAssessment{})
	if len(m.escalations) != 1 {
		t.Error("EscalateToUser not recorded")
	}

	m.OnUserResponse("agent-1", UserRecoveryResponse{})
	if m.userResponseAgentID != "agent-1" {
		t.Error("OnUserResponse not recorded")
	}

	m.NotifyForceKill("session-1", "agent-1", "test reason")
	if len(m.forceKills) != 1 {
		t.Error("NotifyForceKill not recorded")
	}

	m.NotifyReacquireResources("agent-1", []string{"r1"})
	if len(m.reacquireNotifs) != 1 {
		t.Error("NotifyReacquireResources not recorded")
	}
}

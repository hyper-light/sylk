package recovery

import "time"

type RecoveryNotifier interface {
	InjectBreakoutPrompt(agentID string, prompt string) error
	EscalateToUser(sessionID, agentID string, assessment HealthAssessment) error
	OnUserResponse(agentID string, response UserRecoveryResponse)
	NotifyForceKill(sessionID, agentID string, reason string)
	NotifyReacquireResources(agentID string, resources []string)
}

type AgentHealthStatus int

const (
	StatusHealthy AgentHealthStatus = iota
	StatusWarning
	StatusStuck
	StatusCritical
	StatusDeadlocked
)

type HealthAssessment struct {
	AgentID           string
	SessionID         string
	OverallScore      float64
	HeartbeatScore    float64
	ProgressScore     float64
	RepetitionScore   float64
	ResourceScore     float64
	RepetitionConcern bool
	Status            AgentHealthStatus
	LastProgress      time.Time
	StuckSince        *time.Time
	AssessedAt        time.Time
}

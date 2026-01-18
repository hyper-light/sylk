package recovery

import (
	"sync"
	"time"
)

type RecoveryLevel int

const (
	RecoveryNone RecoveryLevel = iota
	RecoverySoftIntervention
	RecoveryUserEscalation
	RecoveryForceKill
)

type RecoveryConfig struct {
	SoftInterventionDelay time.Duration
	UserEscalationDelay   time.Duration
	ForceKillDelay        time.Duration
	MaxSoftAttempts       int
	MonitorInterval       time.Duration
}

func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		SoftInterventionDelay: 30 * time.Second,
		UserEscalationDelay:   60 * time.Second,
		ForceKillDelay:        120 * time.Second,
		MaxSoftAttempts:       2,
		MonitorInterval:       5 * time.Second,
	}
}

type RecoveryState struct {
	AgentID              string
	SessionID            string
	Level                RecoveryLevel
	StuckSince           time.Time
	SoftAttempts         int
	LastSoftIntervention time.Time
	UserEscalated        bool
	UserEscalatedAt      time.Time
	UserResponse         *UserRecoveryResponse
	ResourcesReleased    []string
	mu                   sync.Mutex
}

func NewRecoveryState(agentID, sessionID string) *RecoveryState {
	return &RecoveryState{
		AgentID:   agentID,
		SessionID: sessionID,
		Level:     RecoveryNone,
	}
}

func (s *RecoveryState) Lock() {
	s.mu.Lock()
}

func (s *RecoveryState) Unlock() {
	s.mu.Unlock()
}

func (s *RecoveryState) MarkSoftIntervention() {
	s.SoftAttempts++
	s.LastSoftIntervention = time.Now()
	s.Level = RecoverySoftIntervention
}

func (s *RecoveryState) MarkUserEscalated() {
	s.UserEscalated = true
	s.UserEscalatedAt = time.Now()
	s.Level = RecoveryUserEscalation
}

func (s *RecoveryState) SetUserResponse(response *UserRecoveryResponse) {
	s.UserResponse = response
}

func (s *RecoveryState) MarkForceKill() {
	s.Level = RecoveryForceKill
}

func (s *RecoveryState) AddReleasedResources(resources []string) {
	s.ResourcesReleased = append(s.ResourcesReleased, resources...)
}

func (s *RecoveryState) Reset() {
	s.Level = RecoveryNone
	s.SoftAttempts = 0
	s.UserEscalated = false
	s.UserResponse = nil
	s.ResourcesReleased = nil
}

type UserRecoveryAction int

const (
	UserActionWait UserRecoveryAction = iota
	UserActionKill
	UserActionInspect
)

type UserRecoveryResponse struct {
	Action    UserRecoveryAction
	Timestamp time.Time
}

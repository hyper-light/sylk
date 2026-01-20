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

// markSoftInterventionLocked updates state for a soft intervention attempt.
// REQUIRES: caller holds s.mu.
func (s *RecoveryState) markSoftInterventionLocked() {
	s.SoftAttempts++
	s.LastSoftIntervention = time.Now()
	s.Level = RecoverySoftIntervention
}

// MarkSoftIntervention updates state for a soft intervention attempt (thread-safe).
func (s *RecoveryState) MarkSoftIntervention() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markSoftInterventionLocked()
}

// markUserEscalatedLocked updates state when escalating to user.
// REQUIRES: caller holds s.mu.
func (s *RecoveryState) markUserEscalatedLocked() {
	s.UserEscalated = true
	s.UserEscalatedAt = time.Now()
	s.Level = RecoveryUserEscalation
}

// MarkUserEscalated updates state when escalating to user (thread-safe).
func (s *RecoveryState) MarkUserEscalated() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markUserEscalatedLocked()
}

// setUserResponseLocked sets the user's recovery response.
// REQUIRES: caller holds s.mu.
func (s *RecoveryState) setUserResponseLocked(response *UserRecoveryResponse) {
	s.UserResponse = response
}

// SetUserResponse sets the user's recovery response (thread-safe).
func (s *RecoveryState) SetUserResponse(response *UserRecoveryResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setUserResponseLocked(response)
}

// markForceKillLocked updates state when force killing.
// REQUIRES: caller holds s.mu.
func (s *RecoveryState) markForceKillLocked() {
	s.Level = RecoveryForceKill
}

// MarkForceKill updates state when force killing (thread-safe).
func (s *RecoveryState) MarkForceKill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markForceKillLocked()
}

// addReleasedResourcesLocked appends released resources to the state.
// REQUIRES: caller holds s.mu.
func (s *RecoveryState) addReleasedResourcesLocked(resources []string) {
	s.ResourcesReleased = append(s.ResourcesReleased, resources...)
}

// AddReleasedResources appends released resources to the state (thread-safe).
func (s *RecoveryState) AddReleasedResources(resources []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addReleasedResourcesLocked(resources)
}

// resetLocked resets the recovery state to initial values.
// REQUIRES: caller holds s.mu.
func (s *RecoveryState) resetLocked() {
	s.Level = RecoveryNone
	s.SoftAttempts = 0
	s.UserEscalated = false
	s.UserResponse = nil
	s.ResourcesReleased = nil
}

// Reset resets the recovery state to initial values (thread-safe).
func (s *RecoveryState) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

// GetLevel returns the current recovery level (thread-safe).
func (s *RecoveryState) GetLevel() RecoveryLevel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Level
}

// GetSoftAttempts returns the number of soft intervention attempts (thread-safe).
func (s *RecoveryState) GetSoftAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.SoftAttempts
}

// IsUserEscalated returns whether the state has been escalated to user (thread-safe).
func (s *RecoveryState) IsUserEscalated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.UserEscalated
}

// GetUserResponse returns the user's recovery response (thread-safe).
func (s *RecoveryState) GetUserResponse() *UserRecoveryResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.UserResponse
}

// GetReleasedResources returns a copy of the released resources (thread-safe).
func (s *RecoveryState) GetReleasedResources() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ResourcesReleased == nil {
		return nil
	}
	result := make([]string, len(s.ResourcesReleased))
	copy(result, s.ResourcesReleased)
	return result
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

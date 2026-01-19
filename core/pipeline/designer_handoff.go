// Package pipeline provides pipeline agent handoff state management.
// PH.4: DesignerHandoffState implements the ArchivableHandoffState interface
// for Designer agent handoffs, capturing UI/UX work context for seamless continuity.
package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ComponentInfo struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Type    string   `json:"type"`
	Props   []string `json:"props"`
	Exports []string `json:"exports"`
}

type ComponentChange struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	ChangeType  string `json:"change_type"`
	Description string `json:"description"`
}

type TokenUsage struct {
	TokenName string `json:"token_name"`
	Category  string `json:"category"`
	UsedIn    string `json:"used_in"`
}

type DesignerHandoffState struct {
	mu             sync.RWMutex
	agentID        string
	sessionID      string
	pipelineID     string
	triggerReason  string
	triggerContext float64
	handoffIndex   int
	startedAt      time.Time
	completedAt    time.Time

	OriginalPrompt     string            `json:"original_prompt"`
	Accomplished       []string          `json:"accomplished"`
	ComponentsCreated  []ComponentInfo   `json:"components_created"`
	ComponentsModified []ComponentChange `json:"components_modified"`
	TokensUsed         []TokenUsage      `json:"tokens_used"`
	Remaining          []string          `json:"remaining"`
	ContextNotes       string            `json:"context_notes"`
}

type DesignerHandoffConfig struct {
	AgentID        string
	SessionID      string
	PipelineID     string
	TriggerReason  string
	TriggerContext float64
	HandoffIndex   int
	StartedAt      time.Time
}

func BuildHandoffState(cfg DesignerHandoffConfig) *DesignerHandoffState {
	return &DesignerHandoffState{
		agentID:            cfg.AgentID,
		sessionID:          cfg.SessionID,
		pipelineID:         cfg.PipelineID,
		triggerReason:      cfg.TriggerReason,
		triggerContext:     cfg.TriggerContext,
		handoffIndex:       cfg.HandoffIndex,
		startedAt:          cfg.StartedAt,
		completedAt:        time.Now(),
		Accomplished:       make([]string, 0),
		ComponentsCreated:  make([]ComponentInfo, 0),
		ComponentsModified: make([]ComponentChange, 0),
		TokensUsed:         make([]TokenUsage, 0),
		Remaining:          make([]string, 0),
	}
}

func InjectHandoffState(data []byte) (*DesignerHandoffState, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty handoff state data")
	}

	var envelope handoffEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("failed to unmarshal handoff envelope: %w", err)
	}

	state := &DesignerHandoffState{
		agentID:            envelope.AgentID,
		sessionID:          envelope.SessionID,
		pipelineID:         envelope.PipelineID,
		triggerReason:      envelope.TriggerReason,
		triggerContext:     envelope.TriggerContext,
		handoffIndex:       envelope.HandoffIndex,
		startedAt:          envelope.StartedAt,
		completedAt:        envelope.CompletedAt,
		OriginalPrompt:     envelope.OriginalPrompt,
		Accomplished:       envelope.Accomplished,
		ComponentsCreated:  envelope.ComponentsCreated,
		ComponentsModified: envelope.ComponentsModified,
		TokensUsed:         envelope.TokensUsed,
		Remaining:          envelope.Remaining,
		ContextNotes:       envelope.ContextNotes,
	}

	state.initializeSlices()

	return state, nil
}

func (s *DesignerHandoffState) initializeSlices() {
	s.Accomplished = initStringSlice(s.Accomplished)
	s.ComponentsCreated = initComponentInfoSlice(s.ComponentsCreated)
	s.ComponentsModified = initComponentChangeSlice(s.ComponentsModified)
	s.TokensUsed = initTokenUsageSlice(s.TokensUsed)
	s.Remaining = initStringSlice(s.Remaining)
}

func initStringSlice(s []string) []string {
	if s == nil {
		return make([]string, 0)
	}
	return s
}

func initComponentInfoSlice(s []ComponentInfo) []ComponentInfo {
	if s == nil {
		return make([]ComponentInfo, 0)
	}
	return s
}

func initComponentChangeSlice(s []ComponentChange) []ComponentChange {
	if s == nil {
		return make([]ComponentChange, 0)
	}
	return s
}

func initTokenUsageSlice(s []TokenUsage) []TokenUsage {
	if s == nil {
		return make([]TokenUsage, 0)
	}
	return s
}

type handoffEnvelope struct {
	AgentID            string            `json:"agent_id"`
	AgentType          string            `json:"agent_type"`
	SessionID          string            `json:"session_id"`
	PipelineID         string            `json:"pipeline_id"`
	TriggerReason      string            `json:"trigger_reason"`
	TriggerContext     float64           `json:"trigger_context"`
	HandoffIndex       int               `json:"handoff_index"`
	StartedAt          time.Time         `json:"started_at"`
	CompletedAt        time.Time         `json:"completed_at"`
	OriginalPrompt     string            `json:"original_prompt"`
	Accomplished       []string          `json:"accomplished"`
	ComponentsCreated  []ComponentInfo   `json:"components_created"`
	ComponentsModified []ComponentChange `json:"components_modified"`
	TokensUsed         []TokenUsage      `json:"tokens_used"`
	Remaining          []string          `json:"remaining"`
	ContextNotes       string            `json:"context_notes"`
}

func (s *DesignerHandoffState) GetAgentID() string         { return s.agentID }
func (s *DesignerHandoffState) GetAgentType() string       { return "designer" }
func (s *DesignerHandoffState) GetSessionID() string       { return s.sessionID }
func (s *DesignerHandoffState) GetPipelineID() string      { return s.pipelineID }
func (s *DesignerHandoffState) GetTriggerReason() string   { return s.triggerReason }
func (s *DesignerHandoffState) GetTriggerContext() float64 { return s.triggerContext }
func (s *DesignerHandoffState) GetHandoffIndex() int       { return s.handoffIndex }
func (s *DesignerHandoffState) GetStartedAt() time.Time    { return s.startedAt }
func (s *DesignerHandoffState) GetCompletedAt() time.Time  { return s.completedAt }

func (s *DesignerHandoffState) ToJSON() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	envelope := handoffEnvelope{
		AgentID:            s.agentID,
		AgentType:          s.GetAgentType(),
		SessionID:          s.sessionID,
		PipelineID:         s.pipelineID,
		TriggerReason:      s.triggerReason,
		TriggerContext:     s.triggerContext,
		HandoffIndex:       s.handoffIndex,
		StartedAt:          s.startedAt,
		CompletedAt:        s.completedAt,
		OriginalPrompt:     s.OriginalPrompt,
		Accomplished:       s.Accomplished,
		ComponentsCreated:  s.ComponentsCreated,
		ComponentsModified: s.ComponentsModified,
		TokensUsed:         s.TokensUsed,
		Remaining:          s.Remaining,
		ContextNotes:       s.ContextNotes,
	}
	return json.Marshal(envelope)
}

func (s *DesignerHandoffState) GetSummary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	parts := []string{fmt.Sprintf("Designer handoff #%d", s.handoffIndex)}
	parts = s.appendPromptSummary(parts)
	parts = s.appendAccomplishedSummary(parts)
	parts = s.appendCreatedSummary(parts)
	parts = s.appendModifiedSummary(parts)
	parts = s.appendRemainingSummary(parts)

	return strings.Join(parts, ". ")
}

func (s *DesignerHandoffState) appendPromptSummary(parts []string) []string {
	if s.OriginalPrompt == "" {
		return parts
	}
	return append(parts, fmt.Sprintf("Prompt: %s", truncate(s.OriginalPrompt, 100)))
}

func (s *DesignerHandoffState) appendAccomplishedSummary(parts []string) []string {
	if len(s.Accomplished) == 0 {
		return parts
	}
	accomplished := make([]string, len(s.Accomplished))
	copy(accomplished, s.Accomplished)
	return append(parts, fmt.Sprintf("Done: %s", strings.Join(accomplished, "; ")))
}

func (s *DesignerHandoffState) appendCreatedSummary(parts []string) []string {
	if len(s.ComponentsCreated) == 0 {
		return parts
	}
	created := make([]ComponentInfo, len(s.ComponentsCreated))
	copy(created, s.ComponentsCreated)
	names := extractComponentNames(created)
	return append(parts, fmt.Sprintf("Created: %s", strings.Join(names, ", ")))
}

func (s *DesignerHandoffState) appendModifiedSummary(parts []string) []string {
	if len(s.ComponentsModified) == 0 {
		return parts
	}
	modified := make([]ComponentChange, len(s.ComponentsModified))
	copy(modified, s.ComponentsModified)
	names := extractChangeNames(modified)
	return append(parts, fmt.Sprintf("Modified: %s", strings.Join(names, ", ")))
}

func (s *DesignerHandoffState) appendRemainingSummary(parts []string) []string {
	if len(s.Remaining) == 0 {
		return parts
	}
	remaining := make([]string, len(s.Remaining))
	copy(remaining, s.Remaining)
	return append(parts, fmt.Sprintf("Remaining: %s", strings.Join(remaining, "; ")))
}

func extractComponentNames(components []ComponentInfo) []string {
	names := make([]string, len(components))
	for i, c := range components {
		names[i] = c.Name
	}
	return names
}

func extractChangeNames(changes []ComponentChange) []string {
	names := make([]string, len(changes))
	for i, c := range changes {
		names[i] = c.Name
	}
	return names
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func (s *DesignerHandoffState) SetOriginalPrompt(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OriginalPrompt = prompt
}

func (s *DesignerHandoffState) AddAccomplished(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Accomplished = append(s.Accomplished, task)
}

func (s *DesignerHandoffState) AddComponentCreated(info ComponentInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ComponentsCreated = append(s.ComponentsCreated, info)
}

func (s *DesignerHandoffState) AddComponentModified(change ComponentChange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ComponentsModified = append(s.ComponentsModified, change)
}

func (s *DesignerHandoffState) AddTokenUsed(token TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TokensUsed = append(s.TokensUsed, token)
}

func (s *DesignerHandoffState) AddRemaining(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Remaining = append(s.Remaining, task)
}

func (s *DesignerHandoffState) SetContextNotes(notes string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ContextNotes = notes
}

var _ ArchivableHandoffState = (*DesignerHandoffState)(nil)

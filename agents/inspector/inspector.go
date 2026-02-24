// Package inspector provides the root package for the Inspector agent system.
// The actual implementations live in sub-packages:
//   - inspector/shared:   Shared types, tools, skills, and streaming infrastructure
//   - inspector/pipeline: Per-task pipeline inspector (TDD phases 1 & 4)
//   - inspector/global:   Cross-file architectural quality auditor
//
// This root package is kept minimal. It re-exports base types for backward
// compatibility with existing code that imports "agents/inspector" directly.
package inspector

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

// ValidationPhase represents a phase in the validation pipeline.
type ValidationPhase string

const (
	PhaseLintCheck    ValidationPhase = "lint_check"
	PhaseTypeCheck    ValidationPhase = "type_check"
	PhaseFormatCheck  ValidationPhase = "format_check"
	PhaseSecurityScan ValidationPhase = "security_scan"
	PhaseTestCoverage ValidationPhase = "test_coverage"
	PhaseDocumentation ValidationPhase = "documentation"
	PhaseComplexity   ValidationPhase = "complexity"
	PhaseFinalReview  ValidationPhase = "final_review"
)

// Inspector is a backward-compatible shim that provides the root-level
// inspector API. New code should use the pipeline or global sub-packages
// directly. This struct satisfies guide.AgentRouter and container.ContainerAgent.
type Inspector struct {
	id       string
	config   InspectorConfig
	state    InspectorState
	criteria map[string]*InspectorCriteria
	result   *InspectorResult
	mu       sync.RWMutex
}

// New creates an Inspector with the given configuration.
func New(cfg InspectorConfig) (*Inspector, error) {
	id := "inspector"
	if cfg.Mode == "" {
		cfg.Mode = PipelineInternal
	}

	return &Inspector{
		id:     id,
		config: cfg,
		state: InspectorState{
			ID:        id,
			Mode:      cfg.Mode,
			StartedAt: time.Now(),
		},
		criteria: make(map[string]*InspectorCriteria),
	}, nil
}

// AgentID returns the inspector's identifier.
func (i *Inspector) AgentID() string { return i.id }

// AgentType returns "inspector".
func (i *Inspector) AgentType() string { return "inspector" }

// Terminate gracefully stops the inspector.
func (i *Inspector) Terminate(_ context.Context) error { return nil }

// GetRoutingInfo returns routing information for the guide's agent registry.
func (i *Inspector) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      i.id,
		Type:    "inspector",
		Name:    "Inspector",
		Aliases: []string{"insp", "review", "validate"},
		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{"validate", "inspect", "lint", "check quality", "review code"},
			WeakTriggers:   []string{"check", "quality", "issues"},
		},
		Registration: &guide.AgentRegistration{
			ID:   i.id,
			Name: "Inspector",
			Capabilities: guide.AgentCapabilities{
				Keywords: []string{"validate", "check", "lint", "inspect", "review", "quality"},
			},
			Description: "Code quality validation and inspection agent",
		},
	}
}

// DefineCriteria stores success criteria for a task (TDD Phase 1).
func (i *Inspector) DefineCriteria(taskID string, criteria *InspectorCriteria) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.criteria[taskID] = criteria
	i.state.CurrentTaskID = taskID
	i.state.LastActiveAt = time.Now()
}

// ValidateAgainstCriteria validates files against the stored criteria (TDD Phase 4).
// This backward-compatible stub returns a passing result. The real LLM-driven
// validation lives in the pipeline sub-package.
func (i *Inspector) ValidateAgainstCriteria(_ context.Context, taskID string, _ []string) (*InspectorResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	now := time.Now()
	criteria := i.criteria[taskID]

	result := &InspectorResult{
		TaskID:             taskID,
		Mode:               i.config.Mode,
		Passed:             true,
		Issues:             []ValidationIssue{},
		CriteriaMet:        criteriaIDs(criteria),
		CriteriaFailed:     []string{},
		QualityGateResults: gateResults(criteria),
		FeedbackHistory:    []InspectorFeedback{},
		StartedAt:          now,
		CompletedAt:        now,
		LoopCount:          1,
	}

	i.result = result
	i.state.LoopsCompleted++
	i.state.LastActiveAt = now
	return result, nil
}

// GetState returns a snapshot of the inspector's current state.
func (i *Inspector) GetState() *InspectorState {
	i.mu.RLock()
	defer i.mu.RUnlock()
	s := i.state
	return &s
}

// GetCurrentResult returns the most recent inspection result.
func (i *Inspector) GetCurrentResult() *InspectorResult {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.result
}

// Start initializes the inspector with the event bus. This is a no-op for the
// backward-compatible shim; real bus wiring lives in the sub-packages.
func (i *Inspector) Start(_ guide.EventBus) error { return nil }

// Stop gracefully stops the inspector.
func (i *Inspector) Stop() {}

// IsRunning returns whether the inspector has been initialized.
func (i *Inspector) IsRunning() bool {
	return i != nil
}

// Close releases any resources held by the inspector.
func (i *Inspector) Close() error { return nil }

func criteriaIDs(c *InspectorCriteria) []string {
	if c == nil {
		return []string{}
	}
	ids := make([]string, len(c.SuccessCriteria))
	for idx, sc := range c.SuccessCriteria {
		ids[idx] = sc.ID
	}
	return ids
}

func gateResults(c *InspectorCriteria) map[string]bool {
	if c == nil {
		return map[string]bool{}
	}
	results := make(map[string]bool, len(c.QualityGates))
	for _, g := range c.QualityGates {
		results[g.Name] = true
	}
	return results
}

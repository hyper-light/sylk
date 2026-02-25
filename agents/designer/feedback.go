package designer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
)

// requestEngineerReviewSkill creates a skill that requests review from the Engineer.
func requestEngineerReviewSkill(d *Designer) *skills.Skill {
	type params struct {
		ComponentName       string   `json:"component_name"`
		IntegrationConcerns string   `json:"integration_concerns"`
		FilesChanged        []string `json:"files_changed"`
	}

	return skills.NewSkill("request_engineer_review").
		Description("Request Engineer review of design implementation for integration concerns, shared state, or API boundaries.").
		Domain("collaboration").
		Keywords("review", "engineer", "integration", "api").
		Priority(80).
		StringParam("component_name", "Name of the component to review", true).
		StringParam("integration_concerns", "Specific integration concerns to evaluate", true).
		ArrayParam("files_changed", "Files created or modified", "string", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if d.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			feedback := map[string]any{
				"type":                 "review_request",
				"component_name":      p.ComponentName,
				"integration_concerns": p.IntegrationConcerns,
				"files_changed":       p.FilesChanged,
				"from_agent":          d.id,
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Design review request: %s — %s", p.ComponentName, p.IntegrationConcerns),
				SourceAgentID:   d.id,
				SourceAgentName: "designer",
				TargetAgentID:   "engineer",
				FireAndForget:   true,
				SessionID:       d.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(d.generateMessageID(), req)
			msg.Metadata = feedback

			if err := d.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish to engineer: %w", err)
			}

			return map[string]any{
				"requested":      true,
				"target":         "engineer",
				"component_name": p.ComponentName,
			}, nil
		}).
		Build()
}

// requestInspectorCheckSkill creates a skill that requests validation from Inspector-pipeline.
func requestInspectorCheckSkill(d *Designer) *skills.Skill {
	type params struct {
		Files     []string `json:"files"`
		CheckType string   `json:"check_type"`
	}

	return skills.NewSkill("request_inspector_check").
		Description("Request Inspector-pipeline validation on created or modified files.").
		Domain("collaboration").
		Keywords("inspector", "check", "validate", "quality").
		Priority(75).
		ArrayParam("files", "Files to inspect", "string", true).
		StringParam("check_type", "Type of check: code_quality, security, style", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if d.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Inspect files for %s: %v", p.CheckType, p.Files),
				SourceAgentID:   d.id,
				SourceAgentName: "designer",
				TargetAgentID:   "inspector-pipeline",
				FireAndForget:   true,
				SessionID:       d.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(d.generateMessageID(), req)
			msg.Metadata = map[string]any{
				"type":       "inspection_request",
				"files":      p.Files,
				"check_type": p.CheckType,
				"from_agent": d.id,
			}

			if err := d.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish to inspector: %w", err)
			}

			return map[string]any{
				"requested":  true,
				"target":     "inspector-pipeline",
				"check_type": p.CheckType,
			}, nil
		}).
		Build()
}

// requestTesterValidationSkill creates a skill that requests test validation from Tester-pipeline.
func requestTesterValidationSkill(d *Designer) *skills.Skill {
	type params struct {
		Files     []string `json:"files"`
		TestScope string   `json:"test_scope"`
	}

	return skills.NewSkill("request_tester_validation").
		Description("Request Tester-pipeline validation on implemented files.").
		Domain("collaboration").
		Keywords("tester", "test", "validate", "quality").
		Priority(75).
		ArrayParam("files", "Files to test", "string", true).
		StringParam("test_scope", "Scope of testing: unit, integration, visual", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if d.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Test validation for %s scope: %v", p.TestScope, p.Files),
				SourceAgentID:   d.id,
				SourceAgentName: "designer",
				TargetAgentID:   "tester-pipeline",
				FireAndForget:   true,
				SessionID:       d.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(d.generateMessageID(), req)
			msg.Metadata = map[string]any{
				"type":       "test_request",
				"files":      p.Files,
				"test_scope": p.TestScope,
				"from_agent": d.id,
			}

			if err := d.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish to tester: %w", err)
			}

			return map[string]any{
				"requested":  true,
				"target":     "tester-pipeline",
				"test_scope": p.TestScope,
			}, nil
		}).
		Build()
}

// askUserClarificationSkill creates a skill that publishes a clarification request
// to the orchestrator for surfacing to the user.
func askUserClarificationSkill(d *Designer) *skills.Skill {
	type params struct {
		Question string   `json:"question"`
		Context  string   `json:"context"`
		Options  []string `json:"options,omitempty"`
	}

	return skills.NewSkill("ask_user_clarification").
		Description("Ask the user for clarification when requirements are ambiguous or multiple valid approaches exist.").
		Domain("collaboration").
		Keywords("clarify", "question", "user", "ambiguous").
		Priority(70).
		StringParam("question", "Specific question for the user", true).
		StringParam("context", "Context explaining why clarification is needed", true).
		ArrayParam("options", "Concrete options for the user to choose from", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if d.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Designer needs clarification: %s", p.Question),
				SourceAgentID:   d.id,
				SourceAgentName: "designer",
				TargetAgentID:   "orchestrator",
				FireAndForget:   true,
				SessionID:       d.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(d.generateMessageID(), req)
			msg.Metadata = map[string]any{
				"type":       "clarification_request",
				"question":   p.Question,
				"context":    p.Context,
				"options":    p.Options,
				"from_agent": d.id,
			}

			if err := d.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish clarification: %w", err)
			}

			return map[string]any{
				"asked":    true,
				"question": p.Question,
			}, nil
		}).
		Build()
}

// reportToEngineerSkill creates a skill that proactively sends design decisions
// affecting implementation to the Engineer.
func reportToEngineerSkill(d *Designer) *skills.Skill {
	type params struct {
		DesignDecision   string   `json:"design_decision"`
		AffectedFiles    []string `json:"affected_files"`
		IntegrationNotes string   `json:"integration_notes"`
	}

	return skills.NewSkill("report_to_engineer").
		Description("Send design decisions that affect implementation to the Engineer. Use when decisions impact shared state, API boundaries, or integration points.").
		Domain("collaboration").
		Keywords("report", "engineer", "design", "decision", "integration").
		Priority(85).
		StringParam("design_decision", "Description of the design decision made", true).
		ArrayParam("affected_files", "Files affected by this decision", "string", true).
		StringParam("integration_notes", "Notes on how this affects integration", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if d.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			feedback := map[string]any{
				"type":              "design_decision",
				"design_decision":   p.DesignDecision,
				"affected_files":    p.AffectedFiles,
				"integration_notes": p.IntegrationNotes,
				"from_agent":        d.id,
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Design decision report: %s", p.DesignDecision),
				SourceAgentID:   d.id,
				SourceAgentName: "designer",
				TargetAgentID:   "engineer",
				FireAndForget:   true,
				SessionID:       d.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(d.generateMessageID(), req)
			msg.Metadata = feedback

			if err := d.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish to engineer: %w", err)
			}

			return map[string]any{
				"reported":         true,
				"target":           "engineer",
				"design_decision":  p.DesignDecision,
			}, nil
		}).
		Build()
}

// reportToOrchestratorSkill creates a skill that reports completion or failure
// to the Orchestrator.
func reportToOrchestratorSkill(d *Designer) *skills.Skill {
	type params struct {
		Status       string   `json:"status"`
		Summary      string   `json:"summary"`
		FilesChanged []string `json:"files_changed,omitempty"`
	}

	return skills.NewSkill("report_to_orchestrator").
		Description("Report task completion or failure to the Orchestrator.").
		Domain("collaboration").
		Keywords("report", "orchestrator", "complete", "status").
		Priority(85).
		StringParam("status", "Task status: completed, failed, blocked", true).
		StringParam("summary", "Summary of what was done or why it failed", true).
		ArrayParam("files_changed", "Files created or modified", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if d.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Designer task %s: %s", p.Status, p.Summary),
				SourceAgentID:   d.id,
				SourceAgentName: "designer",
				TargetAgentID:   "orchestrator",
				FireAndForget:   true,
				SessionID:       d.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(d.generateMessageID(), req)
			msg.Metadata = map[string]any{
				"type":          "task_report",
				"status":        p.Status,
				"summary":       p.Summary,
				"files_changed": p.FilesChanged,
				"from_agent":    d.id,
			}

			if err := d.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish to orchestrator: %w", err)
			}

			return map[string]any{
				"reported": true,
				"target":   "orchestrator",
				"status":   p.Status,
			}, nil
		}).
		Build()
}

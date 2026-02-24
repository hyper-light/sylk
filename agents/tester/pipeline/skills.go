package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
)

// reportToEngineerSkill creates a skill that sends failure reports to the Engineer.
func reportToEngineerSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		TestName     string `json:"test_name"`
		ErrorMessage string `json:"error_message"`
		RootCause    string `json:"root_cause"`
		SuggestedFix string `json:"suggested_fix"`
		File         string `json:"file"`
		Line         int    `json:"line,omitempty"`
	}

	return skills.NewSkill("report_to_engineer").
		Description("Send a failure report with root cause and suggested fix to the pipeline Engineer.").
		Domain("testing").
		Keywords("report", "engineer", "feedback", "failure").
		Priority(85).
		StringParam("test_name", "Name of the failing test", true).
		StringParam("error_message", "Error message from the failure", true).
		StringParam("root_cause", "Root cause analysis", true).
		StringParam("suggested_fix", "Suggested fix for the defect", true).
		StringParam("file", "File containing the defect", true).
		IntParam("line", "Line number of the defect", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if pt.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			feedback := map[string]any{
				"type":          "test_failure",
				"test_name":     p.TestName,
				"error_message": p.ErrorMessage,
				"root_cause":    p.RootCause,
				"suggested_fix": p.SuggestedFix,
				"file":          p.File,
				"line":          p.Line,
				"from_agent":    pt.id,
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Test failure report: %s in %s", p.TestName, p.File),
				SourceAgentID:   pt.id,
				SourceAgentName: "tester-pipeline",
				TargetAgentID:   "engineer",
				FireAndForget:   true,
				SessionID:       pt.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(pt.generateMessageID(), req)
			msg.Metadata = feedback

			if err := pt.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish to engineer: %w", err)
			}

			return map[string]any{
				"reported":  true,
				"target":    "engineer",
				"test_name": p.TestName,
			}, nil
		}).
		Build()
}

// reportToDesignerSkill creates a skill that sends failure reports to the Designer.
func reportToDesignerSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		TestName     string `json:"test_name"`
		ErrorMessage string `json:"error_message"`
		RootCause    string `json:"root_cause"`
		SuggestedFix string `json:"suggested_fix"`
		File         string `json:"file"`
	}

	return skills.NewSkill("report_to_designer").
		Description("Send a failure report with root cause and suggested fix to the pipeline Designer.").
		Domain("testing").
		Keywords("report", "designer", "feedback", "failure").
		Priority(85).
		StringParam("test_name", "Name of the failing test", true).
		StringParam("error_message", "Error message from the failure", true).
		StringParam("root_cause", "Root cause analysis", true).
		StringParam("suggested_fix", "Suggested fix for the defect", true).
		StringParam("file", "File containing the defect", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if pt.bus == nil {
				return nil, fmt.Errorf("bus not available")
			}

			feedback := map[string]any{
				"type":          "test_failure",
				"test_name":     p.TestName,
				"error_message": p.ErrorMessage,
				"root_cause":    p.RootCause,
				"suggested_fix": p.SuggestedFix,
				"file":          p.File,
				"from_agent":    pt.id,
			}

			req := &guide.RouteRequest{
				Input:           fmt.Sprintf("Test failure report: %s in %s", p.TestName, p.File),
				SourceAgentID:   pt.id,
				SourceAgentName: "tester-pipeline",
				TargetAgentID:   "designer",
				FireAndForget:   true,
				SessionID:       pt.config.SessionID,
				Timestamp:       time.Now(),
			}

			msg := guide.NewRequestMessage(pt.generateMessageID(), req)
			msg.Metadata = feedback

			if err := pt.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
				return nil, fmt.Errorf("publish to designer: %w", err)
			}

			return map[string]any{
				"reported":  true,
				"target":    "designer",
				"test_name": p.TestName,
			}, nil
		}).
		Build()
}

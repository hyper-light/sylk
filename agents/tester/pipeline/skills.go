package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/pipeline/coordination"
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
		Usage("Use after you have concrete test or diagnosis evidence that should trigger engineering follow-up. The report should be specific enough for Engineer to act on without rediscovering the issue from scratch.").
		Requirement("Requires a real failure signal or verification finding with root cause and a concrete suggested fix.").
		Satisfies("Publishes a reusable verification artifact and opens an engineer review obligation in the task ledger.").
		Avoid("Do not use for speculative concerns that have not been validated by planning, writing, execution, or diagnosis evidence.").
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

			taskID := strings.TrimSpace(pt.pipelineID)
			if taskID == "" {
				return nil, fmt.Errorf("tester task context unavailable")
			}
			artifact, err := pt.coordinationClient().PublishArtifact(ctx, coordination.PublishArtifactInput{
				TaskID:    taskID,
				TaskName:  pt.currentTaskName(),
				Kind:      "verification_result",
				Summary:   fmt.Sprintf("%s failed in %s", p.TestName, p.File),
				ScopeKind: coordination.ScopeKindTestSurface,
				ScopeKey:  strings.TrimSpace(p.TestName),
				Payload: map[string]any{
					"test_name":     p.TestName,
					"error_message": p.ErrorMessage,
					"root_cause":    p.RootCause,
					"suggested_fix": p.SuggestedFix,
					"file":          p.File,
					"line":          p.Line,
				},
				Evidence: testerEvidenceRefs(p.File),
			})
			if err != nil {
				return nil, err
			}
			review, err := pt.coordinationClient().RequestReview(ctx, coordination.RequestReviewInput{
				TaskID:       taskID,
				ArtifactID:   artifact.ID,
				ReviewerType: "engineer",
				Summary:      fmt.Sprintf("Address failing test %s", p.TestName),
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"reported": true, "target": "engineer", "artifact_id": artifact.ID, "review_id": review.ID}, nil
		}).
		Build()
}

// reportToDesignerSkill creates a skill that sends failure reports to the Designer.
func reportToDesignerSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		TestName         string `json:"test_name"`
		ErrorMessage     string `json:"error_message"`
		RootCause        string `json:"root_cause"`
		SuggestedFix     string `json:"suggested_fix"`
		File             string `json:"file"`
		DesignIssue      string `json:"design_issue,omitempty"`
		DesignSuggestion string `json:"design_suggestion,omitempty"`
	}

	return skills.NewSkill("report_to_designer").
		Description("Send a failure report with root cause and suggested fix to the pipeline Designer.").
		Domain("testing").
		Keywords("report", "designer", "feedback", "failure", "design").
		Priority(85).
		Usage("Use when the verification result points to a UX, accessibility, or design-spec problem that Designer needs to address.").
		Requirement("Requires a real validation finding with enough detail for Designer to understand the design implication and proposed change.").
		Satisfies("Publishes a reusable verification artifact and opens a designer review obligation in the task ledger.").
		Avoid("Do not use for purely implementation-local defects that belong with Engineer.").
		StringParam("test_name", "Name of the failing test", true).
		StringParam("error_message", "Error message from the failure", true).
		StringParam("root_cause", "Root cause analysis", true).
		StringParam("suggested_fix", "Suggested fix for the defect", true).
		StringParam("file", "File containing the defect", true).
		StringParam("design_issue", "Description of the design-specific problem", false).
		StringParam("design_suggestion", "Design-specific fix recommendation", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			taskID := strings.TrimSpace(pt.pipelineID)
			if taskID == "" {
				return nil, fmt.Errorf("tester task context unavailable")
			}
			artifact, err := pt.coordinationClient().PublishArtifact(ctx, coordination.PublishArtifactInput{
				TaskID:    taskID,
				TaskName:  pt.currentTaskName(),
				Kind:      "verification_result",
				Summary:   fmt.Sprintf("%s surfaced a design issue", p.TestName),
				ScopeKind: coordination.ScopeKindUXSurface,
				ScopeKey:  strings.TrimSpace(p.File),
				Payload: map[string]any{
					"test_name":         p.TestName,
					"error_message":     p.ErrorMessage,
					"root_cause":        p.RootCause,
					"suggested_fix":     p.SuggestedFix,
					"file":              p.File,
					"design_issue":      p.DesignIssue,
					"design_suggestion": p.DesignSuggestion,
				},
				Evidence: testerEvidenceRefs(p.File),
			})
			if err != nil {
				return nil, err
			}
			review, err := pt.coordinationClient().RequestReview(ctx, coordination.RequestReviewInput{
				TaskID:       taskID,
				ArtifactID:   artifact.ID,
				ReviewerType: "designer",
				Summary:      fmt.Sprintf("Review design implications for failing test %s", p.TestName),
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"reported": true, "target": "designer", "artifact_id": artifact.ID, "review_id": review.ID}, nil
		}).
		Build()
}

func testerEvidenceRefs(file string) []coordination.EvidenceRef {
	if strings.TrimSpace(file) == "" {
		return nil
	}
	return []coordination.EvidenceRef{{Kind: "file", Value: strings.TrimSpace(file)}}
}

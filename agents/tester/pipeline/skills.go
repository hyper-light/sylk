package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

// reportToEngineerSkill creates a skill that sends failure reports to the Engineer.
func reportToEngineerSkill(pt *PipelineTester) *skills.Skill {
	return skills.NewSkill("report_to_engineer").
		Description("Publish the comprehensive tester verification handoff artifact for Engineer and return the artifact reference that handoff_next should route with.").
		Domain("testing").
		Keywords("report", "engineer", "verification", "handoff", "artifact").
		Priority(85).
		Usage("Use after write_test and run_test_suite when you have the full verification state ready for handoff. This publishes the canonical artifact that handoff_next should reference for Engineer.").
		Requirement("Requires current suite execution evidence. The artifact is built from the active task criteria, authored tests, suite results, and any diagnoses already captured in this turn.").
		Satisfies("Creates the canonical verification_result artifact consumed by handoff_next for Engineer handoff.").
		Avoid("Do not use before run_test_suite or as a substitute for handoff_next. This skill publishes the artifact; it does not route the next turn.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var unused map[string]any
			if len(input) > 0 && string(input) != "null" {
				if err := json.Unmarshal(input, &unused); err != nil {
					return nil, fmt.Errorf("invalid parameters: %w", err)
				}
			}

			artifact, err := pt.publishVerificationArtifact(ctx, "engineer")
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"reported":           true,
				"target":             "engineer",
				"artifact_id":        artifact.ID,
				"artifact_kind":      artifact.Kind,
				"artifact_summary":   artifact.Summary,
				"handoff_references": []string{"artifact:" + artifact.ID},
			}, nil
		}).
		Build()
}

// reportToDesignerSkill creates a skill that sends failure reports to the Designer.
func reportToDesignerSkill(pt *PipelineTester) *skills.Skill {
	return skills.NewSkill("report_to_designer").
		Description("Publish the comprehensive tester verification handoff artifact for Designer and return the artifact reference that handoff_next should route with.").
		Domain("testing").
		Keywords("report", "designer", "verification", "handoff", "artifact").
		Priority(85).
		Usage("Use after write_test and run_test_suite when you have the full verification state ready for handoff. This publishes the canonical artifact that handoff_next should reference for Designer.").
		Requirement("Requires current suite execution evidence. The artifact is built from the active task criteria, authored tests, suite results, and any diagnoses already captured in this turn.").
		Satisfies("Creates the canonical verification_result artifact consumed by handoff_next for Designer handoff.").
		Avoid("Do not use before run_test_suite or as a substitute for handoff_next. This skill publishes the artifact; it does not route the next turn.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var unused map[string]any
			if len(input) > 0 && string(input) != "null" {
				if err := json.Unmarshal(input, &unused); err != nil {
					return nil, fmt.Errorf("invalid parameters: %w", err)
				}
			}

			artifact, err := pt.publishVerificationArtifact(ctx, "designer")
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"reported":           true,
				"target":             "designer",
				"artifact_id":        artifact.ID,
				"artifact_kind":      artifact.Kind,
				"artifact_summary":   artifact.Summary,
				"handoff_references": []string{"artifact:" + artifact.ID},
			}, nil
		}).
		Build()
}

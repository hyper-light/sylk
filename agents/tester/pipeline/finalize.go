package pipeline

import (
	"context"
	"fmt"
	"strings"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

// finalizeForTargets is the callback the shared finalize_pipeline skill invokes
// to package one verification artifact per recipient. The skill handler has
// already validated the suite-freshness and no-repeat preconditions; this
// callback just publishes the artifacts and returns the per-target refs the
// protocol state queues for handoff_next or validate_work to consume.
func (pt *PipelineTester) finalizeForTargets(ctx context.Context, suiteID string, specs []agentshared.PipelineTesterFinalizeTargetSpec) ([]agentshared.PipelineHandoffArtifactRef, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("finalize_pipeline received no target specs")
	}
	refs := make([]agentshared.PipelineHandoffArtifactRef, 0, len(specs))
	for _, spec := range specs {
		artifact, err := pt.publishVerificationArtifact(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("publish verification artifact for %q: %w", spec.Target, err)
		}
		ref := agentshared.PipelineHandoffArtifactRef{
			ArtifactID:   strings.TrimSpace(artifact.ID),
			Kind:         strings.TrimSpace(artifact.Kind),
			Target:       strings.TrimSpace(spec.Target),
			SuiteID:      strings.TrimSpace(suiteID),
			Summary:      strings.TrimSpace(spec.Summary),
			EvidenceRefs: append([]string(nil), spec.EvidenceRefs...),
			FailureFocus: append([]string(nil), spec.FailureFocus...),
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// currentSuiteID returns the id of the suite snapshot captured during this
// turn, or empty string when run_test_suite has not produced one yet.
// swapTaskRuntime resets lastSuiteResult on every turn entry, so a non-empty
// result here implies the LLM ran the suite during this turn.
func (pt *PipelineTester) currentSuiteID() string {
	suite := pt.lastSuiteSnapshot()
	if suite == nil {
		return ""
	}
	return strings.TrimSpace(suite.SuiteID)
}

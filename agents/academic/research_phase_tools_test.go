package academic

import (
	"context"
	"reflect"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestAcademicApplyPhaseToolPolicy_GroundPhaseRemovesBroadSearchAndConsults(t *testing.T) {
	a, err := New(Config{ID: "academic"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	tracker := newSearchEvidenceTracker()
	tracker.phase = researchPhaseGround
	tracker.sawSearch = true
	tracker.queryFingerprints["python packaging"] = map[string]struct{}{"python": {}, "packaging": {}}

	tools := academicApplyPhaseToolPolicy(context.Background(), a.buildToolDefinitions(), tracker)
	got := academicToolNames(tools)
	want := []string{"fetch_document", "web_fetch"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ground-phase tools = %#v, want %#v", got, want)
	}
}

func TestAcademicApplyPhaseToolPolicy_SatisfiedArchitectContractRestrictsToResearchPaper(t *testing.T) {
	a, err := New(Config{ID: "academic"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	surface := a.architectResearchWorkflowSurface()
	tools := a.buildToolDefinitionsWithSurface(surface)

	tracker := newSearchEvidenceTracker()
	tracker.phase = researchPhaseSynthesize
	tracker.sawSearch = true
	tracker.fetchedURLs["https://packaging.python.org/en/latest/guides/writing-pyproject-toml/"] = struct{}{}
	tracker.recordEvidenceClass(academicEvidenceCodebaseFit)

	ctx := WithAcademicCompletionContract(context.Background(), academicCompletionContractForForwardedRequest(&guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
		Input:         "Research Python packaging guidance.",
	}))
	ctx = WithAcademicTurnState(ctx, newAcademicTurnState(academicTurnActionResearchPaper, "Architect requires a reusable artifact."))

	filtered := academicApplyPhaseToolPolicy(ctx, tools, tracker)
	got := academicToolNames(filtered)
	want := []string{"author_research_paper"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("synthesis tools = %#v, want %#v", got, want)
	}
}

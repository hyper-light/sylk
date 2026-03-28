package academic

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestAcademicCompletionContractForForwardedRequest_ArchitectRequiresCodebaseFit(t *testing.T) {
	contract := academicCompletionContractForForwardedRequest(&guide.ForwardedRequest{
		SourceAgentID:   "architect-primary",
		SourceAgentName: "Architect",
		Intent:          guide.IntentRecall,
		Input:           "Research Python packaging guidance for the current system.",
	})
	if contract == nil {
		t.Fatal("expected completion contract")
	}
	if !containsEvidenceClass(contract.RequiredEvidence, academicEvidenceCodebaseFit) {
		t.Fatalf("required evidence = %#v, want codebase fit for architect research", contract.RequiredEvidence)
	}
}

func TestAcademicCompletionContractForForwardedRequest_GenericFetchDoesNotRequireCodebaseFit(t *testing.T) {
	contract := academicCompletionContractForForwardedRequest(&guide.ForwardedRequest{
		SourceAgentID: "engineer",
		Intent:        guide.IntentFetch,
		Input:         "Fetch https://packaging.python.org/en/latest/guides/writing-pyproject-toml/ and summarize it.",
	})
	if contract == nil {
		t.Fatal("expected completion contract")
	}
	if containsEvidenceClass(contract.RequiredEvidence, academicEvidenceCodebaseFit) {
		t.Fatalf("required evidence = %#v, did not want codebase fit for generic external fetch", contract.RequiredEvidence)
	}
	if !containsEvidenceClass(contract.PreferredEvidence, academicEvidenceCodebaseFit) {
		t.Fatalf("preferred evidence = %#v, want codebase fit to remain preferred", contract.PreferredEvidence)
	}
}

func TestAcademicCompletionContractForResearchQuery_CodebaseSpecificRecallRequiresCodebaseFit(t *testing.T) {
	contract := academicCompletionContractForResearchQuery(&ResearchQuery{
		Query:  "Recommend the best caching strategy for this codebase.",
		Intent: IntentRecall,
	})
	if contract == nil {
		t.Fatal("expected completion contract")
	}
	if !containsEvidenceClass(contract.RequiredEvidence, academicEvidenceCodebaseFit) {
		t.Fatalf("required evidence = %#v, want codebase fit for codebase-specific recall", contract.RequiredEvidence)
	}
}

func TestAcademicCompletionContractGuidancePrompt_UsesEvidenceFrame(t *testing.T) {
	contract := academicCompletionContractForResearchQuery(&ResearchQuery{
		Query:  "Compare approaches for a decision with current external evidence.",
		Intent: IntentRecall,
	})
	if contract == nil {
		t.Fatal("expected completion contract")
	}
	prompt := contract.guidancePrompt()
	for _, needle := range []string{
		"Before choosing a winner, define the decision frame",
		"Do not collapse the answer into a short answer, shortlist, or clean winner",
		"Treat self-authored, vendor-authored, or project-authored sources as capability evidence first",
		"which evidence classes are relevant",
		"authoritative or primary sources",
		"counterevidence or failure cases",
		"implementation or operational evidence",
		"Prefer substance over terseness: the target is a serious research memo, not a lightweight recommendation blurb.",
		"keep the conclusion provisional or explicitly inconclusive",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("guidancePrompt missing %q:\n%s", needle, prompt)
		}
	}
}

func containsEvidenceClass(classes []academicEvidenceClass, target academicEvidenceClass) bool {
	for _, class := range classes {
		if class == target {
			return true
		}
	}
	return false
}

package academic

import (
	"strings"
	"testing"
)

func TestAcademicResearchSkills_EmphasizeGroundedEvidence(t *testing.T) {
	webSearch := webSearchSkill()
	webSearchPractices := strings.Join(webSearch.BestPractices, "\n")
	for _, needle := range []string{
		"must be grounded with ground_source or an equivalent fetch skill first",
		"search for primary empirical sources such as official benchmarks, standards, incident reports, papers, or vendor telemetry",
	} {
		if !strings.Contains(webSearchPractices, needle) {
			t.Fatalf("web_search best practices missing %q:\n%s", needle, webSearchPractices)
		}
	}

	groundSource := groundSourceSkill(&Academic{})
	if !strings.Contains(groundSource.UsageDoc, "before relying on it or citing it in the response") {
		t.Fatalf("ground_source usage missing citation requirement:\n%s", groundSource.UsageDoc)
	}

	fetchDocument := fetchDocumentSkill(&Academic{})
	fetchDocumentPractices := strings.Join(fetchDocument.BestPractices, "\n")
	for _, needle := range []string{
		"academic papers, official benchmark reports, standards, and incident studies",
		"verify the date, sample size, workload or experimental setup, and major caveats",
	} {
		if !strings.Contains(fetchDocumentPractices, needle) {
			t.Fatalf("fetch_document best practices missing %q:\n%s", needle, fetchDocumentPractices)
		}
	}

	recommend := recommendSolutionSkill(&Academic{})
	recommendPractices := strings.Join(recommend.BestPractices, "\n")
	for _, needle := range []string{
		"Back material claims about performance, reliability, cost, security impact, scale, or adoption with grounded statistics",
		"mostly qualitative, say so plainly instead of overstating confidence",
	} {
		if !strings.Contains(recommendPractices, needle) {
			t.Fatalf("recommend_solution best practices missing %q:\n%s", needle, recommendPractices)
		}
	}
}

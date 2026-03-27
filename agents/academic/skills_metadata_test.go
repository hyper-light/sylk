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
		"search for primary empirical sources such as official benchmarks, standards, incident reports, papers, or official operational telemetry",
	} {
		if !strings.Contains(webSearchPractices, needle) {
			t.Fatalf("web_search best practices missing %q:\n%s", needle, webSearchPractices)
		}
	}
	webSearchRequirements := strings.Join(webSearch.Requirements, "\n")
	for _, needle := range []string{
		"For material claims, do not stop after one promising source when corroborating grounded sources are available.",
		"Look for sources that let you validate assumptions, inspect methodology, and identify bias or threats to validity",
		"Assume every source you may cite later will need its own grounding step",
	} {
		if !strings.Contains(webSearchRequirements, needle) {
			t.Fatalf("web_search requirements missing %q:\n%s", needle, webSearchRequirements)
		}
	}

	groundSource := groundSourceSkill(&Academic{})
	if !strings.Contains(groundSource.UsageDoc, "before relying on it or citing it in the response") {
		t.Fatalf("ground_source usage missing citation requirement:\n%s", groundSource.UsageDoc)
	}
	groundSourcePractices := strings.Join(groundSource.BestPractices, "\n")
	for _, needle := range []string{
		"part of a corroborated evidence set for important claims",
		"Inspect whether the source reveals dataset quality, hidden assumptions, incentive misalignment, or threats to validity",
	} {
		if !strings.Contains(groundSourcePractices, needle) {
			t.Fatalf("ground_source best practices missing %q:\n%s", needle, groundSourcePractices)
		}
	}

	fetchDocument := fetchDocumentSkill(&Academic{})
	fetchDocumentPractices := strings.Join(fetchDocument.BestPractices, "\n")
	for _, needle := range []string{
		"academic papers, official benchmark reports, standards, and incident studies",
		"verify the date, sample size, workload or experimental setup, and major caveats",
		"Call out dataset bias, threat models, assumptions, and threats to validity",
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
		"Surface the key assumptions, methodology caveats, and bias risks",
	} {
		if !strings.Contains(recommendPractices, needle) {
			t.Fatalf("recommend_solution best practices missing %q:\n%s", needle, recommendPractices)
		}
	}
	recommendRequirements := strings.Join(recommend.Requirements, "\n")
	for _, needle := range []string{
		"justify the choice with explicit evidence categories",
		"Validate important assumptions and note where the supporting math, datasets, or empirical evidence is thin, biased, or contested.",
	} {
		if !strings.Contains(recommendRequirements, needle) {
			t.Fatalf("recommend_solution requirements missing %q:\n%s", needle, recommendRequirements)
		}
	}

	compare := compareApproachesSkill(&Academic{})
	compareRequirements := strings.Join(compare.Requirements, "\n")
	for _, needle := range []string{
		"Validate the assumptions behind the comparison and note where the evidence does not cleanly support the ranking.",
		"evaluate the options against the criteria that actually drive the decision and summarize the result in a table",
	} {
		if !strings.Contains(compareRequirements, needle) {
			t.Fatalf("compare_approaches requirements missing %q:\n%s", needle, compareRequirements)
		}
	}

	researchRequirements := strings.Join(researchTopicSkill(&Academic{}).Requirements, "\n")
	for _, needle := range []string{
		"Treat the answer as incomplete if it lacks the relevant structured artifact for the question",
		"run a rigor audit across all relevant dimensions",
	} {
		if !strings.Contains(researchRequirements, needle) {
			t.Fatalf("research_topic requirements missing %q:\n%s", needle, researchRequirements)
		}
	}

	compareAvoids := strings.Join(compare.Avoids, "\n")
	if !strings.Contains(compareAvoids, "Do not reduce the comparison to a default winner, a few alternatives, and a bare source list.") {
		t.Fatalf("compare_approaches avoids missing shallow-roundup guard:\n%s", compareAvoids)
	}
	compareRequirements = strings.Join(compare.Requirements, "\n")
	if !strings.Contains(compareRequirements, "run a rigor audit over all relevant comparison dimensions") {
		t.Fatalf("compare_approaches requirements missing full rigor-audit guard:\n%s", compareRequirements)
	}
	if !strings.Contains(recommendRequirements, "run a rigor audit over all relevant recommendation dimensions") {
		t.Fatalf("recommend_solution requirements missing full rigor-audit guard:\n%s", recommendRequirements)
	}
}

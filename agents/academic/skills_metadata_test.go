package academic

import (
	"strings"
	"testing"
)

func TestAcademicResearchSkills_EmphasizeGroundedEvidence(t *testing.T) {
	webSearch := webSearchSkill()
	webSearchPractices := strings.Join(webSearch.BestPractices, "\n")
	for _, needle := range []string{
		"Treat provider-native web_search as evidence gathering, not just a title/snippet lookup.",
		"decisive exact URL that you plan to carry into the final answer",
		"search for primary empirical sources such as official benchmarks, standards, incident reports, papers, or official operational telemetry",
		"Search across the evidence classes that matter for the question",
		"Treat vendor-authored or self-authored sources as capability evidence first",
		"Do a negative pass: after finding promising support",
		"expect Sylk to auto-ground it when appropriate",
		"Use ground_source, web_fetch, fetch_document, or one bounded crawl_links follow-up",
	} {
		if !strings.Contains(webSearchPractices, needle) {
			t.Fatalf("web_search best practices missing %q:\n%s", needle, webSearchPractices)
		}
	}
	webSearchRequirements := strings.Join(webSearch.Requirements, "\n")
	for _, needle := range []string{
		"For material claims, do not stop after one promising source when corroborating grounded sources are available.",
		"Look for sources that let you validate assumptions, inspect methodology, and identify bias or threats to validity",
		"identify the relevant evidence classes for the domain",
		"support is dominated by self-authored sources and comparative claims matter",
		"Do not stop after supportive evidence if contrary or limiting evidence is likely to exist",
		"do not imply that every intermediate searched URL received the same treatment",
		"decisive surfaced exact URL",
	} {
		if !strings.Contains(webSearchRequirements, needle) {
			t.Fatalf("web_search requirements missing %q:\n%s", needle, webSearchRequirements)
		}
	}

	groundSource := groundSourceSkill(&Academic{})
	if !strings.Contains(groundSource.UsageDoc, "still needs the secured local grounding path") {
		t.Fatalf("ground_source usage missing citation requirement:\n%s", groundSource.UsageDoc)
	}
	groundSourcePractices := strings.Join(groundSource.BestPractices, "\n")
	for _, needle := range []string{
		"Do not call this reflexively after every native web_search turn",
		"what evidence class it represents",
		"self-authored, vendor-authored, independently reported, or otherwise interested",
		"part of a corroborated evidence set for important claims",
		"Inspect whether the source reveals dataset quality, hidden assumptions, incentive misalignment, or threats to validity",
	} {
		if !strings.Contains(groundSourcePractices, needle) {
			t.Fatalf("ground_source best practices missing %q:\n%s", needle, groundSourcePractices)
		}
	}
	groundSourceRequirements := strings.Join(groundSource.Requirements, "\n")
	for _, needle := range []string{
		"still needs the secured fetch path",
		"know its date, scope, evidence basis or methodology, and key limitation",
		"decisive surfaced exact URL",
		"determine whether it is primary evidence, official guidance, secondary commentary",
		"treat it as capability evidence unless independently corroborated",
	} {
		if !strings.Contains(groundSourceRequirements, needle) {
			t.Fatalf("ground_source requirements missing %q:\n%s", needle, groundSourceRequirements)
		}
	}

	fetchDocument := fetchDocumentSkill(&Academic{})
	webFetch := webFetchSkill(&Academic{})
	webFetchPractices := strings.Join(webFetch.BestPractices, "\n")
	for _, needle := range []string{
		"already grounded for citation and synthesis",
		"already grounded the same exact URL",
		"explicit page-style local grounding",
	} {
		if !strings.Contains(webFetchPractices, needle) {
			t.Fatalf("web_fetch best practices missing %q:\n%s", needle, webFetchPractices)
		}
	}
	webFetchRequirements := strings.Join(webFetch.Requirements, "\n")
	for _, needle := range []string{
		"satisfies the exact-URL grounding requirement",
	} {
		if !strings.Contains(webFetchRequirements, needle) {
			t.Fatalf("web_fetch requirements missing %q:\n%s", needle, webFetchRequirements)
		}
	}

	fetchDocumentPractices := strings.Join(fetchDocument.BestPractices, "\n")
	for _, needle := range []string{
		"already grounded for citation and synthesis",
		"academic papers, official benchmark reports, standards, and incident studies",
		"already grounded the same exact URL",
		"explicit document-style local grounding",
		"verify the date, sample size, workload or experimental setup, and major caveats",
		"what evidence class the document represents",
		"Call out dataset bias, threat models, assumptions, and threats to validity",
	} {
		if !strings.Contains(fetchDocumentPractices, needle) {
			t.Fatalf("fetch_document best practices missing %q:\n%s", needle, fetchDocumentPractices)
		}
	}
	fetchDocumentRequirements := strings.Join(fetchDocument.Requirements, "\n")
	for _, needle := range []string{
		"satisfies the exact-URL grounding requirement",
	} {
		if !strings.Contains(fetchDocumentRequirements, needle) {
			t.Fatalf("fetch_document requirements missing %q:\n%s", needle, fetchDocumentRequirements)
		}
	}

	recommend := recommendSolutionSkill(&Academic{})
	recommendPractices := strings.Join(recommend.BestPractices, "\n")
	for _, needle := range []string{
		"Start from decision criteria and evidence quality",
		"Back material claims about performance, reliability, cost, security impact, scale, or adoption with grounded statistics",
		"surface the decision-relevant metrics for this domain",
		"mostly qualitative, say so plainly instead of overstating confidence",
		"Research the strongest downside, failure mode, criticism, lock-in risk, migration burden, or conflicting evidence",
		"Treat self-authored, vendor-authored, or project-authored sources as capability evidence first",
		"Surface the key assumptions, methodology caveats, and bias risks",
	} {
		if !strings.Contains(recommendPractices, needle) {
			t.Fatalf("recommend_solution best practices missing %q:\n%s", needle, recommendPractices)
		}
	}
	recommendRequirements := strings.Join(recommend.Requirements, "\n")
	for _, needle := range []string{
		"map the relevant evidence classes for this domain",
		"justify the choice with explicit evidence categories",
		"Show why the preferred option beats the strongest alternative on the highest-weighted criteria",
		"Validate important assumptions and note where the supporting math, datasets, or empirical evidence is thin, biased, or contested.",
		"Do not open a substantial externally sourced recommendation with a naked default pick",
		"Do not emit a Short Answer, shortlist, or compressed winner section before the evidence basis and strongest-alternative comparison are established.",
		"Do not assign high confidence unless the recommendation is supported by broad grounded evidence",
		"keep the recommendation provisional or explicitly inconclusive",
	} {
		if !strings.Contains(recommendRequirements, needle) {
			t.Fatalf("recommend_solution requirements missing %q:\n%s", needle, recommendRequirements)
		}
	}

	compare := compareApproachesSkill(&Academic{})
	comparePractices := strings.Join(compare.BestPractices, "\n")
	for _, needle := range []string{
		"Define or infer the governing criteria before ranking options",
		"quantify the decision-relevant metrics for this domain",
		"Seek evidence across multiple relevant classes for each option",
		"Treat self-authored, vendor-authored, or project-authored sources as capability evidence first",
	} {
		if !strings.Contains(comparePractices, needle) {
			t.Fatalf("compare_approaches best practices missing %q:\n%s", needle, comparePractices)
		}
	}
	compareRequirements := strings.Join(compare.Requirements, "\n")
	for _, needle := range []string{
		"strongest supporting evidence, the strongest contrary evidence or failure mode",
		"Map the relevant evidence classes for the domain and note which are strong, weak, or unavailable",
		"Validate the assumptions behind the comparison and note where the evidence does not cleanly support the ranking.",
		"evaluate the options against the criteria that actually drive the decision and summarize the result in a table",
		"Do not start a substantial comparison with a winner",
		"Do not emit a Short Answer, shortlist, or compressed winner section before the comparison evidence is established.",
		"Only use high confidence when multiple relevant evidence classes align",
		"mark it provisional or incomplete instead of presenting a clean winner",
	} {
		if !strings.Contains(compareRequirements, needle) {
			t.Fatalf("compare_approaches requirements missing %q:\n%s", needle, compareRequirements)
		}
	}

	researchRequirements := strings.Join(researchTopicSkill(&Academic{}).Requirements, "\n")
	for _, needle := range []string{
		"map the relevant evidence classes for the domain",
		"Do not open a substantial externally sourced synthesis with a naked winner or default pick",
		"Do not emit a Short Answer, shortlist, or compressed winner section before the evidence basis and comparison are established.",
		"Treat the answer as incomplete if it lacks the relevant structured artifact for the question",
		"run a rigor audit across all relevant dimensions",
		"Calibrate confidence to evidence completeness",
		"keep the conclusion provisional or explicitly inconclusive",
	} {
		if !strings.Contains(researchRequirements, needle) {
			t.Fatalf("research_topic requirements missing %q:\n%s", needle, researchRequirements)
		}
	}
	researchAvoids := strings.Join(researchTopicSkill(&Academic{}).Avoids, "\n")
	if !strings.Contains(researchAvoids, "Do not use unsupported adjectives like best, strongest, safer, simpler, faster, mature, or standard") {
		t.Fatalf("research_topic avoids missing unsupported-adjective guard:\n%s", researchAvoids)
	}

	compareAvoids := strings.Join(compare.Avoids, "\n")
	if !strings.Contains(compareAvoids, "Do not reduce the comparison to a default winner, a few alternatives, and a bare source list.") {
		t.Fatalf("compare_approaches avoids missing shallow-roundup guard:\n%s", compareAvoids)
	}
	if !strings.Contains(compareAvoids, "Do not use unsupported adjectives like best, strongest, simpler, faster, safer, or mature") {
		t.Fatalf("compare_approaches avoids missing unsupported-adjective guard:\n%s", compareAvoids)
	}
	compareRequirements = strings.Join(compare.Requirements, "\n")
	if !strings.Contains(compareRequirements, "run a rigor audit over all relevant comparison dimensions") {
		t.Fatalf("compare_approaches requirements missing full rigor-audit guard:\n%s", compareRequirements)
	}
	if !strings.Contains(recommendRequirements, "run a rigor audit over all relevant recommendation dimensions") {
		t.Fatalf("recommend_solution requirements missing full rigor-audit guard:\n%s", recommendRequirements)
	}
	recommendAvoids := strings.Join(recommend.Avoids, "\n")
	if !strings.Contains(recommendAvoids, "Do not use unsupported adjectives like best, strongest, safer, simpler, faster, mature, or standard") {
		t.Fatalf("recommend_solution avoids missing unsupported-adjective guard:\n%s", recommendAvoids)
	}
}

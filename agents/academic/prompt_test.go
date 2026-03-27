package academic

import (
	"strings"
	"testing"
)

func TestDefaultSystemPrompt_IncludesStructuredMarkdownFormattingGuidance(t *testing.T) {
	for _, want := range []string{
		"Assume your own background knowledge is incomplete by default.",
		"`web_search` is the default way to discover authoritative sources",
		"Once `web_search` surfaces a promising source",
		"Any link discovered via `web_search` that you want to cite, quote, or include in a `Sources` section MUST be grounded first",
		"`ground_source` or the appropriate fetch skill",
		"research multiple credible options before settling on one",
		"Do not rely on memory alone when the source could materially change the recommendation.",
		"prefer grounded quantitative evidence whenever feasible",
		"Do not quote percentages, benchmark results, incident rates, adoption figures, or study conclusions unless you have inspected the underlying source",
		"include the measurement date and benchmark, study, or workload context",
		"treat the claim as qualitative or provisional instead of implying false precision",
		"Write clear, readable markdown",
		"Use proper markdown headings with a space after the heading markers",
		"Default structure for substantial recommendation answers",
		"they must still read like a thoughtful technical recommendation, not a stiff report template",
		"substantial research answers should be detailed enough to survive scrutiny from a senior engineer",
		"Do not write like a form with repeated `Applicability:` / `Confidence:` / `Why:` one-liners",
		"Go one level deeper than a surface list of tools or options",
		"When comparing options, say which one you would actually choose and why the others lose",
		"Think like a PhD researcher or principal researcher evaluating design space",
		"include at least one proof-of-concept, prototype sketch, worked example, pseudo-interface, or architecture fragment",
		"Treat proof-of-concept examples as research/theoretical implementation sketches",
		"Do not stop at naming libraries, frameworks, or patterns; show how the preferred choice would actually look and behave",
		"When the recommendation depends on measured outcomes, surface the most decision-relevant grounded statistics",
		"Architecture / System Design Implications",
		"## Recommendation",
		"## Fit For This Codebase",
		"## Caveats",
		"## Sources",
	} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("default system prompt missing %q", want)
		}
	}
}

func TestAcademicConversationPrompt_IncludesScanFriendlyFormattingGuidance(t *testing.T) {
	for _, want := range []string{
		"For longer answers, use clean markdown sections",
		"Once web_search surfaces a promising source, ground it with `ground_source`",
		"Any link found through web_search that you plan to cite or include in the answer must be grounded first",
		"Treat grounded evidence as immediately usable",
		"look at multiple credible options before settling on one",
		"back it with grounded, verifiable statistics whenever credible data exists",
		"Do not quote benchmark numbers, percentages, or study conclusions unless you grounded the source itself.",
		"note the source date and measurement context",
		"If the evidence is mostly qualitative, indirect, or stale, say so directly",
		"Keep headings short and properly formatted.",
		"Prefer a small number of high-signal sections over sprawling outlines.",
		"Sound like you are thinking with the user, not filling out a recommendation form.",
		"After the recommendation, spend a sentence or two on the actual reasoning that makes it a good fit here.",
		"If multiple options are credible, explain why your preferred option beats the strongest alternative.",
		"Avoid laundry-list answers that read like scraped documentation summaries.",
		"Unless the user explicitly asks for brevity, answer with enough detail to justify the conclusion to another senior engineer or researcher.",
		"If the question is architectural, include system-design reasoning instead of stopping at library selection.",
		"Include a small proof-of-concept, prototype sketch, worked example, or pseudo-interface that shows how the preferred idea would work in practice.",
		"Keep that prototype at the level of research and theoretical implementation unless the user explicitly asks for production-ready code.",
		"For tool, library, or framework questions, show what using the preferred option would actually look like instead of stopping at a named recommendation.",
		"When discussing architecture, reason from first principles and make the design legible to another senior engineer.",
		"Make the answer feel like a serious research memo or design note, not a thin executive summary.",
	} {
		if !strings.Contains(AcademicConversationPrompt, want) {
			t.Fatalf("conversation prompt missing %q", want)
		}
	}
}

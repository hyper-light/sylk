package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

func (a *Academic) registerFetchSkills() {
	a.skills.Register(webSearchSkill())
	a.skills.Register(groundSourceSkill(a))
	a.skills.Register(webFetchSkill(a))
	a.skills.Register(fetchDocumentSkill(a))
	a.skills.Register(crawlLinksSkill(a))
}

func webSearchSkill() *skills.Skill {
	return skills.NewSkill("web_search").
		Description(
			"Search the public web using the model provider's native search capability. "+
				"Use this to discover and inspect authoritative sources when you do not already know the URL. "+
				"Native search may already open/read pages in-context, and Sylk may automatically secure-fetch surfaced exact URLs for local grounding and ingestion.",
		).
		Domain("research").
		Keywords("search", "web", "internet", "google", "discover", "find sources", "documentation", "papers").
		Usage("Use when you need to discover candidate sources on the public web and do not already know the right URL. Native search may already inspect the strongest surfaced pages in-context, and Sylk may automatically ground high-quality exact URLs through the guarded fetch pipeline.").
		Example("Search official Go and PostgreSQL sources before recommending connection-pooling practices.").
		Example("Search for standards, RFCs, official docs, and academic papers when the URL is not already known.").
		BestPractice("Prefer official documentation, standards bodies, project maintainers, and primary-source papers over secondary commentary.").
		BestPractice("Treat provider-native web_search as evidence gathering, not just a title/snippet lookup.").
		BestPractice("If web_search surfaces a decisive exact URL that you plan to carry into the final answer, make sure that surfaced URL is one you are prepared to stand behind and that it goes through the secured fetch path if Sylk has not already grounded it.").
		BestPractice("Search across the evidence classes that matter for the question, not only the most convenient class such as vendor docs or supportive commentary.").
		BestPractice("When the answer depends on performance, reliability, cost, scale, security impact, or adoption numbers, search for primary empirical sources such as official benchmarks, standards, incident reports, papers, or official operational telemetry instead of summary blogs.").
		BestPractice("When measurable criteria matter, search for the decision-relevant measurable indicators for this domain rather than relying on generic claims or slogans.").
		BestPractice("Treat vendor-authored or self-authored sources as capability evidence first, then search for independent corroboration if the conclusion depends on comparative claims.").
		BestPractice("Do a negative pass: after finding promising support, search for criticisms, failure cases, conflicting evidence, lock-in, migration burden, or adoption barriers that could overturn the recommendation.").
		BestPractice("When native web_search surfaces a clearly relevant, high-quality exact URL, expect Sylk to auto-ground it when appropriate instead of reflexively issuing a second fetch call.").
		BestPractice("Use ground_source, web_fetch, fetch_document, or one bounded crawl_links follow-up when you already know the exact URL, need a particular fetch mode, or need to force grounding of an exact URL that has not yet gone through the secured fetch path.").
		Requirement("Do not treat a bare web_search hit as citeable evidence. If a decisive surfaced exact URL carries the answer, that surfaced URL must go through the secured fetch path before citation if Sylk has not already done so.").
		Requirement("If a strong candidate exact URL is already available and still has not gone through the secured fetch path, do not end the turn at discovery alone; ground that URL now.").
		Requirement("For substantial questions, identify the relevant evidence classes for the domain and search across them until you know which are strong, weak, or unavailable.").
		Requirement("If quantitative claims matter, keep searching until you find credible empirical or official evidence, or explicitly conclude that strong quantitative evidence is unavailable.").
		Requirement("When relevant literature, benchmarks, or postmortems exist, surface them as candidates for deeper grounding instead of relying only on surface-level pages or summaries.").
		Requirement("For material claims, do not stop after one promising source when corroborating grounded sources are available.").
		Requirement("Look for sources that let you validate assumptions, inspect methodology, and identify bias or threats to validity instead of only finding affirmative evidence.").
		Requirement("Do not stop after supportive evidence if contrary or limiting evidence is likely to exist; search for the strongest competing case too.").
		Requirement("If the current support is dominated by self-authored sources and comparative claims matter, keep searching for independent validation or be prepared to mark the conclusion provisional.").
		Requirement("Assume the decisive surfaced exact URL you carry forward may need its own secured fetch step if Sylk did not already ground it automatically, and do not imply that every intermediate searched URL received the same treatment.").
		Avoid("Do not surface raw search hits, titles, or URLs as references before you have actually examined them and, when needed, grounded the exact URL through the secured fetch path.").
		Avoid("Do not treat popularity or SEO rank as evidence for a technical recommendation.").
		Priority(95).
		TokenEstimate(220).
		ProviderTool(skills.ProviderTool{
			Kind: skills.ProviderToolKindNativeWebSearch,
			WebSearch: &skills.WebSearchOptions{
				SearchContextSize: skills.WebSearchContextSizeHigh,
			},
		}).
		Build()
}

func groundSourceSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("ground_source").
		Description(
			"Force the secured local grounding path for a promising public source URL discovered via web_search or already known. "+
				"Fetch the source through the secure pipeline, return grounded content immediately, "+
				"and queue background persistence into the knowledge graph and document store when configured. "+
				"A successful `web_fetch` or `fetch_document` call for the same exact URL is equally valid grounding for citation and synthesis.",
		).
		Domain("research").
		Keywords("ground", "source", "fetch", "ingest", "document", "page", "evidence").
		Priority(92).
		TokenEstimate(550).
		StringParam("url", "The promising source URL to ground", true).
		StringParam("reason", "Why this source looks promising enough to ground", true).
		EnumParam("expected_type", "Expected source type", []string{"auto", "page", "document"}, false).
		Usage("Use when you already have an exact URL and want the runtime to choose the fetch mode automatically, or when a searched URL still needs the secured local grounding path before you rely on it or cite it.").
		BestPractice("If an exact URL still needs local secured grounding, ground it immediately instead of doing another broad search first.").
		BestPractice("Do not call this reflexively after every native web_search turn; native search may already have inspected the page and Sylk may already auto-ground surfaced high-quality exact URLs.").
		BestPractice("Prefer ground_source over repeating similar web_search queries once you already have a promising candidate URL.").
		BestPractice("For each grounded source, identify what evidence class it represents, what claims it can support, what claims it cannot support, and the strongest limitation or downside it reveals.").
		BestPractice("Identify whether the source is self-authored, vendor-authored, independently reported, or otherwise interested, and adjust how much comparative weight it should carry.").
		BestPractice("Ground the source before quoting statistics, benchmarks, percentages, or study conclusions from it.").
		BestPractice("When a number materially affects the recommendation, capture its publication date and sample, workload, or measurement context from the grounded source when available.").
		BestPractice("When grounding a paper, benchmark, or empirical study, extract the research question, method, key findings, and major limitations.").
		BestPractice("Use grounded sources as part of a corroborated evidence set for important claims whenever multiple credible sources exist.").
		BestPractice("Inspect whether the source reveals dataset quality, hidden assumptions, incentive misalignment, or threats to validity that should lower confidence.").
		Requirement("Use this when an exact URL discovered through web_search still needs the secured fetch path before citing, quoting, or recommending from it.").
		Requirement("Before citing a grounded source for a material claim, know its date, scope, evidence basis or methodology, and key limitation when those factors materially affect the claim.").
		Requirement("Treat this as the grounding tool for the decisive surfaced exact URL you are carrying into the answer, or for any other exact URL that still explicitly needs the secured fetch path.").
		Requirement("Do not treat a grounded source as generic proof of superiority; determine whether it is primary evidence, official guidance, secondary commentary, or another evidence class and cite it accordingly.").
		Requirement("If the source is self-authored or vendor-authored, treat it as capability evidence unless independently corroborated for the comparative claim at issue.").
		Avoid("Do not assume a searched page is citeable just because its title or snippet looks authoritative.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				URL          string `json:"url"`
				Reason       string `json:"reason"`
				ExpectedType string `json:"expected_type"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.URL) == "" {
				return nil, fmt.Errorf("url is required")
			}
			if strings.TrimSpace(params.Reason) == "" {
				return nil, fmt.Errorf("reason is required")
			}
			return a.executeGroundSource(ctx, params.URL, params.Reason, params.ExpectedType)
		}).
		Build()
}

// webFetchSkill fetches a URL through the secure pipeline and returns
// the extracted text content. All content passes through SecurityContext,
// FetchPolicy, ConsentGate, quarantine, and Guardian inspection.
func webFetchSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("web_fetch").
		Description(
			"Fetch a web page or document URL through the secure pipeline. "+
				"Content is quarantined and inspected by Guardian before use. "+
				"User consent is required unless the domain is pre-approved. "+
				"Returns grounded text content immediately and queues background persistence when ingestion is configured. "+
				"A successful call satisfies the per-link grounding obligation for that exact URL.",
		).
		Domain("research").
		Keywords("fetch", "download", "url", "web", "page", "http").
		Usage("Use when you already know the exact URL for a specific page, benchmark, report, or documentation page and want to force page-style local grounding before relying on its claims.").
		BestPractice("Use this when you already know the exact page URL, or when a surfaced URL still needs explicit page-style local grounding.").
		BestPractice("If native web_search or Sylk already grounded the same exact URL, do not redundantly call web_fetch again unless you need to force this fetch mode or refresh the local fetch.").
		BestPractice("Treat a successful web_fetch for an exact URL as already grounded for citation and synthesis; do not call ground_source again for the same URL unless you need a different fetch mode.").
		BestPractice("Do not quote benchmark numbers, percentages, or performance claims from a page until you have fetched and inspected it through the secure pipeline.").
		BestPractice("For numeric claims, note the publication date and benchmark or study context so the recommendation does not overgeneralize stale or narrow results.").
		BestPractice("Extract what kind of evidence the page actually provides, what it can and cannot support, and any obvious limitations or institutional incentives that affect trust.").
		Requirement("Use this for exact URLs you plan to cite, quote, or rely on when you need explicit page-style local grounding and ground_source is not the clearer entry point.").
		Requirement("A successful web_fetch satisfies the exact-URL grounding requirement for that URL.").
		Avoid("Do not cite a page from memory or from search snippets when you can fetch it directly.").
		Priority(90).
		TokenEstimate(500).
		StringParam("url", "The URL to fetch", true).
		StringParam("reason", "Why this content is needed for research", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				URL    string `json:"url"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.URL == "" {
				return nil, fmt.Errorf("url is required")
			}
			if params.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			return a.executeFetch(ctx, params.URL, params.Reason)
		}).
		Build()
}

// fetchDocumentSkill fetches a document (PDF, HTML, Markdown) and extracts
// readable text, then ingests it into the knowledge graph when an ingest
// backend is configured.
func fetchDocumentSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("fetch_document").
		Description(
			"Fetch a document (PDF, HTML, Markdown, plain text) through the secure pipeline and queue persistence into "+
				"the knowledge graph when ingestion is configured. Content is security-scanned, extracted to text, "+
				"and immediately returned as grounded evidence while persistence continues in the background. "+
				"A successful call satisfies the per-link grounding obligation for that exact URL.",
		).
		Domain("research").
		Keywords("document", "pdf", "paper", "article", "ingest", "fetch").
		Usage("Use for papers, PDFs, benchmark reports, standards, or long-form studies when you already know the exact document URL and want to force document-style local grounding.").
		BestPractice("Use this when you already know the exact document URL, or when a surfaced URL still needs explicit document-style local grounding.").
		BestPractice("If native web_search or Sylk already grounded the same exact URL, do not redundantly call fetch_document again unless you need to force this fetch mode or refresh the local fetch.").
		BestPractice("Treat a successful fetch_document for an exact URL as already grounded for citation and synthesis; do not call ground_source again for the same URL unless you need a different fetch mode.").
		BestPractice("Prefer fetch_document for academic papers, official benchmark reports, standards, and incident studies that contain data you expect to cite.").
		BestPractice("Before repeating study results, verify the date, sample size, workload or experimental setup, and major caveats from the grounded document when available.").
		BestPractice("Digest the document into actionable findings, not just extracted text: summarize method, findings, relevance, and limitations.").
		BestPractice("Make explicit what evidence class the document represents and whether it provides primary evidence, formal guidance, institutional policy, or secondary synthesis.").
		BestPractice("Call out dataset bias, threat models, assumptions, and threats to validity when the document's conclusions depend on them.").
		Requirement("Use this when an exact methodology-heavy paper, benchmark report, or standards document still needs the secured fetch path before citation or synthesis.").
		Requirement("A successful fetch_document satisfies the exact-URL grounding requirement for that URL.").
		Avoid("Do not summarize a study's conclusion from abstracts, snippets, or third-party commentary when the source document itself is accessible.").
		Priority(85).
		TokenEstimate(600).
		StringParam("url", "The document URL to fetch and ingest", true).
		StringParam("reason", "Why this document is needed for research", true).
		EnumParam("type", "Expected document type", []string{"html", "pdf", "markdown", "text", "auto"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				URL    string `json:"url"`
				Reason string `json:"reason"`
				Type   string `json:"type"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.URL == "" {
				return nil, fmt.Errorf("url is required")
			}
			if params.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			return a.executeFetchDocument(ctx, params.URL, params.Reason)
		}).
		Build()
}

// crawlLinksSkill extracts and optionally follows links from a fetched page.
// Bounded to prevent unbounded crawling.
func crawlLinksSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("crawl_links").
		Description(
			"Fetch a web page and extract its links. Optionally follow and fetch "+
				"linked pages (bounded to max_depth=1, max_links=10). Each followed "+
				"link passes through the full security pipeline. Returns grounded "+
				"text from the root page and summaries of linked pages while background persistence continues when available.",
		).
		Domain("research").
		Keywords("crawl", "links", "follow", "browse", "explore", "site").
		Priority(75).
		TokenEstimate(800).
		StringParam("url", "The starting URL to crawl", true).
		StringParam("reason", "Why this content is needed for research", true).
		BoolParam("follow_links", "Whether to fetch linked pages (default: false)", false).
		IntParam("max_links", "Maximum number of links to follow (default: 5, max: 10)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				URL         string `json:"url"`
				Reason      string `json:"reason"`
				FollowLinks bool   `json:"follow_links"`
				MaxLinks    int    `json:"max_links"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.URL == "" {
				return nil, fmt.Errorf("url is required")
			}
			if params.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}
			if params.MaxLinks <= 0 {
				params.MaxLinks = 5
			}
			if params.MaxLinks > 10 {
				params.MaxLinks = 10
			}
			return a.executeCrawl(ctx, params.URL, params.Reason, params.FollowLinks, params.MaxLinks)
		}).
		Build()
}

// executeFetch runs a single URL through the fetch pipeline and returns
// extracted text content with provenance.
func (a *Academic) executeFetch(ctx context.Context, url, reason string) (any, error) {
	resp, err := a.executeFetchPipeline(ctx, "web_fetch", url, reason)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return fetchFailureResult("web_fetch", resp)
	}

	result := buildGroundedFetchResult(resp, "content", 12000)
	result["grounding_tool"] = "web_fetch"
	return result, nil
}

// executeFetchDocument fetches a document URL and returns extracted text
// preview alongside ingestion statistics.
func (a *Academic) executeFetchDocument(ctx context.Context, url, reason string) (any, error) {
	resp, err := a.executeFetchPipeline(ctx, "fetch_document", url, reason)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return fetchFailureResult("fetch_document", resp)
	}

	result := buildGroundedFetchResult(resp, "text_preview", 16000)
	result["grounding_tool"] = "fetch_document"
	return result, nil
}

func (a *Academic) executeGroundSource(ctx context.Context, url, reason, expectedType string) (any, error) {
	toolName, err := normalizeGroundSourceTool(url, expectedType)
	if err != nil {
		return nil, err
	}
	resp, err := a.executeFetchPipeline(ctx, toolName, url, reason)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return fetchFailureResult("ground_source", resp)
	}
	contentKey := "content"
	contentLimit := 12000
	if toolName == "fetch_document" {
		contentKey = "text_preview"
		contentLimit = 16000
	}
	result := buildGroundedFetchResult(resp, contentKey, contentLimit)
	result["grounding_tool"] = toolName
	result["expected_type"] = normalizeGroundSourceType(expectedType)
	return result, nil
}

// executeCrawl fetches a page, extracts links, and optionally follows them.
func (a *Academic) executeCrawl(
	ctx context.Context,
	url, reason string,
	followLinks bool,
	maxLinks int,
) (any, error) {
	// Fetch the root page.
	rootResp, err := a.executeFetchPipeline(ctx, "crawl_links", url, reason)
	if err != nil {
		return nil, err
	}

	result := buildGroundedFetchResult(rootResp, "content", 12000)
	result["grounding_tool"] = "crawl_links"

	if !rootResp.Success {
		if _, err := fetchFailureResult("crawl_links", rootResp); err != nil {
			return nil, err
		}
		result["error"] = rootResp.Error
		if len(rootResp.Findings) > 0 {
			result["findings"] = formatFindings(rootResp.Findings)
		}
		return result, nil
	}

	if !followLinks {
		return result, nil
	}

	links := extractLinks(url, rootResp.Extracted)

	if len(links) > maxLinks {
		links = links[:maxLinks]
	}

	linkedResults := make([]map[string]any, 0, len(links))
	for _, link := range links {
		if ctx.Err() != nil {
			break
		}
		linkResp, err := a.executeFetchPipeline(ctx, "crawl_links", link, fmt.Sprintf("following link from %s: %s", url, reason))
		if err != nil {
			return nil, err
		}
		entry := map[string]any{
			"url":     link,
			"success": linkResp.Success,
			"verdict": linkResp.Verdict.String(),
		}
		if !linkResp.Success {
			if _, err := fetchFailureResult("crawl_links", linkResp); err != nil {
				return nil, err
			}
			entry["error"] = linkResp.Error
		} else if linkResp.Extracted != nil {
			entry["content_preview"] = truncateStr(linkResp.Extracted.Text, 4000)
			entry["word_count"] = linkResp.Extracted.WordCount
		}
		appendFetchPersistenceFields(entry, linkResp)
		linkedResults = append(linkedResults, entry)
	}

	result["links_found"] = len(links)
	result["links_fetched"] = linkedResults
	return result, nil
}

func extractLinks(baseURL string, extracted *fetch.ExtractResult) []string {
	if extracted == nil || len(extracted.Links) == 0 {
		return nil
	}

	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil
	}

	baseNoFragment := *base
	baseNoFragment.Fragment = ""
	baseCanonical := baseNoFragment.String()

	links := make([]string, 0, len(extracted.Links))
	seen := make(map[string]struct{}, len(extracted.Links))
	for _, rawLink := range extracted.Links {
		rawLink = strings.TrimSpace(rawLink)
		if rawLink == "" {
			continue
		}

		ref, err := url.Parse(rawLink)
		if err != nil {
			continue
		}
		if ref.Scheme != "" {
			scheme := strings.ToLower(ref.Scheme)
			if scheme != "http" && scheme != "https" {
				continue
			}
		}

		resolved := base.ResolveReference(ref)
		resolved.Fragment = ""

		scheme := strings.ToLower(resolved.Scheme)
		if scheme != "http" && scheme != "https" {
			continue
		}

		normalized := resolved.String()
		if normalized == "" || normalized == baseCanonical {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		links = append(links, normalized)
	}

	return links
}

func formatFindings(findings []fetch.InspectionFinding) []map[string]any {
	result := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		entry := map[string]any{
			"type":       f.Type,
			"severity":   f.Severity,
			"title":      f.Title,
			"confidence": f.Confidence,
		}
		if f.Detail != "" {
			entry["detail"] = truncateStr(f.Detail, 200)
		}
		result = append(result, entry)
	}
	return result
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (a *Academic) executeFetchPipeline(ctx context.Context, toolName, url, reason string) (*fetch.FetchResponse, error) {
	if a.fetchPipeline == nil {
		return nil, fmt.Errorf("external fetch is not configured")
	}
	return a.fetchPipeline.Execute(ctx, &fetch.FetchRequest{
		URL:         url,
		ToolName:    toolName,
		SourceAgent: "academic",
		Reason:      reason,
		SessionID:   firstNonEmptyFetchSessionID(ctx, a.config.SessionID),
		AsyncIngest: true,
	}), nil
}

func buildGroundedFetchResult(resp *fetch.FetchResponse, contentKey string, maxLen int) map[string]any {
	result := map[string]any{
		"success":                      resp != nil && resp.Success,
		"grounded":                     resp != nil && resp.Success,
		"per_link_grounding_satisfied": resp != nil && resp.Success,
	}
	if resp == nil {
		return result
	}
	result["url"] = resp.URL
	result["verdict"] = resp.Verdict.String()
	result["duration"] = resp.Duration.String()
	result["source_type"] = string(sourceTypeFromURL(resp.URL))
	if resp.ExtractionError != "" {
		result["extraction_error"] = resp.ExtractionError
	}
	if resp.Extracted != nil {
		result[contentKey] = truncateStr(resp.Extracted.Text, maxLen)
		result["word_count"] = resp.Extracted.WordCount
		if resp.Extracted.Title != "" {
			result["title"] = resp.Extracted.Title
		}
		if resp.Extracted.Language != "" {
			result["language"] = resp.Extracted.Language
		}
		result[fetchTruncatedField(contentKey)] = len(resp.Extracted.Text) > maxLen
	}
	if resp.Provenance != nil {
		result["content_hash"] = resp.Provenance.ContentHash
		result["fetched_at"] = resp.Provenance.FetchedAt.Format(time.RFC3339)
		result["finding_count"] = resp.Provenance.FindingCount
	}
	appendFetchPersistenceFields(result, resp)
	return result
}

func fetchTruncatedField(contentKey string) string {
	switch strings.TrimSpace(contentKey) {
	case "text_preview":
		return "preview_truncated"
	default:
		return "content_truncated"
	}
}

func appendFetchPersistenceFields(result map[string]any, resp *fetch.FetchResponse) {
	if result == nil || resp == nil {
		return
	}
	result["ingested"] = resp.Ingested
	if resp.IngestStatus != "" {
		result["persistence_status"] = string(resp.IngestStatus)
	}
	if trimmed := strings.TrimSpace(resp.IngestJobID); trimmed != "" {
		result["persistence_job_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(resp.IngestError); trimmed != "" {
		result["persistence_error"] = trimmed
	}
}

func normalizeGroundSourceTool(rawURL, expectedType string) (string, error) {
	switch normalizeGroundSourceType(expectedType) {
	case "page":
		return "web_fetch", nil
	case "document":
		return "fetch_document", nil
	case "auto":
		if looksLikeDocumentURL(rawURL) {
			return "fetch_document", nil
		}
		return "web_fetch", nil
	default:
		return "", fmt.Errorf("expected_type must be auto, page, or document")
	}
}

func normalizeGroundSourceType(expectedType string) string {
	switch strings.ToLower(strings.TrimSpace(expectedType)) {
	case "", "auto":
		return "auto"
	case "page":
		return "page"
	case "document":
		return "document"
	default:
		return strings.ToLower(strings.TrimSpace(expectedType))
	}
}

func looksLikeDocumentURL(rawURL string) bool {
	lowered := strings.ToLower(strings.TrimSpace(rawURL))
	switch {
	case strings.HasSuffix(lowered, ".pdf"),
		strings.HasSuffix(lowered, ".md"),
		strings.HasSuffix(lowered, ".markdown"),
		strings.HasSuffix(lowered, ".txt"),
		strings.HasSuffix(lowered, ".rst"),
		strings.HasSuffix(lowered, ".rtf"),
		strings.HasSuffix(lowered, ".doc"),
		strings.HasSuffix(lowered, ".docx"),
		strings.HasSuffix(lowered, ".ppt"),
		strings.HasSuffix(lowered, ".pptx"),
		strings.HasSuffix(lowered, ".xls"),
		strings.HasSuffix(lowered, ".xlsx"),
		strings.HasSuffix(lowered, ".csv"),
		strings.Contains(lowered, "/pdf"),
		strings.Contains(lowered, "download"),
		strings.Contains(lowered, "arxiv.org/pdf/"):
		return true
	default:
		return false
	}
}

func firstNonEmptyFetchSessionID(ctx context.Context, fallback string) string {
	if trimmed := versioning.SessionIDFromContext(ctx); trimmed != "" {
		return trimmed
	}
	return fallback
}

func fetchFailureResult(toolName string, resp *fetch.FetchResponse) (any, error) {
	if resp != nil && resp.ApprovalDenied {
		return nil, shared.ApprovalDeniedDelegatedError(toolName, strings.TrimSpace(resp.Error))
	}
	result := map[string]any{
		"success": false,
	}
	if resp != nil {
		result["url"] = resp.URL
		result["error"] = resp.Error
		result["verdict"] = resp.Verdict.String()
		result["duration"] = resp.Duration.String()
		if len(resp.Findings) > 0 {
			result["findings_count"] = len(resp.Findings)
			result["findings"] = formatFindings(resp.Findings)
		}
	}
	return result, nil
}

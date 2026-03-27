package academic

import (
	"context"
	"encoding/json"
	"fmt"
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
				"Use this to discover authoritative sources when you do not already know the URL. "+
				"After discovering a source, use web_fetch or fetch_document to retrieve it through the secure pipeline.",
		).
		Domain("research").
		Keywords("search", "web", "internet", "google", "discover", "find sources", "documentation", "papers").
		Usage("Use when you need to discover candidate sources on the public web before fetching them through Sylk's guarded fetch pipeline.").
		Example("Search official Go and PostgreSQL sources before recommending connection-pooling practices.").
		Example("Search for standards, RFCs, vendor docs, and academic papers when the URL is not already known.").
		BestPractice("Prefer official documentation, standards bodies, project maintainers, and primary-source papers over secondary commentary.").
		BestPractice("Any URL discovered through web_search that you plan to cite or surface to the user must be grounded with ground_source or an equivalent fetch skill first.").
		BestPractice("When the answer depends on performance, reliability, cost, scale, security impact, or adoption numbers, search for primary empirical sources such as official benchmarks, standards, incident reports, papers, or vendor telemetry instead of summary blogs.").
		BestPractice("After discovery, fetch the selected source with web_fetch or fetch_document before relying on specific details.").
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
			"Ground a promising public source discovered via web_search. "+
				"Fetch the source through the secure pipeline, return grounded content immediately, "+
				"and queue background persistence into the knowledge graph and document store when configured.",
		).
		Domain("research").
		Keywords("ground", "source", "fetch", "ingest", "document", "page", "evidence").
		Priority(92).
		TokenEstimate(550).
		StringParam("url", "The promising source URL to ground", true).
		StringParam("reason", "Why this source looks promising enough to ground", true).
		EnumParam("expected_type", "Expected source type", []string{"auto", "page", "document"}, false).
		Usage("Use immediately after web_search when a result looks promising enough to inspect directly before relying on it or citing it in the response.").
		BestPractice("Prefer ground_source over repeating similar web_search queries once you already have a promising candidate URL.").
		BestPractice("Ground the source before quoting statistics, benchmarks, percentages, or study conclusions from it.").
		BestPractice("When a number materially affects the recommendation, capture its publication date and sample, workload, or measurement context from the grounded source when available.").
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
				"Returns grounded text content immediately and queues background persistence when ingestion is configured.",
		).
		Domain("research").
		Keywords("fetch", "download", "url", "web", "page", "http").
		Usage("Use when you already know the URL for a specific page, benchmark, report, or documentation page that must be inspected before relying on its claims.").
		BestPractice("Do not quote benchmark numbers, percentages, or performance claims from a page until you have fetched and inspected it through the secure pipeline.").
		BestPractice("For numeric claims, note the publication date and benchmark or study context so the recommendation does not overgeneralize stale or narrow results.").
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
				"and immediately returned as grounded evidence while persistence continues in the background.",
		).
		Domain("research").
		Keywords("document", "pdf", "paper", "article", "ingest", "fetch").
		Usage("Use for papers, PDFs, benchmark reports, standards, or long-form studies when the evidence depends on exact methodology, statistics, or formal guidance.").
		BestPractice("Prefer fetch_document for academic papers, official benchmark reports, standards, and incident studies that contain data you expect to cite.").
		BestPractice("Before repeating study results, verify the date, sample size, workload or experimental setup, and major caveats from the grounded document when available.").
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
				"linked pages (bounded to max_depth=1, max_links=5). Each followed "+
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

	// Extract links from the fetched content using the pipeline's extractor.
	extractor := fetch.NewContentExtractor()
	links := extractLinks(url, extractor)

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

// extractLinks is a placeholder that returns discovered links from a page.
// In practice, this would parse the HTML content for <a href="..."> tags.
// Since the pipeline ingests content (not returns it), we extract links
// from the URL pattern instead. A full implementation would require
// access to the raw content before ingestion.
func extractLinks(_ string, _ *fetch.ContentExtractor) []string {
	// Links are extracted during HTML content extraction by the pipeline's
	// ingest function. For the crawl skill, we rely on the LLM to provide
	// specific URLs from its analysis of the fetched content.
	return nil
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
		"success":  resp != nil && resp.Success,
		"grounded": resp != nil && resp.Success,
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

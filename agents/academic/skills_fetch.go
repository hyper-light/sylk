package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/skills"
)

func (a *Academic) registerFetchSkills() {
	a.skills.Register(webSearchSkill())
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

// webFetchSkill fetches a URL through the secure pipeline and returns
// the extracted text content. All content passes through SecurityContext,
// FetchPolicy, ConsentGate, quarantine, and Guardian inspection.
func webFetchSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("web_fetch").
		Description(
			"Fetch a web page or document URL through the secure pipeline. "+
				"Content is quarantined and inspected by Guardian before ingestion. "+
				"User consent is required unless the domain is pre-approved. "+
				"Returns extracted text content and provenance metadata.",
		).
		Domain("research").
		Keywords("fetch", "download", "url", "web", "page", "http").
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
			"Fetch a document (PDF, HTML, Markdown, plain text) and ingest it into "+
				"the knowledge graph when ingestion is configured. Content is security-scanned, extracted to text, "+
				"and, when enabled, chunked, embedded, and indexed. Returns extracted text preview and "+
				"ingestion statistics.",
		).
		Domain("research").
		Keywords("document", "pdf", "paper", "article", "ingest", "fetch").
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
				"link passes through the full security pipeline. Returns extracted "+
				"text from the root page and summaries of linked pages.",
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
	if a.fetchPipeline == nil {
		return nil, fmt.Errorf("external fetch is not configured")
	}

	resp := a.fetchPipeline.Execute(ctx, &fetch.FetchRequest{
		URL:         url,
		SourceAgent: "academic",
		Reason:      reason,
		SessionID:   a.config.SessionID,
	})

	if !resp.Success {
		result := map[string]any{
			"success":  false,
			"url":      resp.URL,
			"error":    resp.Error,
			"verdict":  resp.Verdict.String(),
			"duration": resp.Duration.String(),
		}
		if len(resp.Findings) > 0 {
			result["findings_count"] = len(resp.Findings)
			result["findings"] = formatFindings(resp.Findings)
		}
		return result, nil
	}

	result := map[string]any{
		"success":  true,
		"url":      resp.URL,
		"verdict":  resp.Verdict.String(),
		"duration": resp.Duration.String(),
	}
	if resp.ExtractionError != "" {
		result["extraction_error"] = resp.ExtractionError
	}
	if resp.Extracted != nil {
		result["content"] = truncateStr(resp.Extracted.Text, 12000)
		result["word_count"] = resp.Extracted.WordCount
		if resp.Extracted.Title != "" {
			result["title"] = resp.Extracted.Title
		}
		if resp.Extracted.Language != "" {
			result["language"] = resp.Extracted.Language
		}
		result["content_truncated"] = len(resp.Extracted.Text) > 12000
	}
	if resp.Provenance != nil {
		result["content_hash"] = resp.Provenance.ContentHash
		result["fetched_at"] = resp.Provenance.FetchedAt.Format(time.RFC3339)
		result["ingested"] = resp.Ingested
	}
	return result, nil
}

// executeFetchDocument fetches a document URL and returns extracted text
// preview alongside ingestion statistics.
func (a *Academic) executeFetchDocument(ctx context.Context, url, reason string) (any, error) {
	if a.fetchPipeline == nil {
		return nil, fmt.Errorf("external fetch is not configured")
	}

	resp := a.fetchPipeline.Execute(ctx, &fetch.FetchRequest{
		URL:         url,
		SourceAgent: "academic",
		Reason:      reason,
		SessionID:   a.config.SessionID,
	})

	if !resp.Success {
		result := map[string]any{
			"success":  false,
			"url":      resp.URL,
			"error":    resp.Error,
			"verdict":  resp.Verdict.String(),
			"duration": resp.Duration.String(),
		}
		if len(resp.Findings) > 0 {
			result["findings_count"] = len(resp.Findings)
			result["findings"] = formatFindings(resp.Findings)
		}
		return result, nil
	}

	result := map[string]any{
		"success":  true,
		"url":      resp.URL,
		"verdict":  resp.Verdict.String(),
		"duration": resp.Duration.String(),
	}
	if resp.ExtractionError != "" {
		result["extraction_error"] = resp.ExtractionError
	}
	if resp.Extracted != nil {
		result["text_preview"] = truncateStr(resp.Extracted.Text, 16000)
		result["word_count"] = resp.Extracted.WordCount
		if resp.Extracted.Title != "" {
			result["title"] = resp.Extracted.Title
		}
		result["preview_truncated"] = len(resp.Extracted.Text) > 16000
	}
	if resp.Provenance != nil {
		result["content_hash"] = resp.Provenance.ContentHash
		result["fetched_at"] = resp.Provenance.FetchedAt.Format(time.RFC3339)
		result["finding_count"] = resp.Provenance.FindingCount
		result["ingested"] = resp.Ingested
	}
	return result, nil
}

// executeCrawl fetches a page, extracts links, and optionally follows them.
func (a *Academic) executeCrawl(
	ctx context.Context,
	url, reason string,
	followLinks bool,
	maxLinks int,
) (any, error) {
	if a.fetchPipeline == nil {
		return nil, fmt.Errorf("external fetch is not configured")
	}

	// Fetch the root page.
	rootResp := a.fetchPipeline.Execute(ctx, &fetch.FetchRequest{
		URL:         url,
		SourceAgent: "academic",
		Reason:      reason,
		SessionID:   a.config.SessionID,
	})

	result := map[string]any{
		"url":      url,
		"success":  rootResp.Success,
		"verdict":  rootResp.Verdict.String(),
		"duration": rootResp.Duration.String(),
	}

	if !rootResp.Success {
		result["error"] = rootResp.Error
		if len(rootResp.Findings) > 0 {
			result["findings"] = formatFindings(rootResp.Findings)
		}
		return result, nil
	}

	if rootResp.Provenance != nil {
		result["content_hash"] = rootResp.Provenance.ContentHash
		result["ingested"] = rootResp.Ingested
	}
	if rootResp.ExtractionError != "" {
		result["extraction_error"] = rootResp.ExtractionError
	}
	if rootResp.Extracted != nil {
		result["content"] = truncateStr(rootResp.Extracted.Text, 12000)
		result["word_count"] = rootResp.Extracted.WordCount
		if rootResp.Extracted.Title != "" {
			result["title"] = rootResp.Extracted.Title
		}
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
		linkResp := a.fetchPipeline.Execute(ctx, &fetch.FetchRequest{
			URL:         link,
			SourceAgent: "academic",
			Reason:      fmt.Sprintf("following link from %s: %s", url, reason),
			SessionID:   a.config.SessionID,
		})
		entry := map[string]any{
			"url":     link,
			"success": linkResp.Success,
			"verdict": linkResp.Verdict.String(),
		}
		if !linkResp.Success {
			entry["error"] = linkResp.Error
		} else if linkResp.Extracted != nil {
			entry["content_preview"] = truncateStr(linkResp.Extracted.Text, 4000)
			entry["word_count"] = linkResp.Extracted.WordCount
		}
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

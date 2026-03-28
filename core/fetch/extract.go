package fetch

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ContentExtractor extracts readable text from raw fetched content
// based on content type. Used after quarantine release, before ingestion.
type ContentExtractor struct{}

// NewContentExtractor creates a content extractor.
func NewContentExtractor() *ContentExtractor {
	return &ContentExtractor{}
}

// ExtractResult holds extracted text and metadata.
type ExtractResult struct {
	Text      string   `json:"text"`
	Title     string   `json:"title,omitempty"`
	Language  string   `json:"language,omitempty"` // For source code
	Links     []string `json:"links,omitempty"`
	WordCount int      `json:"word_count"`
}

// Extract dispatches to the appropriate extractor based on content type.
func (e *ContentExtractor) Extract(content []byte, contentType string) (*ExtractResult, error) {
	ct := strings.ToLower(contentType)

	switch {
	case strings.Contains(ct, "text/html"):
		return e.extractHTML(content)
	case strings.Contains(ct, "text/plain"):
		return e.extractPlainText(content)
	case strings.Contains(ct, "text/markdown"):
		return e.extractPlainText(content) // Markdown is already readable
	case strings.Contains(ct, "application/json"):
		return e.extractPlainText(content) // JSON as text
	case strings.Contains(ct, "application/pdf"):
		return e.extractPDF(content)
	default:
		// Attempt plain text extraction for unknown types.
		if isLikelyText(content) {
			return e.extractPlainText(content)
		}
		return nil, fmt.Errorf("unsupported content type for extraction: %s", contentType)
	}
}

// extractHTML strips HTML tags and extracts readable text content.
// Uses a lightweight approach: remove script/style blocks, strip tags,
// normalize whitespace.
func (e *ContentExtractor) extractHTML(content []byte) (*ExtractResult, error) {
	s := string(content)

	// Extract title.
	title := extractHTMLTitle(s)

	// Remove script and style blocks.
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")

	// Remove HTML comments.
	s = reHTMLComment.ReplaceAllString(s, " ")

	// Extract links from visible anchor tags before stripping all markup.
	links := extractHTMLLinks(s)

	// Replace block-level tags with newlines for readability.
	s = reBlockTags.ReplaceAllString(s, "\n")

	// Replace <br> with newline.
	s = reBR.ReplaceAllString(s, "\n")

	// Strip remaining tags.
	s = reAllTags.ReplaceAllString(s, "")

	// Decode common HTML entities.
	s = decodeHTMLEntities(s)

	// Normalize whitespace.
	s = normalizeWhitespace(s)

	return &ExtractResult{
		Text:      s,
		Title:     title,
		Links:     links,
		WordCount: countWords(s),
	}, nil
}

// extractPlainText normalizes plain text content.
func (e *ContentExtractor) extractPlainText(content []byte) (*ExtractResult, error) {
	s := normalizeWhitespace(string(content))
	return &ExtractResult{
		Text:      s,
		WordCount: countWords(s),
	}, nil
}

// extractPDF extracts text from PDF content. Uses a lightweight approach
// that handles the most common PDF text encodings. For complex PDFs with
// compressed streams, this extracts what it can.
func (e *ContentExtractor) extractPDF(content []byte) (*ExtractResult, error) {
	if len(content) < 5 || string(content[:5]) != "%PDF-" {
		return nil, fmt.Errorf("not a valid PDF file")
	}

	var text strings.Builder

	// Extract text from BT...ET (text object) blocks.
	// This handles uncompressed text streams — the most common case
	// for research papers and documentation.
	for i := 0; i < len(content)-2; i++ {
		if content[i] == 'B' && content[i+1] == 'T' && (i == 0 || !isAlphaNum(content[i-1])) {
			// Find matching ET.
			end := bytes.Index(content[i+2:], []byte("ET"))
			if end < 0 {
				continue
			}
			block := string(content[i+2 : i+2+end])
			extracted := extractPDFTextOperators(block)
			if extracted != "" {
				text.WriteString(extracted)
				text.WriteString("\n")
			}
			i += 2 + end
		}
	}

	// Also scan for FlateDecode streams — we note them but can't
	// decompress without a zlib dependency. The text from BT/ET
	// blocks covers the common case.
	result := normalizeWhitespace(text.String())
	if result == "" {
		return nil, fmt.Errorf("no extractable text found in PDF (streams may be compressed)")
	}

	return &ExtractResult{
		Text:      result,
		WordCount: countWords(result),
	}, nil
}

// extractPDFTextOperators extracts text from PDF text operators within a
// BT...ET block. Handles Tj (show string), TJ (show array), and ' operators.
func extractPDFTextOperators(block string) string {
	var result strings.Builder
	lines := strings.Split(block, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Tj operator: (text) Tj
		if strings.HasSuffix(line, "Tj") {
			if text := extractParenText(line); text != "" {
				result.WriteString(text)
				result.WriteString(" ")
			}
		}

		// TJ operator: [(text1) num (text2)] TJ
		if strings.HasSuffix(line, "TJ") {
			parts := reParenText.FindAllString(line, -1)
			for _, p := range parts {
				if len(p) >= 2 {
					result.WriteString(p[1 : len(p)-1])
				}
			}
			result.WriteString(" ")
		}

		// ' operator: (text) '
		if strings.HasSuffix(line, "'") && strings.Contains(line, "(") {
			if text := extractParenText(line); text != "" {
				result.WriteString(text)
				result.WriteString("\n")
			}
		}
	}

	return result.String()
}

func extractParenText(s string) string {
	start := strings.Index(s, "(")
	end := strings.LastIndex(s, ")")
	if start >= 0 && end > start {
		return s[start+1 : end]
	}
	return ""
}

// =============================================================================
// HTML helpers
// =============================================================================

var (
	reScript      = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reStyle       = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlockTags   = regexp.MustCompile(`(?i)</(p|div|section|article|h[1-6]|li|tr|blockquote|pre)>`)
	reBR          = regexp.MustCompile(`(?i)<br\s*/?>`)
	reAllTags     = regexp.MustCompile(`<[^>]+>`)
	reAnchorHref  = regexp.MustCompile("(?is)<a\\b[^>]*\\bhref\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s\"'<>`]+))")
	reParenText   = regexp.MustCompile(`\([^)]*\)`)
)

func extractHTMLTitle(html string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	m := re.FindStringSubmatch(html)
	if len(m) >= 2 {
		return strings.TrimSpace(reAllTags.ReplaceAllString(m[1], ""))
	}
	return ""
}

func extractHTMLLinks(html string) []string {
	matches := reAnchorHref.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil
	}

	links := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		href := ""
		for i := 1; i < len(match); i++ {
			if strings.TrimSpace(match[i]) != "" {
				href = strings.TrimSpace(decodeHTMLEntities(match[i]))
				break
			}
		}
		if href == "" {
			continue
		}
		if _, ok := seen[href]; ok {
			continue
		}
		seen[href] = struct{}{}
		links = append(links, href)
	}
	return links
}

var htmlEntities = map[string]string{
	"&amp;":    "&",
	"&lt;":     "<",
	"&gt;":     ">",
	"&quot;":   "\"",
	"&apos;":   "'",
	"&#39;":    "'",
	"&nbsp;":   " ",
	"&mdash;":  "—",
	"&ndash;":  "–",
	"&hellip;": "…",
	"&copy;":   "©",
	"&reg;":    "®",
}

func decodeHTMLEntities(s string) string {
	for entity, replacement := range htmlEntities {
		s = strings.ReplaceAll(s, entity, replacement)
	}
	return s
}

// =============================================================================
// Common helpers
// =============================================================================

func normalizeWhitespace(s string) string {
	// Collapse multiple blank lines to one.
	lines := strings.Split(s, "\n")
	result := make([]string, 0, len(lines))
	prevBlank := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevBlank {
				result = append(result, "")
			}
			prevBlank = true
			continue
		}
		prevBlank = false
		result = append(result, trimmed)
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}

func countWords(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
		} else if !inWord {
			inWord = true
			count++
		}
	}
	return count
}

func isLikelyText(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	// Check first 512 bytes for non-text characters.
	check := content
	if len(check) > 512 {
		check = check[:512]
	}
	nonText := 0
	for _, b := range check {
		if b == 0 {
			return false // Null byte — binary
		}
		if b < 32 && b != '\n' && b != '\r' && b != '\t' {
			nonText++
		}
	}
	return nonText < len(check)/10
}

func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

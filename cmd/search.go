// Package cmd provides CLI commands for the Sylk application.
// This file implements the search command for querying the codebase.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/bleve"
	"github.com/spf13/cobra"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// SearchDefaultLimit is the default number of results.
	SearchDefaultLimit = 20

	// SearchMaxLimit is the maximum number of results.
	SearchMaxLimit = 100

	// SearchDefaultIndexPath is the default path for the Bleve index.
	SearchDefaultIndexPath = ".sylk/index"
)

// ANSI color codes for terminal output.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// =============================================================================
// Search Command Flags
// =============================================================================

var (
	searchFuzzy       bool
	searchType        string
	searchPath        string
	searchLimit       int
	searchInteractive bool
	searchJSON        bool
	searchIndexPath   string
	searchHighlight   bool
)

// =============================================================================
// Search Command
// =============================================================================

// searchCmd represents the search command.
var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search the codebase",
	Long: `Search the codebase using full-text search.

Examples:
  sylk search "function handleRequest"
  sylk search --fuzzy "handlerequest"
  sylk search --type go "interface"
  sylk search --path "cmd/" "main"
  sylk search --json "error handling" | jq '.documents'`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSearch,
}

func init() {
	// Register with root command
	rootCmd.AddCommand(searchCmd)

	// Define flags
	searchCmd.Flags().BoolVarP(&searchFuzzy, "fuzzy", "f", false, "Enable fuzzy matching (allows typos)")
	searchCmd.Flags().StringVarP(&searchType, "type", "t", "", "Filter by document type (source_code, markdown, config)")
	searchCmd.Flags().StringVarP(&searchPath, "path", "p", "", "Filter by path prefix")
	searchCmd.Flags().IntVarP(&searchLimit, "limit", "l", SearchDefaultLimit, "Maximum number of results")
	searchCmd.Flags().BoolVarP(&searchInteractive, "interactive", "i", false, "Enable interactive mode")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Output results as JSON")
	searchCmd.Flags().StringVar(&searchIndexPath, "index", SearchDefaultIndexPath, "Path to search index")
	searchCmd.Flags().BoolVar(&searchHighlight, "highlight", true, "Enable search result highlighting")
}

// =============================================================================
// Search Execution
// =============================================================================

// runSearch executes the search command.
func runSearch(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	if searchInteractive {
		// Delegate to the interactive search command (isearch)
		// For now, just inform the user to use the dedicated command
		fmt.Fprintln(cmd.OutOrStdout(), "For interactive search, use: sylk isearch")
		fmt.Fprintln(cmd.OutOrStdout(), "Running standard search with provided query...")
		fmt.Fprintln(cmd.OutOrStdout())
	}

	return runStandardSearch(cmd, query)
}

// runStandardSearch executes a standard search and displays results.
func runStandardSearch(cmd *cobra.Command, query string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build search request
	req := buildSearchRequest(query)

	// Execute search
	result, err := executeSearch(ctx, req)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	// Output results
	return outputSearchResults(cmd.OutOrStdout(), result)
}

// buildSearchRequest creates a SearchRequest from command flags.
func buildSearchRequest(query string) *search.SearchRequest {
	// Apply fuzzy modifier if enabled
	if searchFuzzy {
		query = applyFuzzyQuery(query)
	}

	// Apply type filter if specified
	if searchType != "" {
		query = applyTypeFilter(query, searchType)
	}

	// Apply path filter if specified
	if searchPath != "" {
		query = applyPathFilter(query, searchPath)
	}

	// Normalize limit
	limit := searchLimit
	if limit <= 0 {
		limit = SearchDefaultLimit
	}
	if limit > SearchMaxLimit {
		limit = SearchMaxLimit
	}

	return &search.SearchRequest{
		Query:             query,
		Limit:             limit,
		IncludeHighlights: searchHighlight && !searchJSON,
	}
}

// applyFuzzyQuery modifies query for fuzzy matching.
func applyFuzzyQuery(query string) string {
	// Bleve supports fuzzy queries with ~ suffix
	terms := strings.Fields(query)
	fuzzyTerms := make([]string, len(terms))
	for i, term := range terms {
		// Add fuzzy modifier (~1 allows 1 edit distance)
		fuzzyTerms[i] = term + "~1"
	}
	return strings.Join(fuzzyTerms, " ")
}

// applyTypeFilter adds type filter to query.
func applyTypeFilter(query, docType string) string {
	return fmt.Sprintf("%s +type:%s", query, docType)
}

// applyPathFilter adds path filter to query.
func applyPathFilter(query, pathPrefix string) string {
	return fmt.Sprintf("%s +path:%s*", query, pathPrefix)
}

// executeSearch runs the search against the index.
func executeSearch(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	// Open index manager
	indexManager := bleve.NewIndexManager(searchIndexPath)
	if err := indexManager.Open(); err != nil {
		return nil, fmt.Errorf("failed to open index: %w", err)
	}
	defer indexManager.Close()

	// Execute search
	return indexManager.Search(ctx, req)
}

// =============================================================================
// Output Formatting
// =============================================================================

// outputSearchResults formats and outputs search results.
func outputSearchResults(w io.Writer, result *search.SearchResult) error {
	if searchJSON {
		return outputJSONResults(w, result)
	}
	return outputRichResults(w, result)
}

// outputJSONResults outputs search results as JSON.
func outputJSONResults(w io.Writer, result *search.SearchResult) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(newSearchOutput(result))
}

// searchOutput is the JSON output structure.
type searchOutput struct {
	Query      string               `json:"query"`
	TotalHits  int64                `json:"total_hits"`
	SearchTime string               `json:"search_time"`
	Documents  []documentOutput     `json:"documents"`
}

// documentOutput is the JSON output for a single document.
type documentOutput struct {
	Path          string              `json:"path"`
	Type          string              `json:"type"`
	Language      string              `json:"language,omitempty"`
	Score         float64             `json:"score"`
	Snippet       string              `json:"snippet,omitempty"`
	Highlights    map[string][]string `json:"highlights,omitempty"`
	MatchedFields []string            `json:"matched_fields,omitempty"`
}

// newSearchOutput creates a searchOutput from a SearchResult.
func newSearchOutput(result *search.SearchResult) *searchOutput {
	docs := make([]documentOutput, 0, len(result.Documents))
	for _, doc := range result.Documents {
		docs = append(docs, documentOutput{
			Path:          doc.Path,
			Type:          string(doc.Type),
			Language:      doc.Language,
			Score:         doc.Score,
			Snippet:       extractSnippet(doc.Content, 200),
			Highlights:    doc.Highlights,
			MatchedFields: doc.MatchedFields,
		})
	}

	return &searchOutput{
		Query:      result.Query,
		TotalHits:  result.TotalHits,
		SearchTime: result.SearchTime.String(),
		Documents:  docs,
	}
}

// outputRichResults outputs search results with terminal formatting.
func outputRichResults(w io.Writer, result *search.SearchResult) error {
	// Header
	fmt.Fprintf(w, "%s%sSearch Results%s\n", colorBold, colorCyan, colorReset)
	fmt.Fprintf(w, "%sQuery:%s %s\n", colorGray, colorReset, result.Query)
	fmt.Fprintf(w, "%sFound:%s %d results in %v\n", colorGray, colorReset, result.TotalHits, result.SearchTime)
	fmt.Fprintln(w)

	if !result.HasResults() {
		fmt.Fprintf(w, "%sNo results found.%s\n", colorYellow, colorReset)
		return nil
	}

	// Results
	for i, doc := range result.Documents {
		outputRichDocument(w, i+1, &doc)
	}

	return nil
}

// outputRichDocument outputs a single document with rich formatting.
func outputRichDocument(w io.Writer, index int, doc *search.ScoredDocument) {
	// Document header
	fmt.Fprintf(w, "%s%d.%s %s%s%s\n",
		colorYellow, index, colorReset,
		colorBold, doc.Path, colorReset)

	// Metadata line
	fmt.Fprintf(w, "   %sType:%s %s  %sLang:%s %s  %sScore:%s %.4f\n",
		colorGray, colorReset, doc.Type,
		colorGray, colorReset, getLanguageDisplay(doc.Language),
		colorGray, colorReset, doc.Score)

	// Highlights or snippet
	if len(doc.Highlights) > 0 {
		outputHighlights(w, doc.Highlights)
	} else {
		snippet := extractSnippet(doc.Content, 150)
		if snippet != "" {
			fmt.Fprintf(w, "   %s%s%s\n", colorGray, snippet, colorReset)
		}
	}

	fmt.Fprintln(w)
}

// outputHighlights outputs search highlights.
func outputHighlights(w io.Writer, highlights map[string][]string) {
	for field, fragments := range highlights {
		if len(fragments) > 0 {
			fmt.Fprintf(w, "   %s[%s]%s\n", colorBlue, field, colorReset)
			for _, fragment := range fragments {
				// Bleve's ANSI highlighter already adds colors
				fmt.Fprintf(w, "      %s\n", fragment)
			}
		}
	}
}

// getLanguageDisplay returns a display string for language.
func getLanguageDisplay(lang string) string {
	if lang == "" {
		return "unknown"
	}
	return lang
}

// extractSnippet extracts a content snippet of maxLen characters.
func extractSnippet(content string, maxLen int) string {
	if content == "" {
		return ""
	}

	// Remove extra whitespace
	content = strings.Join(strings.Fields(content), " ")

	if len(content) <= maxLen {
		return content
	}

	// Find a good break point
	snippet := content[:maxLen]
	lastSpace := strings.LastIndex(snippet, " ")
	if lastSpace > maxLen/2 {
		snippet = snippet[:lastSpace]
	}

	return snippet + "..."
}

// =============================================================================
// Utility Functions
// =============================================================================

// isTerminal returns true if the given writer is a terminal.
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		return (fi.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

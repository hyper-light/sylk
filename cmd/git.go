// Package cmd provides CLI commands for the Sylk application.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adalundhe/sylk/core/search/git"
	"github.com/spf13/cobra"
)

// =============================================================================
// Output Format Type
// =============================================================================

// GitOutputFormat represents the output format for git commands.
type GitOutputFormat string

const (
	// GitOutputTable outputs as formatted table.
	GitOutputTable GitOutputFormat = "table"
	// GitOutputJSON outputs as JSON.
	GitOutputJSON GitOutputFormat = "json"
	// GitOutputPlain outputs as plain text.
	GitOutputPlain GitOutputFormat = "plain"
)

// =============================================================================
// Git Command Flags
// =============================================================================

var (
	gitFormat  string
	gitSince   string
	gitUntil   string
	gitAuthor  string
	gitLimit   int
	gitRepoDir string
)

// =============================================================================
// Git Commands
// =============================================================================

var gitCmd = &cobra.Command{
	Use:   "git",
	Short: "Git-related operations",
	Long:  `Commands for interacting with git repository data including diffs, logs, and blame information.`,
}

var gitDiffCmd = &cobra.Command{
	Use:   "diff [from] [to]",
	Short: "Show diff between commits",
	Long: `Show the diff between two commits.
If no arguments are provided, shows working tree changes.
If one argument is provided, shows diff between that commit and HEAD.
If two arguments are provided, shows diff between the two commits.`,
	Args: cobra.MaximumNArgs(2),
	RunE: runGitDiff,
}

var gitLogCmd = &cobra.Command{
	Use:   "log [path]",
	Short: "Show commit history",
	Long: `Show commit history for the repository or a specific file.
If a path is provided, shows commits that affected that file.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runGitLog,
}

var gitBlameCmd = &cobra.Command{
	Use:   "blame <file>",
	Short: "Show line-by-line authorship",
	Long: `Show what commit and author last modified each line of a file.
Displays line number, commit hash, author, date, and content.`,
	Args: cobra.ExactArgs(1),
	RunE: runGitBlame,
}

// =============================================================================
// Init
// =============================================================================

func init() {
	rootCmd.AddCommand(gitCmd)
	gitCmd.AddCommand(gitDiffCmd)
	gitCmd.AddCommand(gitLogCmd)
	gitCmd.AddCommand(gitBlameCmd)

	// Common flags
	gitCmd.PersistentFlags().StringVarP(&gitFormat, "format", "f", "table", "Output format (table, json, plain)")
	gitCmd.PersistentFlags().StringVar(&gitRepoDir, "repo", ".", "Repository directory path")

	// Log-specific flags
	gitLogCmd.Flags().StringVar(&gitSince, "since", "", "Show commits after date (e.g., 24h, 7d, 2024-01-01)")
	gitLogCmd.Flags().StringVar(&gitUntil, "until", "", "Show commits before date (e.g., 24h, 2024-01-01)")
	gitLogCmd.Flags().StringVar(&gitAuthor, "author", "", "Filter by author name or email")
	gitLogCmd.Flags().IntVarP(&gitLimit, "limit", "n", 10, "Maximum number of commits to show")

	// Blame-specific flags (since/until can be useful here too)
	gitBlameCmd.Flags().StringVar(&gitSince, "since", "", "Show blame as of a date")
}

// =============================================================================
// Git Diff Command
// =============================================================================

func runGitDiff(cmd *cobra.Command, args []string) error {
	client, err := git.NewGitClient(gitRepoDir)
	if err != nil {
		return fmt.Errorf("failed to create git client: %w", err)
	}
	defer client.Close()

	if !client.IsGitRepo() {
		return fmt.Errorf("not a git repository: %s", gitRepoDir)
	}

	var diffs []git.FileDiff

	switch len(args) {
	case 0:
		// Show working tree changes
		diffs, err = client.GetWorkingTreeDiff()
	case 1:
		// Diff between commit and HEAD
		head, headErr := client.GetHead()
		if headErr != nil {
			return fmt.Errorf("failed to get HEAD: %w", headErr)
		}
		diffs, err = client.GetDiff(args[0], head)
	case 2:
		// Diff between two commits
		diffs, err = client.GetDiff(args[0], args[1])
	}

	if err != nil {
		return fmt.Errorf("failed to get diff: %w", err)
	}

	return formatDiffOutput(diffs, parseGitFormat(gitFormat))
}

// formatDiffOutput formats and outputs the diff results.
func formatDiffOutput(diffs []git.FileDiff, format GitOutputFormat) error {
	if len(diffs) == 0 {
		if format != GitOutputJSON {
			fmt.Println("No changes found.")
		} else {
			fmt.Println("[]")
		}
		return nil
	}

	switch format {
	case GitOutputJSON:
		return outputDiffJSON(diffs)
	case GitOutputPlain:
		return outputDiffPlain(diffs)
	default:
		return outputDiffTable(diffs)
	}
}

func outputDiffJSON(diffs []git.FileDiff) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(diffs)
}

func outputDiffPlain(diffs []git.FileDiff) error {
	for _, diff := range diffs {
		fmt.Printf("%s %s\n", diff.Status, diff.Path)
		if diff.OldPath != "" {
			fmt.Printf("  (from %s)\n", diff.OldPath)
		}
		for _, hunk := range diff.Hunks {
			for _, line := range hunk.Lines {
				fmt.Printf("  %s\n", line)
			}
		}
	}
	return nil
}

func outputDiffTable(diffs []git.FileDiff) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tFILE\tADDED\tDELETED")
	fmt.Fprintln(w, "------\t----\t-----\t-------")

	for _, diff := range diffs {
		status := formatDiffStatus(diff.Status)
		path := diff.Path
		if diff.OldPath != "" {
			path = fmt.Sprintf("%s <- %s", diff.Path, diff.OldPath)
		}
		fmt.Fprintf(w, "%s\t%s\t+%d\t-%d\n", status, path, diff.Additions, diff.Deletions)
	}

	return w.Flush()
}

func formatDiffStatus(status string) string {
	switch status {
	case "A":
		return "Added"
	case "M":
		return "Modified"
	case "D":
		return "Deleted"
	case "R":
		return "Renamed"
	case "C":
		return "Copied"
	default:
		return status
	}
}

// =============================================================================
// Git Log Command
// =============================================================================

func runGitLog(cmd *cobra.Command, args []string) error {
	client, err := git.NewGitClient(gitRepoDir)
	if err != nil {
		return fmt.Errorf("failed to create git client: %w", err)
	}
	defer client.Close()

	if !client.IsGitRepo() {
		return fmt.Errorf("not a git repository: %s", gitRepoDir)
	}

	var commits []*git.CommitInfo

	if len(args) == 1 {
		// File history
		opts := buildFileHistoryOptions()
		commits, err = client.GetFileHistory(args[0], opts)
	} else {
		// Repository history
		commits, err = getFilteredCommits(client)
	}

	if err != nil {
		return fmt.Errorf("failed to get log: %w", err)
	}

	// Filter by author if specified
	if gitAuthor != "" {
		commits = filterCommitsByAuthor(commits, gitAuthor)
	}

	return formatLogOutput(commits, parseGitFormat(gitFormat))
}

func buildFileHistoryOptions() git.FileHistoryOptions {
	opts := git.FileHistoryOptions{
		Limit: gitLimit,
	}

	if gitSince != "" {
		if t, err := parseGitDuration(gitSince); err == nil {
			opts.Since = t
		}
	}

	if gitUntil != "" {
		if t, err := parseGitDuration(gitUntil); err == nil {
			opts.Until = t
		}
	}

	return opts
}

func getFilteredCommits(client *git.GitClient) ([]*git.CommitInfo, error) {
	if gitSince != "" {
		sinceTime, err := parseGitDuration(gitSince)
		if err != nil {
			return nil, fmt.Errorf("invalid --since value: %w", err)
		}

		commits, err := client.GetCommitsSince(sinceTime)
		if err != nil {
			return nil, err
		}

		// Apply until filter
		if gitUntil != "" {
			untilTime, untilErr := parseGitDuration(gitUntil)
			if untilErr != nil {
				return nil, fmt.Errorf("invalid --until value: %w", untilErr)
			}
			commits = filterCommitsByTime(commits, sinceTime, untilTime)
		}

		// Apply limit
		if gitLimit > 0 && len(commits) > gitLimit {
			commits = commits[:gitLimit]
		}

		return commits, nil
	}

	// No since filter, get all commits up to limit
	return client.GetAllCommits(gitLimit)
}

func filterCommitsByTime(commits []*git.CommitInfo, since, until time.Time) []*git.CommitInfo {
	filtered := make([]*git.CommitInfo, 0, len(commits))
	for _, c := range commits {
		if c.AuthorTime.Before(until) && c.AuthorTime.After(since) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func filterCommitsByAuthor(commits []*git.CommitInfo, author string) []*git.CommitInfo {
	author = strings.ToLower(author)
	filtered := make([]*git.CommitInfo, 0, len(commits))
	for _, c := range commits {
		if strings.Contains(strings.ToLower(c.Author), author) ||
			strings.Contains(strings.ToLower(c.AuthorEmail), author) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func formatLogOutput(commits []*git.CommitInfo, format GitOutputFormat) error {
	if len(commits) == 0 {
		if format != GitOutputJSON {
			fmt.Println("No commits found.")
		} else {
			fmt.Println("[]")
		}
		return nil
	}

	switch format {
	case GitOutputJSON:
		return outputLogJSON(commits)
	case GitOutputPlain:
		return outputLogPlain(commits)
	default:
		return outputLogTable(commits)
	}
}

func outputLogJSON(commits []*git.CommitInfo) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(commits)
}

func outputLogPlain(commits []*git.CommitInfo) error {
	for _, c := range commits {
		fmt.Printf("commit %s\n", c.Hash)
		fmt.Printf("Author: %s <%s>\n", c.Author, c.AuthorEmail)
		fmt.Printf("Date:   %s\n\n", c.AuthorTime.Format(time.RFC1123))
		fmt.Printf("    %s\n\n", c.Subject)
	}
	return nil
}

func outputLogTable(commits []*git.CommitInfo) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HASH\tAUTHOR\tDATE\tSUBJECT")
	fmt.Fprintln(w, "----\t------\t----\t-------")

	for _, c := range commits {
		date := c.AuthorTime.Format("2006-01-02 15:04")
		subject := truncateString(c.Subject, 60)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.ShortHash, c.Author, date, subject)
	}

	return w.Flush()
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// =============================================================================
// Git Blame Command
// =============================================================================

func runGitBlame(cmd *cobra.Command, args []string) error {
	client, err := git.NewGitClient(gitRepoDir)
	if err != nil {
		return fmt.Errorf("failed to create git client: %w", err)
	}
	defer client.Close()

	if !client.IsGitRepo() {
		return fmt.Errorf("not a git repository: %s", gitRepoDir)
	}

	// Use a context with timeout for blame operations
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_ = ctx // Context used for potential future async operations

	result, err := client.GetBlameInfo(args[0])
	if err != nil {
		return fmt.Errorf("failed to get blame: %w", err)
	}

	return formatBlameOutput(result, parseGitFormat(gitFormat))
}

func formatBlameOutput(result *git.BlameResult, format GitOutputFormat) error {
	if result == nil || len(result.Lines) == 0 {
		if format != GitOutputJSON {
			fmt.Println("No blame information found.")
		} else {
			fmt.Println("{}")
		}
		return nil
	}

	switch format {
	case GitOutputJSON:
		return outputBlameJSON(result)
	case GitOutputPlain:
		return outputBlamePlain(result)
	default:
		return outputBlameTable(result)
	}
}

func outputBlameJSON(result *git.BlameResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func outputBlamePlain(result *git.BlameResult) error {
	for _, line := range result.Lines {
		date := line.AuthorTime.Format("2006-01-02")
		fmt.Printf("%s (%s %s %4d) %s\n",
			line.CommitHash[:8],
			truncateString(line.Author, 15),
			date,
			line.LineNumber,
			line.Content,
		)
	}
	return nil
}

func outputBlameTable(result *git.BlameResult) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LINE\tCOMMIT\tAUTHOR\tDATE\tCONTENT")
	fmt.Fprintln(w, "----\t------\t------\t----\t-------")

	for _, line := range result.Lines {
		date := line.AuthorTime.Format("2006-01-02")
		content := truncateString(line.Content, 50)
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			line.LineNumber,
			line.CommitHash[:8],
			truncateString(line.Author, 15),
			date,
			content,
		)
	}

	return w.Flush()
}

// =============================================================================
// Utility Functions
// =============================================================================

func parseGitFormat(s string) GitOutputFormat {
	switch strings.ToLower(s) {
	case "json":
		return GitOutputJSON
	case "plain":
		return GitOutputPlain
	default:
		return GitOutputTable
	}
}

func parseGitDuration(s string) (time.Time, error) {
	// Try absolute date formats
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try relative duration
	return parseGitRelativeDuration(s)
}

func parseGitRelativeDuration(s string) (time.Time, error) {
	now := time.Now()

	if strings.HasSuffix(s, "h") {
		var hours int
		if _, err := fmt.Sscanf(s, "%dh", &hours); err == nil {
			return now.Add(-time.Duration(hours) * time.Hour), nil
		}
	}

	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
			return now.AddDate(0, 0, -days), nil
		}
	}

	if strings.HasSuffix(s, "w") {
		var weeks int
		if _, err := fmt.Sscanf(s, "%dw", &weeks); err == nil {
			return now.AddDate(0, 0, -weeks*7), nil
		}
	}

	if strings.HasSuffix(s, "m") {
		var months int
		if _, err := fmt.Sscanf(s, "%dm", &months); err == nil {
			return now.AddDate(0, -months, 0), nil
		}
	}

	if strings.HasSuffix(s, "y") {
		var years int
		if _, err := fmt.Sscanf(s, "%dy", &years); err == nil {
			return now.AddDate(-years, 0, 0), nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported duration format: %s", s)
}

package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/security"
	"github.com/spf13/cobra"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Audit log management commands",
	Long:  `Query, verify, export, and purge audit logs.`,
}

var auditQueryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query audit log entries",
	Long:  `Search audit logs with various filters.`,
	RunE:  runAuditQuery,
}

var auditVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify audit log integrity",
	Long:  `Check the cryptographic integrity of audit logs.`,
	RunE:  runAuditVerify,
}

var auditExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export audit logs",
	Long:  `Export audit logs for external analysis.`,
	RunE:  runAuditExport,
}

var auditPurgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Purge old audit entries",
	Long:  `Remove audit entries older than specified date.`,
	RunE:  runAuditPurge,
}

var (
	auditCategory string
	auditSeverity string
	auditSince    string
	auditBefore   string
	auditSession  string
	auditAgent    string
	auditAction   string
	auditTarget   string
	auditFormat   string
	auditLimit    int
	auditOffset   int
	auditBackup   bool
	auditConfirm  bool
	auditLogPath  string
)

func init() {
	rootCmd.AddCommand(auditCmd)
	auditCmd.AddCommand(auditQueryCmd)
	auditCmd.AddCommand(auditVerifyCmd)
	auditCmd.AddCommand(auditExportCmd)
	auditCmd.AddCommand(auditPurgeCmd)

	addQueryFlags(auditQueryCmd)
	addQueryFlags(auditExportCmd)

	auditPurgeCmd.Flags().StringVar(&auditBefore, "before", "", "Purge entries before this date (e.g., 90d, 2024-01-01)")
	auditPurgeCmd.Flags().BoolVar(&auditBackup, "backup", true, "Create backup before purging")
	auditPurgeCmd.Flags().BoolVarP(&auditConfirm, "yes", "y", false, "Skip confirmation prompt")

	auditCmd.PersistentFlags().StringVar(&auditLogPath, "log-path", "", "Path to audit log file")
}

func addQueryFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&auditCategory, "category", "", "Filter by category (permission,file,process,network,llm,session,config)")
	cmd.Flags().StringVar(&auditSeverity, "severity", "", "Filter by severity (info,warning,security,critical)")
	cmd.Flags().StringVar(&auditSince, "since", "", "Entries since (e.g., 24h, 7d, 2024-01-01)")
	cmd.Flags().StringVar(&auditBefore, "before", "", "Entries before (e.g., 24h, 2024-01-01)")
	cmd.Flags().StringVar(&auditSession, "session", "", "Filter by session ID")
	cmd.Flags().StringVar(&auditAgent, "agent", "", "Filter by agent ID")
	cmd.Flags().StringVar(&auditAction, "action", "", "Filter by action")
	cmd.Flags().StringVar(&auditTarget, "target", "", "Filter by target (regex)")
	cmd.Flags().StringVarP(&auditFormat, "format", "f", "table", "Output format (table,json,csv)")
	cmd.Flags().IntVar(&auditLimit, "limit", 100, "Maximum entries to return")
	cmd.Flags().IntVar(&auditOffset, "offset", 0, "Offset for pagination")
}

func runAuditQuery(cmd *cobra.Command, args []string) error {
	filter, err := buildQueryFilter()
	if err != nil {
		return err
	}

	querier := security.NewAuditQuerier(getAuditLogPath())
	entries, err := querier.Query(filter)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	return security.FormatEntries(entries, parseOutputFormat(auditFormat), os.Stdout)
}

func runAuditVerify(cmd *cobra.Command, args []string) error {
	cfg := security.AuditLogConfig{LogPath: getAuditLogPath()}
	logger, err := security.NewAuditLogger(cfg)
	if err != nil {
		return fmt.Errorf("failed to open audit log: %w", err)
	}
	defer logger.Close()

	report, err := logger.VerifyIntegrity()
	if err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}

	printVerificationReport(report)
	if !report.Valid {
		return fmt.Errorf("integrity check failed")
	}
	return nil
}

func printVerificationReport(report *security.IntegrityReport) {
	fmt.Println("Audit Log Integrity Report")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("Entries verified:    %d\n", report.EntriesVerified)
	fmt.Printf("Signatures verified: %d\n", report.SignaturesVerified)
	fmt.Printf("Duration:            %v\n", report.EndTime.Sub(report.StartTime))

	if report.Valid {
		fmt.Println("\nResult: VALID ✓")
	} else {
		fmt.Println("\nResult: INVALID ✗")
		fmt.Println("\nErrors:")
		for _, e := range report.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}
}

func runAuditExport(cmd *cobra.Command, args []string) error {
	filter, err := buildQueryFilter()
	if err != nil {
		return err
	}

	querier := security.NewAuditQuerier(getAuditLogPath())
	return querier.Export(filter, parseOutputFormat(auditFormat), os.Stdout)
}

func runAuditPurge(cmd *cobra.Command, args []string) error {
	if auditBefore == "" {
		return fmt.Errorf("--before is required")
	}

	beforeTime, err := parseDuration(auditBefore)
	if err != nil {
		return fmt.Errorf("invalid --before value: %w", err)
	}

	if !auditConfirm {
		fmt.Printf("This will purge all entries before %s\n", beforeTime.Format(time.RFC3339))
		fmt.Print("Are you sure? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	querier := security.NewAuditQuerier(getAuditLogPath())
	opts := security.PurgeOptions{
		Before:       beforeTime,
		CreateBackup: auditBackup,
	}

	if err := querier.Purge(opts, nil); err != nil {
		return fmt.Errorf("purge failed: %w", err)
	}

	fmt.Println("Purge completed successfully.")
	return nil
}

func buildQueryFilter() (security.QueryFilter, error) {
	filter := security.QueryFilter{
		Limit:  auditLimit,
		Offset: auditOffset,
	}

	if err := parseCategories(&filter); err != nil {
		return filter, err
	}
	if err := parseSeverities(&filter); err != nil {
		return filter, err
	}
	if err := parseTimeFilters(&filter); err != nil {
		return filter, err
	}
	if err := parseIdentifierFilters(&filter); err != nil {
		return filter, err
	}

	return filter, nil
}

func parseCategories(filter *security.QueryFilter) error {
	if auditCategory == "" {
		return nil
	}
	for _, c := range strings.Split(auditCategory, ",") {
		filter.Categories = append(filter.Categories, security.AuditCategory(strings.TrimSpace(c)))
	}
	return nil
}

func parseSeverities(filter *security.QueryFilter) error {
	if auditSeverity == "" {
		return nil
	}
	for _, s := range strings.Split(auditSeverity, ",") {
		filter.Severities = append(filter.Severities, security.AuditSeverity(strings.TrimSpace(s)))
	}
	return nil
}

func parseTimeFilters(filter *security.QueryFilter) error {
	var err error
	if auditSince != "" {
		filter.StartTime, err = parseDuration(auditSince)
		if err != nil {
			return fmt.Errorf("invalid --since: %w", err)
		}
	}
	if auditBefore != "" {
		filter.EndTime, err = parseDuration(auditBefore)
		if err != nil {
			return fmt.Errorf("invalid --before: %w", err)
		}
	}
	return nil
}

func parseIdentifierFilters(filter *security.QueryFilter) error {
	filter.SessionID = auditSession
	filter.AgentID = auditAgent
	filter.Action = auditAction

	if auditTarget != "" {
		re, err := regexp.Compile(auditTarget)
		if err != nil {
			return fmt.Errorf("invalid --target regex: %w", err)
		}
		filter.Target = re
	}
	return nil
}

func parseDuration(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	return parseRelativeDuration(s)
}

func parseRelativeDuration(s string) (time.Time, error) {
	now := time.Now()

	if strings.HasSuffix(s, "h") {
		hours, err := parseNumericSuffix(s, "h")
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(-time.Duration(hours) * time.Hour), nil
	}

	if strings.HasSuffix(s, "d") {
		days, err := parseNumericSuffix(s, "d")
		if err != nil {
			return time.Time{}, err
		}
		return now.AddDate(0, 0, -days), nil
	}

	return time.Time{}, fmt.Errorf("unsupported format: %s", s)
}

func parseNumericSuffix(s, suffix string) (int, error) {
	var value int
	_, err := fmt.Sscanf(s, "%d"+suffix, &value)
	return value, err
}

func parseOutputFormat(s string) security.OutputFormat {
	switch strings.ToLower(s) {
	case "json":
		return security.OutputFormatJSON
	case "csv":
		return security.OutputFormatCSV
	default:
		return security.OutputFormatTable
	}
}

func getAuditLogPath() string {
	if auditLogPath != "" {
		return auditLogPath
	}
	return ".sylk/audit/audit.log"
}

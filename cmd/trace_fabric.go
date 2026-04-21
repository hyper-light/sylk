package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/fabriclog"
	"github.com/spf13/cobra"
)

// traceFabricCmd is `sylk trace fabric …`. It reads the session-
// global fabric observability stream at
// .sylk/sessions/{sid}/fabric/{YYYY-MM-DD}/fabric.{nnn}.jsonl and
// pivots it into useful views.
//
// Subcommands favor analysis questions over raw dumps — the raw log
// is readable JSONL, anyone can cat/jq it. These subcommands answer:
//
//   summary      how is the fabric being used in aggregate?
//   unread       which publications never got consumed?
//   lifecycles   which consults/challenges got a resolution, and when?
//   follow <id>  full publish → consume → resolve chain for one activity
//   scopes       which scopes see the most / least interaction?
var traceFabricCmd = &cobra.Command{
	Use:   "fabric",
	Short: "Analyze the Activity Fabric observability log",
	Long: `Pivot the session-global fabric log into analysis views:

  sylk trace fabric summary              Top-level counts (publishes, reads, consume joins, drops)
  sylk trace fabric unread               Publications that never got consumed
  sylk trace fabric lifecycles           Consult/challenge end-to-end lifecycles
  sylk trace fabric follow <activity-id> Publish → consume → resolve chain for one activity
  sylk trace fabric scopes               Scope → activity counts with publisher and consumer sets`,
}

var traceFabricSession string

func init() {
	traceFabricCmd.PersistentFlags().StringVar(&traceFabricSession, "session", "", "Override session ID (defaults to the most recent session with a fabric log)")

	traceFabricCmd.AddCommand(traceFabricSummaryCmd)
	traceFabricCmd.AddCommand(traceFabricUnreadCmd)
	traceFabricCmd.AddCommand(traceFabricLifecyclesCmd)
	traceFabricCmd.AddCommand(traceFabricFollowCmd)
	traceFabricCmd.AddCommand(traceFabricScopesCmd)

	traceCmd.AddCommand(traceFabricCmd)
}

var traceFabricSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Top-level fabric interaction counts",
	RunE:  runTraceFabricSummary,
}

var traceFabricUnreadCmd = &cobra.Command{
	Use:   "unread",
	Short: "Publications with no consume edge (dead-letter analysis)",
	RunE:  runTraceFabricUnread,
}

var traceFabricLifecyclesCmd = &cobra.Command{
	Use:   "lifecycles",
	Short: "Consult/challenge/claim lifecycles end-to-end",
	RunE:  runTraceFabricLifecycles,
}

var traceFabricFollowCmd = &cobra.Command{
	Use:   "follow <activity-id>",
	Short: "Full publish → consume → resolve chain for one activity",
	Args:  cobra.ExactArgs(1),
	RunE:  runTraceFabricFollow,
}

var traceFabricScopesCmd = &cobra.Command{
	Use:   "scopes",
	Short: "Per-scope activity, publisher set, and consumer set",
	RunE:  runTraceFabricScopes,
}

// ─── command runners ────────────────────────────────────────────────

func runTraceFabricSummary(cmd *cobra.Command, _ []string) error {
	records, err := loadFabricRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	counts := map[fabriclog.RecordKind]int{}
	var totalBytes int
	agentPublishes := map[string]int{}
	agentReads := map[string]int{}
	actionKinds := map[string]int{}
	scopes := map[string]int{}
	var earliest, latest time.Time
	var totalDrops int64
	var totalEmittedSeq int64

	for _, r := range records {
		counts[r.Kind]++
		if !r.Timestamp.IsZero() {
			if earliest.IsZero() || r.Timestamp.Before(earliest) {
				earliest = r.Timestamp
			}
			if latest.IsZero() || r.Timestamp.After(latest) {
				latest = r.Timestamp
			}
		}
		if r.Seq > totalEmittedSeq {
			totalEmittedSeq = r.Seq
		}
		switch r.Kind {
		case fabriclog.KindPublish:
			if r.Publish != nil {
				actionKinds[r.Publish.ActionKind]++
				if r.Publish.Scope != "" {
					scopes[r.Publish.Scope]++
				}
				totalBytes += r.Publish.PayloadBytes
			}
			agentPublishes[agentLabel(r.Agent)]++
		case fabriclog.KindRead:
			agentReads[agentLabel(r.Agent)]++
		case fabriclog.KindDrop:
			if r.Drop != nil && r.Drop.DroppedCount > totalDrops {
				totalDrops = r.Drop.DroppedCount
			}
		}
	}

	fmt.Fprintln(out, "Fabric observability summary")
	fmt.Fprintln(out, "============================")
	if !earliest.IsZero() {
		fmt.Fprintf(out, "span                : %s → %s (%s)\n",
			earliest.Format(time.RFC3339),
			latest.Format(time.RFC3339),
			latest.Sub(earliest).Round(time.Second))
	}
	fmt.Fprintf(out, "records             : %d (highest seq: %d)\n", len(records), totalEmittedSeq)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "by kind:")
	for _, k := range []fabriclog.RecordKind{
		fabriclog.KindPublish, fabriclog.KindRead, fabriclog.KindConsume,
		fabriclog.KindAmbient, fabriclog.KindResolve, fabriclog.KindDrop,
	} {
		fmt.Fprintf(out, "  %-16s %d\n", string(k), counts[k])
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "publish payload total : %d bytes\n", totalBytes)
	if totalDrops > 0 {
		fmt.Fprintf(out, "cumulative drops      : %d (buffer_full)\n", totalDrops)
	}

	if len(actionKinds) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "top action kinds:")
		printTopN(out, actionKinds, 10)
	}
	if len(scopes) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "top scopes:")
		printTopN(out, scopes, 10)
	}
	if len(agentPublishes) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "top publishers:")
		printTopN(out, agentPublishes, 10)
	}
	if len(agentReads) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "top readers:")
		printTopN(out, agentReads, 10)
	}
	return nil
}

func runTraceFabricUnread(cmd *cobra.Command, _ []string) error {
	records, err := loadFabricRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	consumed := map[string]int{} // activity_id → consume count
	publishes := map[string]*fabriclog.FabricLogRecord{}

	for i := range records {
		r := &records[i]
		switch r.Kind {
		case fabriclog.KindPublish:
			if r.Publish != nil && r.Publish.ActivityID != "" {
				publishes[r.Publish.ActivityID] = r
			}
		case fabriclog.KindConsume:
			if r.Consume != nil && r.Consume.ActivityID != "" {
				consumed[r.Consume.ActivityID]++
			}
		}
	}

	type unreadRow struct {
		ID         string
		ActionKind string
		Scope      string
		Actor      string
		Published  time.Time
		AgeSecs    int64
	}
	var rows []unreadRow
	now := time.Now()
	for id, rec := range publishes {
		if consumed[id] > 0 {
			continue
		}
		row := unreadRow{
			ID:         id,
			Scope:      rec.Publish.Scope,
			ActionKind: rec.Publish.ActionKind,
			Actor:      agentLabel(rec.Agent),
			Published:  rec.Timestamp,
		}
		if !rec.Timestamp.IsZero() {
			row.AgeSecs = int64(now.Sub(rec.Timestamp).Seconds())
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].AgeSecs > rows[j].AgeSecs
	})

	fmt.Fprintf(out, "Unread publications: %d of %d\n", len(rows), len(publishes))
	if len(rows) == 0 {
		return nil
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%-32s  %-24s  %-32s  %-22s  %s\n", "activity_id", "action_kind", "scope", "actor", "age")
	fmt.Fprintln(out, strings.Repeat("-", 130))
	for _, r := range rows {
		scope := r.Scope
		if scope == "" {
			scope = "-"
		}
		fmt.Fprintf(out, "%-32s  %-24s  %-32s  %-22s  %ds\n",
			truncateStr(r.ID, 32), truncateStr(r.ActionKind, 24),
			truncateStr(scope, 32), truncateStr(r.Actor, 22), r.AgeSecs)
	}
	return nil
}

func runTraceFabricLifecycles(cmd *cobra.Command, _ []string) error {
	records, err := loadFabricRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	// Lifecycle activities worth surfacing: things that declare work
	// expected to be resolved (challenge_emitted, consult_emitted,
	// claim_emitted). We pair each emit with any resolve record whose
	// resolved_activity_id matches.
	const (
		challengeKind = "challenge_emitted"
		consultKind   = "consult_emitted"
		claimKind     = "claim_emitted"
	)
	emits := map[string]*fabriclog.FabricLogRecord{} // activity_id → emit record
	resolves := map[string]*fabriclog.FabricLogRecord{}
	for i := range records {
		r := &records[i]
		switch r.Kind {
		case fabriclog.KindPublish:
			if r.Publish == nil {
				continue
			}
			switch r.Publish.ActionKind {
			case challengeKind, consultKind, claimKind:
				emits[r.Publish.ActivityID] = r
			}
		case fabriclog.KindResolve:
			if r.Resolve != nil {
				resolves[r.Resolve.ResolvedActivityID] = r
			}
		}
	}

	type lifeRow struct {
		EmitKind     string
		ID           string
		Scope        string
		Emitter      string
		EmittedAt    time.Time
		Resolved     bool
		Resolver     string
		ResolvedKind string
		Latency      time.Duration
	}
	var rows []lifeRow
	for id, emit := range emits {
		row := lifeRow{
			EmitKind:  emit.Publish.ActionKind,
			ID:        id,
			Scope:     emit.Publish.Scope,
			Emitter:   agentLabel(emit.Agent),
			EmittedAt: emit.Timestamp,
		}
		if res, ok := resolves[id]; ok && res.Resolve != nil {
			row.Resolved = true
			row.Resolver = agentLabel(res.Agent)
			row.ResolvedKind = res.Resolve.ResolverKind
			row.Latency = time.Duration(res.Resolve.LatencyMicros) * time.Microsecond
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].EmittedAt.Before(rows[j].EmittedAt)
	})

	resolved := 0
	for _, r := range rows {
		if r.Resolved {
			resolved++
		}
	}
	fmt.Fprintf(out, "Lifecycles: %d total, %d resolved, %d pending\n",
		len(rows), resolved, len(rows)-resolved)
	if len(rows) == 0 {
		return nil
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%-18s  %-32s  %-22s  %-8s  %-22s  %s\n",
		"kind", "activity_id", "emitter", "state", "resolver", "latency")
	fmt.Fprintln(out, strings.Repeat("-", 126))
	for _, r := range rows {
		state := "pending"
		if r.Resolved {
			state = "ok"
		}
		latency := "-"
		if r.Latency > 0 {
			latency = r.Latency.Round(time.Millisecond).String()
		}
		resolver := r.Resolver
		if resolver == "" {
			resolver = "-"
		}
		fmt.Fprintf(out, "%-18s  %-32s  %-22s  %-8s  %-22s  %s\n",
			truncateStr(r.EmitKind, 18),
			truncateStr(r.ID, 32),
			truncateStr(r.Emitter, 22),
			state,
			truncateStr(resolver, 22),
			latency,
		)
	}
	return nil
}

func runTraceFabricFollow(cmd *cobra.Command, args []string) error {
	activityID := strings.TrimSpace(args[0])
	if activityID == "" {
		return fmt.Errorf("activity-id required")
	}
	records, err := loadFabricRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	// Collect everything touching this activity, in seq order.
	var relevant []*fabriclog.FabricLogRecord
	for i := range records {
		r := &records[i]
		switch r.Kind {
		case fabriclog.KindPublish:
			if r.Publish != nil && r.Publish.ActivityID == activityID {
				relevant = append(relevant, r)
			}
		case fabriclog.KindConsume:
			if r.Consume != nil && r.Consume.ActivityID == activityID {
				relevant = append(relevant, r)
			}
		case fabriclog.KindResolve:
			if r.Resolve != nil && (r.Resolve.ResolvedActivityID == activityID || r.Resolve.ResolverActivityID == activityID) {
				relevant = append(relevant, r)
			}
		case fabriclog.KindAmbient:
			if r.Ambient != nil {
				for _, id := range r.Ambient.ActivityIDs {
					if id == activityID {
						relevant = append(relevant, r)
						break
					}
				}
			}
		}
	}
	if len(relevant) == 0 {
		fmt.Fprintf(out, "no records found for activity %s\n", activityID)
		return nil
	}
	sort.Slice(relevant, func(i, j int) bool {
		return relevant[i].Seq < relevant[j].Seq
	})
	fmt.Fprintf(out, "Activity %s — %d touchpoints\n", activityID, len(relevant))
	fmt.Fprintln(out)
	for _, r := range relevant {
		ts := r.Timestamp.Format("15:04:05.000")
		switch r.Kind {
		case fabriclog.KindPublish:
			fmt.Fprintf(out, "[%s] publish   seq=%-5d %s by %s (scope=%s)\n",
				ts, r.Seq, r.Publish.ActionKind, agentLabel(r.Agent), r.Publish.Scope)
		case fabriclog.KindConsume:
			latency := ""
			if r.Consume.LatencyMicros > 0 {
				latency = fmt.Sprintf(" (publish→consume: %s)", (time.Duration(r.Consume.LatencyMicros) * time.Microsecond).Round(time.Millisecond))
			}
			fmt.Fprintf(out, "[%s] consume   seq=%-5d by %s reader_seq=%d%s\n",
				ts, r.Seq, agentLabel(r.Agent), r.Consume.ReaderRecordSeq, latency)
		case fabriclog.KindResolve:
			role := "resolver"
			if r.Resolve.ResolvedActivityID == activityID {
				role = "resolved-by"
			}
			latency := ""
			if r.Resolve.LatencyMicros > 0 {
				latency = fmt.Sprintf(" (resolve latency: %s)", (time.Duration(r.Resolve.LatencyMicros) * time.Microsecond).Round(time.Millisecond))
			}
			fmt.Fprintf(out, "[%s] resolve   seq=%-5d role=%s kind=%s%s\n",
				ts, r.Seq, role, r.Resolve.ResolverKind, latency)
		case fabriclog.KindAmbient:
			fmt.Fprintf(out, "[%s] ambient   seq=%-5d surfaced to %s (tool=%s)\n",
				ts, r.Seq, agentLabel(r.Agent), r.Ambient.ToolName)
		}
	}
	return nil
}

func runTraceFabricScopes(cmd *cobra.Command, _ []string) error {
	records, err := loadFabricRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	type scopeAgg struct {
		Publishes int
		Consumes  int
		Reads     int
		Pubs      map[string]struct{}
		Cons      map[string]struct{}
	}
	scopes := map[string]*scopeAgg{}
	get := func(name string) *scopeAgg {
		if s, ok := scopes[name]; ok {
			return s
		}
		s := &scopeAgg{
			Pubs: make(map[string]struct{}),
			Cons: make(map[string]struct{}),
		}
		scopes[name] = s
		return s
	}
	// We need to cross-reference consume → publish-scope, since consume
	// records don't carry scope directly. Build a side map first.
	activityScope := map[string]string{}
	for i := range records {
		r := &records[i]
		if r.Kind == fabriclog.KindPublish && r.Publish != nil {
			activityScope[r.Publish.ActivityID] = r.Publish.Scope
		}
	}
	for i := range records {
		r := &records[i]
		switch r.Kind {
		case fabriclog.KindPublish:
			if r.Publish == nil {
				continue
			}
			scope := r.Publish.Scope
			if scope == "" {
				scope = "(unscoped)"
			}
			agg := get(scope)
			agg.Publishes++
			if agl := agentLabel(r.Agent); agl != "" {
				agg.Pubs[agl] = struct{}{}
			}
		case fabriclog.KindConsume:
			if r.Consume == nil {
				continue
			}
			scope := activityScope[r.Consume.ActivityID]
			if scope == "" {
				scope = "(unscoped)"
			}
			agg := get(scope)
			agg.Consumes++
			if agl := agentLabel(r.Agent); agl != "" {
				agg.Cons[agl] = struct{}{}
			}
		case fabriclog.KindRead:
			if r.Read == nil || r.Read.Filter == nil {
				continue
			}
			scope := r.Read.Filter.SubjectPathPrefix
			if scope == "" {
				continue
			}
			agg := get(scope)
			agg.Reads++
		}
	}
	type row struct {
		Scope    string
		Pubs     int
		Cons     int
		Reads    int
		Authors  int
		Readers  int
		Activity int
	}
	var rows []row
	for name, agg := range scopes {
		rows = append(rows, row{
			Scope:    name,
			Pubs:     agg.Publishes,
			Cons:     agg.Consumes,
			Reads:    agg.Reads,
			Authors:  len(agg.Pubs),
			Readers:  len(agg.Cons),
			Activity: agg.Publishes + agg.Consumes + agg.Reads,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Activity > rows[j].Activity
	})

	fmt.Fprintf(out, "Scopes: %d\n\n", len(rows))
	fmt.Fprintf(out, "%-48s  %-8s  %-8s  %-8s  %-8s  %s\n",
		"scope", "pubs", "cons", "reads", "authors", "readers")
	fmt.Fprintln(out, strings.Repeat("-", 96))
	for _, r := range rows {
		fmt.Fprintf(out, "%-48s  %-8d  %-8d  %-8d  %-8d  %d\n",
			truncateStr(r.Scope, 48), r.Pubs, r.Cons, r.Reads, r.Authors, r.Readers)
	}
	return nil
}

// ─── shared log loader ──────────────────────────────────────────────

// loadFabricRecords walks the resolved session's fabric directory
// and parses every record in seq order. Missing directory / no
// records returns an empty slice (not an error) so the runner can
// present "no data" instead of crashing.
func loadFabricRecords() ([]fabriclog.FabricLogRecord, error) {
	sessionsRoot := traceSessionDirResolved()
	sid := strings.TrimSpace(traceFabricSession)
	if sid == "" {
		resolved, err := inferLatestSessionWithFabric(sessionsRoot)
		if err != nil {
			return nil, err
		}
		sid = resolved
	}
	if sid == "" {
		return nil, fmt.Errorf("no session with a fabric log under %s; pass --session explicitly", sessionsRoot)
	}
	fabricDir := filepath.Join(sessionsRoot, sid, "fabric")
	var files []string
	_ = filepath.WalkDir(fabricDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "fabric.") || !strings.HasSuffix(base, ".jsonl") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	var out []fabriclog.FabricLogRecord
	for _, p := range files {
		recs, err := readFabricFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Seq < out[j].Seq
	})
	return out, nil
}

func readFabricFile(path string) ([]fabriclog.FabricLogRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var out []fabriclog.FabricLogRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec fabriclog.FabricLogRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// tolerate malformed lines so one bad record can't block
			// reconstruction
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}

// inferLatestSessionWithFabric picks the most-recently-modified
// session directory that contains a fabric subdirectory. Lets a
// bare `sylk trace fabric summary` work without --session in the
// common single-session case.
func inferLatestSessionWithFabric(sessionsRoot string) (string, error) {
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", sessionsRoot, err)
	}
	var best string
	var bestMtime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fabricDir := filepath.Join(sessionsRoot, e.Name(), "fabric")
		info, err := os.Stat(fabricDir)
		if err != nil || !info.IsDir() {
			continue
		}
		if info.ModTime().After(bestMtime) {
			best = e.Name()
			bestMtime = info.ModTime()
		}
	}
	return best, nil
}

// ─── helpers ────────────────────────────────────────────────────────

func agentLabel(a fabriclog.AgentRef) string {
	if a.AgentID == "" && a.AgentType == "" {
		return "(unknown)"
	}
	if a.AgentID == "" {
		return a.AgentType
	}
	if a.AgentType == "" {
		return a.AgentID
	}
	return fmt.Sprintf("%s/%s", a.AgentType, a.AgentID)
}

func truncateStr(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

func printTopN(out io.Writer, m map[string]int, n int) {
	type kv struct {
		K string
		V int
	}
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].V != pairs[j].V {
			return pairs[i].V > pairs[j].V
		}
		return pairs[i].K < pairs[j].K
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	for _, p := range pairs {
		fmt.Fprintf(out, "  %-48s %d\n", truncateStr(p.K, 48), p.V)
	}
}

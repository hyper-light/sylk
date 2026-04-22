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

	"github.com/adalundhe/sylk/core/buslog"
	"github.com/spf13/cobra"
)

// traceBusCmd is `sylk trace bus …`. Pivots the session-global bus
// observability log into analysis views.
var traceBusCmd = &cobra.Command{
	Use:   "bus",
	Short: "Analyze the ChannelBus observability log",
	Long: `Pivot the session-global bus log into analysis views:

  sylk trace bus summary                   Counts by kind, top topics, top agents, publish/delivery totals
  sylk trace bus topics                    Per-topic publish + subscriber counts
  sylk trace bus agent <agent-id>          One agent's bus footprint (published vs received)
  sylk trace bus follow <correlation-id>   Publish → delivery chain for a correlation ID
  sylk trace bus overflows                 Every subscription queue overflow with correlation IDs`,
}

var traceBusSession string

func init() {
	traceBusCmd.PersistentFlags().StringVar(&traceBusSession, "session", "", "Override session ID (defaults to the most recent session with a bus log)")

	traceBusCmd.AddCommand(traceBusSummaryCmd)
	traceBusCmd.AddCommand(traceBusTopicsCmd)
	traceBusCmd.AddCommand(traceBusAgentCmd)
	traceBusCmd.AddCommand(traceBusFollowCmd)
	traceBusCmd.AddCommand(traceBusOverflowsCmd)

	traceCmd.AddCommand(traceBusCmd)
}

var traceBusSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Top-level bus interaction counts",
	RunE:  runTraceBusSummary,
}

var traceBusTopicsCmd = &cobra.Command{
	Use:   "topics",
	Short: "Per-topic publish + subscriber counts",
	RunE:  runTraceBusTopics,
}

var traceBusAgentCmd = &cobra.Command{
	Use:   "agent <agent-id>",
	Short: "One agent's bus footprint",
	Args:  cobra.ExactArgs(1),
	RunE:  runTraceBusAgent,
}

var traceBusFollowCmd = &cobra.Command{
	Use:   "follow <correlation-id>",
	Short: "Publish → delivery chain for a correlation ID",
	Args:  cobra.ExactArgs(1),
	RunE:  runTraceBusFollow,
}

var traceBusOverflowsCmd = &cobra.Command{
	Use:   "overflows",
	Short: "Every subscription queue overflow",
	RunE:  runTraceBusOverflows,
}

// ─── command runners ────────────────────────────────────────────────

func runTraceBusSummary(cmd *cobra.Command, _ []string) error {
	records, err := loadBusRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	counts := map[buslog.RecordKind]int{}
	topics := map[string]int{}
	publishers := map[string]int{}
	subscribers := map[string]int{}
	var earliest, latest time.Time
	var totalDrops int64
	var overflowDrops int
	var maxSeq int64

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
		if r.Seq > maxSeq {
			maxSeq = r.Seq
		}
		switch r.Kind {
		case buslog.KindPublish:
			if r.Publish != nil {
				topics[r.Publish.Topic]++
				if a := r.Publish.Message.SourceAgent; a != "" {
					publishers[a]++
				}
			}
		case buslog.KindDelivery:
			if r.Delivery != nil && r.Delivery.SubscriberAgent != "" {
				subscribers[r.Delivery.SubscriberAgent]++
			}
		case buslog.KindOverflow:
			if r.Overflow != nil {
				overflowDrops += r.Overflow.Dropped
			}
		case buslog.KindDrop:
			if r.Drop != nil && r.Drop.DroppedCount > totalDrops {
				totalDrops = r.Drop.DroppedCount
			}
		}
	}

	fmt.Fprintln(out, "Bus observability summary")
	fmt.Fprintln(out, "=========================")
	if !earliest.IsZero() {
		fmt.Fprintf(out, "span                : %s → %s (%s)\n",
			earliest.Format(time.RFC3339),
			latest.Format(time.RFC3339),
			latest.Sub(earliest).Round(time.Second))
	}
	fmt.Fprintf(out, "records             : %d (highest seq: %d)\n", len(records), maxSeq)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "by kind:")
	for _, k := range []buslog.RecordKind{
		buslog.KindPublish, buslog.KindDelivery, buslog.KindSubscribe,
		buslog.KindUnsubscribe, buslog.KindOverflow, buslog.KindExpired, buslog.KindDrop,
	} {
		fmt.Fprintf(out, "  %-18s %d\n", string(k), counts[k])
	}
	fmt.Fprintln(out)
	if overflowDrops > 0 {
		fmt.Fprintf(out, "overflow drops total: %d messages\n", overflowDrops)
	}
	if totalDrops > 0 {
		fmt.Fprintf(out, "buslog drops        : %d (buffer_full)\n", totalDrops)
	}
	if len(topics) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "top topics:")
		printTopN(out, topics, 15)
	}
	if len(publishers) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "top publishers:")
		printTopN(out, publishers, 10)
	}
	if len(subscribers) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "top subscribers:")
		printTopN(out, subscribers, 10)
	}
	return nil
}

func runTraceBusTopics(cmd *cobra.Command, _ []string) error {
	records, err := loadBusRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	type agg struct {
		Publishes   int
		Deliveries  int
		Subscribers map[int64]struct{}
	}
	topics := map[string]*agg{}
	get := func(name string) *agg {
		a, ok := topics[name]
		if !ok {
			a = &agg{Subscribers: map[int64]struct{}{}}
			topics[name] = a
		}
		return a
	}
	for _, r := range records {
		switch r.Kind {
		case buslog.KindPublish:
			if r.Publish == nil {
				continue
			}
			get(r.Publish.Topic).Publishes++
		case buslog.KindDelivery:
			if r.Delivery == nil {
				continue
			}
			a := get(r.Delivery.PublishTopic)
			a.Deliveries++
			a.Subscribers[r.Delivery.SubscriberID] = struct{}{}
		case buslog.KindSubscribe:
			if r.Subscribe == nil {
				continue
			}
			a := get(r.Subscribe.Topic)
			a.Subscribers[r.Subscribe.SubscriberID] = struct{}{}
		}
	}

	type row struct {
		Topic       string
		Publishes   int
		Deliveries  int
		Subscribers int
	}
	rows := make([]row, 0, len(topics))
	for name, a := range topics {
		rows = append(rows, row{
			Topic:       name,
			Publishes:   a.Publishes,
			Deliveries:  a.Deliveries,
			Subscribers: len(a.Subscribers),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Publishes != rows[j].Publishes {
			return rows[i].Publishes > rows[j].Publishes
		}
		return rows[i].Topic < rows[j].Topic
	})

	fmt.Fprintf(out, "Topics: %d\n\n", len(rows))
	fmt.Fprintf(out, "%-52s  %-10s  %-10s  %s\n", "topic", "pubs", "deliveries", "subscribers")
	fmt.Fprintln(out, strings.Repeat("-", 92))
	for _, r := range rows {
		fmt.Fprintf(out, "%-52s  %-10d  %-10d  %d\n",
			truncateStr(r.Topic, 52), r.Publishes, r.Deliveries, r.Subscribers)
	}
	return nil
}

func runTraceBusAgent(cmd *cobra.Command, args []string) error {
	agentID := strings.TrimSpace(args[0])
	if agentID == "" {
		return fmt.Errorf("agent-id required")
	}
	records, err := loadBusRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	var published []buslog.BusLogRecord
	var received []buslog.BusLogRecord
	for _, r := range records {
		switch r.Kind {
		case buslog.KindPublish:
			if r.Publish != nil && r.Publish.Message.SourceAgent == agentID {
				published = append(published, r)
			}
		case buslog.KindDelivery:
			if r.Delivery != nil && strings.Contains(r.Delivery.SubscriberAgent, agentID) {
				received = append(received, r)
			}
		}
	}
	sort.Slice(published, func(i, j int) bool { return published[i].Seq < published[j].Seq })
	sort.Slice(received, func(i, j int) bool { return received[i].Seq < received[j].Seq })

	fmt.Fprintf(out, "Agent %s footprint: %d published, %d received\n\n", agentID, len(published), len(received))

	if len(published) > 0 {
		fmt.Fprintln(out, "Published:")
		fmt.Fprintf(out, "  %-12s  %-38s  %-18s  %s\n", "seq", "topic", "type", "correlation_id")
		for _, r := range published {
			fmt.Fprintf(out, "  %-12d  %-38s  %-18s  %s\n",
				r.Seq, truncateStr(r.Publish.Topic, 38),
				truncateStr(r.Publish.Message.Type, 18),
				r.Publish.Message.CorrelationID)
		}
	}
	if len(received) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Received:")
		fmt.Fprintf(out, "  %-12s  %-38s  %-18s  %s\n", "seq", "topic", "type", "correlation_id")
		for _, r := range received {
			fmt.Fprintf(out, "  %-12d  %-38s  %-18s  %s\n",
				r.Seq, truncateStr(r.Delivery.PublishTopic, 38),
				truncateStr(r.Delivery.Message.Type, 18),
				r.Delivery.Message.CorrelationID)
		}
	}
	return nil
}

func runTraceBusFollow(cmd *cobra.Command, args []string) error {
	corrID := strings.TrimSpace(args[0])
	if corrID == "" {
		return fmt.Errorf("correlation-id required")
	}
	records, err := loadBusRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	var chain []buslog.BusLogRecord
	for _, r := range records {
		var cid string
		switch r.Kind {
		case buslog.KindPublish:
			if r.Publish != nil {
				cid = r.Publish.Message.CorrelationID
			}
		case buslog.KindDelivery:
			if r.Delivery != nil {
				cid = r.Delivery.Message.CorrelationID
			}
		case buslog.KindExpired:
			if r.Expired != nil {
				cid = r.Expired.Message.CorrelationID
			}
		}
		if cid == corrID {
			chain = append(chain, r)
		}
	}
	if len(chain) == 0 {
		fmt.Fprintf(out, "no records for correlation %s\n", corrID)
		return nil
	}
	sort.Slice(chain, func(i, j int) bool { return chain[i].Seq < chain[j].Seq })
	fmt.Fprintf(out, "Correlation %s — %d touchpoints\n\n", corrID, len(chain))
	for _, r := range chain {
		ts := r.Timestamp.Format("15:04:05.000")
		switch r.Kind {
		case buslog.KindPublish:
			fmt.Fprintf(out, "[%s] publish   seq=%-5d topic=%-38s src=%-16s type=%s\n",
				ts, r.Seq,
				truncateStr(r.Publish.Topic, 38),
				truncateStr(r.Publish.Message.SourceAgent, 16),
				r.Publish.Message.Type)
		case buslog.KindDelivery:
			lat := latencyMicrosHuman(r.Delivery.QueueLatencyMicros)
			errSeg := ""
			if r.Delivery.HandlerErr != "" {
				errSeg = fmt.Sprintf(" err=%q", r.Delivery.HandlerErr)
			}
			fmt.Fprintf(out, "[%s] deliver   seq=%-5d sub=%-22s queue=%s%s\n",
				ts, r.Seq,
				truncateStr(r.Delivery.SubscriberAgent, 22),
				lat, errSeg)
		case buslog.KindExpired:
			fmt.Fprintf(out, "[%s] expired   seq=%-5d topic=%s expired_at=%s\n",
				ts, r.Seq,
				truncateStr(r.Expired.Topic, 38),
				r.Expired.ExpiredAt.Format(time.RFC3339))
		}
	}
	return nil
}

func runTraceBusOverflows(cmd *cobra.Command, _ []string) error {
	records, err := loadBusRecords()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	var overflows []buslog.BusLogRecord
	for _, r := range records {
		if r.Kind == buslog.KindOverflow {
			overflows = append(overflows, r)
		}
	}
	sort.Slice(overflows, func(i, j int) bool { return overflows[i].Seq < overflows[j].Seq })

	fmt.Fprintf(out, "Overflows: %d\n\n", len(overflows))
	if len(overflows) == 0 {
		return nil
	}
	fmt.Fprintf(out, "%-6s  %-44s  %-8s  %-10s  %-8s  %s\n",
		"seq", "topic", "dropped", "total_drop", "remain", "correlations")
	fmt.Fprintln(out, strings.Repeat("-", 110))
	for _, r := range overflows {
		corr := strings.Join(r.Overflow.Correlations, ",")
		fmt.Fprintf(out, "%-6d  %-44s  %-8d  %-10d  %-8d  %s\n",
			r.Seq,
			truncateStr(r.Overflow.Topic, 44),
			r.Overflow.Dropped,
			r.Overflow.TotalDropped,
			r.Overflow.QueueRemaining,
			truncateStr(corr, 40))
	}
	return nil
}

// ─── shared log loader ──────────────────────────────────────────────

func loadBusRecords() ([]buslog.BusLogRecord, error) {
	sessionsRoot := traceSessionDirResolved()
	sid := strings.TrimSpace(traceBusSession)
	if sid == "" {
		resolved, err := inferLatestSessionWithSubdir(sessionsRoot, "bus")
		if err != nil {
			return nil, err
		}
		sid = resolved
	}
	if sid == "" {
		return nil, fmt.Errorf("no session with a bus log under %s; pass --session explicitly", sessionsRoot)
	}
	busDir := filepath.Join(sessionsRoot, sid, "bus")
	var files []string
	_ = filepath.WalkDir(busDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "bus.") || !strings.HasSuffix(base, ".jsonl") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	var out []buslog.BusLogRecord
	for _, p := range files {
		recs, err := readBusFile(p)
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

func readBusFile(path string) ([]buslog.BusLogRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var out []buslog.BusLogRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec buslog.BusLogRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}

// latencyMicrosHuman renders a microsecond count as the tightest
// human-readable duration. "-" is returned for zero-or-negative
// inputs so deliveries with no queue wait render cleanly.
func latencyMicrosHuman(micros int64) string {
	if micros <= 0 {
		return "<1µs"
	}
	const (
		msInMicros = 1_000
		sInMicros  = 1_000_000
	)
	switch {
	case micros < msInMicros:
		return fmt.Sprintf("%dµs", micros)
	case micros < sInMicros:
		return fmt.Sprintf("%dms", micros/msInMicros)
	default:
		return fmt.Sprintf("%.1fs", float64(micros)/float64(sInMicros))
	}
}

// inferLatestSessionWithSubdir picks the most-recently-modified
// session directory containing a given subdirectory. Used by both
// trace fabric and trace bus to default `--session` in the common
// single-session case.
func inferLatestSessionWithSubdir(sessionsRoot, subdir string) (string, error) {
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
		target := filepath.Join(sessionsRoot, e.Name(), subdir)
		info, err := os.Stat(target)
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

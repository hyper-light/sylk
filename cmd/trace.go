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

	"github.com/spf13/cobra"
)

// traceCmd is the entry point for `sylk trace`. It reconstructs
// cross-agent timelines from the structured JSONL logs written under
// .sylk/sessions/{sid}/agents/{agent}/logs/{date}/.
//
// Without a subcommand, `sylk trace <correlation-id>` shows the full
// timeline for a correlation chain. Subcommands `tool`, `llm`, and
// `session` offer scoped lookups.
var traceCmd = &cobra.Command{
	Use:   "trace [correlation-id]",
	Short: "Reconstruct an agent timeline from structured logs",
	Long: `Walk the session log tree and render an indented, chronological
timeline of events, tool calls, and LLM round-trips for a given
correlation ID. Use the subcommands for scoped lookups:

  sylk trace <correlation-id>         Full timeline for a correlation
  sylk trace tool <tool-call-key>     One tool call's lifecycle
  sylk trace llm <llm-call-id>        One LLM round-trip + emitted tools
  sylk trace session <session-id>     Everything in a session (use --agent/--pipeline to scope)`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTraceCorrelation,
}

var (
	traceSessionDir string
	traceAgent      string
	tracePipeline   string
	traceSince      string
	traceUntil      string
)

func init() {
	traceCmd.PersistentFlags().StringVar(&traceSessionDir, "session-dir", "", "Override session root (defaults to .sylk/sessions relative to cwd)")
	traceCmd.PersistentFlags().StringVar(&traceAgent, "agent", "", "Filter to a single agent name")
	traceCmd.PersistentFlags().StringVar(&tracePipeline, "pipeline", "", "Filter to a single pipeline ID")
	traceCmd.PersistentFlags().StringVar(&traceSince, "since", "", "Include entries at or after this RFC3339 timestamp")
	traceCmd.PersistentFlags().StringVar(&traceUntil, "until", "", "Include entries before this RFC3339 timestamp")

	traceCmd.AddCommand(traceToolCmd)
	traceCmd.AddCommand(traceLLMCmd)
	traceCmd.AddCommand(traceSessionCmd)

	rootCmd.AddCommand(traceCmd)
}

var traceToolCmd = &cobra.Command{
	Use:   "tool <tool-call-key>",
	Short: "Trace one tool call's lifecycle (start + complete + children)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTraceTool,
}

var traceLLMCmd = &cobra.Command{
	Use:   "llm <llm-call-id>",
	Short: "Trace one LLM round-trip and the tool calls it emitted",
	Args:  cobra.ExactArgs(1),
	RunE:  runTraceLLM,
}

var traceSessionCmd = &cobra.Command{
	Use:   "session <session-id>",
	Short: "Trace all activity in a session",
	Args:  cobra.ExactArgs(1),
	RunE:  runTraceSession,
}

// --- command runners ---

func runTraceCorrelation(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	corrID := strings.TrimSpace(args[0])
	if corrID == "" {
		return fmt.Errorf("correlation-id is required")
	}

	entries, err := collectEntries(traceSessionDirResolved())
	if err != nil {
		return err
	}

	// Follow the correlation ID directly, plus any tool_call_key that
	// belongs to a tool whose start record had this correlation. This
	// catches child tool calls whose records carry the parent's CID.
	filter := func(e *traceEntry) bool {
		if e.CorrelationID == corrID {
			return true
		}
		return false
	}

	return renderFiltered(cmd.OutOrStdout(), entries, filter)
}

func runTraceTool(cmd *cobra.Command, args []string) error {
	key := strings.TrimSpace(args[0])
	if key == "" {
		return fmt.Errorf("tool-call-key is required")
	}

	entries, err := collectEntries(traceSessionDirResolved())
	if err != nil {
		return err
	}

	// Include any entry that IS this tool call, its children (parent
	// pointer match), or an event tagged with this tool_call_key.
	filter := func(e *traceEntry) bool {
		if e.ToolCallKey == key {
			return true
		}
		if e.ParentToolCallKey == key {
			return true
		}
		return false
	}

	return renderFiltered(cmd.OutOrStdout(), entries, filter)
}

func runTraceLLM(cmd *cobra.Command, args []string) error {
	id := strings.TrimSpace(args[0])
	if id == "" {
		return fmt.Errorf("llm-call-id is required")
	}

	entries, err := collectEntries(traceSessionDirResolved())
	if err != nil {
		return err
	}

	filter := func(e *traceEntry) bool {
		return e.LLMCallID == id
	}

	return renderFiltered(cmd.OutOrStdout(), entries, filter)
}

func runTraceSession(cmd *cobra.Command, args []string) error {
	sid := strings.TrimSpace(args[0])
	if sid == "" {
		return fmt.Errorf("session-id is required")
	}

	entries, err := collectEntries(traceSessionDirResolved())
	if err != nil {
		return err
	}

	filter := func(e *traceEntry) bool {
		return e.SessionID == sid
	}

	return renderFiltered(cmd.OutOrStdout(), entries, filter)
}

// --- core machinery ---

// traceEntry is the unified shape the CLI reasons over. Fields are
// populated from one of three record shapes (events, tools, llm);
// unset fields remain zero-valued.
type traceEntry struct {
	Timestamp         time.Time
	Stream            string // "events" | "tools" | "llm"
	Kind              string // within-stream classifier ("tool-start", "llm-complete", etc.)
	Level             string
	SessionID         string
	AgentType         string
	AgentID           string
	PipelineID        string
	ToolName          string
	CorrelationID     string
	ToolCallKey       string
	ParentToolCallKey string
	LLMCallID         string
	DurationMs        int64
	Success           bool
	SuccessKnown      bool
	Error             string
	Summary           string // one-line human-readable
	RawPath           string
}

// traceSessionDirResolved returns the configured session directory
// (from --session-dir) or falls back to cwd/.sylk/sessions.
func traceSessionDirResolved() string {
	if strings.TrimSpace(traceSessionDir) != "" {
		return traceSessionDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Join(".sylk", "sessions")
	}
	return filepath.Join(cwd, ".sylk", "sessions")
}

// collectEntries walks baseDir for every JSONL file that belongs to
// the new per-day logging tree, parses each line as a traceEntry, and
// returns the combined set sorted by timestamp.
//
// The walker intentionally tolerates missing directories (no session
// logs yet) and malformed JSON lines (skips them) so one bad record
// can't block reconstruction of the rest.
func collectEntries(baseDir string) ([]*traceEntry, error) {
	info, err := os.Stat(baseDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("trace: session directory not found: %s", baseDir)
	}

	sinceTS, err := parseTimeFlag(traceSince)
	if err != nil {
		return nil, fmt.Errorf("--since: %w", err)
	}
	untilTS, err := parseTimeFlag(traceUntil)
	if err != nil {
		return nil, fmt.Errorf("--until: %w", err)
	}

	var all []*traceEntry
	walkErr := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, _ error) error {
		if d == nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		stream, ok := streamForFilename(name)
		if !ok {
			return nil
		}
		if traceAgent != "" && !strings.Contains(path, string(os.PathSeparator)+"agents"+string(os.PathSeparator)+traceAgent+string(os.PathSeparator)) {
			return nil
		}

		entries, err := parseLogFile(path, stream)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if tracePipeline != "" && e.PipelineID != tracePipeline {
				continue
			}
			if !sinceTS.IsZero() && e.Timestamp.Before(sinceTS) {
				continue
			}
			if !untilTS.IsZero() && !e.Timestamp.Before(untilTS) {
				continue
			}
			all = append(all, e)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})
	return all, nil
}

// streamForFilename classifies a JSONL filename into its stream. Returns
// false for files we don't recognize (WAL, activity.jsonl, etc.).
func streamForFilename(name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, "events."):
		return "events", true
	case strings.HasPrefix(name, "tools."):
		return "tools", true
	case strings.HasPrefix(name, "llm."):
		return "llm", true
	case strings.HasPrefix(name, "bus."):
		return "bus", true
	}
	return "", false
}

// parseLogFile streams the given JSONL file and converts each record
// into a traceEntry. The parsing strategy depends on stream type —
// each has its own field conventions.
func parseLogFile(path, stream string) ([]*traceEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var out []*traceEntry
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		entry := parseRawRecord(raw, stream, path)
		if entry != nil {
			out = append(out, entry)
		}
	}
	return out, nil
}

// parseRawRecord converts a generic JSON map into a traceEntry. The
// mapping is stream-aware: events records carry different fields than
// tools or llm records, and the summary text is derived differently
// for each.
func parseRawRecord(raw map[string]any, stream, path string) *traceEntry {
	e := &traceEntry{Stream: stream, RawPath: path}
	if ts, ok := raw["ts"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			e.Timestamp = t
		}
	}
	e.Level = stringField(raw, "level")
	e.SessionID = stringField(raw, "session_id")
	e.AgentType = firstString(raw, "agent_type", "agent")
	e.AgentID = stringField(raw, "agent_id")
	e.PipelineID = stringField(raw, "pipeline_id")
	e.CorrelationID = firstString(raw, "correlation_id", "corr_id")
	e.ToolCallKey = stringField(raw, "tool_call_key")
	e.ParentToolCallKey = stringField(raw, "parent_tool_call_key")
	e.LLMCallID = stringField(raw, "llm_call_id")
	e.ToolName = stringField(raw, "tool_name")

	switch stream {
	case "events":
		e.Kind = firstString(raw, "event", "event_code")
		e.Summary = firstString(raw, "msg", "event")
		if errStr := stringField(raw, "err"); errStr != "" {
			e.Error = errStr
		}
	case "tools":
		phase := stringField(raw, "phase")
		e.Kind = "tool-" + phase
		if phase == "complete" {
			e.DurationMs = int64Field(raw, "duration_ms")
			if v, ok := raw["success"].(bool); ok {
				e.Success = v
				e.SuccessKnown = true
			}
			e.Error = stringField(raw, "error")
		}
		e.Summary = toolSummary(raw, phase)
	case "llm":
		e.Kind = stringField(raw, "event")
		if e.Kind == "" {
			e.Kind = "llm"
		}
		if e.Kind == "llm_complete" {
			e.DurationMs = int64Field(raw, "duration_ms")
			e.Error = stringField(raw, "error")
		}
		e.Summary = llmSummary(raw)
	case "bus":
		e.Kind = "bus"
		e.Summary = firstString(raw, "type", "topic")
	}
	return e
}

func toolSummary(raw map[string]any, phase string) string {
	name := stringField(raw, "tool_name")
	if phase == "complete" {
		ok := "ok"
		if v, _ := raw["success"].(bool); !v {
			ok = "fail"
		}
		ms := int64Field(raw, "duration_ms")
		if errStr := stringField(raw, "error"); errStr != "" {
			return fmt.Sprintf("%s %s %dms — %s", name, ok, ms, errStr)
		}
		return fmt.Sprintf("%s %s %dms", name, ok, ms)
	}
	targets := ""
	if m, ok := raw["inter_agent"].(map[string]any); ok {
		if list, ok := m["agent_types"].([]any); ok && len(list) > 0 {
			vals := make([]string, 0, len(list))
			for _, v := range list {
				if s, ok := v.(string); ok {
					vals = append(vals, s)
				}
			}
			if len(vals) > 0 {
				targets = " → " + strings.Join(vals, ",")
			}
		}
	}
	return name + targets
}

func llmSummary(raw map[string]any) string {
	switch stringField(raw, "event") {
	case "llm_start":
		model := stringField(raw, "model")
		count := int64Field(raw, "tool_definition_count")
		if count > 0 {
			return fmt.Sprintf("%s (%d tools)", model, count)
		}
		return model
	case "llm_complete":
		usage := ""
		if u, ok := raw["usage"].(map[string]any); ok {
			in := int64FromAny(u["input_tokens"])
			out := int64FromAny(u["output_tokens"])
			if in > 0 || out > 0 {
				usage = fmt.Sprintf(" %d in / %d out", in, out)
			}
		}
		ms := int64Field(raw, "duration_ms")
		return fmt.Sprintf("%dms%s", ms, usage)
	}
	return stringField(raw, "event")
}

// renderFiltered writes the indented timeline for entries that pass
// the predicate. Indentation is keyed off nesting depth in the
// parent-tool-call chain: each tool_call_key resolves to its depth
// in the parent graph formed by the full unfiltered entry set.
func renderFiltered(w io.Writer, all []*traceEntry, filter func(*traceEntry) bool) error {
	if len(all) == 0 {
		_, err := fmt.Fprintln(w, "(no log entries found)")
		return err
	}
	depth := computeDepths(all)
	for _, e := range all {
		if !filter(e) {
			continue
		}
		renderEntry(w, e, depth[e.ToolCallKey])
	}
	return nil
}

// computeDepths walks the parent-tool-call graph (inferred from tool
// start records) and returns a map tool_call_key → nesting depth.
// Tools with no parent are depth 0; children are depth parent+1.
func computeDepths(all []*traceEntry) map[string]int {
	parent := make(map[string]string)
	for _, e := range all {
		if e.Stream == "tools" && e.Kind == "tool-start" && e.ToolCallKey != "" {
			if e.ParentToolCallKey != "" {
				parent[e.ToolCallKey] = e.ParentToolCallKey
			} else if _, ok := parent[e.ToolCallKey]; !ok {
				parent[e.ToolCallKey] = ""
			}
		}
	}
	depths := make(map[string]int, len(parent))
	var resolve func(k string) int
	resolve = func(k string) int {
		if k == "" {
			return 0
		}
		if d, ok := depths[k]; ok {
			return d
		}
		p := parent[k]
		d := 0
		if p != "" {
			d = resolve(p) + 1
		}
		depths[k] = d
		return d
	}
	for k := range parent {
		resolve(k)
	}
	return depths
}

// renderEntry writes one timeline row: timestamp, agent, kind, the
// short identifier (tool_call_key or llm_call_id), and the summary.
// Depth controls indentation of the agent column so nested tool
// calls visibly nest under their parents.
func renderEntry(w io.Writer, e *traceEntry, depth int) {
	indent := strings.Repeat("  ", depth)
	ts := e.Timestamp.Format("15:04:05.000")
	agent := firstNonEmpty(e.AgentID, e.AgentType)
	id := firstNonEmpty(e.ToolCallKey, e.LLMCallID)
	level := ""
	if e.Stream == "events" && e.Level != "" && e.Level != "info" {
		level = "[" + strings.ToUpper(e.Level) + "] "
	}
	dur := ""
	if e.DurationMs > 0 {
		dur = fmt.Sprintf(" %dms", e.DurationMs)
	}
	status := ""
	if e.Stream == "tools" && e.Kind == "tool-complete" && e.SuccessKnown && !e.Success {
		status = " [FAIL]"
	}
	fmt.Fprintf(w, "%s  %s%-24s %-16s %-24s %s%s%s\n",
		ts,
		indent,
		truncate(agent, 24),
		e.Kind,
		truncate(id, 24),
		level+e.Summary,
		dur,
		status,
	)
	if e.Error != "" && e.Stream != "tools" {
		fmt.Fprintf(w, "%s  %s    %s\n", ts, indent, e.Error)
	}
}

// --- small helpers ---

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := stringField(m, k); v != "" {
			return v
		}
	}
	return ""
}

func int64Field(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		return int64FromAny(v)
	}
	return 0
}

func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func parseTimeFlag(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

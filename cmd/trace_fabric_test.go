package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/fabriclog"
)

// seedFabricLog writes a minimal set of synthetic records into a
// temporary session dir so the runTraceFabric* functions have
// something to parse. The caller configures --session-dir at
// runtime via the traceFabricSession + traceSessionDir package
// vars.
func seedFabricLog(t *testing.T, sessionID string, records []fabriclog.FabricLogRecord) string {
	t.Helper()
	root := t.TempDir()
	fabricDir := filepath.Join(root, sessionID, "fabric", "2026-04-21")
	if err := os.MkdirAll(fabricDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(filepath.Join(fabricDir, "fabric.000.jsonl"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return root
}

func makePublishRecord(seq int64, id, action, scope, agentID, agentType string, ts time.Time) fabriclog.FabricLogRecord {
	return fabriclog.FabricLogRecord{
		Kind:      fabriclog.KindPublish,
		SessionID: "sess-trace-test",
		Seq:       seq,
		Timestamp: ts,
		Agent: fabriclog.AgentRef{
			AgentID:   agentID,
			AgentType: agentType,
		},
		Publish: &fabriclog.PublishBody{
			ActivityID:   id,
			ActionKind:   action,
			Scope:        scope,
			PayloadBytes: 128,
			Activity:     json.RawMessage(`{}`),
		},
	}
}

func makeConsumeRecord(seq, readerSeq int64, id, readerID string, ts time.Time) fabriclog.FabricLogRecord {
	return fabriclog.FabricLogRecord{
		Kind:      fabriclog.KindConsume,
		SessionID: "sess-trace-test",
		Seq:       seq,
		Timestamp: ts,
		Agent: fabriclog.AgentRef{
			AgentID:   readerID,
			AgentType: "inspector",
		},
		Consume: &fabriclog.ConsumeBody{
			ReaderRecordSeq: readerSeq,
			ActivityID:      id,
			LatencyMicros:   250_000,
		},
	}
}

func makeResolveRecord(seq int64, resolverID, resolvedID, resolverKind string, ts time.Time) fabriclog.FabricLogRecord {
	return fabriclog.FabricLogRecord{
		Kind:      fabriclog.KindResolve,
		SessionID: "sess-trace-test",
		Seq:       seq,
		Timestamp: ts,
		Agent: fabriclog.AgentRef{
			AgentID:   "resolver-agent",
			AgentType: "engineer",
		},
		Resolve: &fabriclog.ResolveBody{
			ResolverActivityID: resolverID,
			ResolverKind:       resolverKind,
			ResolvedActivityID: resolvedID,
			LatencyMicros:      500_000,
		},
	}
}

func runFabricCmd(t *testing.T, fn func(*bytes.Buffer) error) string {
	t.Helper()
	var out bytes.Buffer
	if err := fn(&out); err != nil {
		t.Fatalf("cmd error: %v", err)
	}
	return out.String()
}

func TestTraceFabricSummary_CountsAndTopN(t *testing.T) {
	ts := time.Now().Add(-time.Minute)
	records := []fabriclog.FabricLogRecord{
		makePublishRecord(1, "act-1", "decision_declared", "services/billing", "engineer-1", "engineer", ts),
		makePublishRecord(2, "act-2", "decision_declared", "services/billing", "engineer-1", "engineer", ts),
		makePublishRecord(3, "act-3", "consult_emitted", "services/auth", "engineer-2", "engineer", ts),
		makeConsumeRecord(4, 0, "act-1", "librarian-1", ts),
	}
	root := seedFabricLog(t, "sess-trace-test", records)

	prevDir := traceSessionDir
	prevSid := traceFabricSession
	traceSessionDir = root
	traceFabricSession = "sess-trace-test"
	t.Cleanup(func() {
		traceSessionDir = prevDir
		traceFabricSession = prevSid
	})

	out := runFabricCmd(t, func(w *bytes.Buffer) error {
		cmd := traceFabricSummaryCmd
		cmd.SetOut(w)
		return runTraceFabricSummary(cmd, nil)
	})

	for _, want := range []string{
		"records             : 4",
		"fabric_publish   3",
		"fabric_consume   1",
		"top action kinds:",
		"decision_declared",
		"consult_emitted",
		"top scopes:",
		"services/billing",
		"top publishers:",
		"engineer/engineer-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary output missing %q; output:\n%s", want, out)
		}
	}
}

func TestTraceFabricUnread_IdentifiesUnconsumed(t *testing.T) {
	ts := time.Now().Add(-5 * time.Minute)
	records := []fabriclog.FabricLogRecord{
		makePublishRecord(1, "consumed", "decision_declared", "s/a", "e1", "engineer", ts),
		makePublishRecord(2, "orphan", "decision_declared", "s/b", "e1", "engineer", ts),
		makeConsumeRecord(3, 0, "consumed", "inspector-1", ts),
	}
	root := seedFabricLog(t, "sess-trace-test", records)
	traceSessionDir = root
	traceFabricSession = "sess-trace-test"

	out := runFabricCmd(t, func(w *bytes.Buffer) error {
		cmd := traceFabricUnreadCmd
		cmd.SetOut(w)
		return runTraceFabricUnread(cmd, nil)
	})

	if !strings.Contains(out, "Unread publications: 1 of 2") {
		t.Errorf("unread count missing; output:\n%s", out)
	}
	if !strings.Contains(out, "orphan") {
		t.Errorf("orphan id missing; output:\n%s", out)
	}
	if strings.Contains(out, "\nconsumed") {
		t.Errorf("consumed id should not appear in unread; output:\n%s", out)
	}
}

func TestTraceFabricLifecycles_PairsEmitWithResolve(t *testing.T) {
	ts := time.Now().Add(-10 * time.Minute)
	records := []fabriclog.FabricLogRecord{
		makePublishRecord(1, "c1", "consult_emitted", "s/a", "eng-1", "engineer", ts),
		makePublishRecord(2, "c2", "challenge_emitted", "s/b", "eng-1", "engineer", ts),
		// resolver activity for c1
		{
			Kind: fabriclog.KindPublish,
			Seq:  3,
			Agent: fabriclog.AgentRef{
				AgentID:   "lib-1",
				AgentType: "librarian",
			},
			Timestamp: ts.Add(time.Second),
			Publish: &fabriclog.PublishBody{
				ActivityID: "c1-response",
				ActionKind: "consult_response",
				Resolves:   "c1",
				Activity:   json.RawMessage(`{}`),
			},
		},
		makeResolveRecord(4, "c1-response", "c1", "consult_response", ts.Add(time.Second)),
	}
	root := seedFabricLog(t, "sess-trace-test", records)
	traceSessionDir = root
	traceFabricSession = "sess-trace-test"

	out := runFabricCmd(t, func(w *bytes.Buffer) error {
		cmd := traceFabricLifecyclesCmd
		cmd.SetOut(w)
		return runTraceFabricLifecycles(cmd, nil)
	})

	if !strings.Contains(out, "2 total, 1 resolved, 1 pending") {
		t.Errorf("lifecycle counts wrong; output:\n%s", out)
	}
	if !strings.Contains(out, "consult_emitted") || !strings.Contains(out, "challenge_emitted") {
		t.Errorf("lifecycle kinds missing; output:\n%s", out)
	}
	// c1 should be resolved, c2 should be pending.
	lines := strings.Split(out, "\n")
	foundResolved := false
	foundPending := false
	for _, line := range lines {
		if strings.Contains(line, "c1") && strings.Contains(line, "ok") {
			foundResolved = true
		}
		if strings.Contains(line, "c2") && strings.Contains(line, "pending") {
			foundPending = true
		}
	}
	if !foundResolved {
		t.Errorf("c1 should be marked resolved; output:\n%s", out)
	}
	if !foundPending {
		t.Errorf("c2 should be marked pending; output:\n%s", out)
	}
}

func TestTraceFabricFollow_FullChain(t *testing.T) {
	ts := time.Now()
	records := []fabriclog.FabricLogRecord{
		makePublishRecord(1, "target", "decision_declared", "s/a", "eng-1", "engineer", ts),
		makeConsumeRecord(2, 0, "target", "inspector-1", ts.Add(500*time.Millisecond)),
		makeResolveRecord(3, "resolver-act", "target", "decision_promoted", ts.Add(time.Second)),
	}
	root := seedFabricLog(t, "sess-trace-test", records)
	traceSessionDir = root
	traceFabricSession = "sess-trace-test"

	out := runFabricCmd(t, func(w *bytes.Buffer) error {
		cmd := traceFabricFollowCmd
		cmd.SetOut(w)
		return runTraceFabricFollow(cmd, []string{"target"})
	})

	for _, want := range []string{
		"Activity target — 3 touchpoints",
		"publish",
		"consume",
		"resolve",
		"decision_declared",
		"publish→consume",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("follow output missing %q; output:\n%s", want, out)
		}
	}
}

func TestTraceFabricScopes_AggregatesByScope(t *testing.T) {
	ts := time.Now()
	records := []fabriclog.FabricLogRecord{
		makePublishRecord(1, "a1", "decision_declared", "services/billing", "eng-1", "engineer", ts),
		makePublishRecord(2, "a2", "decision_declared", "services/billing", "eng-2", "engineer", ts),
		makePublishRecord(3, "a3", "decision_declared", "services/auth", "eng-1", "engineer", ts),
		makeConsumeRecord(4, 0, "a1", "librarian-1", ts),
		makeConsumeRecord(5, 0, "a1", "librarian-2", ts),
	}
	root := seedFabricLog(t, "sess-trace-test", records)
	traceSessionDir = root
	traceFabricSession = "sess-trace-test"

	out := runFabricCmd(t, func(w *bytes.Buffer) error {
		cmd := traceFabricScopesCmd
		cmd.SetOut(w)
		return runTraceFabricScopes(cmd, nil)
	})

	if !strings.Contains(out, "services/billing") {
		t.Errorf("services/billing missing; output:\n%s", out)
	}
	if !strings.Contains(out, "services/auth") {
		t.Errorf("services/auth missing; output:\n%s", out)
	}
	// services/billing has 2 pubs + 2 cons = 4 activity, should rank first.
	lines := strings.Split(out, "\n")
	billingIdx := -1
	authIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "services/billing") {
			billingIdx = i
		}
		if strings.Contains(line, "services/auth") {
			authIdx = i
		}
	}
	if billingIdx < 0 || authIdx < 0 || billingIdx > authIdx {
		t.Errorf("services/billing should rank before services/auth (higher activity); billing=%d auth=%d; output:\n%s", billingIdx, authIdx, out)
	}
}

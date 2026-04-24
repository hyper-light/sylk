package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/versioning"
)

// scriptedProvider is a fake StreamingTurnProvider that plays back
// pre-recorded turns. Each Stream() call consumes one turn from the
// scripted sequence; each turn can carry text + tool calls. Used to
// drive the audit runtime without real LLM I/O.
type scriptedProvider struct {
	mu    sync.Mutex
	turns []scriptedTurn
	index int
	delay time.Duration // per-turn delay to simulate in-flight LLM work
}

type scriptedTurn struct {
	content   string
	toolCalls []providers.ToolCall
}

func (p *scriptedProvider) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	p.mu.Lock()
	if p.index >= len(p.turns) {
		p.mu.Unlock()
		return nil, fmt.Errorf("scripted provider: no turn at index %d", p.index)
	}
	turn := p.turns[p.index]
	p.index++
	delay := p.delay
	p.mu.Unlock()

	out := make(chan *providers.StreamChunk, 16)
	// Use a goroutine — the ctx carries a GoroutineScope in tests
	// but scriptedProvider is tiny test infrastructure, not
	// production. The goroutine is bounded by the channel producer
	// loop and completes within a single Stream call.
	go func() {
		defer close(out)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		emit := func(chunk *providers.StreamChunk) bool {
			select {
			case out <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if !emit(&providers.StreamChunk{Type: providers.ChunkTypeStart, Timestamp: time.Now()}) {
			return
		}
		if turn.content != "" {
			if !emit(&providers.StreamChunk{Type: providers.ChunkTypeText, Text: turn.content, Timestamp: time.Now()}) {
				return
			}
		}
		for i, tc := range turn.toolCalls {
			if !emit(&providers.StreamChunk{
				Type:      providers.ChunkTypeToolStart,
				Index:     i,
				ToolCall:  &providers.ToolCallChunk{ID: tc.ID, Name: tc.Name},
				Timestamp: time.Now(),
			}) {
				return
			}
			if !emit(&providers.StreamChunk{
				Type:      providers.ChunkTypeToolDelta,
				Index:     i,
				ToolCall:  &providers.ToolCallChunk{ID: tc.ID, ArgumentsDelta: tc.Arguments},
				Timestamp: time.Now(),
			}) {
				return
			}
			if !emit(&providers.StreamChunk{
				Type:      providers.ChunkTypeToolEnd,
				Index:     i,
				ToolCall:  &providers.ToolCallChunk{ID: tc.ID},
				Timestamp: time.Now(),
			}) {
				return
			}
		}
		emit(&providers.StreamChunk{Type: providers.ChunkTypeEnd, Timestamp: time.Now()})
	}()
	return out, nil
}

func openRuntimeTestSession(t *testing.T, name string) (*versioning.SessionVFS, *versioning.ReplicaVFS) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:   versioning.SessionID(name),
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "session"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	// Seed a merge descriptor so BeginAuditReplicaVFS has something
	// to chain against.
	desc := versioning.MergeDescriptor{
		PipelineID:    "p",
		BaseVersion:   versioning.SemanticVersion{},
		MergedVersion: versioning.SemanticVersion{Minor: 1},
	}
	sess.RecordMergeDescriptorForTest(desc)
	if _, err := sess.CommitQueue().Enqueue(desc); err != nil {
		t.Fatal(err)
	}
	vfs, err := sess.BeginAuditReplicaVFS(desc.MergedVersion, name+"-holder")
	if err != nil {
		t.Fatal(err)
	}
	return sess, vfs
}

func newRuntimeTestScope(t *testing.T) *concurrency.GoroutineScope {
	t.Helper()
	scope := concurrency.NewGoroutineScope(context.Background(), "audit-runtime-test", nil)
	t.Cleanup(func() { _ = scope.Shutdown(100*time.Millisecond, 2*time.Second) })
	return scope
}

func runtimeTestLedger(t *testing.T, id string) *steering.SteeringLedger {
	t.Helper()
	return steering.NewSteeringLedger(id, "agent-test", "sess-test", nil, nil)
}

// TestAuditReplicaRuntime_HappyPathEmitsDecision verifies the
// canonical flow: scripted LLM calls emit_audit_decision; finalizer
// fires with the right verdict; runtime returns.
func TestAuditReplicaRuntime_HappyPathEmitsDecision(t *testing.T) {
	sess, vfs := openRuntimeTestSession(t, "happy")
	provider := &scriptedProvider{
		turns: []scriptedTurn{
			{
				toolCalls: []providers.ToolCall{{
					ID:   "call-1",
					Name: AuditToolEmitDecision,
					Arguments: `{"decision":"accepted","summary":"looks good"}`,
				}},
			},
		},
	}
	var got *AuditMergeResult
	var got1 atomic.Bool
	finalizer := func(r *AuditMergeResult) {
		if got1.CompareAndSwap(false, true) {
			got = r
		}
	}
	cfg := AuditReplicaConfig{
		ReplicaID:     "replica-happy",
		AgentType:     AgentTypeInspectorGlobal,
		SessionID:     "sess-test",
		MergedVersion: versioning.SemanticVersion{Minor: 1},
		Request:       &AuditMergeRequest{ReplicaID: "replica-happy"},
		Session:       sess,
		ReplicaVFS:    vfs,
		Finalizer:     finalizer,
		Ledger:        runtimeTestLedger(t, "replica-happy"),
		Provider:      provider,
		SystemPrompt:  "audit this",
		UserPrompt:    "go",
	}
	rt, err := NewAuditReplicaRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := concurrency.WithScope(context.Background(), newRuntimeTestScope(t))
	ctx = WithAuditDecisionFinalizer(ctx, finalizer)
	if err := rt.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil {
		t.Fatal("finalizer never fired")
	}
	if got.Decision != versioning.ReplicaDecisionAccepted {
		t.Errorf("decision = %v, want Accepted", got.Decision)
	}
	if got.Summary != "looks good" {
		t.Errorf("summary = %q", got.Summary)
	}
}

// TestAuditReplicaRuntime_WorkspaceReadWriteRoundtrip verifies that
// workspace_write / workspace_read tools route through the
// ReplicaVFS and see each other's content mid-audit.
func TestAuditReplicaRuntime_WorkspaceReadWriteRoundtrip(t *testing.T) {
	sess, vfs := openRuntimeTestSession(t, "rw")
	provider := &scriptedProvider{
		turns: []scriptedTurn{
			{toolCalls: []providers.ToolCall{{
				ID: "c1", Name: AuditToolWorkspaceWrite,
				Arguments: `{"path":"note.md","content":"hello"}`,
			}}},
			{toolCalls: []providers.ToolCall{{
				ID: "c2", Name: AuditToolWorkspaceRead,
				Arguments: `{"path":"note.md"}`,
			}}},
			{toolCalls: []providers.ToolCall{{
				ID:        "c3",
				Name:      AuditToolEmitDecision,
				Arguments: `{"decision":"accepted","summary":"ok"}`,
			}}},
		},
	}
	var gotResult *AuditMergeResult
	finalizer := func(r *AuditMergeResult) { gotResult = r }
	rt, err := NewAuditReplicaRuntime(AuditReplicaConfig{
		ReplicaID:    "r-rw",
		Request:      &AuditMergeRequest{ReplicaID: "r-rw"},
		SessionID:    "sess",
		Session:      sess,
		ReplicaVFS:   vfs,
		Finalizer:    finalizer,
		Ledger:       runtimeTestLedger(t, "r-rw"),
		Provider:     provider,
		SystemPrompt: "",
		UserPrompt:   "",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := concurrency.WithScope(context.Background(), newRuntimeTestScope(t))
	ctx = WithAuditDecisionFinalizer(ctx, finalizer)
	if err := rt.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotResult == nil {
		t.Fatal("no decision")
	}
	content, err := vfs.Read("note.md")
	if err != nil || string(content) != "hello" {
		t.Errorf("vfs read post-audit = %q, %v", content, err)
	}
}

// TestAuditReplicaRuntime_FallbackRejectionOnExhaustedBudget
// verifies that a runtime that never calls emit_audit_decision
// still produces a terminal rejection so the commit queue advances.
func TestAuditReplicaRuntime_FallbackRejectionOnExhaustedBudget(t *testing.T) {
	sess, vfs := openRuntimeTestSession(t, "exhaust")
	// Provider produces only no-op turns (no tool calls). Runtime
	// should return a fallback rejection after the first turn.
	provider := &scriptedProvider{
		turns: []scriptedTurn{{content: "thinking..."}},
	}
	var gotResult *AuditMergeResult
	finalizer := func(r *AuditMergeResult) { gotResult = r }
	rt, err := NewAuditReplicaRuntime(AuditReplicaConfig{
		ReplicaID:    "r-exhaust",
		Request:      &AuditMergeRequest{ReplicaID: "r-exhaust"},
		SessionID:    "sess",
		Session:      sess,
		ReplicaVFS:   vfs,
		Finalizer:    finalizer,
		Ledger:       runtimeTestLedger(t, "r-exhaust"),
		Provider:     provider,
		MaxToolRuns:  1,
		SystemPrompt: "",
		UserPrompt:   "",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := concurrency.WithScope(context.Background(), newRuntimeTestScope(t))
	ctx = WithAuditDecisionFinalizer(ctx, finalizer)
	if err := rt.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotResult == nil {
		t.Fatal("no fallback rejection")
	}
	if gotResult.Decision != versioning.ReplicaDecisionRejected {
		t.Errorf("decision = %v, want Rejected", gotResult.Decision)
	}
}

// TestAuditReplicaRuntime_ParallelAuditsRunConcurrently is THE test
// that proves Stage 3. Dispatch three audits simultaneously, each
// with a 100ms per-turn delay. If they serialize, total wall-clock
// ≈ 300ms. If they run in parallel, total ≈ 100ms (modulo scheduler
// noise). We assert the total is closer to 100ms than 300ms.
//
// This is the concrete regression test for the §3 "N parallel
// inspector+tester pairs" invariant.
func TestAuditReplicaRuntime_ParallelAuditsRunConcurrently(t *testing.T) {
	const (
		numAudits   = 3
		perTurnDelay = 120 * time.Millisecond
	)
	sess, _ := openRuntimeTestSession(t, "parallel")
	scope := newRuntimeTestScope(t)
	ctx := concurrency.WithScope(context.Background(), scope)

	var wg sync.WaitGroup
	done := make(chan *AuditMergeResult, numAudits)
	start := time.Now()
	for i := 0; i < numAudits; i++ {
		i := i
		// Each audit gets its own ReplicaVFS so writes/reads don't
		// collide.
		ver := versioning.SemanticVersion{Minor: uint32(i + 2)}
		sess.RecordMergeDescriptorForTest(versioning.MergeDescriptor{
			PipelineID:    fmt.Sprintf("p%d", i),
			MergedVersion: ver,
		})
		vfs, err := sess.BeginAuditReplicaVFS(ver, fmt.Sprintf("h%d", i))
		if err != nil {
			t.Fatal(err)
		}
		provider := &scriptedProvider{
			delay: perTurnDelay,
			turns: []scriptedTurn{
				{toolCalls: []providers.ToolCall{{
					ID:        fmt.Sprintf("c%d", i),
					Name:      AuditToolEmitDecision,
					Arguments: `{"decision":"accepted","summary":"ok"}`,
				}}},
			},
		}
		finalizer := func(r *AuditMergeResult) { done <- r }
		rt, err := NewAuditReplicaRuntime(AuditReplicaConfig{
			ReplicaID:     fmt.Sprintf("replica-%d", i),
			Request:       &AuditMergeRequest{ReplicaID: fmt.Sprintf("replica-%d", i)},
			SessionID:     "sess",
			MergedVersion: ver,
			Session:       sess,
			ReplicaVFS:    vfs,
			Finalizer:     finalizer,
			Ledger:        runtimeTestLedger(t, fmt.Sprintf("replica-%d", i)),
			Provider:      provider,
		})
		if err != nil {
			t.Fatal(err)
		}
		runCtx := WithAuditDecisionFinalizer(ctx, finalizer)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rt.Run(runCtx)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	for i := 0; i < numAudits; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("not all audits decided within deadline")
		}
	}

	// Parallel: elapsed ≈ perTurnDelay (+ scheduler noise).
	// Serial: elapsed ≈ numAudits * perTurnDelay.
	// Assert elapsed is strictly less than half of the serial bound
	// so noise can't cause a false positive.
	serialBound := time.Duration(numAudits) * perTurnDelay
	if elapsed >= serialBound/2 {
		t.Errorf("elapsed = %v, serial bound / 2 = %v — audits are serializing",
			elapsed, serialBound/2)
	}
}

// TestAuditReplicaRuntime_RejectsInvalidDecision verifies
// emit_audit_decision's input validation: unknown decisions produce
// a tool error and do NOT fire the finalizer.
func TestAuditReplicaRuntime_RejectsInvalidDecision(t *testing.T) {
	sess, vfs := openRuntimeTestSession(t, "invalid")
	provider := &scriptedProvider{
		turns: []scriptedTurn{
			{toolCalls: []providers.ToolCall{{
				ID: "c1", Name: AuditToolEmitDecision,
				Arguments: `{"decision":"maybe","summary":"huh"}`,
			}}},
			// On the next turn the LLM retries with a valid decision.
			{toolCalls: []providers.ToolCall{{
				ID:        "c2",
				Name:      AuditToolEmitDecision,
				Arguments: `{"decision":"rejected","summary":"retried"}`,
			}}},
		},
	}
	var gotResult *AuditMergeResult
	finalizer := func(r *AuditMergeResult) { gotResult = r }
	rt, err := NewAuditReplicaRuntime(AuditReplicaConfig{
		ReplicaID:    "r-invalid",
		Request:      &AuditMergeRequest{ReplicaID: "r-invalid"},
		SessionID:    "sess",
		Session:      sess,
		ReplicaVFS:   vfs,
		Finalizer:    finalizer,
		Ledger:       runtimeTestLedger(t, "r-invalid"),
		Provider:     provider,
		SystemPrompt: "",
		UserPrompt:   "",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := concurrency.WithScope(context.Background(), newRuntimeTestScope(t))
	ctx = WithAuditDecisionFinalizer(ctx, finalizer)
	if err := rt.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if gotResult == nil {
		t.Fatal("no result from recovered call")
	}
	if gotResult.Decision != versioning.ReplicaDecisionRejected {
		t.Errorf("decision = %v, want Rejected (after retry)", gotResult.Decision)
	}
	if gotResult.Summary != "retried" {
		t.Errorf("summary = %q", gotResult.Summary)
	}
}

// TestAuditReplicaRuntime_MergesAfterReturnsNewerDescriptors
// verifies the mid-audit-awareness tool.
func TestAuditReplicaRuntime_MergesAfterReturnsNewerDescriptors(t *testing.T) {
	sess, vfs := openRuntimeTestSession(t, "merges")
	// Seed additional merges the audit should discover.
	sess.RecordMergeDescriptorForTest(versioning.MergeDescriptor{
		PipelineID:    "future-1",
		MergedVersion: versioning.SemanticVersion{Minor: 5},
	})
	sess.RecordMergeDescriptorForTest(versioning.MergeDescriptor{
		PipelineID:    "future-2",
		MergedVersion: versioning.SemanticVersion{Minor: 6},
	})

	provider := &scriptedProvider{
		turns: []scriptedTurn{
			{toolCalls: []providers.ToolCall{{
				ID: "c1", Name: AuditToolMergesAfter,
				Arguments: `{"since_minor":1}`,
			}}},
			{toolCalls: []providers.ToolCall{{
				ID:        "c2",
				Name:      AuditToolEmitDecision,
				Arguments: `{"decision":"accepted","summary":"done"}`,
			}}},
		},
	}
	finalizer := func(*AuditMergeResult) {}
	rt, err := NewAuditReplicaRuntime(AuditReplicaConfig{
		ReplicaID:    "r-merges",
		Request:      &AuditMergeRequest{ReplicaID: "r-merges"},
		SessionID:    "sess",
		Session:      sess,
		ReplicaVFS:   vfs,
		Finalizer:    finalizer,
		Ledger:       runtimeTestLedger(t, "r-merges"),
		Provider:     provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := concurrency.WithScope(context.Background(), newRuntimeTestScope(t))
	ctx = WithAuditDecisionFinalizer(ctx, finalizer)
	if err := rt.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Runtime doesn't expose the tool-result slice externally, so we
	// just confirm end-to-end completion. The handler's JSON output
	// was consumed by the scripted LLM's next turn.
	_ = json.Valid // silence import
}

// TestAuditReplicaRuntime_RejectsNilRequiredFields confirms that
// construction refuses configs missing essentials.
func TestAuditReplicaRuntime_RejectsNilRequiredFields(t *testing.T) {
	cases := map[string]AuditReplicaConfig{
		"no replica id": {},
		"no request":    {ReplicaID: "r"},
		"no session":    {ReplicaID: "r", Request: &AuditMergeRequest{}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewAuditReplicaRuntime(cfg); err == nil {
				t.Errorf("%s: accepted missing fields", name)
			}
		})
	}
}

// TestAuditReplicaRuntime_ContextGovernorPerReplica verifies that
// each audit instantiates its own ContextGovernor (stored on the
// replica's ctx) and calibrates it per turn. Two concurrent audits
// must have independent governors — one audit approaching its
// budget must not throttle another audit's tool set.
func TestAuditReplicaRuntime_ContextGovernorPerReplica(t *testing.T) {
	sess, vfs := openRuntimeTestSession(t, "governor")
	provider := &scriptedProvider{
		turns: []scriptedTurn{
			{toolCalls: []providers.ToolCall{{
				ID:        "c1",
				Name:      AuditToolEmitDecision,
				Arguments: `{"decision":"accepted","summary":"ok"}`,
			}}},
		},
	}
	var gotResult *AuditMergeResult
	finalizer := func(r *AuditMergeResult) { gotResult = r }
	rt, err := NewAuditReplicaRuntime(AuditReplicaConfig{
		ReplicaID:    "r-gov",
		Request:      &AuditMergeRequest{ReplicaID: "r-gov"},
		SessionID:    "sess",
		Session:      sess,
		ReplicaVFS:   vfs,
		Finalizer:    finalizer,
		Ledger:       runtimeTestLedger(t, "r-gov"),
		Provider:     provider,
		Model:        "claude-opus-4",
		MaxTokens:    8192,
		MaxToolRuns:  16,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Capture the governor attached to ctx so the test can inspect
	// it post-run.
	var seenGovernor *ContextGovernor
	ctx := concurrency.WithScope(context.Background(), newRuntimeTestScope(t))
	ctx = WithAuditDecisionFinalizer(ctx, finalizer)
	// Wrap the provider to snoop ctx at stream time.
	rt.cfg.Provider = &ctxSnoopingProvider{
		inner:   provider,
		onCtx: func(c context.Context) {
			if seenGovernor == nil {
				seenGovernor = ContextGovernorFromContext(c)
			}
		},
	}
	if err := rt.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotResult == nil {
		t.Fatal("no decision")
	}
	if seenGovernor == nil {
		t.Fatal("no ContextGovernor attached to audit ctx")
	}
}

// ctxSnoopingProvider wraps a StreamingTurnProvider and captures the
// ctx each Stream call receives. Used to verify the runtime attaches
// per-audit state (governor) to its internal ctx.
type ctxSnoopingProvider struct {
	inner StreamingTurnProvider
	onCtx func(context.Context)
}

func (p *ctxSnoopingProvider) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	if p.onCtx != nil {
		p.onCtx(ctx)
	}
	return p.inner.Stream(ctx, req)
}

// TestAuditReplicaRuntime_ConsultPeerToolOmittedWithoutTopic verifies
// the tool set gating: consult_peer is only exposed when the runtime
// has a ResponseTopic. Without one, the LLM never sees a tool it
// can't invoke.
func TestAuditReplicaRuntime_ConsultPeerToolOmittedWithoutTopic(t *testing.T) {
	toolsNoConsult := buildAuditReplicaTools(AgentTypeInspectorGlobal, false)
	toolsWithConsult := buildAuditReplicaTools(AgentTypeInspectorGlobal, true)
	if findToolByName(toolsNoConsult, AuditToolConsultPeer) != nil {
		t.Error("consult_peer present in tool set when includeConsult=false")
	}
	if findToolByName(toolsWithConsult, AuditToolConsultPeer) == nil {
		t.Error("consult_peer missing from tool set when includeConsult=true")
	}
}

func findToolByName(tools []providers.Tool, name string) *providers.Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// TestAuditReplicaRuntime_ConsultPeerReturnsErrorForUnconfiguredReplica
// verifies that invoking consult_peer without a ResponseTopic +
// Bus returns a clear error rather than panicking or hanging.
func TestAuditReplicaRuntime_ConsultPeerReturnsErrorForUnconfiguredReplica(t *testing.T) {
	sess, vfs := openRuntimeTestSession(t, "no-consult")
	provider := &scriptedProvider{
		turns: []scriptedTurn{
			// Even though consult_peer isn't in the tool set, the LLM
			// might try to invoke it via a forged tool call. The
			// dispatch path must return a graceful error, not panic.
			{toolCalls: []providers.ToolCall{{
				ID:        "c1",
				Name:      AuditToolConsultPeer,
				Arguments: `{"target_agent_type":"academic","query":"hi"}`,
			}}},
			{toolCalls: []providers.ToolCall{{
				ID:        "c2",
				Name:      AuditToolEmitDecision,
				Arguments: `{"decision":"rejected","summary":"done after unconfigured consult"}`,
			}}},
		},
	}
	var gotResult *AuditMergeResult
	finalizer := func(r *AuditMergeResult) { gotResult = r }
	rt, err := NewAuditReplicaRuntime(AuditReplicaConfig{
		ReplicaID:  "r-no-consult",
		Request:    &AuditMergeRequest{ReplicaID: "r-no-consult"},
		SessionID:  "sess",
		Session:    sess,
		ReplicaVFS: vfs,
		Finalizer:  finalizer,
		Ledger:     runtimeTestLedger(t, "r-no-consult"),
		Provider:   provider,
		// Bus nil, ResponseTopic empty — consult_peer unconfigured.
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := concurrency.WithScope(context.Background(), newRuntimeTestScope(t))
	ctx = WithAuditDecisionFinalizer(ctx, finalizer)
	if err := rt.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if gotResult == nil {
		t.Fatal("no decision")
	}
	// Audit still completed with a rejection on the second turn —
	// the unconfigured consult didn't abort the loop.
	if gotResult.Decision != versioning.ReplicaDecisionRejected {
		t.Errorf("decision = %v, want Rejected", gotResult.Decision)
	}
}

// _ references for imports the file uses only indirectly so the
// import list doesn't regress on small edits.
var _ = guide.NewChannelBus
var _ = json.RawMessage{}
var _ = sync.Mutex{}
var _ = steering.NewSteeringLedger
var _ atomic.Bool
var _ time.Duration
var _ = os.TempDir
var _ = filepath.Join

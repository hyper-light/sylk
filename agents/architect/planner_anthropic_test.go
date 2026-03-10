package architect

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

func TestDecodeJSONPayload_FencedJSON(t *testing.T) {
	raw := "Here is the result:\n```json\n{\"goals\":[\"ship\"],\"scope\":\"api\"}\n```"
	var out map[string]any
	if err := decodeJSONPayload(raw, &out); err != nil {
		t.Fatalf("decodeJSONPayload() error = %v", err)
	}
	if out["scope"] != "api" {
		t.Fatalf("scope = %v, want api", out["scope"])
	}
}

func TestParseTaskPayload_Array(t *testing.T) {
	raw := `[{"name":"Implement auth","description":"desc","agent_type":"documenter","dependencies":[]}]`
	tasks, err := parseTaskPayload(raw)
	if err != nil {
		t.Fatalf("parseTaskPayload() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	if tasks[0].AgentType != "engineer" {
		t.Fatalf("AgentType = %q, want engineer", tasks[0].AgentType)
	}
}

func TestNormalizeTaskGraph_MapsDependencyNameToID(t *testing.T) {
	tasks := []*AtomicTask{
		{ID: "task_1", Name: "Implement storage", AgentType: "engineer"},
		{ID: "task_2", Name: "Implement api", Dependencies: []string{"Implement storage"}, AgentType: "designer"},
	}
	normalized := normalizeTaskGraph(tasks)
	if len(normalized[1].Dependencies) != 1 {
		t.Fatalf("len(dependencies) = %d, want 1", len(normalized[1].Dependencies))
	}
	if normalized[1].Dependencies[0] != "task_1" {
		t.Fatalf("dependency = %q, want task_1", normalized[1].Dependencies[0])
	}
}

func TestNormalizeTaskGraph_AssignsUniqueTaskIDsAndSlugs(t *testing.T) {
	tasks := []*AtomicTask{
		{ID: "Task 1", Name: "Auth Checkout", AgentType: "engineer"},
		{ID: "Task 1", Name: "Auth Checkout", AgentType: "designer", Dependencies: []string{"Task 1"}},
		{Name: "Payment Retry", AgentType: "engineer", Dependencies: []string{"Auth Checkout"}},
	}

	normalized := normalizeTaskGraph(tasks)

	if normalized[0].ID != "task_1" {
		t.Fatalf("first ID = %q, want task_1", normalized[0].ID)
	}
	if normalized[1].ID != "task_1_2" {
		t.Fatalf("second ID = %q, want task_1_2", normalized[1].ID)
	}
	if normalized[0].Slug != "auth-checkout" {
		t.Fatalf("first slug = %q, want auth-checkout", normalized[0].Slug)
	}
	if normalized[1].Slug != "auth-checkout-2" {
		t.Fatalf("second slug = %q, want auth-checkout-2", normalized[1].Slug)
	}
	if normalized[2].Slug != "payment-retry" {
		t.Fatalf("third slug = %q, want payment-retry", normalized[2].Slug)
	}
	if len(normalized[1].Dependencies) != 1 || normalized[1].Dependencies[0] != "task_1" {
		t.Fatalf("second dependencies = %v, want [task_1]", normalized[1].Dependencies)
	}
	if len(normalized[2].Dependencies) != 1 || normalized[2].Dependencies[0] != "task_1" {
		t.Fatalf("third dependencies = %v, want [task_1]", normalized[2].Dependencies)
	}
}

func TestNormalizeTaskGraph_DerivesWorkspaceAndWorkerPackets(t *testing.T) {
	tasks := []*AtomicTask{
		{
			Name:      "Build checkout UI",
			AgentType: "engineer",
			AffectedFiles: []TaskFileTarget{
				{Path: "ui/checkout/page.tsx", Operation: "modify"},
			},
			AgentScopes: []AgentScope{
				{
					AgentType:           "engineer",
					Role:                "primary",
					ImplementationGuide: "Wire the checkout flow.",
					AffectedFiles:       []TaskFileTarget{{Path: "ui/checkout/page.tsx", Operation: "modify"}},
					TestRequirements:    []string{"checkout submit succeeds"},
				},
				{
					AgentType:           "designer",
					Role:                "co_agent",
					ImplementationGuide: "Refine spacing and states.",
					AffectedFiles:       []TaskFileTarget{{Path: "ui/checkout/styles.css", Operation: "modify"}},
				},
			},
		},
	}

	normalized := normalizeTaskGraph(tasks)
	task := normalized[0]

	if task.Workspace.BaseVersion != "session_head" {
		t.Fatalf("workspace base version = %q, want session_head", task.Workspace.BaseVersion)
	}
	if len(task.Workspace.WriteSet) < 2 {
		t.Fatalf("workspace write set = %v, want derived file coverage", task.Workspace.WriteSet)
	}
	if len(task.WorkerPackets) != 2 {
		t.Fatalf("worker packets = %d, want 2", len(task.WorkerPackets))
	}
	if task.WorkerPackets[0].AgentType != "engineer" || task.WorkerPackets[1].AgentType != "designer" {
		t.Fatalf("worker packets = %+v, want engineer/designer packets", task.WorkerPackets)
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	if !containsIgnoreCase("Architect Planner", "planner") {
		t.Fatal("expected containsIgnoreCase to match case-insensitive substring")
	}
	if containsIgnoreCase("Architect", "tester") {
		t.Fatal("did not expect unrelated substring to match")
	}
}

func TestResolveThinkingBudget(t *testing.T) {
	t.Run("returns zero when thinkingBudget is zero (adaptive thinking)", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 0}
		budget := p.resolveThinkingBudget(6000)
		if budget != 0 {
			t.Fatalf("expected 0 for adaptive thinking, got %d", budget)
		}
	})

	t.Run("explicit budget used when set", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 8192}
		budget := p.resolveThinkingBudget(16384)
		if budget != 8192 {
			t.Fatalf("expected 8192, got %d", budget)
		}
	})

	t.Run("clamps when budget >= maxTokens", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 8192}
		budget := p.resolveThinkingBudget(4096)
		if budget != 4095 {
			t.Fatalf("expected 4095 (maxTokens-1), got %d", budget)
		}
	})

	t.Run("disabled for small maxTokens", func(t *testing.T) {
		p := &anthropicPlanner{thinkingBudget: 8192}
		budget := p.resolveThinkingBudget(1024)
		if budget != 0 {
			t.Fatalf("expected 0 for small maxTokens, got %d", budget)
		}
	})
}

func TestThoughtEmitter_BatchesEmissions(t *testing.T) {
	// Create emitter with callback present.
	ctx := withPlannerThoughtCallback(context.Background(), func(string, string) {})
	emitter := newThoughtEmitter(ctx)

	// Incomplete prefixes stay buffered until a sentence boundary arrives.
	if got := emitter.addDelta("Clarifying"); got != "" {
		t.Fatalf("incomplete prefix should stay buffered, got %q", got)
	}

	if got := emitter.addDelta(" questions"); got != "" {
		t.Fatalf("still incomplete without punctuation, got %q", got)
	}

	// Sentence boundary (period) triggers emission.
	if got := emitter.addDelta("."); got == "" {
		t.Fatal("sentence boundary should trigger emission")
	} else if got != "Clarifying questions." {
		t.Fatalf("emission = %q, want %q", got, "Clarifying questions.")
	}
}

func TestThoughtEmitter_NoCallback_SkipsEmission(t *testing.T) {
	// Emitter without callback never emits snapshots.
	emitter := newThoughtEmitter(context.Background())

	if got := emitter.addDelta("some thought."); got != "" {
		t.Fatalf("emitter without callback should skip emission, got %q", got)
	}
}

func TestThoughtEmitter_DoublingTrigger(t *testing.T) {
	ctx := withPlannerThoughtCallback(context.Background(), func(string, string) {})
	emitter := newThoughtEmitter(ctx)

	if got := emitter.addDelta("a"); got != "" {
		t.Fatalf("prefix should stay buffered, got %q", got)
	}
	if got := emitter.addDelta("b"); got != "" {
		t.Fatalf("prefix should stay buffered, got %q", got)
	}
	if got := emitter.addDelta("c"); got != "" {
		t.Fatalf("prefix should stay buffered, got %q", got)
	}
	if got := emitter.addDelta("d."); got != "abcd." {
		t.Fatalf("sentence boundary should emit %q, got %q", "abcd.", got)
	}
}

func TestThoughtEmitter_Flush(t *testing.T) {
	ctx := withPlannerThoughtCallback(context.Background(), func(string, string) {})
	emitter := newThoughtEmitter(ctx)

	if got := emitter.addDelta("**Sequencing planning"); got != "" {
		t.Fatalf("incomplete prefix should stay buffered, got %q", got)
	}
	if got := emitter.addDelta(" tasks and"); got != "" {
		t.Fatal("should not emit mid-buffer")
	}

	// Flush returns the full buffer including the un-emitted tail.
	flushed := emitter.flush()
	if flushed != "**Sequencing planning tasks and" {
		t.Fatalf("flush() = %q, want %q", flushed, "**Sequencing planning tasks and")
	}

	// Second flush is a no-op (nothing new).
	if got := emitter.flush(); got != "" {
		t.Fatalf("second flush should return empty, got %q", got)
	}
}

func TestThoughtEmitter_FlushEmpty(t *testing.T) {
	ctx := withPlannerThoughtCallback(context.Background(), func(string, string) {})
	emitter := newThoughtEmitter(ctx)

	// Flush on empty buffer returns nothing.
	if got := emitter.flush(); got != "" {
		t.Fatalf("flush on empty buffer should return empty, got %q", got)
	}
}

func TestThoughtEmitter_FlushNoCallback(t *testing.T) {
	emitter := newThoughtEmitter(context.Background())

	emitter.addDelta("some thinking content")

	// Without callback, flush should return empty (no materialization).
	if got := emitter.flush(); got != "" {
		t.Fatalf("flush without callback should return empty, got %q", got)
	}
}

func TestContainsSentenceBoundary(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"hello", false},
		{"hello.", true},
		{"hello!", true},
		{"hello?", true},
		{"hello\nworld", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := containsSentenceBoundary(tt.input); got != tt.want {
			t.Errorf("containsSentenceBoundary(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- Continuation tests ---

// mockStreamCall describes a single streaming response the mock should produce.
type mockStreamCall struct {
	Text       string
	StopReason providers.StopReason
	Usage      *providers.Usage
	Err        error
}

// mockStreamProvider replays a sequence of mockStreamCall responses,
// one per invocation of StreamWithHandler.
type mockStreamProvider struct {
	calls    []mockStreamCall
	invoked  int
	requests []*providers.Request
}

func (m *mockStreamProvider) StreamWithHandler(
	ctx context.Context,
	req *providers.Request,
	handler providers.StreamHandler,
) error {
	if m.invoked >= len(m.calls) {
		return fmt.Errorf("mock: no more configured calls (invoked=%d, configured=%d)", m.invoked, len(m.calls))
	}
	call := m.calls[m.invoked]
	m.requests = append(m.requests, req)
	m.invoked++

	if call.Err != nil {
		return call.Err
	}

	if err := handler(&providers.StreamChunk{Type: providers.ChunkTypeStart, Usage: call.Usage}); err != nil {
		return err
	}
	if call.Text != "" {
		if err := handler(&providers.StreamChunk{Type: providers.ChunkTypeText, Text: call.Text}); err != nil {
			return err
		}
	}
	endChunk := &providers.StreamChunk{
		Type:       providers.ChunkTypeEnd,
		Usage:      call.Usage,
		StopReason: call.StopReason,
	}
	return handler(endChunk)
}

// plannerStreamProviderFunc adapts a function to the plannerStreamProvider interface.
type plannerStreamProviderFunc func(ctx context.Context, req *providers.Request, handler providers.StreamHandler) error

func (f plannerStreamProviderFunc) StreamWithHandler(ctx context.Context, req *providers.Request, handler providers.StreamHandler) error {
	return f(ctx, req, handler)
}

// timeoutStreamProvider simulates a context timeout mid-stream: it sends the
// text chunk, then returns a context.DeadlineExceeded error WITHOUT sending
// the ChunkTypeEnd event — exactly what happens when a real stream is cut off.
type timeoutStreamProvider struct {
	text    string
	invoked int
}

func (m *timeoutStreamProvider) StreamWithHandler(
	ctx context.Context,
	req *providers.Request,
	handler providers.StreamHandler,
) error {
	m.invoked++
	if err := handler(&providers.StreamChunk{Type: providers.ChunkTypeStart, Usage: &providers.Usage{InputTokens: 100}}); err != nil {
		return err
	}
	if m.text != "" {
		if err := handler(&providers.StreamChunk{Type: providers.ChunkTypeText, Text: m.text}); err != nil {
			return err
		}
	}
	// Simulate timeout — no ChunkTypeEnd sent.
	return context.DeadlineExceeded
}

// testContextWindow and testMaxOutputTokens define the model parameters used by
// newTestPlanner. Continuation bounds are derived from these via
// deriveContinuationConfig: maxRounds = 16384/4096 = 4.
const (
	testContextWindow   = 16384
	testMaxOutputTokens = 4096
)

func newTestPlanner(mock *mockStreamProvider) *anthropicPlanner {
	return &anthropicPlanner{
		provider:       mock,
		maxTokens:      testMaxOutputTokens,
		thinkingBudget: 0,
		contextWindow:  testContextWindow,
		system:         "test system",
		timeout:        30 * time.Second,
		logger:         slog.Default(),
	}
}

func TestStreamResult_StopReasonPropagation(t *testing.T) {
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: "hello", StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{OutputTokens: 10}},
		},
	}
	p := newTestPlanner(mock)
	sr, err := p.requestTextStreamingOnce(context.Background(), "prompt", 4096, "", "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sr.StopReason != providers.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", sr.StopReason, providers.StopReasonEndTurn)
	}
	if sr.Text != "hello" {
		t.Fatalf("Text = %q, want %q", sr.Text, "hello")
	}
}

func TestContinuation_NoMaxTokens(t *testing.T) {
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: "complete response", StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{OutputTokens: 50}},
		},
	}
	p := newTestPlanner(mock)
	text, usage, err := p.requestTextStreamingWithContinuation(
		context.Background(), "prompt", 4096, "", "test", nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "complete response" {
		t.Fatalf("text = %q, want %q", text, "complete response")
	}
	if usage.OutputTokens != 50 {
		t.Fatalf("OutputTokens = %d, want 50", usage.OutputTokens)
	}
	if mock.invoked != 1 {
		t.Fatalf("invoked = %d, want 1 (no continuation)", mock.invoked)
	}
}

func TestContinuation_SingleRound(t *testing.T) {
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: "first part", StopReason: providers.StopReasonMaxTokens, Usage: &providers.Usage{InputTokens: 100, OutputTokens: 200}},
			{Text: "second part", StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{InputTokens: 150, OutputTokens: 100}},
		},
	}
	p := newTestPlanner(mock)

	var chunks []string
	onChunk := func(s string) { chunks = append(chunks, s) }

	text, usage, err := p.requestTextStreamingWithContinuation(
		context.Background(), "prompt", 4096, "", "test", onChunk,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "first partsecond part" {
		t.Fatalf("text = %q, want %q", text, "first partsecond part")
	}
	if mock.invoked != 2 {
		t.Fatalf("invoked = %d, want 2", mock.invoked)
	}
	if usage.InputTokens != 250 {
		t.Fatalf("InputTokens = %d, want 250", usage.InputTokens)
	}
	if usage.OutputTokens != 300 {
		t.Fatalf("OutputTokens = %d, want 300", usage.OutputTokens)
	}
	// Verify continuation request has ThinkingBudget = 0.
	if mock.requests[1].ThinkingBudget != 0 {
		t.Fatalf("continuation ThinkingBudget = %d, want 0", mock.requests[1].ThinkingBudget)
	}
	// Verify chunks were streamed to callback.
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunk callbacks, got %d", len(chunks))
	}
}

func TestContinuation_MaxRoundsExhausted(t *testing.T) {
	// Derive round count from model parameters — no magic numbers.
	cfg := deriveContinuationConfig(testContextWindow, testMaxOutputTokens, defaultCharsPerToken)
	totalCalls := cfg.maxRounds + 1 // 1 initial + maxRounds continuation

	calls := make([]mockStreamCall, totalCalls)
	for i := range calls {
		calls[i] = mockStreamCall{
			Text:       fmt.Sprintf("part%d", i),
			StopReason: providers.StopReasonMaxTokens,
			Usage:      &providers.Usage{OutputTokens: 10},
		}
	}
	mock := &mockStreamProvider{calls: calls}
	p := newTestPlanner(mock)

	var chunks []string
	onChunk := func(s string) { chunks = append(chunks, s) }

	text, _, err := p.requestTextStreamingWithContinuation(
		context.Background(), "prompt", testMaxOutputTokens, "", "test", onChunk,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.invoked != totalCalls {
		t.Fatalf("invoked = %d, want %d", mock.invoked, totalCalls)
	}
	// Returned text must NOT contain the indicator — it feeds into conversation
	// history and would confuse the LLM into thinking the response failed.
	if strings.Contains(text, truncationIndicator) {
		t.Fatal("returned text must not contain truncation indicator (poisons conversation history)")
	}
	// But the indicator WAS streamed to the UI via onChunk.
	lastChunk := chunks[len(chunks)-1]
	if !strings.Contains(lastChunk, "truncated") {
		t.Fatalf("last chunk = %q, expected truncation indicator streamed to UI", lastChunk)
	}
}

func TestContinuation_ErrorMidway(t *testing.T) {
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: "first part", StopReason: providers.StopReasonMaxTokens, Usage: &providers.Usage{OutputTokens: 100}},
			{Err: fmt.Errorf("network error")},
		},
	}
	p := newTestPlanner(mock)

	// With onChunk: indicator streamed to UI, but NOT in returned text.
	var chunks []string
	onChunk := func(s string) { chunks = append(chunks, s) }
	text, _, err := p.requestTextStreamingWithContinuation(
		context.Background(), "prompt", 4096, "", "test", onChunk,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v (should return partial)", err)
	}
	if text != "first part" {
		t.Fatalf("text = %q, expected clean partial text without indicator", text)
	}
	// Indicator streamed to UI.
	lastChunk := chunks[len(chunks)-1]
	if !strings.Contains(lastChunk, "truncated") {
		t.Fatalf("last chunk = %q, expected truncation indicator streamed to UI", lastChunk)
	}
}

func TestContinuation_ErrorMidway_JSONPath(t *testing.T) {
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: `{"partial":true`, StopReason: providers.StopReasonMaxTokens, Usage: &providers.Usage{OutputTokens: 100}},
			{Err: fmt.Errorf("network error")},
		},
	}
	p := newTestPlanner(mock)

	// Without onChunk (JSON path): no truncation indicator — raw text returned.
	text, _, err := p.requestTextStreamingWithContinuation(
		context.Background(), "prompt", 4096, "", "test", nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v (should return partial)", err)
	}
	if text != `{"partial":true` {
		t.Fatalf("text = %q, expected raw partial JSON without indicator", text)
	}
}

func TestContinuation_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: "partial", StopReason: providers.StopReasonMaxTokens, Usage: &providers.Usage{OutputTokens: 50}},
		},
	}
	p := newTestPlanner(mock)

	// Cancel after first call — continuation should skip due to context.
	cancel()

	// The continuation loop should detect context cancellation via
	// hasTimeForContinuation or the next streamRequest call failing.
	text, _, err := p.requestTextStreamingWithContinuation(
		ctx, "prompt", 4096, "", "test", nil,
	)
	// Context cancelled — we get either an error or partial text with truncation.
	if err != nil {
		// Acceptable: context cancelled error.
		return
	}
	if !strings.HasPrefix(text, "partial") {
		t.Fatalf("text = %q, expected to start with 'partial'", text)
	}
}

func TestContinuation_EmptyResponse(t *testing.T) {
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: "first part", StopReason: providers.StopReasonMaxTokens, Usage: &providers.Usage{OutputTokens: 100}},
			// Empty text — streamRequest will return "planner returned empty content" error.
			{Text: "", StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{OutputTokens: 0}},
		},
	}
	p := newTestPlanner(mock)

	// With onChunk: indicator streamed to UI, returned text clean.
	var chunks []string
	text, _, err := p.requestTextStreamingWithContinuation(
		context.Background(), "prompt", 4096, "", "test", func(s string) { chunks = append(chunks, s) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v (should return partial)", err)
	}
	if text != "first part" {
		t.Fatalf("text = %q, expected clean partial text", text)
	}
	// Indicator streamed to UI.
	lastChunk := chunks[len(chunks)-1]
	if !strings.Contains(lastChunk, "truncated") {
		t.Fatalf("last chunk = %q, expected truncation indicator streamed to UI", lastChunk)
	}
}

func TestStreamRequest_PartialTextOnTimeout(t *testing.T) {
	// Simulates a context timeout mid-stream. streamRequest should return
	// the partial text with StopReason=max_tokens instead of discarding it.
	mock := &timeoutStreamProvider{text: "partial plan content"}
	p := &anthropicPlanner{
		provider:       mock,
		maxTokens:      4096,
		thinkingBudget: 0,
		system:         "test system",
		timeout:        30 * time.Second,
		logger:         slog.Default(),
	}

	// Create a cancelled context to simulate deadline exceeded.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sr, err := p.streamRequest(ctx, &providers.Request{
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: "test"}},
		MaxTokens: 4096,
	}, "test", nil)

	if err != nil {
		t.Fatalf("expected partial result, got error: %v", err)
	}
	if sr.Text != "partial plan content" {
		t.Fatalf("Text = %q, want partial plan content", sr.Text)
	}
	if sr.StopReason != providers.StopReasonError {
		t.Fatalf("StopReason = %q, want %q (timeout preserved as error for continuation triage)", sr.StopReason, providers.StopReasonError)
	}
}

func TestContinuation_RecoverFromTimeout(t *testing.T) {
	// First round times out mid-stream with partial text.
	// Continuation should pick up from the partial text and complete.
	timeoutMock := &timeoutStreamProvider{text: "first half"}
	completionMock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: " second half", StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{OutputTokens: 50}},
		},
	}

	// Use a two-phase provider: first call times out, second completes.
	callCount := 0
	p := &anthropicPlanner{
		provider: plannerStreamProviderFunc(func(ctx context.Context, req *providers.Request, handler providers.StreamHandler) error {
			callCount++
			if callCount == 1 {
				return timeoutMock.StreamWithHandler(ctx, req, handler)
			}
			return completionMock.StreamWithHandler(ctx, req, handler)
		}),
		maxTokens:      testMaxOutputTokens,
		thinkingBudget: 0,
		contextWindow:  testContextWindow,
		system:         "test system",
		timeout:        30 * time.Second,
		logger:         slog.Default(),
	}

	text, _, err := p.requestTextStreamingWithContinuation(
		context.Background(), "prompt", testMaxOutputTokens, "", "test", nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "first halfsecond half" {
		t.Fatalf("text = %q, want 'first halfsecond half'", text)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2 (timeout + continuation)", callCount)
	}
}

func TestRequestJSONText_NonFinalBudget_FastEscalates(t *testing.T) {
	// Non-final budget returns error immediately on max_tokens, no continuation.
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: `{"partial":true`, StopReason: providers.StopReasonMaxTokens, Usage: &providers.Usage{OutputTokens: 100}},
		},
	}
	p := newTestPlanner(mock)

	_, err := p.requestJSONText(
		context.Background(), "generate plan", 1536, "", "test", false,
	)
	if err == nil {
		t.Fatal("expected error for truncated non-final budget")
	}
	if mock.invoked != 1 {
		t.Fatalf("invoked = %d, want 1 (no continuation at non-final budget)", mock.invoked)
	}
}

func TestRequestJSONText_FinalBudget_UsesContinuation(t *testing.T) {
	// Final budget uses continuation to extend truncated JSON.
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: `{"goals":["ship"]`, StopReason: providers.StopReasonMaxTokens, Usage: &providers.Usage{OutputTokens: 100}},
			{Text: `,"scope":"api"}`, StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{OutputTokens: 50}},
		},
	}
	p := newTestPlanner(mock)

	text, err := p.requestJSONText(
		context.Background(), "generate plan", 4096, "", "test", true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.invoked != 2 {
		t.Fatalf("invoked = %d, want 2 (continuation at final budget)", mock.invoked)
	}
	if text != `{"goals":["ship"],"scope":"api"}` {
		t.Fatalf("text = %q, want complete JSON", text)
	}
}

func TestRequestJSONText_FinalBudget_NoContinuationNeeded(t *testing.T) {
	// Final budget with end_turn: no continuation overhead.
	mock := &mockStreamProvider{
		calls: []mockStreamCall{
			{Text: `{"complete":true}`, StopReason: providers.StopReasonEndTurn, Usage: &providers.Usage{OutputTokens: 50}},
		},
	}
	p := newTestPlanner(mock)

	text, err := p.requestJSONText(
		context.Background(), "generate plan", 4096, "", "test", true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.invoked != 1 {
		t.Fatalf("invoked = %d, want 1 (no continuation needed)", mock.invoked)
	}
	if text != `{"complete":true}` {
		t.Fatalf("text = %q, want complete JSON", text)
	}
}

func TestMergeUsage(t *testing.T) {
	a := &providers.Usage{InputTokens: 100, OutputTokens: 200, TotalTokens: 300, CacheReadTokens: 10, CacheWriteTokens: 5}
	b := &providers.Usage{InputTokens: 50, OutputTokens: 100, TotalTokens: 150, CacheReadTokens: 20, CacheWriteTokens: 15}
	merged := mergeUsage(a, b)

	if merged.InputTokens != 150 {
		t.Fatalf("InputTokens = %d, want 150", merged.InputTokens)
	}
	if merged.OutputTokens != 300 {
		t.Fatalf("OutputTokens = %d, want 300", merged.OutputTokens)
	}
	if merged.TotalTokens != 450 {
		t.Fatalf("TotalTokens = %d, want 450", merged.TotalTokens)
	}
	if merged.CacheReadTokens != 30 {
		t.Fatalf("CacheReadTokens = %d, want 30", merged.CacheReadTokens)
	}
	if merged.CacheWriteTokens != 20 {
		t.Fatalf("CacheWriteTokens = %d, want 20", merged.CacheWriteTokens)
	}

	// Nil handling.
	if got := mergeUsage(nil, nil); got != nil {
		t.Fatal("mergeUsage(nil, nil) should return nil")
	}
	if got := mergeUsage(a, nil); got.InputTokens != 100 {
		t.Fatalf("mergeUsage(a, nil).InputTokens = %d, want 100", got.InputTokens)
	}
	if got := mergeUsage(nil, b); got.InputTokens != 50 {
		t.Fatalf("mergeUsage(nil, b).InputTokens = %d, want 50", got.InputTokens)
	}
}

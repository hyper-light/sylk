package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
)

type fakeProvider struct {
	name      string
	resp      *Response
	err       error
	stream    []*StreamChunk
	streamErr error
	tokens    int
	tokenErr  error
}

func (f *fakeProvider) Name() string                         { return f.name }
func (f *fakeProvider) SupportedModels() []ModelInfo         { return nil }
func (f *fakeProvider) CountTokens(_ []Message) (int, error) { return f.tokens, f.tokenErr }
func (f *fakeProvider) MaxContextTokens(_ string) int        { return 0 }
func (f *fakeProvider) HealthCheck(_ context.Context) error  { return nil }
func (f *fakeProvider) Complete(_ context.Context, _ *Request) (*Response, error) {
	return f.resp, f.err
}

func (f *fakeProvider) Stream(_ context.Context, _ *Request) (<-chan *StreamChunk, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	out := make(chan *StreamChunk, len(f.stream))
	for _, c := range f.stream {
		out <- c
	}
	close(out)
	return out, nil
}

type panicProvider struct {
	fakeProvider
}

func (p *panicProvider) Complete(_ context.Context, _ *Request) (*Response, error) {
	panic("nil provider state")
}

func TestProviderGatewayExecutionBackendReturnsFaultOnProviderPanic(t *testing.T) {
	backend := newProviderGatewayExecutionBackend(&panicProvider{fakeProvider: fakeProvider{name: "panic-provider"}}, &Request{Model: "m"})
	data, err := backend.HandleProviderGatewayCall(context.Background(), claims.ProviderGatewayCallRequest{
		Call: claims.ExpectedToolCall{Tool: claims.ProviderGatewayToolComplete, Arguments: map[string]any{
			"model":    "m",
			"identity": "architect",
			"prompt":   "hello",
		}},
		Requested: claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolComplete, Model: "m", Identity: "architect"},
	})
	if err == nil {
		t.Fatal("expected provider gateway fault")
	}
	if !strings.Contains(err.Error(), "provider gateway execution fault") || data.Status != claims.InfrastructureStatusFailed {
		t.Fatalf("data=%+v err=%v, want failed provider gateway fault", data, err)
	}
	stored, resp := backend.Result()
	if resp != nil || stored.Status != claims.InfrastructureStatusFailed {
		t.Fatalf("stored=%+v resp=%v, want failed data and nil response", stored, resp)
	}
}

func TestInstrument_NilWrapsToNil(t *testing.T) {
	if got := Instrument(nil); got != nil {
		t.Fatalf("Instrument(nil) = %v, want nil", got)
	}
}

func TestInstrument_IdempotentDoubleWrap(t *testing.T) {
	inner := &fakeProvider{name: "x"}
	a := Instrument(inner)
	b := Instrument(a)
	if a != b {
		t.Fatal("double-wrap must be idempotent")
	}
}

func TestUnderlyingProvider_StripsWrapper(t *testing.T) {
	inner := &fakeProvider{name: "x"}
	wrapped := Instrument(inner)
	if got := UnderlyingProvider(wrapped); got != ProviderAdapter(inner) {
		t.Fatal("UnderlyingProvider must unwrap")
	}
}

func TestInstrument_EmitsLLMRequestActivityForComplete(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	inner := &fakeProvider{
		name: "anthropic",
		resp: &Response{
			Model:      "claude-opus-4-7",
			StopReason: StopReasonEndTurn,
			Usage:      Usage{InputTokens: 100, OutputTokens: 250, TotalTokens: 350},
		},
	}
	wrapped := Instrument(inner)

	ctx := activity.EnsureFabricContext(context.Background())
	resp, err := wrapped.Complete(ctx, &Request{Model: "claude-opus-4-7"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}

	emitted := col.FilterByKind(activity.ActionLLMResponseCompleted)
	if len(emitted) == 0 {
		t.Fatal("Complete must emit ActionLLMResponseCompleted")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), "claude-opus-4-7") {
		t.Errorf("payload should embed model; got %s", string(last.Payload))
	}
	if !strings.Contains(string(last.Payload), `"input_tokens":100`) {
		t.Errorf("payload should embed input tokens; got %s", string(last.Payload))
	}
	if !strings.Contains(string(last.Payload), `"output_tokens":250`) {
		t.Errorf("payload should embed output tokens; got %s", string(last.Payload))
	}
}

func TestInstrument_TagsErrorOnCompleteFailure(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	inner := &fakeProvider{name: "anthropic", err: errors.New("provider boom")}
	wrapped := Instrument(inner)

	ctx := activity.EnsureFabricContext(context.Background())
	_, err := wrapped.Complete(ctx, &Request{Model: "x"})
	if err == nil {
		t.Fatal("expected Complete to surface the provider error")
	}

	emitted := col.FilterByKind(activity.ActionLLMResponseCompleted)
	if len(emitted) == 0 {
		t.Fatal("failed Complete must still emit a terminal activity")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), "provider boom") {
		t.Errorf("error path should embed the error message; got %s", string(last.Payload))
	}
}

func TestInstrument_CompletePostsProviderGatewayServiceClaim(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "provider-gateway-board", SessionID: "provider-session", TaskID: "task"})
	acc := claims.NewTestamentAccumulator("guide", board.SessionID()).WithBoard(board).WithClaimID("claim-parent").WithResponseClaimID("claim-parent")
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	inner := &fakeProvider{
		name: "anthropic",
		resp: &Response{
			Content:          "service response",
			Model:            "claude-opus-4-7",
			StopReason:       StopReasonEndTurn,
			Usage:            Usage{InputTokens: 12, OutputTokens: 8, TotalTokens: 20},
			ProviderMetadata: map[string]any{"request_id": "provider-req-1"},
		},
	}
	wrapped := Instrument(inner)

	resp, err := wrapped.Complete(ctx, &Request{Model: "claude-opus-4-7", MaxTokens: 64, Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp == nil || resp.Content != "service response" {
		t.Fatalf("response = %+v, want service response", resp)
	}
	assertProviderGatewayServiceClaim(t, board, claims.InfrastructureStatusOK, "provider-req-1", "")
}

func TestInstrument_CompleteErrorPostsFailedProviderGatewayServiceClaim(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "provider-gateway-failure-board", SessionID: "provider-session", TaskID: "task"})
	acc := claims.NewTestamentAccumulator("guide", board.SessionID()).WithBoard(board)
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	inner := &fakeProvider{name: "anthropic", err: errors.New("provider boom")}
	wrapped := Instrument(inner)

	if _, err := wrapped.Complete(ctx, &Request{Model: "claude-opus-4-7", MaxTokens: 64, Messages: []Message{{Role: RoleUser, Content: "hello"}}}); err == nil {
		t.Fatal("expected Complete to return provider error")
	}
	assertProviderGatewayServiceClaim(t, board, claims.InfrastructureStatusFailed, "", "provider boom")
}

func TestInstrument_StreamEmitsActivityWithChunkCount(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	chunks := []*StreamChunk{
		{Index: 0, Type: ChunkTypeStart, Timestamp: time.Now()},
		{Index: 1, Type: ChunkTypeText, Text: "hi", Timestamp: time.Now()},
		{Index: 2, Type: ChunkTypeText, Text: " there", Timestamp: time.Now()},
		{Index: 3, Type: ChunkTypeEnd, Usage: &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, StopReason: StopReasonEndTurn, Timestamp: time.Now()},
	}
	inner := &fakeProvider{name: "google", stream: chunks}
	wrapped := Instrument(inner)

	ctx := activity.EnsureFabricContext(context.Background())
	out, err := wrapped.Stream(ctx, &Request{Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Drain to force the goroutine that emits the terminal activity
	// to complete.
	got := 0
	for range out {
		got++
	}
	if got != 3 {
		t.Fatalf("expected 3 service-synthesized chunks, got %d", got)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if col.CountByKind(activity.ActionLLMResponseCompleted) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	emitted := col.FilterByKind(activity.ActionLLMResponseCompleted)
	if len(emitted) == 0 {
		t.Fatal("Stream must eventually emit ActionLLMResponseCompleted")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), `"chunk_count":3`) {
		t.Errorf("payload should record chunk count; got %s", string(last.Payload))
	}
	if !strings.Contains(string(last.Payload), `"input_tokens":10`) {
		t.Errorf("payload should record token usage; got %s", string(last.Payload))
	}
}

func TestInstrument_StreamSetupFailureEmitsErrorActivity(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	inner := &fakeProvider{name: "openai", streamErr: errors.New("rate limited")}
	wrapped := Instrument(inner)

	ctx := activity.EnsureFabricContext(context.Background())
	if _, err := wrapped.Stream(ctx, &Request{Model: "gpt-x"}); err == nil {
		t.Fatal("expected Stream setup to fail")
	}

	emitted := col.FilterByKind(activity.ActionLLMResponseCompleted)
	if len(emitted) == 0 {
		t.Fatal("setup-failed Stream must emit a terminal activity")
	}
	last := emitted[len(emitted)-1]
	if !strings.Contains(string(last.Payload), "rate limited") {
		t.Errorf("error path should embed setup error; got %s", string(last.Payload))
	}
}

// TestInstrument_PreservesNativeWebSearchEvidenceCapability documents
// that capability detection works through the wrapper. If a future
// optimization removes the SupportsNativeWebSearchEvidence forwarding,
// this test fails — without the forwarder, every Anthropic-backed
// provider would silently lose web-search evidence support once
// instrumentation is enabled.
type webSearchProvider struct{ *fakeProvider }

func (webSearchProvider) SupportsNativeWebSearchEvidence() bool { return true }

func TestInstrument_PreservesNativeWebSearchEvidenceCapability(t *testing.T) {
	inner := webSearchProvider{fakeProvider: &fakeProvider{name: "anthropic"}}
	wrapped := Instrument(inner)
	if !wrapped.(NativeWebSearchEvidenceProvider).SupportsNativeWebSearchEvidence() {
		t.Fatal("wrapper must forward NativeWebSearchEvidenceProvider capability")
	}
}

func assertProviderGatewayServiceClaim(t *testing.T, board *claims.ClaimsBoard, status, requestID, reason string) {
	t.Helper()
	projection := board.Projection()
	if len(projection.Claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(projection.Claims))
	}
	if projection.Claims[0].LifecycleStatus != claims.ClaimLifecycleSatisfied {
		t.Fatalf("claim lifecycle = %s, want satisfied", projection.Claims[0].LifecycleStatus)
	}
	testaments := board.TestamentsByClaim(projection.Claims[0].ID)
	if len(testaments) != 1 {
		t.Fatalf("testaments = %d, want 1", len(testaments))
	}
	data, err := claims.ArtifactData[claims.ProviderGatewayCallArtifactData](testaments[0].Artifacts[0])
	if err != nil {
		t.Fatalf("provider gateway artifact data: %v", err)
	}
	if data.Status != status || data.ProviderRequestID != requestID && requestID != "" || data.FailureReason != reason {
		t.Fatalf("provider gateway data = %+v, want status=%s requestID=%q reason=%q", data, status, requestID, reason)
	}
	if data.Identity != "guide" {
		t.Fatalf("provider identity = %q, want guide", data.Identity)
	}
}

package providers_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers"
	providermocks "github.com/adalundhe/sylk/core/providers/mocks"
	"github.com/stretchr/testify/mock"
)

type streamPublisherAdapter struct {
	mock *providermocks.MockStreamPublisher
}

func (a *streamPublisherAdapter) Publish(msg *messaging.Message[providers.StreamChunk]) error {
	args := a.mock.Called(msg)
	return args.Error(0)
}

func (a *streamPublisherAdapter) PublishControl(msg *providers.StreamControlMessage) error {
	args := a.mock.Called(msg)
	return args.Error(0)
}

type streamProviderAdapter struct {
	mock *providermocks.MockStreamProvider
}

func (a *streamProviderAdapter) Name() string {
	args := a.mock.Called()
	return args.String(0)
}

func (a *streamProviderAdapter) SupportedModels() []providers.ModelInfo {
	args := a.mock.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).([]providers.ModelInfo)
}

func (a *streamProviderAdapter) Complete(ctx context.Context, req *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	args := a.mock.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*providers.CompletionResponse), args.Error(1)
}

func (a *streamProviderAdapter) Stream(ctx context.Context, req *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	args := a.mock.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan *providers.StreamChunk), args.Error(1)
}

func (a *streamProviderAdapter) CountTokens(messages []providers.Message) (int, error) {
	args := a.mock.Called(messages)
	return args.Int(0), args.Error(1)
}

func (a *streamProviderAdapter) MaxContextTokens(model string) int {
	args := a.mock.Called(model)
	return args.Int(0)
}

func (a *streamProviderAdapter) HealthCheck(ctx context.Context) error {
	args := a.mock.Called(ctx)
	return args.Error(0)
}

func (a *streamProviderAdapter) StreamWithHandler(ctx context.Context, req *providers.StreamRequest, handler providers.StreamHandler) error {
	args := a.mock.Called(ctx, req, handler)
	return args.Error(0)
}

func capturePublishedMessages(publisher *providermocks.MockStreamPublisher) func() []*messaging.Message[providers.StreamChunk] {
	var mu sync.Mutex
	messages := make([]*messaging.Message[providers.StreamChunk], 0)

	call := publisher.On("Publish", mock.Anything)
	call.Run(func(args mock.Arguments) {
		msg, ok := args.Get(0).(*messaging.Message[providers.StreamChunk])
		if !ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		messages = append(messages, msg)
	})
	call.Return(nil)
	call.Maybe()

	return func() []*messaging.Message[providers.StreamChunk] {
		mu.Lock()
		defer mu.Unlock()
		result := make([]*messaging.Message[providers.StreamChunk], len(messages))
		copy(result, messages)
		return result
	}
}

func captureControlMessages(publisher *providermocks.MockStreamPublisher) func() []*providers.StreamControlMessage {
	var mu sync.Mutex
	messages := make([]*providers.StreamControlMessage, 0)

	call := publisher.On("PublishControl", mock.Anything)
	call.Run(func(args mock.Arguments) {
		msg, ok := args.Get(0).(*providers.StreamControlMessage)
		if !ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		messages = append(messages, msg)
	})
	call.Return(nil)
	call.Maybe()

	return func() []*providers.StreamControlMessage {
		mu.Lock()
		defer mu.Unlock()
		result := make([]*providers.StreamControlMessage, len(messages))
		copy(result, messages)
		return result
	}
}

func allowPublish(publisher *providermocks.MockStreamPublisher) {
	call := publisher.On("Publish", mock.Anything)
	call.Return(nil)
	call.Maybe()
}

func expectStreamWithHandler(
	provider *providermocks.MockStreamProvider,
	chunks []*providers.StreamChunk,
	streamErr error,
	streamWait time.Duration,
) {
	provider.On("StreamWithHandler", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx, ok := args.Get(0).(context.Context)
			if !ok {
				return
			}
			handler, ok := args.Get(2).(providers.StreamHandler)
			if !ok {
				return
			}
			if streamWait > 0 {
				select {
				case <-time.After(streamWait):
				case <-ctx.Done():
					return
				}
			}

			if streamErr != nil {
				return
			}

			for _, chunk := range chunks {
				select {
				case <-ctx.Done():
					return
				default:
					if err := handler(chunk); err != nil {
						return
					}
				}
			}
		}).
		Return(streamErr)
}

func TestNewStreamBridge(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	config := providers.DefaultStreamBridgeConfig()

	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, config)

	if bridge == nil {
		t.Fatal("expected non-nil StreamBridge")
	}
}

func TestDefaultStreamBridgeConfig(t *testing.T) {
	config := providers.DefaultStreamBridgeConfig()

	if config.ChunkBufferSize != 100 {
		t.Errorf("ChunkBufferSize = %d, want 100", config.ChunkBufferSize)
	}

	if config.PublishTimeout != 5*time.Second {
		t.Errorf("PublishTimeout = %v, want 5s", config.PublishTimeout)
	}
}

func TestStreamBridge_StartStream(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	messages := capturePublishedMessages(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	chunks := []*providers.StreamChunk{
		{Index: 0, Type: providers.ChunkTypeStart, Timestamp: time.Now()},
		{Index: 1, Type: providers.ChunkTypeText, Text: "Hello", Timestamp: time.Now()},
		{Index: 2, Type: providers.ChunkTypeText, Text: " World", Timestamp: time.Now()},
		{Index: 3, Type: providers.ChunkTypeEnd, Timestamp: time.Now()},
	}

	provider := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, chunks, nil, 0)
	streamProvider := &streamProviderAdapter{mock: provider}
	req := &providers.Request{Messages: []providers.Message{{Role: providers.RoleUser, Content: "test"}}}
	streamCtx := providers.NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), streamProvider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	published := messages()
	if len(published) == 0 {
		t.Error("expected at least some published messages")
	}

	for i, msg := range published {
		if msg.CorrelationID != streamCtx.CorrelationID {
			t.Errorf("message %d CorrelationID = %q, want %q", i, msg.CorrelationID, streamCtx.CorrelationID)
		}
	}
}

func TestStreamBridge_StartStream_TracksActiveStream(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	provider := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(
		provider,
		[]*providers.StreamChunk{{Index: 0, Type: providers.ChunkTypeEnd}},
		nil,
		200*time.Millisecond,
	)
	streamProvider := &streamProviderAdapter{mock: provider}

	req := &providers.Request{}
	streamCtx := providers.NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), streamProvider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if len(bridge.GetActiveStreams()) == 0 {
		t.Error("stream should be active after starting")
	}

	time.Sleep(300 * time.Millisecond)
	if len(bridge.GetActiveStreams()) != 0 {
		t.Error("stream should not be active after completing")
	}
}

func TestStreamBridge_CancelStream(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	controlMessages := captureControlMessages(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	provider := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(
		provider,
		[]*providers.StreamChunk{{Index: 0, Type: providers.ChunkTypeEnd}},
		nil,
		5*time.Second,
	)
	streamProvider := &streamProviderAdapter{mock: provider}

	req := &providers.Request{}
	streamCtx := providers.NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), streamProvider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	err = bridge.CancelStream(streamCtx.CorrelationID, "user requested")
	if err != nil {
		t.Fatalf("CancelStream failed: %v", err)
	}

	controlMsgs := controlMessages()
	if len(controlMsgs) != 1 {
		t.Fatalf("expected 1 control message, got %d", len(controlMsgs))
	}

	if controlMsgs[0].Action != "cancel" {
		t.Errorf("control message action = %q, want %q", controlMsgs[0].Action, "cancel")
	}

	if controlMsgs[0].Reason != "user requested" {
		t.Errorf("control message reason = %q, want %q", controlMsgs[0].Reason, "user requested")
	}

	if controlMsgs[0].CorrelationID != streamCtx.CorrelationID {
		t.Errorf("control message CorrelationID mismatch")
	}

	time.Sleep(50 * time.Millisecond)
	if len(bridge.GetActiveStreams()) != 0 {
		t.Error("stream should not be active after cancellation")
	}
}

func TestStreamBridge_CancelStream_NonExistent(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	controlMessages := captureControlMessages(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	err := bridge.CancelStream("non-existent-id", "test reason")
	if err != nil {
		t.Fatalf("CancelStream should not error for non-existent stream: %v", err)
	}

	if len(controlMessages()) != 0 {
		t.Errorf("expected 0 control messages, got %d", len(controlMessages()))
	}
}

func TestStreamBridge_GetActiveStreams(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	streams := bridge.GetActiveStreams()
	if len(streams) != 0 {
		t.Errorf("expected 0 active streams, got %d", len(streams))
	}

	for i := 0; i < 3; i++ {
		provider := providermocks.NewMockStreamProvider(t)
		expectStreamWithHandler(
			provider,
			[]*providers.StreamChunk{{Index: 0, Type: providers.ChunkTypeEnd}},
			nil,
			500*time.Millisecond,
		)
		streamProvider := &streamProviderAdapter{mock: provider}
		streamCtx := providers.NewStreamContext("session", "mock", "model", "agent")
		_ = bridge.StartStream(context.Background(), streamProvider, &providers.Request{}, streamCtx)

	}

	time.Sleep(10 * time.Millisecond)

	streams = bridge.GetActiveStreams()
	if len(streams) != 3 {
		t.Errorf("expected 3 active streams, got %d", len(streams))
	}

	_ = bridge.Close()

	time.Sleep(50 * time.Millisecond)

	streams = bridge.GetActiveStreams()
	if len(streams) != 0 {
		t.Errorf("expected 0 active streams after Close, got %d", len(streams))
	}
}

func TestStreamBridge_Close(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	var correlationIDs []string
	for i := 0; i < 3; i++ {
		provider := providermocks.NewMockStreamProvider(t)
		expectStreamWithHandler(
			provider,
			[]*providers.StreamChunk{{Index: 0, Type: providers.ChunkTypeEnd}},
			nil,
			5*time.Second,
		)
		streamProvider := &streamProviderAdapter{mock: provider}
		streamCtx := providers.NewStreamContext("session", "mock", "model", "agent")
		correlationIDs = append(correlationIDs, streamCtx.CorrelationID)
		_ = bridge.StartStream(context.Background(), streamProvider, &providers.Request{}, streamCtx)

	}

	time.Sleep(10 * time.Millisecond)

	if len(bridge.GetActiveStreams()) != 3 {
		t.Fatalf("expected 3 active streams before Close")
	}

	err := bridge.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if len(bridge.GetActiveStreams()) != 0 {
		t.Error("expected 0 active streams after Close")
	}
}

func TestStreamBridge_ContextCancellation(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	provider := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(
		provider,
		[]*providers.StreamChunk{{Index: 0, Type: providers.ChunkTypeEnd}},
		nil,
		5*time.Second,
	)
	streamProvider := &streamProviderAdapter{mock: provider}

	ctx, cancel := context.WithCancel(context.Background())
	req := &providers.Request{}
	streamCtx := providers.NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(ctx, streamProvider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	cancel()

	time.Sleep(100 * time.Millisecond)

	if len(bridge.GetActiveStreams()) != 0 {
		t.Error("stream should not be active after context cancellation")
	}
}

func TestStreamBridge_StreamError(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	messages := capturePublishedMessages(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	streamErr := errors.New("provider error")
	provider := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, nil, streamErr, 0)
	streamProvider := &streamProviderAdapter{mock: provider}

	req := &providers.Request{}
	streamCtx := providers.NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), streamProvider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	published := messages()
	if len(published) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(published))
	}

	if published[0].Payload.Type != providers.ChunkTypeError {
		t.Errorf("payload type = %q, want %q", published[0].Payload.Type, providers.ChunkTypeError)
	}

	if published[0].Payload.Text != streamErr.Error() {
		t.Errorf("error text = %q, want %q", published[0].Payload.Text, streamErr.Error())
	}
}

func TestStreamBridge_PublishError(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	publisher.On("Publish", mock.Anything).Return(errors.New("publish error"))
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	chunks := []*providers.StreamChunk{{Index: 0, Type: providers.ChunkTypeText, Text: "test"}}
	provider := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, chunks, nil, 0)
	streamProvider := &streamProviderAdapter{mock: provider}
	req := &providers.Request{}
	streamCtx := providers.NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), streamProvider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if len(bridge.GetActiveStreams()) != 0 {
		t.Error("stream should complete even with publish errors")
	}
}

func TestStreamBridge_Concurrent(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	messages := capturePublishedMessages(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	var wg sync.WaitGroup
	streamCount := 10
	providerList := make([]*providermocks.MockStreamProvider, streamCount)

	for i := 0; i < streamCount; i++ {
		provider := providermocks.NewMockStreamProvider(t)
		chunks := []*providers.StreamChunk{
			{Index: 0, Type: providers.ChunkTypeStart, Timestamp: time.Now()},
			{Index: 1, Type: providers.ChunkTypeText, Text: "chunk", Timestamp: time.Now()},
			{Index: 2, Type: providers.ChunkTypeEnd, Timestamp: time.Now()},
		}
		expectStreamWithHandler(provider, chunks, nil, 0)
		providerList[i] = provider
	}

	for i := 0; i < streamCount; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()

			streamCtx := providers.NewStreamContext("session", "mock", "model", "agent")
			streamProvider := &streamProviderAdapter{mock: providerList[streamNum]}
			_ = bridge.StartStream(context.Background(), streamProvider, &providers.Request{}, streamCtx)
		}(i)
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	published := messages()
	if len(published) == 0 {
		t.Error("expected at least some published messages from concurrent streams")
	}

	if len(bridge.GetActiveStreams()) != 0 {
		t.Error("no streams should be active after completion")
	}
}

func TestStreamBridge_MultipleStartsSameCorrelation(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	chunks := []*providers.StreamChunk{{Index: 0, Type: providers.ChunkTypeEnd}}
	provider := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, chunks, nil, 0)
	streamProvider := &streamProviderAdapter{mock: provider}
	streamCtx := providers.NewStreamContext("session", "mock", "model", "agent")

	_ = bridge.StartStream(context.Background(), streamProvider, &providers.Request{}, streamCtx)

	time.Sleep(10 * time.Millisecond)

	provider2 := providermocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider2, chunks, nil, 100*time.Millisecond)
	streamProvider2 := &streamProviderAdapter{mock: provider2}
	_ = bridge.StartStream(context.Background(), streamProvider2, &providers.Request{}, streamCtx)

	time.Sleep(10 * time.Millisecond)

	if len(bridge.GetActiveStreams()) != 1 {
		t.Errorf("expected 1 active stream, got %d", len(bridge.GetActiveStreams()))
	}
}

func TestStreamBridgeConfig_CustomValues(t *testing.T) {
	config := providers.StreamBridgeConfig{
		ChunkBufferSize: 500,
		PublishTimeout:  10 * time.Second,
	}

	if config.ChunkBufferSize != 500 {
		t.Errorf("ChunkBufferSize = %d, want 500", config.ChunkBufferSize)
	}

	if config.PublishTimeout != 10*time.Second {
		t.Errorf("PublishTimeout = %v, want 10s", config.PublishTimeout)
	}
}

// =============================================================================
// W12.45 Panic Recovery Tests
// =============================================================================

// panicStreamProvider is a StreamProvider that panics during streaming.
type panicStreamProvider struct {
	panicMessage string
}

func (p *panicStreamProvider) Name() string {
	return "panic-provider"
}

func (p *panicStreamProvider) SupportedModels() []providers.ModelInfo {
	return nil
}

func (p *panicStreamProvider) Complete(ctx context.Context, req *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	return nil, nil
}

func (p *panicStreamProvider) Stream(ctx context.Context, req *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	panic(p.panicMessage)
}

func (p *panicStreamProvider) CountTokens(messages []providers.Message) (int, error) {
	return 0, nil
}

func (p *panicStreamProvider) MaxContextTokens(model string) int {
	return 0
}

func (p *panicStreamProvider) HealthCheck(ctx context.Context) error {
	return nil
}

func (p *panicStreamProvider) StreamWithHandler(ctx context.Context, req *providers.StreamRequest, handler providers.StreamHandler) error {
	panic(p.panicMessage)
}

// TestStreamBridge_PanicRecovery verifies that panics in stream handlers
// are recovered and published as error chunks (W12.45 fix).
func TestStreamBridge_PanicRecovery(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	messages := capturePublishedMessages(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	panicProvider := &panicStreamProvider{panicMessage: "test panic"}
	req := &providers.Request{}
	streamCtx := providers.NewStreamContext("session-1", "panic", "panic-model", "agent-1")

	// This should not panic the test
	err := bridge.StartStream(context.Background(), panicProvider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	// Wait for the stream goroutine to process the panic
	time.Sleep(100 * time.Millisecond)

	// Verify an error chunk was published
	published := messages()
	if len(published) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(published))
	}

	if published[0].Payload.Type != providers.ChunkTypeError {
		t.Errorf("payload type = %q, want %q", published[0].Payload.Type, providers.ChunkTypeError)
	}

	// The panic is recovered by StreamToChannel and converted to an error
	if !strings.Contains(published[0].Payload.Text, "panic in stream") {
		t.Errorf("error text should contain 'panic in stream', got %q", published[0].Payload.Text)
	}

	if !strings.Contains(published[0].Payload.Text, "test panic") {
		t.Errorf("error text should contain panic message 'test panic', got %q", published[0].Payload.Text)
	}

	// Verify stream was untracked
	if len(bridge.GetActiveStreams()) != 0 {
		t.Error("stream should be untracked after panic recovery")
	}
}

// TestStreamBridge_PanicRecovery_Concurrent verifies panic recovery
// works correctly under concurrent stream operations.
func TestStreamBridge_PanicRecovery_Concurrent(t *testing.T) {
	publisher := providermocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := providers.NewStreamBridge(&streamPublisherAdapter{mock: publisher}, providers.DefaultStreamBridgeConfig())

	var wg sync.WaitGroup
	streamCount := 10

	for i := 0; i < streamCount; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			panicProvider := &panicStreamProvider{
				panicMessage: "concurrent panic " + string(rune('0'+n)),
			}
			streamCtx := providers.NewStreamContext("session", "panic", "model", "agent")
			_ = bridge.StartStream(context.Background(), panicProvider, &providers.Request{}, streamCtx)
		}(i)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	// All streams should be untracked after panic recovery
	if len(bridge.GetActiveStreams()) != 0 {
		t.Errorf("expected 0 active streams after panic recovery, got %d", len(bridge.GetActiveStreams()))
	}
}

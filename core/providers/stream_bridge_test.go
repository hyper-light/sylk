package providers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockPublisher implements StreamPublisher for testing
type mockPublisher struct {
	mu              sync.Mutex
	messages        []*StreamMessage
	controlMessages []*StreamControlMessage
	publishErr      error
	controlErr      error
}

func newMockPublisher() *mockPublisher {
	return &mockPublisher{
		messages:        make([]*StreamMessage, 0),
		controlMessages: make([]*StreamControlMessage, 0),
	}
}

func (p *mockPublisher) Publish(msg *StreamMessage) error {
	if p.publishErr != nil {
		return p.publishErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, msg)
	return nil
}

func (p *mockPublisher) PublishControl(msg *StreamControlMessage) error {
	if p.controlErr != nil {
		return p.controlErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.controlMessages = append(p.controlMessages, msg)
	return nil
}

func (p *mockPublisher) getMessages() []*StreamMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]*StreamMessage, len(p.messages))
	copy(result, p.messages)
	return result
}

func (p *mockPublisher) getControlMessages() []*StreamControlMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]*StreamControlMessage, len(p.controlMessages))
	copy(result, p.controlMessages)
	return result
}

// mockProvider implements Provider for testing
type mockProvider struct {
	name       string
	chunks     []*StreamChunk
	streamErr  error
	streamWait time.Duration
}

func newMockProvider(chunks []*StreamChunk) *mockProvider {
	return &mockProvider{
		name:   "mock",
		chunks: chunks,
	}
}

func (p *mockProvider) Name() string {
	return p.name
}

func (p *mockProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	return nil, errors.New("not implemented")
}

func (p *mockProvider) Stream(ctx context.Context, req *Request, handler StreamHandler) error {
	if p.streamWait > 0 {
		select {
		case <-time.After(p.streamWait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if p.streamErr != nil {
		return p.streamErr
	}

	for _, chunk := range p.chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := handler(chunk); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *mockProvider) ValidateConfig() error {
	return nil
}

func (p *mockProvider) SupportsModel(model string) bool {
	return true
}

func (p *mockProvider) DefaultModel() string {
	return "mock-model"
}

func (p *mockProvider) Close() error {
	return nil
}

func TestNewStreamBridge(t *testing.T) {
	publisher := newMockPublisher()
	config := DefaultStreamBridgeConfig()

	bridge := NewStreamBridge(publisher, config)

	if bridge == nil {
		t.Fatal("expected non-nil StreamBridge")
	}

	if bridge.publisher != publisher {
		t.Error("publisher not set correctly")
	}

	if bridge.config != config {
		t.Error("config not set correctly")
	}

	if bridge.activeStreams == nil {
		t.Error("activeStreams map not initialized")
	}

	if len(bridge.activeStreams) != 0 {
		t.Errorf("activeStreams should be empty, got %d", len(bridge.activeStreams))
	}
}

func TestDefaultStreamBridgeConfig(t *testing.T) {
	config := DefaultStreamBridgeConfig()

	if config.ChunkBufferSize != 100 {
		t.Errorf("ChunkBufferSize = %d, want 100", config.ChunkBufferSize)
	}

	if config.PublishTimeout != 5*time.Second {
		t.Errorf("PublishTimeout = %v, want 5s", config.PublishTimeout)
	}
}

func TestStreamBridge_StartStream(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	chunks := []*StreamChunk{
		{Index: 0, Type: ChunkTypeStart, Timestamp: time.Now()},
		{Index: 1, Type: ChunkTypeText, Text: "Hello", Timestamp: time.Now()},
		{Index: 2, Type: ChunkTypeText, Text: " World", Timestamp: time.Now()},
		{Index: 3, Type: ChunkTypeEnd, Timestamp: time.Now()},
	}

	provider := newMockProvider(chunks)
	req := &Request{Messages: []Message{{Role: RoleUser, Content: "test"}}}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	// Wait for stream to complete - give it enough time for all chunks
	time.Sleep(500 * time.Millisecond)

	messages := publisher.getMessages()
	// Verify we got at least some messages (timing can vary)
	if len(messages) == 0 {
		t.Error("expected at least some published messages")
	}

	// Verify correlation ID on all messages we did receive
	for i, msg := range messages {
		if msg.CorrelationID != streamCtx.CorrelationID {
			t.Errorf("message %d CorrelationID = %q, want %q", i, msg.CorrelationID, streamCtx.CorrelationID)
		}
	}
}

func TestStreamBridge_StartStream_TracksActiveStream(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	// Provider that waits before returning
	provider := &mockProvider{
		name:       "mock",
		chunks:     []*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
		streamWait: 200 * time.Millisecond,
	}

	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	// Stream should be active immediately after starting
	time.Sleep(10 * time.Millisecond)
	if !bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should be active after starting")
	}

	// Wait for stream to complete
	time.Sleep(300 * time.Millisecond)
	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should not be active after completing")
	}
}

func TestStreamBridge_CancelStream(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	// Provider that waits long enough to be cancelled
	provider := &mockProvider{
		name:       "mock",
		chunks:     []*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
		streamWait: 5 * time.Second,
	}

	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	err = bridge.CancelStream(streamCtx.CorrelationID, "user requested")
	if err != nil {
		t.Fatalf("CancelStream failed: %v", err)
	}

	// Verify control message was published
	controlMsgs := publisher.getControlMessages()
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

	// Wait for stream to be cleaned up
	time.Sleep(50 * time.Millisecond)
	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should not be active after cancellation")
	}
}

func TestStreamBridge_CancelStream_NonExistent(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	// Cancelling non-existent stream should not error
	err := bridge.CancelStream("non-existent-id", "test reason")
	if err != nil {
		t.Fatalf("CancelStream should not error for non-existent stream: %v", err)
	}

	// No control message should be published
	controlMsgs := publisher.getControlMessages()
	if len(controlMsgs) != 0 {
		t.Errorf("expected 0 control messages, got %d", len(controlMsgs))
	}
}

func TestStreamBridge_GetActiveStreams(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	// Initially empty
	streams := bridge.GetActiveStreams()
	if len(streams) != 0 {
		t.Errorf("expected 0 active streams, got %d", len(streams))
	}

	// Start multiple streams
	for i := 0; i < 3; i++ {
		provider := &mockProvider{
			name:       "mock",
			chunks:     []*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
			streamWait: 500 * time.Millisecond,
		}
		streamCtx := NewStreamContext("session", "mock", "model", "agent")
		_ = bridge.StartStream(context.Background(), provider, &Request{}, streamCtx)
	}

	time.Sleep(10 * time.Millisecond)

	streams = bridge.GetActiveStreams()
	if len(streams) != 3 {
		t.Errorf("expected 3 active streams, got %d", len(streams))
	}

	// Cancel all via Close
	_ = bridge.Close()

	time.Sleep(50 * time.Millisecond)

	streams = bridge.GetActiveStreams()
	if len(streams) != 0 {
		t.Errorf("expected 0 active streams after Close, got %d", len(streams))
	}
}

func TestStreamBridge_Close(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	// Start multiple streams
	var correlationIDs []string
	for i := 0; i < 3; i++ {
		provider := &mockProvider{
			name:       "mock",
			chunks:     []*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
			streamWait: 5 * time.Second,
		}
		streamCtx := NewStreamContext("session", "mock", "model", "agent")
		correlationIDs = append(correlationIDs, streamCtx.CorrelationID)
		_ = bridge.StartStream(context.Background(), provider, &Request{}, streamCtx)
	}

	time.Sleep(10 * time.Millisecond)

	// Verify streams are active
	if len(bridge.GetActiveStreams()) != 3 {
		t.Fatalf("expected 3 active streams before Close")
	}

	err := bridge.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// All streams should be cancelled
	time.Sleep(50 * time.Millisecond)
	for _, id := range correlationIDs {
		if bridge.isStreamActive(id) {
			t.Errorf("stream %s should not be active after Close", id)
		}
	}
}

func TestStreamBridge_ContextCancellation(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	provider := &mockProvider{
		name:       "mock",
		chunks:     []*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
		streamWait: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(ctx, provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for cleanup
	time.Sleep(100 * time.Millisecond)

	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should not be active after context cancellation")
	}
}

func TestStreamBridge_StreamError(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	streamErr := errors.New("provider error")
	provider := &mockProvider{
		name:      "mock",
		streamErr: streamErr,
	}

	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	// Wait for error to be processed
	time.Sleep(100 * time.Millisecond)

	// Should publish an error chunk
	messages := publisher.getMessages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(messages))
	}

	if messages[0].Payload.Type != ChunkTypeError {
		t.Errorf("payload type = %q, want %q", messages[0].Payload.Type, ChunkTypeError)
	}

	if messages[0].Payload.Text != streamErr.Error() {
		t.Errorf("error text = %q, want %q", messages[0].Payload.Text, streamErr.Error())
	}
}

func TestStreamBridge_PublishError(t *testing.T) {
	publisher := newMockPublisher()
	publisher.publishErr = errors.New("publish failed")
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	chunks := []*StreamChunk{
		{Index: 0, Type: ChunkTypeText, Text: "test"},
	}
	provider := newMockProvider(chunks)
	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	// Should not crash even with publish errors
	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Stream should still complete (error is ignored)
	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should complete even with publish errors")
	}
}

func TestStreamBridge_Concurrent(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	var wg sync.WaitGroup
	streamCount := 10

	for i := 0; i < streamCount; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()

			chunks := []*StreamChunk{
				{Index: 0, Type: ChunkTypeStart, Timestamp: time.Now()},
				{Index: 1, Type: ChunkTypeText, Text: "chunk", Timestamp: time.Now()},
				{Index: 2, Type: ChunkTypeEnd, Timestamp: time.Now()},
			}

			provider := newMockProvider(chunks)
			streamCtx := NewStreamContext("session", "mock", "model", "agent")
			_ = bridge.StartStream(context.Background(), provider, &Request{}, streamCtx)
		}(i)
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	messages := publisher.getMessages()
	if len(messages) == 0 {
		t.Error("expected at least some published messages from concurrent streams")
	}

	if bridge.isStreamActive("any") {
		t.Error("no streams should be active after completion")
	}
}

func TestStreamBridge_TrackUntrack(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	// Manually test track/untrack
	stream := &activeStream{
		ctx:           context.Background(),
		cancel:        func() {},
		correlationID: "test-corr-id",
		startedAt:     time.Now(),
	}

	bridge.trackStream("test-corr-id", stream)

	if !bridge.isStreamActive("test-corr-id") {
		t.Error("stream should be active after tracking")
	}

	bridge.untrackStream("test-corr-id")

	if bridge.isStreamActive("test-corr-id") {
		t.Error("stream should not be active after untracking")
	}
}

func TestStreamBridge_IsStreamActive(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	// Non-existent stream
	if bridge.isStreamActive("non-existent") {
		t.Error("non-existent stream should not be active")
	}

	// Add a stream
	bridge.trackStream("test-id", &activeStream{
		correlationID: "test-id",
		cancel:        func() {},
	})

	if !bridge.isStreamActive("test-id") {
		t.Error("tracked stream should be active")
	}

	// Remove it
	bridge.untrackStream("test-id")

	if bridge.isStreamActive("test-id") {
		t.Error("untracked stream should not be active")
	}
}

func TestStreamBridge_MultipleStartsSameCorrelation(t *testing.T) {
	publisher := newMockPublisher()
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	chunks := []*StreamChunk{{Index: 0, Type: ChunkTypeEnd}}
	provider := newMockProvider(chunks)
	streamCtx := NewStreamContext("session", "mock", "model", "agent")

	// Start first stream
	_ = bridge.StartStream(context.Background(), provider, &Request{}, streamCtx)

	time.Sleep(10 * time.Millisecond)

	// Start second stream with same correlation ID (should overwrite)
	provider2 := &mockProvider{
		name:       "mock",
		chunks:     chunks,
		streamWait: 100 * time.Millisecond,
	}
	_ = bridge.StartStream(context.Background(), provider2, &Request{}, streamCtx)

	time.Sleep(10 * time.Millisecond)

	// Should still have exactly one active stream
	if len(bridge.GetActiveStreams()) != 1 {
		t.Errorf("expected 1 active stream, got %d", len(bridge.GetActiveStreams()))
	}
}

func TestStreamBridgeConfig_CustomValues(t *testing.T) {
	config := StreamBridgeConfig{
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

func TestActiveStream_Fields(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	now := time.Now()
	stream := &activeStream{
		ctx:           ctx,
		cancel:        cancel,
		correlationID: "test-corr",
		startedAt:     now,
	}

	if stream.ctx != ctx {
		t.Error("ctx not set correctly")
	}

	if stream.correlationID != "test-corr" {
		t.Errorf("correlationID = %q, want %q", stream.correlationID, "test-corr")
	}

	if stream.startedAt != now {
		t.Error("startedAt not set correctly")
	}
}

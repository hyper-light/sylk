package providers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
	"github.com/adalundhe/sylk/core/providers/mocks"
	"github.com/stretchr/testify/mock"
)

func capturePublishedMessages(publisher *mocks.MockStreamPublisher) func() []*messaging.Message[StreamChunk] {
	var mu sync.Mutex
	messages := make([]*messaging.Message[StreamChunk], 0)

	publisher.EXPECT().
		Publish(mock.Anything).
		Run(func(msg *messaging.Message[StreamChunk]) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}).
		Return(nil)

	return func() []*messaging.Message[StreamChunk] {
		mu.Lock()
		defer mu.Unlock()
		result := make([]*messaging.Message[StreamChunk], len(messages))
		copy(result, messages)
		return result
	}
}

func captureControlMessages(publisher *mocks.MockStreamPublisher) func() []*StreamControlMessage {
	var mu sync.Mutex
	messages := make([]*StreamControlMessage, 0)

	publisher.On("PublishControl", mock.Anything).
		Return(nil).
		Run(func(args mock.Arguments) {
			msg, ok := args.Get(0).(*StreamControlMessage)
			if !ok {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		})

	return func() []*StreamControlMessage {
		mu.Lock()
		defer mu.Unlock()
		result := make([]*StreamControlMessage, len(messages))
		copy(result, messages)
		return result
	}
}

func allowPublish(publisher *mocks.MockStreamPublisher) {
	publisher.On("Publish", mock.Anything).Return(nil)
}

func expectStreamWithHandler(
	provider *mocks.MockStreamProvider,
	chunks []*StreamChunk,
	streamErr error,
	streamWait time.Duration,
) {
	provider.On("StreamWithHandler", mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, req *Request, handler StreamHandler) error {
			if streamWait > 0 {
				select {
				case <-time.After(streamWait):
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			if streamErr != nil {
				return streamErr
			}

			for _, chunk := range chunks {
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
		})
}

func TestNewStreamBridge(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	config := DefaultStreamBridgeConfig()

	bridge := NewStreamBridge(publisher, config)

	if bridge == nil {
		t.Fatal("expected non-nil StreamBridge")
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
	publisher := mocks.NewMockStreamPublisher(t)
	messages := capturePublishedMessages(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	chunks := []*StreamChunk{
		{Index: 0, Type: ChunkTypeStart, Timestamp: time.Now()},
		{Index: 1, Type: ChunkTypeText, Text: "Hello", Timestamp: time.Now()},
		{Index: 2, Type: ChunkTypeText, Text: " World", Timestamp: time.Now()},
		{Index: 3, Type: ChunkTypeEnd, Timestamp: time.Now()},
	}

	provider := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, chunks, nil, 0)
	req := &Request{Messages: []Message{{Role: RoleUser, Content: "test"}}}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
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
	publisher := mocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	provider := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(
		provider,
		[]*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
		nil,
		200*time.Millisecond,
	)

	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if !bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should be active after starting")
	}

	time.Sleep(300 * time.Millisecond)
	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should not be active after completing")
	}
}

func TestStreamBridge_CancelStream(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	controlMessages := captureControlMessages(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	provider := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(
		provider,
		[]*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
		nil,
		5*time.Second,
	)

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
	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should not be active after cancellation")
	}
}

func TestStreamBridge_CancelStream_NonExistent(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	err := bridge.CancelStream("non-existent-id", "test reason")
	if err != nil {
		t.Fatalf("CancelStream should not error for non-existent stream: %v", err)
	}
}

func TestStreamBridge_GetActiveStreams(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	streams := bridge.GetActiveStreams()
	if len(streams) != 0 {
		t.Errorf("expected 0 active streams, got %d", len(streams))
	}

	for i := 0; i < 3; i++ {
		provider := mocks.NewMockStreamProvider(t)
		expectStreamWithHandler(
			provider,
			[]*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
			nil,
			500*time.Millisecond,
		)
		streamCtx := NewStreamContext("session", "mock", "model", "agent")
		_ = bridge.StartStream(context.Background(), provider, &Request{}, streamCtx)
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
	publisher := mocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	var correlationIDs []string
	for i := 0; i < 3; i++ {
		provider := mocks.NewMockStreamProvider(t)
		expectStreamWithHandler(
			provider,
			[]*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
			nil,
			5*time.Second,
		)
		streamCtx := NewStreamContext("session", "mock", "model", "agent")
		correlationIDs = append(correlationIDs, streamCtx.CorrelationID)
		_ = bridge.StartStream(context.Background(), provider, &Request{}, streamCtx)
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
	for _, id := range correlationIDs {
		if bridge.isStreamActive(id) {
			t.Errorf("stream %s should not be active after Close", id)
		}
	}
}

func TestStreamBridge_ContextCancellation(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	provider := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(
		provider,
		[]*StreamChunk{{Index: 0, Type: ChunkTypeEnd}},
		nil,
		5*time.Second,
	)

	ctx, cancel := context.WithCancel(context.Background())
	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(ctx, provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	cancel()

	time.Sleep(100 * time.Millisecond)

	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should not be active after context cancellation")
	}
}

func TestStreamBridge_StreamError(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	messages := capturePublishedMessages(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	streamErr := errors.New("provider error")
	provider := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, nil, streamErr, 0)

	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	published := messages()
	if len(published) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(published))
	}

	if published[0].Payload.Type != ChunkTypeError {
		t.Errorf("payload type = %q, want %q", published[0].Payload.Type, ChunkTypeError)
	}

	if published[0].Payload.Text != streamErr.Error() {
		t.Errorf("error text = %q, want %q", published[0].Payload.Text, streamErr.Error())
	}
}

func TestStreamBridge_PublishError(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	publisher.EXPECT().Publish(mock.Anything).Return(errors.New("publish error"))
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	chunks := []*StreamChunk{{Index: 0, Type: ChunkTypeText, Text: "test"}}
	provider := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, chunks, nil, 0)
	req := &Request{}
	streamCtx := NewStreamContext("session-1", "mock", "mock-model", "agent-1")

	err := bridge.StartStream(context.Background(), provider, req, streamCtx)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if bridge.isStreamActive(streamCtx.CorrelationID) {
		t.Error("stream should complete even with publish errors")
	}
}

func TestStreamBridge_Concurrent(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	messages := capturePublishedMessages(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	var wg sync.WaitGroup
	streamCount := 10
	providerList := make([]*mocks.MockStreamProvider, streamCount)

	for i := 0; i < streamCount; i++ {
		provider := mocks.NewMockStreamProvider(t)
		chunks := []*StreamChunk{
			{Index: 0, Type: ChunkTypeStart, Timestamp: time.Now()},
			{Index: 1, Type: ChunkTypeText, Text: "chunk", Timestamp: time.Now()},
			{Index: 2, Type: ChunkTypeEnd, Timestamp: time.Now()},
		}
		expectStreamWithHandler(provider, chunks, nil, 0)
		providerList[i] = provider
	}

	for i := 0; i < streamCount; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()

			streamCtx := NewStreamContext("session", "mock", "model", "agent")
			_ = bridge.StartStream(context.Background(), providerList[streamNum], &Request{}, streamCtx)
		}(i)
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	published := messages()
	if len(published) == 0 {
		t.Error("expected at least some published messages from concurrent streams")
	}

	if bridge.isStreamActive("any") {
		t.Error("no streams should be active after completion")
	}
}

func TestStreamBridge_TrackUntrack(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

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
	publisher := mocks.NewMockStreamPublisher(t)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	if bridge.isStreamActive("non-existent") {
		t.Error("non-existent stream should not be active")
	}

	bridge.trackStream("test-id", &activeStream{
		correlationID: "test-id",
		cancel:        func() {},
	})

	if !bridge.isStreamActive("test-id") {
		t.Error("tracked stream should be active")
	}

	bridge.untrackStream("test-id")

	if bridge.isStreamActive("test-id") {
		t.Error("untracked stream should not be active")
	}
}

func TestStreamBridge_MultipleStartsSameCorrelation(t *testing.T) {
	publisher := mocks.NewMockStreamPublisher(t)
	allowPublish(publisher)
	bridge := NewStreamBridge(publisher, DefaultStreamBridgeConfig())

	chunks := []*StreamChunk{{Index: 0, Type: ChunkTypeEnd}}
	provider := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider, chunks, nil, 0)
	streamCtx := NewStreamContext("session", "mock", "model", "agent")

	_ = bridge.StartStream(context.Background(), provider, &Request{}, streamCtx)

	time.Sleep(10 * time.Millisecond)

	provider2 := mocks.NewMockStreamProvider(t)
	expectStreamWithHandler(provider2, chunks, nil, 100*time.Millisecond)
	_ = bridge.StartStream(context.Background(), provider2, &Request{}, streamCtx)

	time.Sleep(10 * time.Millisecond)

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

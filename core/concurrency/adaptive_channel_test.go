package concurrency

import (
	"sync"
	"testing"
	"time"
)

func TestNewAdaptiveChannel(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())

	if ch.Size() != DefaultMinSize {
		t.Errorf("got size %d, want %d", ch.Size(), DefaultMinSize)
	}
	if ch.Len() != 0 {
		t.Errorf("got len %d, want 0", ch.Len())
	}
	if ch.OverflowLen() != 0 {
		t.Errorf("got overflow len %d, want 0", ch.OverflowLen())
	}
}

func TestAdaptiveChannel_SendReceive(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	if err := ch.Send(42); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	msg, err := ch.Receive()
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}
	if msg != 42 {
		t.Errorf("got %d, want 42", msg)
	}
}

func TestAdaptiveChannel_SendTimeout(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 1, MaxSize: 1, SendTimeout: 50 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	_ = ch.Send(1)

	err := ch.SendTimeout(2, 50*time.Millisecond)
	if err != ErrSendTimeout {
		t.Errorf("got error %v, want %v", err, ErrSendTimeout)
	}
}

func TestAdaptiveChannel_SendTimeout_OverflowAllowed(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 1, MaxSize: 1, AllowOverflow: true, SendTimeout: 50 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	_ = ch.Send(1)

	err := ch.SendTimeout(2, 50*time.Millisecond)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if ch.OverflowLen() == 0 {
		t.Error("expected overflow to be used")
	}
}

func TestAdaptiveChannel_ReceiveTimeout(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	_, err := ch.ReceiveTimeout(50 * time.Millisecond)
	if err != ErrReceiveTimeout {
		t.Errorf("got error %v, want %v", err, ErrReceiveTimeout)
	}
}

func TestAdaptiveChannel_TryReceive(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	_, ok := ch.TryReceive()
	if ok {
		t.Error("TryReceive should return false on empty channel")
	}

	_ = ch.Send(42)
	msg, ok := ch.TryReceive()
	if !ok {
		t.Error("TryReceive should return true after send")
	}
	if msg != 42 {
		t.Errorf("got %d, want 42", msg)
	}
}

func TestAdaptiveChannel_Close(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	ch.Close()

	if !ch.IsClosed() {
		t.Error("channel should be closed")
	}

	err := ch.Send(1)
	if err != ErrChannelClosed {
		t.Errorf("got error %v, want %v", err, ErrChannelClosed)
	}
}

func TestAdaptiveChannel_ResizeUp(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 4, MaxSize: 32, AllowOverflow: true, SendTimeout: 10 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	for i := 0; i < 10; i++ {
		_ = ch.SendTimeout(i, 10*time.Millisecond)
	}

	time.Sleep(AdaptInterval * 4)

	stats := ch.Stats()
	if stats.ResizeUpCount == 0 {
		t.Error("expected at least one resize up")
	}
	if ch.Size() <= 4 {
		t.Errorf("expected size > 4, got %d", ch.Size())
	}
}

func TestAdaptiveChannel_Overflow(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 2, MaxSize: 2, AllowOverflow: true, SendTimeout: 10 * time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	for i := 0; i < 5; i++ {
		_ = ch.SendTimeout(i, 10*time.Millisecond)
	}

	if ch.OverflowLen() == 0 {
		t.Error("expected overflow to be used")
	}

	received := make(map[int]bool)
	for i := 0; i < 5; i++ {
		msg, err := ch.ReceiveTimeout(100 * time.Millisecond)
		if err != nil {
			t.Fatalf("Receive %d failed: %v", i, err)
		}
		received[msg] = true
	}

	for i := 0; i < 5; i++ {
		if !received[i] {
			t.Errorf("missing value %d", i)
		}
	}
}

func TestAdaptiveChannel_Stats(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	_ = ch.Send(1)
	_ = ch.Send(2)
	_, _ = ch.Receive()

	stats := ch.Stats()
	if stats.SendCount != 2 {
		t.Errorf("got SendCount %d, want 2", stats.SendCount)
	}
	if stats.ReceiveCount != 1 {
		t.Errorf("got ReceiveCount %d, want 1", stats.ReceiveCount)
	}
	if stats.MessageCount != 1 {
		t.Errorf("got MessageCount %d, want 1", stats.MessageCount)
	}
}

func TestAdaptiveChannel_EnableDisableOverflow(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	if err := ch.EnableOverflow(); err != nil {
		t.Errorf("EnableOverflow failed: %v", err)
	}

	err := ch.EnableOverflow()
	if err != ErrOverflowEnabled {
		t.Errorf("got error %v, want %v", err, ErrOverflowEnabled)
	}

	ch.DisableOverflow()
}

func TestAdaptiveChannel_ConcurrentSendReceive(t *testing.T) {
	ch := NewAdaptiveChannel[int](DefaultAdaptiveChannelConfig())
	defer ch.Close()

	var wg sync.WaitGroup
	count := 100

	wg.Add(2)
	go sendN(ch, count, &wg)
	go receiveN(ch, count, &wg)
	wg.Wait()

	stats := ch.Stats()
	if stats.SendCount != int64(count) {
		t.Errorf("got SendCount %d, want %d", stats.SendCount, count)
	}
}

func sendN(ch *AdaptiveChannel[int], n int, wg *sync.WaitGroup) {
	defer wg.Done()
	for i := 0; i < n; i++ {
		ch.Send(i)
	}
}

func receiveN(ch *AdaptiveChannel[int], n int, wg *sync.WaitGroup) {
	defer wg.Done()
	for i := 0; i < n; i++ {
		ch.ReceiveTimeout(time.Second)
	}
}

func TestAdaptiveChannel_MaxSizeRespected(t *testing.T) {
	config := AdaptiveChannelConfig{MinSize: 4, MaxSize: 8, SendTimeout: time.Millisecond}
	ch := NewAdaptiveChannel[int](config)
	defer ch.Close()

	for i := 0; i < 20; i++ {
		ch.SendTimeout(i, time.Millisecond)
	}

	time.Sleep(AdaptInterval * 4)

	if ch.Size() > 8 {
		t.Errorf("got size %d, want <= 8", ch.Size())
	}
}

func TestDefaultAdaptiveChannelConfig(t *testing.T) {
	config := DefaultAdaptiveChannelConfig()

	if config.MinSize != DefaultMinSize {
		t.Errorf("got MinSize %d, want %d", config.MinSize, DefaultMinSize)
	}
	if config.MaxSize != DefaultMaxSize {
		t.Errorf("got MaxSize %d, want %d", config.MaxSize, DefaultMaxSize)
	}
	if config.AllowOverflow {
		t.Error("AllowOverflow should be false by default")
	}
}

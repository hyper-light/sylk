package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrChannelClosed   = errors.New("channel is closed")
	ErrSendTimeout     = errors.New("send timed out")
	ErrReceiveTimeout  = errors.New("receive timed out")
	ErrOverflowEnabled = errors.New("overflow already enabled")
)

const (
	DefaultMinSize     = 16
	DefaultMaxSize     = 4096
	DefaultInitSize    = 64
	HighWaterThreshold = 0.8
	LowWaterThreshold  = 0.2
	HighWaterTrigger   = 3
	LowWaterTrigger    = 10
	AdaptInterval      = 1 * time.Second
)

type ChannelStats struct {
	CurrentSize     int
	MessageCount    int
	OverflowCount   int
	ResizeUpCount   int64
	ResizeDownCount int64
	SendCount       int64
	ReceiveCount    int64
}

type AdaptiveChannelConfig struct {
	MinSize       int
	MaxSize       int
	InitialSize   int
	AllowOverflow bool
	SendTimeout   time.Duration
}

func DefaultAdaptiveChannelConfig() AdaptiveChannelConfig {
	return AdaptiveChannelConfig{
		MinSize:       DefaultMinSize,
		MaxSize:       DefaultMaxSize,
		InitialSize:   DefaultMinSize,
		AllowOverflow: false,
		SendTimeout:   0,
	}
}

type AdaptiveChannel[T any] struct {
	config AdaptiveChannelConfig

	mu           sync.Mutex
	ch           chan T
	overflow     []T
	currentSize  int
	highWaterCnt int
	lowWaterCnt  int

	closed     atomic.Bool
	stopCh     chan struct{}
	closeOnce  sync.Once
	resizeUp   atomic.Int64
	resizeDown atomic.Int64
	sendCount  atomic.Int64
	recvCount  atomic.Int64
}

func NewAdaptiveChannel[T any](config AdaptiveChannelConfig) *AdaptiveChannel[T] {
	config = normalizeAdaptiveChannelConfig(config)
	ac := &AdaptiveChannel[T]{
		config:      config,
		ch:          make(chan T, config.InitialSize),
		overflow:    make([]T, 0),
		currentSize: config.InitialSize,
		stopCh:      make(chan struct{}),
	}
	go ac.adaptLoop()
	return ac
}

func normalizeAdaptiveChannelConfig(cfg AdaptiveChannelConfig) AdaptiveChannelConfig {
	if cfg.MinSize <= 0 {
		cfg.MinSize = DefaultMinSize
	}
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = DefaultMaxSize
	}
	if cfg.MaxSize < cfg.MinSize {
		cfg.MaxSize = cfg.MinSize
	}
	if cfg.InitialSize <= 0 {
		cfg.InitialSize = cfg.MinSize
	}
	if cfg.InitialSize < cfg.MinSize {
		cfg.InitialSize = cfg.MinSize
	}
	if cfg.InitialSize > cfg.MaxSize {
		cfg.InitialSize = cfg.MaxSize
	}
	if cfg.SendTimeout < 0 {
		cfg.SendTimeout = 0
	}
	return cfg
}

func (ac *AdaptiveChannel[T]) Send(msg T) error {
	return ac.SendWithContext(context.Background(), msg)
}

func (ac *AdaptiveChannel[T]) SendWithContext(ctx context.Context, msg T) error {
	if ac.closed.Load() {
		return ErrChannelClosed
	}

	ac.sendCount.Add(1)
	if ac.trySendFast(msg) {
		return nil
	}

	timeout := ac.config.SendTimeout
	if timeout <= 0 {
		return ac.blockSend(ctx, msg)
	}

	return ac.sendWithTimeout(ctx, msg, timeout)
}

func (ac *AdaptiveChannel[T]) trySendFast(msg T) bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if ac.closed.Load() {
		return false
	}

	select {
	case ac.ch <- msg:
		return true
	default:
		return false
	}
}

func (ac *AdaptiveChannel[T]) blockSend(ctx context.Context, msg T) error {
	ac.mu.Lock()
	ch := ac.ch
	closed := ac.closed.Load()
	ac.mu.Unlock()

	if closed {
		return ErrChannelClosed
	}

	select {
	case ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ac *AdaptiveChannel[T]) sendWithTimeout(ctx context.Context, msg T, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ac.mu.Lock()
	ch := ac.ch
	closed := ac.closed.Load()
	allowOverflow := ac.config.AllowOverflow
	ac.mu.Unlock()

	if closed {
		return ErrChannelClosed
	}

	select {
	case ch <- msg:
		return nil
	case <-timer.C:
		if allowOverflow {
			ac.enqueueOverflow(msg)
			return nil
		}
		return ErrSendTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ac *AdaptiveChannel[T]) enqueueOverflow(msg T) {
	ac.mu.Lock()
	if !ac.closed.Load() {
		ac.overflow = append(ac.overflow, msg)
	}
	ac.mu.Unlock()
}

func (ac *AdaptiveChannel[T]) SendTimeout(msg T, timeout time.Duration) error {
	if timeout <= 0 {
		return ac.Send(msg)
	}
	return ac.sendWithTimeout(context.Background(), msg, timeout)
}

func (ac *AdaptiveChannel[T]) SendTimeoutWithContext(ctx context.Context, msg T, timeout time.Duration) error {
	if timeout <= 0 {
		return ac.SendWithContext(ctx, msg)
	}
	return ac.sendWithTimeout(ctx, msg, timeout)
}

func (ac *AdaptiveChannel[T]) Receive() (T, error) {
	return ac.ReceiveWithContext(context.Background())
}

func (ac *AdaptiveChannel[T]) ReceiveWithContext(ctx context.Context) (T, error) {
	var zero T
	if ac.isClosedAndEmpty() {
		return zero, ErrChannelClosed
	}

	if msg, ok := ac.drainOverflow(); ok {
		ac.recvCount.Add(1)
		return msg, nil
	}

	ac.mu.Lock()
	ch := ac.ch
	ac.mu.Unlock()

	select {
	case msg, ok := <-ch:
		if !ok {
			return zero, ErrChannelClosed
		}
		ac.recvCount.Add(1)
		return msg, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (ac *AdaptiveChannel[T]) ReceiveTimeout(timeout time.Duration) (T, error) {
	var zero T
	if ac.isClosedAndEmpty() {
		return zero, ErrChannelClosed
	}

	if msg, ok := ac.drainOverflow(); ok {
		ac.recvCount.Add(1)
		return msg, nil
	}

	ac.mu.Lock()
	ch := ac.ch
	ac.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg, ok := <-ch:
		if !ok {
			return zero, ErrChannelClosed
		}
		ac.recvCount.Add(1)
		return msg, nil
	case <-timer.C:
		return zero, ErrReceiveTimeout
	}
}

func (ac *AdaptiveChannel[T]) TryReceive() (T, bool) {
	var zero T
	if ac.isClosedAndEmpty() {
		return zero, false
	}

	if msg, ok := ac.drainOverflow(); ok {
		ac.recvCount.Add(1)
		return msg, true
	}

	ac.mu.Lock()
	ch := ac.ch
	ac.mu.Unlock()

	select {
	case msg, ok := <-ch:
		if !ok {
			return zero, false
		}
		ac.recvCount.Add(1)
		return msg, true
	default:
		return zero, false
	}
}

func (ac *AdaptiveChannel[T]) Size() int {
	ac.mu.Lock()
	size := ac.currentSize
	ac.mu.Unlock()
	return size
}

func (ac *AdaptiveChannel[T]) Len() int {
	ac.mu.Lock()
	length := len(ac.ch)
	ac.mu.Unlock()
	return length
}

func (ac *AdaptiveChannel[T]) OverflowLen() int {
	ac.mu.Lock()
	length := len(ac.overflow)
	ac.mu.Unlock()
	return length
}

func (ac *AdaptiveChannel[T]) Stats() ChannelStats {
	ac.mu.Lock()
	stats := ChannelStats{
		CurrentSize:     ac.currentSize,
		MessageCount:    len(ac.ch),
		OverflowCount:   len(ac.overflow),
		ResizeUpCount:   ac.resizeUp.Load(),
		ResizeDownCount: ac.resizeDown.Load(),
		SendCount:       ac.sendCount.Load(),
		ReceiveCount:    ac.recvCount.Load(),
	}
	ac.mu.Unlock()
	return stats
}

func (ac *AdaptiveChannel[T]) Close() {
	ac.closeOnce.Do(func() {
		ac.closed.Store(true)
		close(ac.stopCh)
		ac.mu.Lock()
		close(ac.ch)
		ac.mu.Unlock()
	})
}

func (ac *AdaptiveChannel[T]) IsClosed() bool {
	return ac.closed.Load()
}

func (ac *AdaptiveChannel[T]) EnableOverflow() error {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if ac.config.AllowOverflow {
		return ErrOverflowEnabled
	}
	ac.config.AllowOverflow = true
	return nil
}

func (ac *AdaptiveChannel[T]) DisableOverflow() {
	ac.mu.Lock()
	ac.config.AllowOverflow = false
	ac.mu.Unlock()
}

func (ac *AdaptiveChannel[T]) adaptLoop() {
	ticker := time.NewTicker(AdaptInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ac.stopCh:
			return
		case <-ticker.C:
			ac.evaluateResize()
		}
	}
}

func (ac *AdaptiveChannel[T]) evaluateResize() {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if ac.closed.Load() {
		return
	}

	utilization := ac.utilization()
	ac.adjustWatermarks(utilization)
	ac.maybeResize(utilization)
}

func (ac *AdaptiveChannel[T]) utilization() float64 {
	if ac.currentSize == 0 {
		return 0
	}
	return float64(len(ac.ch)) / float64(ac.currentSize)
}

func (ac *AdaptiveChannel[T]) adjustWatermarks(utilization float64) {
	if utilization >= HighWaterThreshold {
		ac.highWaterCnt++
		ac.lowWaterCnt = 0
		return
	}
	if utilization <= LowWaterThreshold {
		ac.lowWaterCnt++
		ac.highWaterCnt = 0
		return
	}
	ac.highWaterCnt = 0
	ac.lowWaterCnt = 0
}

func (ac *AdaptiveChannel[T]) maybeResize(utilization float64) {
	if utilization >= HighWaterThreshold {
		ac.resizeUpIfNeeded()
		return
	}
	if utilization <= LowWaterThreshold {
		ac.resizeDownIfNeeded()
	}
}

func (ac *AdaptiveChannel[T]) resizeUpIfNeeded() {
	if ac.highWaterCnt < HighWaterTrigger {
		return
	}
	if ac.currentSize >= ac.config.MaxSize {
		return
	}

	newSize := min(ac.currentSize*2, ac.config.MaxSize)
	ac.resizeLocked(newSize)
	ac.resizeUp.Add(1)
}

func (ac *AdaptiveChannel[T]) resizeDownIfNeeded() {
	if ac.lowWaterCnt < LowWaterTrigger {
		return
	}
	if ac.currentSize <= ac.config.MinSize {
		return
	}

	newSize := max(ac.currentSize/2, ac.config.MinSize)
	ac.resizeLocked(newSize)
	ac.resizeDown.Add(1)
}

func (ac *AdaptiveChannel[T]) resizeLocked(newSize int) {
	if newSize == ac.currentSize {
		return
	}

	newCh := make(chan T, newSize)
	for {
		select {
		case msg := <-ac.ch:
			newCh <- msg
		default:
			ac.ch = newCh
			ac.currentSize = newSize
			ac.highWaterCnt = 0
			ac.lowWaterCnt = 0
			return
		}
	}
}

func (ac *AdaptiveChannel[T]) isClosedAndEmpty() bool {
	ac.mu.Lock()
	closed := ac.closed.Load()
	empty := len(ac.ch) == 0 && len(ac.overflow) == 0
	ac.mu.Unlock()
	return closed && empty
}

func (ac *AdaptiveChannel[T]) drainOverflow() (T, bool) {
	var zero T
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if len(ac.overflow) == 0 {
		return zero, false
	}

	msg := ac.overflow[0]
	ac.overflow[0] = zero
	ac.overflow = ac.overflow[1:]
	return msg, true
}

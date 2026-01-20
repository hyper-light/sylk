package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrChannelClosed    = errors.New("channel is closed")
	ErrSendTimeout      = errors.New("send timed out")
	ErrReceiveTimeout   = errors.New("receive timed out")
	ErrOverflowEnabled  = errors.New("overflow already enabled")
	ErrOverflowFull     = errors.New("overflow buffer is full")
	ErrNoOverflowBuffer = errors.New("no overflow buffer configured")
)

const (
	DefaultMinSize             = 16
	DefaultMaxSize             = 4096
	DefaultInitSize            = 64
	DefaultAdaptiveMaxOverflow = 10000
	HighWaterThreshold         = 0.8
	LowWaterThreshold          = 0.2
	HighWaterTrigger           = 3
	LowWaterTrigger            = 10
	AdaptInterval              = 1 * time.Second
)

// ChannelStats contains statistics for an AdaptiveChannel.
type ChannelStats struct {
	CurrentSize      int   // Current channel buffer size
	MessageCount     int   // Messages in channel buffer
	OverflowCount    int   // Messages in overflow buffer
	OverflowCapacity int   // Max overflow capacity (-1 if unbounded)
	DroppedCount     int64 // Messages dropped due to overflow full
	ResizeUpCount    int64
	ResizeDownCount  int64
	SendCount        int64
	ReceiveCount     int64
}

// AdaptiveChannelConfig configures an AdaptiveChannel.
type AdaptiveChannelConfig struct {
	MinSize       int
	MaxSize       int
	InitialSize   int
	AllowOverflow bool
	SendTimeout   time.Duration

	// MaxOverflowSize is the maximum number of items in the overflow buffer.
	// Set to 0 for unlimited (legacy behavior, not recommended).
	// Default: 10000
	MaxOverflowSize int

	// OverflowDropPolicy determines behavior when overflow is full.
	// Only applies when MaxOverflowSize > 0 and AllowOverflow is true.
	// Default: DropNewest (reject new messages when full)
	OverflowDropPolicy DropPolicy
}

// DefaultAdaptiveChannelConfig returns sensible defaults for AdaptiveChannel.
func DefaultAdaptiveChannelConfig() AdaptiveChannelConfig {
	return AdaptiveChannelConfig{
		MinSize:            DefaultMinSize,
		MaxSize:            DefaultMaxSize,
		InitialSize:        DefaultMinSize,
		AllowOverflow:      false,
		SendTimeout:        0,
		MaxOverflowSize:    DefaultAdaptiveMaxOverflow,
		OverflowDropPolicy: DropNewest,
	}
}

// AdaptiveChannel is a channel that automatically resizes based on usage patterns.
// It supports optional overflow buffering when the channel is full during send timeout.
//
// With MaxOverflowSize > 0 and AllowOverflow=true, the overflow buffer is bounded
// to prevent memory exhaustion. When overflow is full, behavior depends on OverflowDropPolicy:
//   - DropOldest: evict oldest message to make room for new one
//   - DropNewest: reject new message (returns error from Send)
//   - Block: wait until space is available (not recommended)
//
// With MaxOverflowSize = 0, the overflow grows without bound (legacy behavior).
type AdaptiveChannel[T any] struct {
	config AdaptiveChannelConfig

	mu            sync.Mutex
	ch            chan T
	boundedOvfl   *BoundedOverflow[T] // Used when MaxOverflowSize > 0
	unboundedOvfl []T                 // Used when MaxOverflowSize = 0 (legacy)
	currentSize   int
	highWaterCnt  int
	lowWaterCnt   int

	// version is incremented on each resize to detect stale channel references
	version atomic.Uint64

	closed       atomic.Bool
	stopCh       chan struct{}
	closeOnce    sync.Once
	resizeUp     atomic.Int64
	resizeDown   atomic.Int64
	sendCount    atomic.Int64
	recvCount    atomic.Int64
	droppedCount atomic.Int64

	// Context for cancellation propagation
	ctx context.Context
}

// NewAdaptiveChannel creates a new adaptive channel with the given configuration.
// Deprecated: Use NewAdaptiveChannelWithContext instead for proper context propagation.
func NewAdaptiveChannel[T any](config AdaptiveChannelConfig) *AdaptiveChannel[T] {
	return NewAdaptiveChannelWithContext[T](context.Background(), config)
}

// NewAdaptiveChannelWithContext creates a new adaptive channel with context for cancellation propagation.
// The context is used to signal the adapt loop goroutine to stop when the parent context is cancelled.
func NewAdaptiveChannelWithContext[T any](ctx context.Context, config AdaptiveChannelConfig) *AdaptiveChannel[T] {
	config = normalizeAdaptiveChannelConfig(config)
	ac := &AdaptiveChannel[T]{
		config:      config,
		ch:          make(chan T, config.InitialSize),
		currentSize: config.InitialSize,
		stopCh:      make(chan struct{}),
		ctx:         ctx,
	}

	// Initialize overflow buffer based on configuration
	if config.MaxOverflowSize > 0 {
		// Bounded overflow mode (recommended)
		ac.boundedOvfl = NewBoundedOverflow[T](config.MaxOverflowSize, config.OverflowDropPolicy)
	} else {
		// Legacy unbounded mode (not recommended)
		ac.unboundedOvfl = make([]T, 0)
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
	// Get channel reference under lock, then release lock to avoid blocking other operations.
	// The resize operation drains all messages from old channel to new one, so even if
	// we send to an "old" channel, the message will be transferred during resize.
	// We use version tracking to detect if a resize happened and retry if needed.
	for {
		if ac.closed.Load() {
			return ErrChannelClosed
		}

		ac.mu.Lock()
		ch := ac.ch
		ver := ac.version.Load()
		ac.mu.Unlock()

		// Non-blocking send attempt first
		select {
		case ch <- msg:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Channel is full, check if we should retry due to resize
		}

		// Check if resize happened, if so retry to get fresh channel
		if ac.version.Load() != ver {
			continue
		}

		// Do blocking send with context cancellation
		select {
		case ch <- msg:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (ac *AdaptiveChannel[T]) sendWithTimeout(ctx context.Context, msg T, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ac.mu.Lock()
	closed := ac.closed.Load()
	allowOverflow := ac.config.AllowOverflow
	ch := ac.ch
	ac.mu.Unlock()

	if closed {
		return ErrChannelClosed
	}

	select {
	case ch <- msg:
		return nil
	case <-timer.C:
		if allowOverflow {
			if err := ac.enqueueOverflow(msg); err != nil {
				// Overflow is full - return the error (ErrOverflowFull)
				return err
			}
			return nil
		}
		return ErrSendTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enqueueOverflow adds a message to the overflow buffer.
// Returns an error if the overflow is full and using DropNewest policy.
func (ac *AdaptiveChannel[T]) enqueueOverflow(msg T) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if ac.closed.Load() {
		return ErrChannelClosed
	}

	if ac.boundedOvfl != nil {
		// Bounded overflow mode
		if !ac.boundedOvfl.Add(msg) {
			// DropNewest policy - message was rejected
			ac.droppedCount.Add(1)
			return ErrOverflowFull
		}
		return nil
	}

	// Legacy unbounded mode
	ac.unboundedOvfl = append(ac.unboundedOvfl, msg)
	return nil
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

	// Get channel reference under lock, then release.
	// If channel is replaced during resize, our pending receive on old channel
	// will be served as resize drains the old channel.
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

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// Get channel reference under lock, then release.
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

	// Get channel reference under lock, then release.
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
	length := ac.overflowLenLocked()
	ac.mu.Unlock()
	return length
}

// overflowLenLocked returns the overflow length. Caller must hold mu.
func (ac *AdaptiveChannel[T]) overflowLenLocked() int {
	if ac.boundedOvfl != nil {
		return ac.boundedOvfl.Len()
	}
	return len(ac.unboundedOvfl)
}

func (ac *AdaptiveChannel[T]) Stats() ChannelStats {
	ac.mu.Lock()
	var overflowCap int
	var droppedFromOvfl int64

	if ac.boundedOvfl != nil {
		overflowCap = ac.boundedOvfl.Cap()
		droppedFromOvfl = ac.boundedOvfl.DroppedCount()
	} else {
		overflowCap = -1 // Unbounded
	}

	stats := ChannelStats{
		CurrentSize:      ac.currentSize,
		MessageCount:     len(ac.ch),
		OverflowCount:    ac.overflowLenLocked(),
		OverflowCapacity: overflowCap,
		DroppedCount:     ac.droppedCount.Load() + droppedFromOvfl,
		ResizeUpCount:    ac.resizeUp.Load(),
		ResizeDownCount:  ac.resizeDown.Load(),
		SendCount:        ac.sendCount.Load(),
		ReceiveCount:     ac.recvCount.Load(),
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
		if ac.boundedOvfl != nil {
			ac.boundedOvfl.Close()
		}
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
		case <-ac.ctx.Done():
			// Context cancelled - exit the adapt loop
			// Note: We don't call Close() here to avoid racing with ongoing operations.
			// The caller is responsible for closing the channel when appropriate.
			return
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
	oldCh := ac.ch

	// Drain all messages from old channel to new channel
	for {
		select {
		case msg := <-oldCh:
			newCh <- msg
		default:
			goto swapChannel
		}
	}

swapChannel:
	// Atomically swap the channel reference
	ac.ch = newCh
	ac.currentSize = newSize
	ac.highWaterCnt = 0
	ac.lowWaterCnt = 0
	ac.version.Add(1) // Increment version to signal channel change

	// After swap, any messages sent to oldCh by blocked senders will be
	// received here and forwarded to newCh. We drain until no more messages
	// arrive (giving senders time to complete their blocked sends).
	// This is a best-effort approach - we can't guarantee we catch all messages
	// if new senders arrive continuously.
	for {
		select {
		case msg := <-oldCh:
			newCh <- msg
		default:
			// No more messages in old channel
			return
		}
	}
}

func (ac *AdaptiveChannel[T]) isClosedAndEmpty() bool {
	ac.mu.Lock()
	closed := ac.closed.Load()
	empty := len(ac.ch) == 0 && ac.overflowLenLocked() == 0
	ac.mu.Unlock()
	return closed && empty
}

func (ac *AdaptiveChannel[T]) drainOverflow() (T, bool) {
	var zero T
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if ac.boundedOvfl != nil {
		// Bounded overflow mode
		return ac.boundedOvfl.Take()
	}

	// Legacy unbounded mode
	if len(ac.unboundedOvfl) == 0 {
		return zero, false
	}

	msg := ac.unboundedOvfl[0]
	ac.unboundedOvfl[0] = zero // Clear reference for GC
	ac.unboundedOvfl = ac.unboundedOvfl[1:]
	return msg, true
}

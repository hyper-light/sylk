package concurrency

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	ErrChannelClosed   = errors.New("channel is closed")
	ErrSendTimeout     = errors.New("send timed out")
	ErrReceiveTimeout  = errors.New("receive timed out")
	ErrOverflowEnabled = errors.New("overflow already enabled")
)

const (
	DefaultMinSize      = 16
	DefaultMaxSize      = 4096
	ResizeUpThreshold   = 0.8
	ResizeDownThreshold = 0.2
	ResizeDownDelay     = time.Minute
	ResizeCheckInterval = 100 * time.Millisecond
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
	AllowOverflow bool
}

func DefaultAdaptiveChannelConfig() AdaptiveChannelConfig {
	return AdaptiveChannelConfig{
		MinSize:       DefaultMinSize,
		MaxSize:       DefaultMaxSize,
		AllowOverflow: false,
	}
}

type channelState[T any] struct {
	ch   chan T
	size int
}

type AdaptiveChannel[T any] struct {
	config        AdaptiveChannelConfig
	state         unsafe.Pointer
	overflow      []T
	closed        atomic.Bool
	lastLowUsage  atomic.Int64
	resizeUp      atomic.Int64
	resizeDown    atomic.Int64
	sendCount     atomic.Int64
	receiveCount  atomic.Int64
	allowOverflow atomic.Bool
	mu            sync.Mutex
}

func NewAdaptiveChannel[T any](config AdaptiveChannelConfig) *AdaptiveChannel[T] {
	if config.MinSize <= 0 {
		config.MinSize = DefaultMinSize
	}
	if config.MaxSize <= 0 {
		config.MaxSize = DefaultMaxSize
	}
	if config.MaxSize < config.MinSize {
		config.MaxSize = config.MinSize
	}

	initial := &channelState[T]{
		ch:   make(chan T, config.MinSize),
		size: config.MinSize,
	}

	ac := &AdaptiveChannel[T]{
		config:   config,
		overflow: make([]T, 0),
	}
	atomic.StorePointer(&ac.state, unsafe.Pointer(initial))
	ac.allowOverflow.Store(config.AllowOverflow)

	return ac
}

func (ac *AdaptiveChannel[T]) getState() *channelState[T] {
	return (*channelState[T])(atomic.LoadPointer(&ac.state))
}

func (ac *AdaptiveChannel[T]) Send(msg T) error {
	if ac.closed.Load() {
		return ErrChannelClosed
	}

	ac.sendCount.Add(1)
	ac.checkResize()

	s := ac.getState()
	select {
	case s.ch <- msg:
		return nil
	default:
		return ac.handleFullChannel(msg)
	}
}

func (ac *AdaptiveChannel[T]) handleFullChannel(msg T) error {
	if ac.tryResizeUp() {
		return ac.trySendAfterResize(msg)
	}
	return ac.handleOverflowOrBlock(msg)
}

func (ac *AdaptiveChannel[T]) trySendAfterResize(msg T) error {
	s := ac.getState()
	select {
	case s.ch <- msg:
		return nil
	default:
		return ac.handleOverflowOrBlock(msg)
	}
}

func (ac *AdaptiveChannel[T]) handleOverflowOrBlock(msg T) error {
	if !ac.allowOverflow.Load() {
		s := ac.getState()
		s.ch <- msg
		return nil
	}

	ac.mu.Lock()
	ac.overflow = append(ac.overflow, msg)
	ac.mu.Unlock()
	return nil
}

func (ac *AdaptiveChannel[T]) SendTimeout(msg T, timeout time.Duration) error {
	if ac.closed.Load() {
		return ErrChannelClosed
	}

	ac.sendCount.Add(1)
	ac.checkResize()

	s := ac.getState()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case s.ch <- msg:
		return nil
	case <-timer.C:
		return ErrSendTimeout
	}
}

func (ac *AdaptiveChannel[T]) Receive() (T, error) {
	var zero T
	if ac.isClosedAndEmpty() {
		return zero, ErrChannelClosed
	}

	if msg, ok := ac.drainOverflow(); ok {
		ac.receiveCount.Add(1)
		return msg, nil
	}

	s := ac.getState()
	msg := <-s.ch
	ac.receiveCount.Add(1)
	ac.checkResizeDown()
	return msg, nil
}

func (ac *AdaptiveChannel[T]) isClosedAndEmpty() bool {
	s := ac.getState()
	return ac.closed.Load() && len(s.ch) == 0 && ac.OverflowLen() == 0
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

func (ac *AdaptiveChannel[T]) ReceiveTimeout(timeout time.Duration) (T, error) {
	var zero T
	if ac.isClosedAndEmpty() {
		return zero, ErrChannelClosed
	}

	if msg, ok := ac.drainOverflow(); ok {
		ac.receiveCount.Add(1)
		return msg, nil
	}

	return ac.receiveWithTimeout(timeout)
}

func (ac *AdaptiveChannel[T]) receiveWithTimeout(timeout time.Duration) (T, error) {
	var zero T
	s := ac.getState()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg := <-s.ch:
		ac.receiveCount.Add(1)
		ac.checkResizeDown()
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
		ac.receiveCount.Add(1)
		return msg, true
	}

	return ac.tryReceiveFromChannel()
}

func (ac *AdaptiveChannel[T]) tryReceiveFromChannel() (T, bool) {
	var zero T
	s := ac.getState()

	select {
	case msg := <-s.ch:
		ac.receiveCount.Add(1)
		ac.checkResizeDown()
		return msg, true
	default:
		return zero, false
	}
}

func (ac *AdaptiveChannel[T]) Size() int {
	return ac.getState().size
}

func (ac *AdaptiveChannel[T]) Len() int {
	return len(ac.getState().ch)
}

func (ac *AdaptiveChannel[T]) OverflowLen() int {
	ac.mu.Lock()
	n := len(ac.overflow)
	ac.mu.Unlock()
	return n
}

func (ac *AdaptiveChannel[T]) Stats() ChannelStats {
	s := ac.getState()
	ac.mu.Lock()
	overflowLen := len(ac.overflow)
	ac.mu.Unlock()

	return ChannelStats{
		CurrentSize:     s.size,
		MessageCount:    len(s.ch),
		OverflowCount:   overflowLen,
		ResizeUpCount:   ac.resizeUp.Load(),
		ResizeDownCount: ac.resizeDown.Load(),
		SendCount:       ac.sendCount.Load(),
		ReceiveCount:    ac.receiveCount.Load(),
	}
}

func (ac *AdaptiveChannel[T]) Close() {
	ac.closed.Store(true)
	s := ac.getState()
	close(s.ch)
}

func (ac *AdaptiveChannel[T]) IsClosed() bool {
	return ac.closed.Load()
}

func (ac *AdaptiveChannel[T]) checkResize() {
	usage := ac.usageRatio()
	if usage > ResizeUpThreshold {
		ac.tryResizeUp()
	}
}

func (ac *AdaptiveChannel[T]) checkResizeDown() {
	usage := ac.usageRatio()
	if usage >= ResizeDownThreshold {
		ac.lastLowUsage.Store(0)
		return
	}

	if ac.shouldResizeDown() {
		ac.resizeDown.Add(1)
		ac.doResizeDown()
	}
}

func (ac *AdaptiveChannel[T]) usageRatio() float64 {
	s := ac.getState()
	if s.size == 0 {
		return 0
	}
	return float64(len(s.ch)) / float64(s.size)
}

func (ac *AdaptiveChannel[T]) shouldResizeDown() bool {
	s := ac.getState()
	if s.size <= ac.config.MinSize {
		return false
	}

	now := time.Now().UnixNano()
	last := ac.lastLowUsage.Load()
	if last == 0 {
		ac.lastLowUsage.Store(now)
		return false
	}

	return time.Duration(now-last) >= ResizeDownDelay
}

func (ac *AdaptiveChannel[T]) tryResizeUp() bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	s := ac.getState()
	if s.size >= ac.config.MaxSize {
		return false
	}

	newSize := s.size * 2
	if newSize > ac.config.MaxSize {
		newSize = ac.config.MaxSize
	}

	ac.resize(s, newSize)
	ac.resizeUp.Add(1)
	return true
}

func (ac *AdaptiveChannel[T]) doResizeDown() {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	s := ac.getState()
	newSize := s.size / 2
	if newSize < ac.config.MinSize {
		newSize = ac.config.MinSize
	}

	if newSize < s.size {
		ac.resize(s, newSize)
		ac.lastLowUsage.Store(0)
	}
}

func (ac *AdaptiveChannel[T]) resize(old *channelState[T], newSize int) {
	newCh := make(chan T, newSize)

	for {
		select {
		case msg := <-old.ch:
			newCh <- msg
		default:
			newState := &channelState[T]{ch: newCh, size: newSize}
			atomic.StorePointer(&ac.state, unsafe.Pointer(newState))
			return
		}
	}
}

func (ac *AdaptiveChannel[T]) EnableOverflow() error {
	if ac.allowOverflow.Load() {
		return ErrOverflowEnabled
	}
	ac.allowOverflow.Store(true)
	return nil
}

func (ac *AdaptiveChannel[T]) DisableOverflow() {
	ac.allowOverflow.Store(false)
}

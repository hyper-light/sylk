package watcher

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultDedupeWindow is the default time window for deduplication.
const DefaultDedupeWindow = time.Second

// DefaultRateLimit is the default maximum events per second.
const DefaultRateLimit = 100

// cleanupInterval is how often old dedupe entries are cleaned.
const cleanupInterval = 30 * time.Second

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrDetectorAlreadyRunning indicates the detector is already running.
	ErrDetectorAlreadyRunning = errors.New("change detector is already running")
)

// Note: ChangeSource, FileOperation, and ChangeEvent are defined in types.go.

// =============================================================================
// ChangeDetectorConfig
// =============================================================================

// ChangeDetectorConfig configures the change detection coordinator.
type ChangeDetectorConfig struct {
	// DedupeWindow is the time window for deduplication.
	// Events for the same path within this window are deduplicated.
	// Default: 1 second
	DedupeWindow time.Duration

	// RateLimit is the maximum events per second to emit.
	// Events beyond this limit are dropped (with logging).
	// Default: 100
	RateLimit int
}

// DefaultChangeDetectorConfig returns a configuration with default values.
func DefaultChangeDetectorConfig() ChangeDetectorConfig {
	return ChangeDetectorConfig{
		DedupeWindow: DefaultDedupeWindow,
		RateLimit:    DefaultRateLimit,
	}
}

// =============================================================================
// dedupeEntry
// =============================================================================

// dedupeEntry tracks when a path was last seen and from which source.
type dedupeEntry struct {
	time   time.Time
	source ChangeSource
}

// =============================================================================
// ChangeDetector
// =============================================================================

// ChangeDetector coordinates all change detection sources.
// It merges events from fsnotify, git hooks, and periodic scanning,
// providing deduplication, priority handling, and rate limiting.
type ChangeDetector struct {
	config ChangeDetectorConfig

	// Event source channels
	fsEvents       <-chan *ChangeEvent
	gitEvents      <-chan *ChangeEvent
	periodicEvents <-chan *ChangeEvent

	// Deduplication state
	seen   map[string]*dedupeEntry
	seenMu sync.RWMutex

	// Rate limiting state
	tokens     int
	tokenMu    sync.Mutex
	lastRefill time.Time

	// Lifecycle state
	running atomic.Bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewChangeDetector creates a new change detection coordinator.
// Sources are configured separately via SetFSNotifySource, etc.
func NewChangeDetector(config ChangeDetectorConfig) *ChangeDetector {
	return newDetector(config, nil, nil, nil)
}

// NewChangeDetectorWithSources creates a detector with pre-configured sources.
// This is useful for testing and when sources are already available.
func NewChangeDetectorWithSources(
	config ChangeDetectorConfig,
	fsEvents <-chan *ChangeEvent,
	gitEvents <-chan *ChangeEvent,
	periodicEvents <-chan *ChangeEvent,
) *ChangeDetector {
	return newDetector(config, fsEvents, gitEvents, periodicEvents)
}

// newDetector creates a detector with the given configuration and sources.
func newDetector(
	config ChangeDetectorConfig,
	fsEvents <-chan *ChangeEvent,
	gitEvents <-chan *ChangeEvent,
	periodicEvents <-chan *ChangeEvent,
) *ChangeDetector {
	if config.DedupeWindow == 0 {
		config.DedupeWindow = DefaultDedupeWindow
	}
	if config.RateLimit == 0 {
		config.RateLimit = DefaultRateLimit
	}

	return &ChangeDetector{
		config:         config,
		fsEvents:       fsEvents,
		gitEvents:      gitEvents,
		periodicEvents: periodicEvents,
		seen:           make(map[string]*dedupeEntry),
		tokens:         config.RateLimit,
		lastRefill:     time.Now(),
	}
}

// =============================================================================
// Start/Stop
// =============================================================================

// Start begins watching all configured sources.
// Returns a unified channel of change events.
func (d *ChangeDetector) Start(ctx context.Context) (<-chan *ChangeEvent, error) {
	if !d.running.CompareAndSwap(false, true) {
		return nil, ErrDetectorAlreadyRunning
	}

	d.stopCh = make(chan struct{})
	output := make(chan *ChangeEvent)

	// Start the main coordination loop
	d.wg.Add(1)
	go d.coordinationLoop(ctx, output)

	// Start cleanup routine
	d.wg.Add(1)
	go d.cleanupLoop(ctx)

	return output, nil
}

// Stop stops all watchers and closes the event channel.
func (d *ChangeDetector) Stop() error {
	if !d.running.Load() {
		return nil
	}

	close(d.stopCh)
	d.wg.Wait()
	d.running.Store(false)

	return nil
}

// =============================================================================
// Coordination Loop
// =============================================================================

// coordinationLoop is the main event processing loop.
func (d *ChangeDetector) coordinationLoop(ctx context.Context, output chan<- *ChangeEvent) {
	defer close(output)
	defer d.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		default:
			d.processOnce(ctx, output)
		}
	}
}

// processOnce processes events from all sources once.
func (d *ChangeDetector) processOnce(ctx context.Context, output chan<- *ChangeEvent) {
	// Use select with all sources to avoid blocking
	select {
	case <-ctx.Done():
		return
	case <-d.stopCh:
		return
	case event, ok := <-d.fsEvents:
		if ok && event != nil {
			d.handleEvent(ctx, event, output)
		}
	case event, ok := <-d.gitEvents:
		if ok && event != nil {
			d.handleEvent(ctx, event, output)
		}
	case event, ok := <-d.periodicEvents:
		if ok && event != nil {
			d.handleEvent(ctx, event, output)
		}
	case <-time.After(10 * time.Millisecond):
		// Prevent busy loop when no events
	}
}

// handleEvent processes a single event with deduplication and rate limiting.
func (d *ChangeDetector) handleEvent(ctx context.Context, event *ChangeEvent, output chan<- *ChangeEvent) {
	if d.shouldDedupe(event) {
		return
	}

	if !d.tryAcquireToken(event.Source) {
		return // Rate limited
	}

	d.recordEvent(event)
	d.emit(ctx, event, output)
}

// =============================================================================
// Deduplication
// =============================================================================

// shouldDedupe returns true if the event should be deduplicated.
func (d *ChangeDetector) shouldDedupe(event *ChangeEvent) bool {
	d.seenMu.RLock()
	entry, exists := d.seen[event.Path]
	d.seenMu.RUnlock()

	if !exists {
		return false
	}

	// Check if within dedupe window
	if time.Since(entry.time) > d.config.DedupeWindow {
		return false
	}

	// Higher priority source can override lower priority
	if event.Source < entry.source {
		return false // Don't dedupe - higher priority
	}

	return true
}

// recordEvent records an event for deduplication tracking.
func (d *ChangeDetector) recordEvent(event *ChangeEvent) {
	d.seenMu.Lock()
	d.seen[event.Path] = &dedupeEntry{
		time:   time.Now(),
		source: event.Source,
	}
	d.seenMu.Unlock()
}

// =============================================================================
// Rate Limiting
// =============================================================================

// tryAcquireToken attempts to acquire a rate limit token.
// FSNotify events have priority and bypass rate limiting when necessary.
func (d *ChangeDetector) tryAcquireToken(source ChangeSource) bool {
	d.tokenMu.Lock()
	defer d.tokenMu.Unlock()

	d.refillTokens()

	// FSNotify has priority - always allow
	if source == SourceFSNotify {
		if d.tokens > 0 {
			d.tokens--
		}
		return true
	}

	// Other sources need tokens
	if d.tokens > 0 {
		d.tokens--
		return true
	}

	return false
}

// refillTokens refills the token bucket based on elapsed time.
func (d *ChangeDetector) refillTokens() {
	now := time.Now()
	elapsed := now.Sub(d.lastRefill)

	// Add tokens based on elapsed time
	tokensToAdd := int(float64(d.config.RateLimit) * elapsed.Seconds())
	if tokensToAdd > 0 {
		d.tokens += tokensToAdd
		if d.tokens > d.config.RateLimit {
			d.tokens = d.config.RateLimit
		}
		d.lastRefill = now
	}
}

// =============================================================================
// Event Emission
// =============================================================================

// emit sends an event to the output channel.
func (d *ChangeDetector) emit(ctx context.Context, event *ChangeEvent, output chan<- *ChangeEvent) {
	select {
	case <-ctx.Done():
		return
	case <-d.stopCh:
		return
	case output <- event:
	}
}

// =============================================================================
// Cleanup
// =============================================================================

// cleanupLoop periodically removes old entries from the dedupe map.
func (d *ChangeDetector) cleanupLoop(ctx context.Context) {
	defer d.wg.Done()

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.cleanupOldEntries()
		}
	}
}

// cleanupOldEntries removes dedupe entries older than the window.
func (d *ChangeDetector) cleanupOldEntries() {
	d.seenMu.Lock()
	defer d.seenMu.Unlock()

	cutoff := time.Now().Add(-d.config.DedupeWindow * 2)

	for path, entry := range d.seen {
		if entry.time.Before(cutoff) {
			delete(d.seen, path)
		}
	}
}

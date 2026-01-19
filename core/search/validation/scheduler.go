// Package validation provides cross-source validation for the Sylk Document Search System.
package validation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrSchedulerAlreadyRunning indicates the scheduler is already running.
	ErrSchedulerAlreadyRunning = errors.New("scheduler is already running")

	// ErrSchedulerNotRunning indicates the scheduler is not running.
	ErrSchedulerNotRunning = errors.New("scheduler is not running")

	// ErrInvalidValidationInterval indicates an invalid interval.
	ErrInvalidValidationInterval = errors.New("validation interval must be positive")
)

// =============================================================================
// Validator Interface
// =============================================================================

// Validator validates file consistency across sources.
// This interface decouples the scheduler from the validator implementation.
type Validator interface {
	// Validate performs validation and returns a report.
	Validate(ctx context.Context) (*ValidationReport, error)
}

// =============================================================================
// SchedulerConfig
// =============================================================================

// SchedulerConfig configures the validation scheduler.
type SchedulerConfig struct {
	// Interval is the time between validation runs.
	Interval time.Duration

	// AutoResolveMinor enables automatic resolution of minor discrepancies.
	AutoResolveMinor bool

	// DryRun prevents actual changes during resolution.
	DryRun bool
}

// Validate checks that the configuration is valid.
func (c *SchedulerConfig) Validate() error {
	if c.Interval <= 0 {
		return ErrInvalidValidationInterval
	}
	return nil
}

// DefaultSchedulerConfig returns default scheduler configuration.
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Interval:         time.Hour,
		AutoResolveMinor: true,
		DryRun:           false,
	}
}

// =============================================================================
// ValidationScheduler
// =============================================================================

// ValidationScheduler runs periodic validation at low priority.
// It yields to active operations and auto-resolves minor discrepancies.
type ValidationScheduler struct {
	config   SchedulerConfig
	validator Validator
	resolver *DiscrepancyResolver

	paused  atomic.Bool
	running atomic.Bool

	mu     sync.Mutex
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Callbacks for events
	onValidationComplete func(*ValidationReport)
	onResolutionComplete func([]*Resolution)
}

// NewValidationScheduler creates a new validation scheduler.
func NewValidationScheduler(
	config SchedulerConfig,
	validator Validator,
	resolver *DiscrepancyResolver,
) *ValidationScheduler {
	return &ValidationScheduler{
		config:    config,
		validator: validator,
		resolver:  resolver,
	}
}

// =============================================================================
// Start/Stop
// =============================================================================

// Start begins periodic validation.
// Returns a channel of validation reports.
func (s *ValidationScheduler) Start(ctx context.Context) (<-chan *ValidationReport, error) {
	if err := s.validateAndInit(); err != nil {
		return nil, err
	}

	reports := make(chan *ValidationReport)

	s.wg.Add(1)
	go s.runLoop(ctx, reports)

	return reports, nil
}

// validateAndInit validates config and initializes the scheduler.
func (s *ValidationScheduler) validateAndInit() error {
	if err := s.config.Validate(); err != nil {
		return err
	}

	if !s.running.CompareAndSwap(false, true) {
		return ErrSchedulerAlreadyRunning
	}

	s.mu.Lock()
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	return nil
}

// Stop stops the scheduler.
func (s *ValidationScheduler) Stop() error {
	if !s.running.Load() {
		return ErrSchedulerNotRunning
	}

	s.mu.Lock()
	if s.stopCh != nil {
		close(s.stopCh)
	}
	s.mu.Unlock()

	s.wg.Wait()
	s.running.Store(false)

	return nil
}

// =============================================================================
// Run Loop
// =============================================================================

// runLoop is the main scheduler loop.
func (s *ValidationScheduler) runLoop(ctx context.Context, reports chan<- *ValidationReport) {
	defer close(reports)
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.getStopCh():
			return
		case <-ticker.C:
			s.performValidation(ctx, reports)
		}
	}
}

// getStopCh returns the stop channel under lock.
func (s *ValidationScheduler) getStopCh() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopCh
}

// performValidation runs a single validation cycle.
func (s *ValidationScheduler) performValidation(ctx context.Context, reports chan<- *ValidationReport) {
	if s.shouldSkip() {
		return
	}

	report := s.runValidation(ctx)
	if report == nil {
		return
	}

	s.handleReport(ctx, report)
	s.sendReport(ctx, reports, report)
}

// shouldSkip returns true if validation should be skipped.
func (s *ValidationScheduler) shouldSkip() bool {
	return s.paused.Load()
}

// runValidation executes the validation.
func (s *ValidationScheduler) runValidation(ctx context.Context) *ValidationReport {
	if s.validator == nil {
		return nil
	}

	report, err := s.validator.Validate(ctx)
	if err != nil {
		return &ValidationReport{
			CheckedAt:     time.Now(),
			Discrepancies: nil,
		}
	}

	return report
}

// handleReport processes the validation report.
func (s *ValidationScheduler) handleReport(ctx context.Context, report *ValidationReport) {
	if s.onValidationComplete != nil {
		s.onValidationComplete(report)
	}

	if !s.config.AutoResolveMinor {
		return
	}

	s.autoResolveMinor(ctx, report)
}

// autoResolveMinor automatically resolves minor discrepancies.
func (s *ValidationScheduler) autoResolveMinor(ctx context.Context, report *ValidationReport) {
	if s.resolver == nil {
		return
	}

	minor := s.filterMinorDiscrepancies(report.Discrepancies)
	if len(minor) == 0 {
		return
	}

	resolutions, err := s.resolver.ResolveAndApply(ctx, minor)
	if err != nil {
		return
	}

	if s.onResolutionComplete != nil {
		s.onResolutionComplete(resolutions)
	}
}

// filterMinorDiscrepancies returns only minor discrepancies.
func (s *ValidationScheduler) filterMinorDiscrepancies(discrepancies []*Discrepancy) []*Discrepancy {
	var minor []*Discrepancy
	for _, d := range discrepancies {
		if IsMinorDiscrepancy(d) {
			minor = append(minor, d)
		}
	}
	return minor
}

// sendReport sends the report to the channel.
func (s *ValidationScheduler) sendReport(
	ctx context.Context,
	reports chan<- *ValidationReport,
	report *ValidationReport,
) {
	select {
	case <-ctx.Done():
		return
	case <-s.getStopCh():
		return
	case reports <- report:
	}
}

// =============================================================================
// Pause/Resume
// =============================================================================

// Pause pauses the scheduler to yield to active operations.
func (s *ValidationScheduler) Pause() {
	s.paused.Store(true)
}

// Resume resumes the scheduler after a pause.
func (s *ValidationScheduler) Resume() {
	s.paused.Store(false)
}

// IsPaused returns whether the scheduler is paused.
func (s *ValidationScheduler) IsPaused() bool {
	return s.paused.Load()
}

// IsRunning returns whether the scheduler is running.
func (s *ValidationScheduler) IsRunning() bool {
	return s.running.Load()
}

// =============================================================================
// Callbacks
// =============================================================================

// OnValidationComplete sets a callback for validation completion.
func (s *ValidationScheduler) OnValidationComplete(fn func(*ValidationReport)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onValidationComplete = fn
}

// OnResolutionComplete sets a callback for resolution completion.
func (s *ValidationScheduler) OnResolutionComplete(fn func([]*Resolution)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onResolutionComplete = fn
}

// =============================================================================
// Manual Trigger
// =============================================================================

// TriggerValidation manually triggers a validation run.
// This bypasses the schedule and runs immediately.
func (s *ValidationScheduler) TriggerValidation(ctx context.Context) (*ValidationReport, error) {
	if !s.running.Load() {
		return nil, ErrSchedulerNotRunning
	}

	if s.validator == nil {
		return nil, errors.New("no validator configured")
	}

	return s.validator.Validate(ctx)
}

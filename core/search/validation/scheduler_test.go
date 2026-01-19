package validation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// mockValidator is a mock implementation of Validator.
type mockValidator struct {
	mu          sync.Mutex
	validateErr error
	reports     []*ValidationReport
	callCount   int
}

func newMockValidator() *mockValidator {
	return &mockValidator{
		reports: make([]*ValidationReport, 0),
	}
}

func (m *mockValidator) Validate(ctx context.Context) (*ValidationReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.validateErr != nil {
		return nil, m.validateErr
	}
	report := &ValidationReport{
		CheckedAt:     time.Now(),
		TotalFiles:    10,
		Discrepancies: make([]*Discrepancy, 0),
	}
	m.reports = append(m.reports, report)
	return report, nil
}

func (m *mockValidator) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *mockValidator) SetReport(report *ValidationReport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Next call will return this report
}

func (m *mockValidator) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validateErr = err
}

// mockValidatorWithDiscrepancies returns a validator that produces discrepancies.
type mockValidatorWithDiscrepancies struct {
	mu            sync.Mutex
	discrepancies []*Discrepancy
	callCount     int
}

func newMockValidatorWithDiscrepancies(discrepancies []*Discrepancy) *mockValidatorWithDiscrepancies {
	return &mockValidatorWithDiscrepancies{
		discrepancies: discrepancies,
	}
}

func (m *mockValidatorWithDiscrepancies) Validate(ctx context.Context) (*ValidationReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	return &ValidationReport{
		CheckedAt:     time.Now(),
		TotalFiles:    len(m.discrepancies),
		Discrepancies: m.discrepancies,
	}, nil
}

func (m *mockValidatorWithDiscrepancies) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// =============================================================================
// SchedulerConfig Tests
// =============================================================================

func TestSchedulerConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  SchedulerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: SchedulerConfig{
				Interval: time.Hour,
			},
			wantErr: false,
		},
		{
			name: "zero interval",
			config: SchedulerConfig{
				Interval: 0,
			},
			wantErr: true,
		},
		{
			name: "negative interval",
			config: SchedulerConfig{
				Interval: -time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultSchedulerConfig(t *testing.T) {
	t.Parallel()

	config := DefaultSchedulerConfig()

	if config.Interval != time.Hour {
		t.Errorf("Interval = %v, want %v", config.Interval, time.Hour)
	}
	if !config.AutoResolveMinor {
		t.Error("expected AutoResolveMinor = true")
	}
	if config.DryRun {
		t.Error("expected DryRun = false")
	}
}

// =============================================================================
// ValidationScheduler Creation Tests
// =============================================================================

func TestNewValidationScheduler(t *testing.T) {
	t.Parallel()

	config := DefaultSchedulerConfig()
	validator := newMockValidator()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), newMockHandler())

	scheduler := NewValidationScheduler(config, validator, resolver)

	if scheduler == nil {
		t.Fatal("expected non-nil scheduler")
	}
}

func TestNewValidationScheduler_NilValidator(t *testing.T) {
	t.Parallel()

	config := DefaultSchedulerConfig()
	scheduler := NewValidationScheduler(config, nil, nil)

	if scheduler == nil {
		t.Fatal("expected non-nil scheduler even with nil validator")
	}
}

// =============================================================================
// Start/Stop Tests
// =============================================================================

func TestValidationScheduler_Start_InvalidConfig(t *testing.T) {
	t.Parallel()

	config := SchedulerConfig{Interval: 0} // Invalid
	scheduler := NewValidationScheduler(config, newMockValidator(), nil)
	ctx := context.Background()

	_, err := scheduler.Start(ctx)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestValidationScheduler_Start_ContextCancellation(t *testing.T) {
	t.Parallel()

	config := SchedulerConfig{Interval: 50 * time.Millisecond}
	scheduler := NewValidationScheduler(config, newMockValidator(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Cancel context
	cancel()

	// Channel should close
	timeout := time.After(500 * time.Millisecond)
	select {
	case _, ok := <-reports:
		if ok {
			// Drain remaining reports
			for range reports {
			}
		}
	case <-timeout:
		t.Fatal("channel did not close after context cancellation")
	}
}

func TestValidationScheduler_Stop(t *testing.T) {
	t.Parallel()

	config := SchedulerConfig{Interval: 50 * time.Millisecond}
	scheduler := NewValidationScheduler(config, newMockValidator(), nil)

	ctx := context.Background()
	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Stop the scheduler
	if err := scheduler.Stop(); err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	// Channel should close
	timeout := time.After(500 * time.Millisecond)
	select {
	case _, ok := <-reports:
		if ok {
			for range reports {
			}
		}
	case <-timeout:
		t.Fatal("channel did not close after Stop()")
	}
}

func TestValidationScheduler_Stop_NotRunning(t *testing.T) {
	t.Parallel()

	config := DefaultSchedulerConfig()
	scheduler := NewValidationScheduler(config, newMockValidator(), nil)

	err := scheduler.Stop()
	if err != ErrSchedulerNotRunning {
		t.Errorf("expected ErrSchedulerNotRunning, got %v", err)
	}
}

func TestValidationScheduler_MultipleStarts(t *testing.T) {
	t.Parallel()

	config := SchedulerConfig{Interval: 50 * time.Millisecond}
	scheduler := NewValidationScheduler(config, newMockValidator(), nil)

	ctx := context.Background()

	// First start
	_, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("First Start() error = %v", err)
	}

	// Second start should return error
	_, err = scheduler.Start(ctx)
	if err != ErrSchedulerAlreadyRunning {
		t.Errorf("expected ErrSchedulerAlreadyRunning, got %v", err)
	}

	scheduler.Stop()
}

// =============================================================================
// Periodic Validation Tests
// =============================================================================

func TestValidationScheduler_RunsAtInterval(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: 30 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Drain reports
	var reportCount int
	for range reports {
		reportCount++
	}

	// With 30ms interval and 200ms timeout, expect at least 4-5 validations
	if validator.CallCount() < 4 {
		t.Errorf("expected at least 4 validation calls, got %d", validator.CallCount())
	}
}

func TestValidationScheduler_ReceivesReports(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: 30 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Count received reports
	var reportCount int
	for range reports {
		reportCount++
	}

	if reportCount < 2 {
		t.Errorf("expected at least 2 reports, got %d", reportCount)
	}
}

// =============================================================================
// Pause/Resume Tests
// =============================================================================

func TestValidationScheduler_Pause_StopsValidation(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: 20 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Let one validation run
	time.Sleep(30 * time.Millisecond)
	countBefore := validator.CallCount()

	// Pause
	scheduler.Pause()

	if !scheduler.IsPaused() {
		t.Error("expected IsPaused() = true")
	}

	// Wait a bit and check count didn't increase significantly
	time.Sleep(60 * time.Millisecond)
	countAfter := validator.CallCount()

	// During pause, count should not have increased much (at most 1 if one was in progress)
	if countAfter-countBefore > 1 {
		t.Errorf("validation continued during pause: before=%d, after=%d", countBefore, countAfter)
	}

	// Drain channel
	go func() {
		for range reports {
		}
	}()
}

func TestValidationScheduler_Resume_ContinuesValidation(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: 20 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Pause immediately
	scheduler.Pause()
	time.Sleep(50 * time.Millisecond)
	countPaused := validator.CallCount()

	// Resume
	scheduler.Resume()

	if scheduler.IsPaused() {
		t.Error("expected IsPaused() = false after Resume()")
	}

	// Wait and check validation continues
	time.Sleep(80 * time.Millisecond)
	countAfterResume := validator.CallCount()

	if countAfterResume <= countPaused {
		t.Errorf("validation did not continue after resume: paused=%d, after=%d", countPaused, countAfterResume)
	}

	// Drain channel
	go func() {
		for range reports {
		}
	}()
}

func TestValidationScheduler_IsRunning(t *testing.T) {
	t.Parallel()

	config := SchedulerConfig{Interval: time.Hour}
	scheduler := NewValidationScheduler(config, newMockValidator(), nil)

	if scheduler.IsRunning() {
		t.Error("expected IsRunning() = false before Start()")
	}

	ctx, cancel := context.WithCancel(context.Background())
	scheduler.Start(ctx)

	if !scheduler.IsRunning() {
		t.Error("expected IsRunning() = true after Start()")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}

// =============================================================================
// Auto-Resolve Tests
// =============================================================================

func TestValidationScheduler_AutoResolvesMinor(t *testing.T) {
	t.Parallel()

	// Create discrepancies with minor type
	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
	}
	validator := newMockValidatorWithDiscrepancies(discrepancies)

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)

	config := SchedulerConfig{
		Interval:         30 * time.Millisecond,
		AutoResolveMinor: true,
	}
	scheduler := NewValidationScheduler(config, validator, resolver)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Drain reports
	for range reports {
	}

	// Should have auto-resolved the minor discrepancy
	if handler.MetadataCount() < 1 {
		t.Errorf("expected at least 1 metadata update call, got %d", handler.MetadataCount())
	}
}

func TestValidationScheduler_DoesNotAutoResolveMajor(t *testing.T) {
	t.Parallel()

	// Create discrepancies with major type
	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyContentMismatch, SourceFilesystem, SourceCMT),
	}
	validator := newMockValidatorWithDiscrepancies(discrepancies)

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)

	config := SchedulerConfig{
		Interval:         30 * time.Millisecond,
		AutoResolveMinor: true, // Only auto-resolves minor
	}
	scheduler := NewValidationScheduler(config, validator, resolver)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Drain reports
	for range reports {
	}

	// Should not have auto-resolved the major discrepancy
	if handler.ReindexCount() > 0 {
		t.Errorf("expected 0 reindex calls for major discrepancy, got %d", handler.ReindexCount())
	}
}

func TestValidationScheduler_AutoResolveDisabled(t *testing.T) {
	t.Parallel()

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
	}
	validator := newMockValidatorWithDiscrepancies(discrepancies)

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)

	config := SchedulerConfig{
		Interval:         30 * time.Millisecond,
		AutoResolveMinor: false, // Disabled
	}
	scheduler := NewValidationScheduler(config, validator, resolver)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Drain reports
	for range reports {
	}

	// Should not have auto-resolved anything
	if handler.MetadataCount() > 0 {
		t.Errorf("expected 0 metadata calls when auto-resolve disabled, got %d", handler.MetadataCount())
	}
}

// =============================================================================
// Callback Tests
// =============================================================================

func TestValidationScheduler_OnValidationComplete(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: 30 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	var callbackCount atomic.Int32
	scheduler.OnValidationComplete(func(report *ValidationReport) {
		callbackCount.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Drain reports
	for range reports {
	}

	if callbackCount.Load() < 1 {
		t.Errorf("expected at least 1 callback invocation, got %d", callbackCount.Load())
	}
}

func TestValidationScheduler_OnResolutionComplete(t *testing.T) {
	t.Parallel()

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
	}
	validator := newMockValidatorWithDiscrepancies(discrepancies)

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)

	config := SchedulerConfig{
		Interval:         30 * time.Millisecond,
		AutoResolveMinor: true,
	}
	scheduler := NewValidationScheduler(config, validator, resolver)

	var callbackCount atomic.Int32
	scheduler.OnResolutionComplete(func(resolutions []*Resolution) {
		callbackCount.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Drain reports
	for range reports {
	}

	if callbackCount.Load() < 1 {
		t.Errorf("expected at least 1 resolution callback, got %d", callbackCount.Load())
	}
}

// =============================================================================
// TriggerValidation Tests
// =============================================================================

func TestValidationScheduler_TriggerValidation(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: time.Hour} // Long interval
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Trigger immediate validation
	report, err := scheduler.TriggerValidation(ctx)
	if err != nil {
		t.Fatalf("TriggerValidation() error = %v", err)
	}

	if report == nil {
		t.Error("expected non-nil report")
	}
}

func TestValidationScheduler_TriggerValidation_NotRunning(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := DefaultSchedulerConfig()
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx := context.Background()
	_, err := scheduler.TriggerValidation(ctx)
	if err != ErrSchedulerNotRunning {
		t.Errorf("expected ErrSchedulerNotRunning, got %v", err)
	}
}

func TestValidationScheduler_TriggerValidation_NoValidator(t *testing.T) {
	t.Parallel()

	config := SchedulerConfig{Interval: time.Hour}
	scheduler := NewValidationScheduler(config, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx)

	_, err := scheduler.TriggerValidation(ctx)
	if err == nil {
		t.Error("expected error when no validator configured")
	}
}

// =============================================================================
// Error Handling Tests
// =============================================================================

func TestValidationScheduler_ValidatorError(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	validator.SetError(errors.New("validation failed"))

	config := SchedulerConfig{Interval: 30 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should still receive reports (empty ones)
	var reportCount int
	for range reports {
		reportCount++
	}

	if reportCount < 1 {
		t.Errorf("expected at least 1 report even with validator error, got %d", reportCount)
	}
}

func TestValidationScheduler_NilValidator(t *testing.T) {
	t.Parallel()

	config := SchedulerConfig{Interval: 30 * time.Millisecond}
	scheduler := NewValidationScheduler(config, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should not receive any reports with nil validator
	var reportCount int
	for range reports {
		reportCount++
	}

	if reportCount > 0 {
		t.Errorf("expected 0 reports with nil validator, got %d", reportCount)
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestValidationScheduler_ConcurrentPauseResume(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: 10 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var wg sync.WaitGroup

	// Multiple goroutines pause/resume
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				scheduler.Pause()
				time.Sleep(time.Millisecond)
				scheduler.Resume()
			}
		}()
	}

	// Drain reports
	go func() {
		for range reports {
		}
	}()

	wg.Wait()
}

func TestValidationScheduler_RaceConditions(t *testing.T) {
	t.Parallel()

	validator := newMockValidator()
	config := SchedulerConfig{Interval: 5 * time.Millisecond}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithCancel(context.Background())
	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var wg sync.WaitGroup

	// Consumer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range reports {
		}
	}()

	// Pause/Resume
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if i%2 == 0 {
				scheduler.Pause()
			} else {
				scheduler.Resume()
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Run for a bit then cancel
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestValidationScheduler_NilResolver(t *testing.T) {
	t.Parallel()

	discrepancies := []*Discrepancy{
		makeDiscrepancy("/test/file.go", DiscrepancyModTimeMismatch, SourceFilesystem, SourceCMT),
	}
	validator := newMockValidatorWithDiscrepancies(discrepancies)

	config := SchedulerConfig{
		Interval:         30 * time.Millisecond,
		AutoResolveMinor: true, // Enabled but no resolver
	}
	scheduler := NewValidationScheduler(config, validator, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should not panic with nil resolver
	var reportCount int
	for range reports {
		reportCount++
	}

	if reportCount < 1 {
		t.Errorf("expected at least 1 report, got %d", reportCount)
	}
}

func TestValidationScheduler_EmptyDiscrepancies(t *testing.T) {
	t.Parallel()

	validator := newMockValidatorWithDiscrepancies(nil) // No discrepancies

	handler := newMockHandler()
	resolver := NewDiscrepancyResolver(DefaultResolverConfig(), handler)

	config := SchedulerConfig{
		Interval:         30 * time.Millisecond,
		AutoResolveMinor: true,
	}
	scheduler := NewValidationScheduler(config, validator, resolver)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reports, err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Drain reports
	for range reports {
	}

	// Should not have called handler since no discrepancies
	totalCalls := handler.ReindexCount() + handler.RemoveCount() + handler.MetadataCount()
	if totalCalls > 0 {
		t.Errorf("expected 0 handler calls with no discrepancies, got %d", totalCalls)
	}
}

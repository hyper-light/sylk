package staleness

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Mock File Info Provider
// =============================================================================

// testMtimeFileInfoProvider implements FileInfoProvider for testing.
type testMtimeFileInfoProvider struct {
	files    []string
	modTimes map[string]time.Time
	mu       sync.RWMutex
}

func newTestMtimeFileInfoProvider() *testMtimeFileInfoProvider {
	return &testMtimeFileInfoProvider{
		files:    []string{},
		modTimes: make(map[string]time.Time),
	}
}

func (m *testMtimeFileInfoProvider) ListFiles() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, len(m.files))
	copy(result, m.files)
	return result
}

func (m *testMtimeFileInfoProvider) GetModTime(path string) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.modTimes[path]
	return t, ok
}

func (m *testMtimeFileInfoProvider) AddFile(path string, modTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files = append(m.files, path)
	m.modTimes[path] = modTime
}

func (m *testMtimeFileInfoProvider) SetModTime(path string, modTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modTimes[path] = modTime
}

// =============================================================================
// NewMtimeSampler Tests
// =============================================================================

func TestNewMtimeSampler_Success(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()

	sampler, err := NewMtimeSampler("/tmp/test", config, provider)

	if err != nil {
		t.Fatalf("NewMtimeSampler() error = %v", err)
	}
	if sampler == nil {
		t.Fatal("NewMtimeSampler() returned nil")
	}
}

func TestNewMtimeSampler_EmptyBaseDir(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()

	sampler, err := NewMtimeSampler("", config, provider)

	if err != ErrBaseDirEmpty {
		t.Errorf("NewMtimeSampler() error = %v, want %v", err, ErrBaseDirEmpty)
	}
	if sampler != nil {
		t.Errorf("NewMtimeSampler() returned non-nil sampler")
	}
}

func TestNewMtimeSampler_InvalidSampleSize(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := SampleConfig{
		SampleSize: 0, // Invalid
		Strategy:   RandomSample,
	}

	sampler, err := NewMtimeSampler("/tmp/test", config, provider)

	if err != ErrSampleSizeInvalid {
		t.Errorf("NewMtimeSampler() error = %v, want %v", err, ErrSampleSizeInvalid)
	}
	if sampler != nil {
		t.Errorf("NewMtimeSampler() returned non-nil sampler")
	}
}

func TestNewMtimeSampler_NegativeSampleSize(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := SampleConfig{
		SampleSize: -1, // Invalid
		Strategy:   RandomSample,
	}

	sampler, err := NewMtimeSampler("/tmp/test", config, provider)

	if err != ErrSampleSizeInvalid {
		t.Errorf("NewMtimeSampler() error = %v, want %v", err, ErrSampleSizeInvalid)
	}
	if sampler != nil {
		t.Errorf("NewMtimeSampler() returned non-nil sampler")
	}
}

func TestNewMtimeSampler_NilProvider(t *testing.T) {
	t.Parallel()

	config := DefaultSampleConfig()

	// Nil provider is allowed (results in empty file list)
	sampler, err := NewMtimeSampler("/tmp/test", config, nil)

	if err != nil {
		t.Fatalf("NewMtimeSampler() error = %v", err)
	}
	if sampler == nil {
		t.Fatal("NewMtimeSampler() returned nil")
	}
}

// =============================================================================
// Sample Tests - Empty Provider
// =============================================================================

func TestMtimeSampler_Sample_NoFiles(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()
	sampler, _ := NewMtimeSampler("/tmp/test", config, provider)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if result.Level != Fresh {
		t.Errorf("Sample() Level = %v, want %v", result.Level, Fresh)
	}
	if result.Confidence != 0.0 {
		t.Errorf("Sample() Confidence = %v, want 0.0", result.Confidence)
	}
	if result.Strategy != MtimeSample {
		t.Errorf("Sample() Strategy = %v, want %v", result.Strategy, MtimeSample)
	}
}

func TestMtimeSampler_Sample_NilProvider(t *testing.T) {
	t.Parallel()

	config := DefaultSampleConfig()
	sampler, _ := NewMtimeSampler("/tmp/test", config, nil)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if result.Level != Fresh {
		t.Errorf("Sample() Level = %v, want %v", result.Level, Fresh)
	}
}

// =============================================================================
// Sample Tests - Fresh Detection
// =============================================================================

func TestMtimeSampler_Sample_Fresh(t *testing.T) {
	t.Parallel()

	// Create temp directory with test files
	tmpDir := t.TempDir()

	// Create test files
	testFiles := []string{"file1.go", "file2.go", "file3.go"}
	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Set up provider with matching mtimes
	provider := newTestMtimeFileInfoProvider()
	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		provider.AddFile(f, info.ModTime())
	}

	config := SampleConfig{
		SampleSize: 10,
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42), // Deterministic
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if result.Level != Fresh {
		t.Errorf("Sample() Level = %v, want %v", result.Level, Fresh)
	}
	if len(result.StaleFiles) != 0 {
		t.Errorf("Sample() StaleFiles = %v, want empty", result.StaleFiles)
	}
}

// =============================================================================
// Sample Tests - Stale Detection
// =============================================================================

func TestMtimeSampler_Sample_DetectsStaleFile(t *testing.T) {
	t.Parallel()

	// Create temp directory with test files
	tmpDir := t.TempDir()

	// Create test files
	testFiles := []string{"file1.go", "file2.go", "file3.go"}
	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Set up provider with outdated mtimes
	provider := newTestMtimeFileInfoProvider()
	oldTime := time.Now().Add(-time.Hour)
	for _, f := range testFiles {
		provider.AddFile(f, oldTime) // Stored mtime is older
	}

	config := SampleConfig{
		SampleSize: 10,
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if result.Level == Fresh {
		t.Errorf("Sample() should detect staleness")
	}
	if len(result.StaleFiles) == 0 {
		t.Errorf("Sample() should have stale files")
	}
}

func TestMtimeSampler_Sample_SlightlyStale(t *testing.T) {
	t.Parallel()

	// Create temp directory with test files
	tmpDir := t.TempDir()

	// Create 20 test files
	var testFiles []string
	for i := 0; i < 20; i++ {
		f := "file" + string(rune('a'+i)) + ".go"
		testFiles = append(testFiles, f)
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Set up provider - 1 file is stale (< 10% of sample)
	provider := newTestMtimeFileInfoProvider()
	oldTime := time.Now().Add(-time.Hour)
	for i, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		if i == 0 {
			// First file has old stored mtime (stale)
			provider.AddFile(f, oldTime)
		} else {
			// Others match current mtime
			provider.AddFile(f, info.ModTime())
		}
	}

	config := SampleConfig{
		SampleSize: 20,
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if result.Level != SlightlyStale {
		t.Errorf("Sample() Level = %v, want %v", result.Level, SlightlyStale)
	}
}

func TestMtimeSampler_Sample_SeverelyStale(t *testing.T) {
	t.Parallel()

	// Create temp directory with test files
	tmpDir := t.TempDir()

	// Create 10 test files
	var testFiles []string
	for i := 0; i < 10; i++ {
		f := "file" + string(rune('a'+i)) + ".go"
		testFiles = append(testFiles, f)
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Set up provider - all files are stale (100% of sample > 30%)
	provider := newTestMtimeFileInfoProvider()
	oldTime := time.Now().Add(-time.Hour)
	for _, f := range testFiles {
		provider.AddFile(f, oldTime) // All have old stored mtime
	}

	config := SampleConfig{
		SampleSize: 10,
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if result.Level != SeverelyStale {
		t.Errorf("Sample() Level = %v, want %v", result.Level, SeverelyStale)
	}
}

// =============================================================================
// Sample Tests - Context Cancellation
// =============================================================================

func TestMtimeSampler_Sample_CanceledContext(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()
	sampler, _ := NewMtimeSampler("/tmp/test", config, provider)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := sampler.Sample(ctx)

	if err != context.Canceled {
		t.Errorf("Sample() error = %v, want %v", err, context.Canceled)
	}
	if result != nil {
		t.Errorf("Sample() result = %v, want nil", result)
	}
}

func TestMtimeSampler_Sample_DeadlineExceeded(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()
	sampler, _ := NewMtimeSampler("/tmp/test", config, provider)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	result, err := sampler.Sample(ctx)

	if err != context.DeadlineExceeded {
		t.Errorf("Sample() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if result != nil {
		t.Errorf("Sample() result = %v, want nil", result)
	}
}

// =============================================================================
// Sample Tests - File Not Found
// =============================================================================

func TestMtimeSampler_Sample_FileNotFound(t *testing.T) {
	t.Parallel()

	// Create temp directory
	tmpDir := t.TempDir()

	// Set up provider with files that don't exist on disk
	provider := newTestMtimeFileInfoProvider()
	provider.AddFile("nonexistent.go", time.Now())

	config := SampleConfig{
		SampleSize: 10,
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	// Non-existent files are not considered stale
	if result.Level != Fresh {
		t.Errorf("Sample() Level = %v, want %v", result.Level, Fresh)
	}
}

// =============================================================================
// Sample Tests - Sample Size
// =============================================================================

func TestMtimeSampler_Sample_LargerThanFileCount(t *testing.T) {
	t.Parallel()

	// Create temp directory with few files
	tmpDir := t.TempDir()

	// Create only 3 files
	testFiles := []string{"file1.go", "file2.go", "file3.go"}
	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Set up provider
	provider := newTestMtimeFileInfoProvider()
	for _, f := range testFiles {
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		provider.AddFile(f, info.ModTime())
	}

	// Sample size larger than file count
	config := SampleConfig{
		SampleSize: 100, // More than 3 files
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, err := sampler.Sample(context.Background())

	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	// Should still work, sampling all available files
	if result.Level != Fresh {
		t.Errorf("Sample() Level = %v, want %v", result.Level, Fresh)
	}
}

// =============================================================================
// Config and BaseDir Tests
// =============================================================================

func TestMtimeSampler_Config(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := SampleConfig{
		SampleSize: 50,
		Strategy:   RandomSample,
		MaxAge:     10 * time.Minute,
	}

	sampler, _ := NewMtimeSampler("/tmp/test", config, provider)

	got := sampler.Config()

	if got.SampleSize != config.SampleSize {
		t.Errorf("Config().SampleSize = %v, want %v", got.SampleSize, config.SampleSize)
	}
	if got.Strategy != config.Strategy {
		t.Errorf("Config().Strategy = %v, want %v", got.Strategy, config.Strategy)
	}
}

func TestMtimeSampler_BaseDir(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()
	baseDir := "/path/to/project"

	sampler, _ := NewMtimeSampler(baseDir, config, provider)

	if sampler.BaseDir() != baseDir {
		t.Errorf("BaseDir() = %v, want %v", sampler.BaseDir(), baseDir)
	}
}

// =============================================================================
// StalenessReport Tests
// =============================================================================

func TestMtimeSampler_Result_HasTimestamp(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()
	sampler, _ := NewMtimeSampler("/tmp/test", config, provider)

	before := time.Now()
	result, _ := sampler.Sample(context.Background())
	after := time.Now()

	if result.DetectedAt.Before(before) || result.DetectedAt.After(after) {
		t.Errorf("DetectedAt = %v, want between %v and %v", result.DetectedAt, before, after)
	}
}

func TestMtimeSampler_Result_HasDuration(t *testing.T) {
	t.Parallel()

	provider := newTestMtimeFileInfoProvider()
	config := DefaultSampleConfig()
	sampler, _ := NewMtimeSampler("/tmp/test", config, provider)

	result, _ := sampler.Sample(context.Background())

	if result.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", result.Duration)
	}
}

func TestMtimeSampler_Result_ContainsStaleFileInfo(t *testing.T) {
	t.Parallel()

	// Create temp directory with test file
	tmpDir := t.TempDir()

	path := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Set up provider with old mtime
	provider := newTestMtimeFileInfoProvider()
	oldTime := time.Now().Add(-time.Hour)
	provider.AddFile("test.go", oldTime)

	config := SampleConfig{
		SampleSize: 10,
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, _ := sampler.Sample(context.Background())

	if len(result.StaleFiles) == 0 {
		t.Fatal("Expected stale files")
	}

	staleFile := result.StaleFiles[0]
	if staleFile.Path != "test.go" {
		t.Errorf("StaleFile.Path = %v, want test.go", staleFile.Path)
	}
	if staleFile.Reason != "mtime changed" {
		t.Errorf("StaleFile.Reason = %v, want 'mtime changed'", staleFile.Reason)
	}
	if staleFile.LastIndexed.IsZero() {
		t.Errorf("StaleFile.LastIndexed should not be zero")
	}
	if staleFile.CurrentMtime.IsZero() {
		t.Errorf("StaleFile.CurrentMtime should not be zero")
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestMtimeSampler_ConcurrentSampling(t *testing.T) {
	t.Parallel()

	// Create temp directory with test files
	tmpDir := t.TempDir()

	for i := 0; i < 10; i++ {
		f := "file" + string(rune('a'+i)) + ".go"
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	provider := newTestMtimeFileInfoProvider()
	for i := 0; i < 10; i++ {
		f := "file" + string(rune('a'+i)) + ".go"
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		provider.AddFile(f, info.ModTime())
	}

	config := SampleConfig{
		SampleSize: 5,
		Strategy:   RandomSample,
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = sampler.Sample(context.Background())
		}()
	}

	wg.Wait()
}

// =============================================================================
// SampleConfig Tests
// =============================================================================

func TestDefaultSampleConfig(t *testing.T) {
	t.Parallel()

	config := DefaultSampleConfig()

	if config.SampleSize != 100 {
		t.Errorf("SampleSize = %v, want 100", config.SampleSize)
	}
	if config.Strategy != RandomSample {
		t.Errorf("Strategy = %v, want %v", config.Strategy, RandomSample)
	}
	if config.MaxAge != 5*time.Minute {
		t.Errorf("MaxAge = %v, want %v", config.MaxAge, 5*time.Minute)
	}
}

func TestSampleConfig_Validate_Valid(t *testing.T) {
	t.Parallel()

	config := DefaultSampleConfig()
	err := config.Validate()

	if err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestSampleConfig_Validate_Invalid(t *testing.T) {
	t.Parallel()

	config := SampleConfig{SampleSize: 0}
	err := config.Validate()

	if err != ErrSampleSizeInvalid {
		t.Errorf("Validate() error = %v, want %v", err, ErrSampleSizeInvalid)
	}
}

// =============================================================================
// SampleStrategy Tests
// =============================================================================

func TestSampleStrategy_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		strategy SampleStrategy
		want     string
	}{
		{RandomSample, "random"},
		{RecentFirstSample, "recent_first"},
		{LargestFirstSample, "largest_first"},
		{SampleStrategy(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.strategy.String(); got != tt.want {
			t.Errorf("%v.String() = %v, want %v", tt.strategy, got, tt.want)
		}
	}
}

// =============================================================================
// Confidence Calculation Tests
// =============================================================================

func TestMtimeSampler_Confidence_FullSample(t *testing.T) {
	t.Parallel()

	// Create temp directory with test files
	tmpDir := t.TempDir()

	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	provider := newTestMtimeFileInfoProvider()
	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		provider.AddFile(f, info.ModTime())
	}

	config := SampleConfig{
		SampleSize: 100,
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, _ := sampler.Sample(context.Background())

	// Full sample should have high confidence
	if result.Confidence < 0.9 {
		t.Errorf("Confidence = %v, want >= 0.9", result.Confidence)
	}
}

func TestMtimeSampler_Confidence_PartialSample(t *testing.T) {
	t.Parallel()

	// Create temp directory with few files
	tmpDir := t.TempDir()

	for i := 0; i < 10; i++ {
		f := "file" + string(rune('a'+i)) + ".go"
		path := filepath.Join(tmpDir, f)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	provider := newTestMtimeFileInfoProvider()
	for i := 0; i < 10; i++ {
		f := "file" + string(rune('a'+i)) + ".go"
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		provider.AddFile(f, info.ModTime())
	}

	config := SampleConfig{
		SampleSize: 100, // More than available
		Strategy:   RandomSample,
		RandSource: rand.NewSource(42),
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)

	result, _ := sampler.Sample(context.Background())

	// Partial sample should have lower confidence (10/100 = 0.09 * 0.9 = 0.081)
	expectedConfidence := 0.9 * 10.0 / 100.0
	if result.Confidence != expectedConfidence {
		t.Errorf("Confidence = %v, want %v", result.Confidence, expectedConfidence)
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkMtimeSampler_Sample_Fresh(b *testing.B) {
	tmpDir := b.TempDir()

	// Create test files
	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		path := filepath.Join(tmpDir, f)
		_ = os.WriteFile(path, []byte("content"), 0644)
	}

	provider := newTestMtimeFileInfoProvider()
	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		provider.AddFile(f, info.ModTime())
	}

	config := SampleConfig{
		SampleSize: 50,
		Strategy:   RandomSample,
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sampler.Sample(ctx)
	}
}

func BenchmarkMtimeSampler_Sample_Stale(b *testing.B) {
	tmpDir := b.TempDir()

	// Create test files
	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		path := filepath.Join(tmpDir, f)
		_ = os.WriteFile(path, []byte("content"), 0644)
	}

	provider := newTestMtimeFileInfoProvider()
	oldTime := time.Now().Add(-time.Hour)
	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		provider.AddFile(f, oldTime) // All stale
	}

	config := SampleConfig{
		SampleSize: 50,
		Strategy:   RandomSample,
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sampler.Sample(ctx)
	}
}

func BenchmarkMtimeSampler_ConcurrentSample(b *testing.B) {
	tmpDir := b.TempDir()

	// Create test files
	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		path := filepath.Join(tmpDir, f)
		_ = os.WriteFile(path, []byte("content"), 0644)
	}

	provider := newTestMtimeFileInfoProvider()
	for i := 0; i < 100; i++ {
		f := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".go"
		path := filepath.Join(tmpDir, f)
		info, _ := os.Stat(path)
		provider.AddFile(f, info.ModTime())
	}

	config := SampleConfig{
		SampleSize: 50,
		Strategy:   RandomSample,
	}

	sampler, _ := NewMtimeSampler(tmpDir, config, provider)
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = sampler.Sample(ctx)
		}
	})
}

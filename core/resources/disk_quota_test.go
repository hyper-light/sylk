package resources

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// MockSignalPublisher records all signal calls for verification.
type MockSignalPublisher struct {
	mu           sync.Mutex
	WarningCalls []SignalCall
	CleanupCalls []SignalCall
}

type SignalCall struct {
	UsagePercent float64
	UsedBytes    int64
	QuotaBytes   int64
}

func NewMockSignalPublisher() *MockSignalPublisher {
	return &MockSignalPublisher{
		WarningCalls: make([]SignalCall, 0),
		CleanupCalls: make([]SignalCall, 0),
	}
}

func (m *MockSignalPublisher) PublishWarning(usagePercent float64, usedBytes, quotaBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WarningCalls = append(m.WarningCalls, SignalCall{
		UsagePercent: usagePercent,
		UsedBytes:    usedBytes,
		QuotaBytes:   quotaBytes,
	})
}

func (m *MockSignalPublisher) PublishCleanupNeeded(usagePercent float64, usedBytes, quotaBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CleanupCalls = append(m.CleanupCalls, SignalCall{
		UsagePercent: usagePercent,
		UsedBytes:    usedBytes,
		QuotaBytes:   quotaBytes,
	})
}

func (m *MockSignalPublisher) GetWarningCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.WarningCalls)
}

func (m *MockSignalPublisher) GetCleanupCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.CleanupCalls)
}

// TestNewDiskQuotaManager tests manager creation.
func TestNewDiskQuotaManager(t *testing.T) {
	t.Run("valid path", func(t *testing.T) {
		tempDir := t.TempDir()
		config := DefaultDiskQuotaConfig()

		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if manager == nil {
			t.Fatal("expected manager, got nil")
		}
		if manager.BasePath() != tempDir {
			t.Errorf("expected basePath %s, got %s", tempDir, manager.BasePath())
		}
	})

	t.Run("empty path", func(t *testing.T) {
		config := DefaultDiskQuotaConfig()
		_, err := NewDiskQuotaManager("", config)
		if err != ErrInvalidBasePath {
			t.Errorf("expected ErrInvalidBasePath, got %v", err)
		}
	})
}

// TestCalculateQuota tests quota calculation respects bounds.
func TestCalculateQuota(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("quota clamped to min", func(t *testing.T) {
		config := DiskQuotaConfig{
			QuotaMin:         10 * GB, // Very high min
			QuotaMax:         20 * GB,
			QuotaPercent:     0.0001, // Very small percent
			WarningThreshold: 0.8,
			CleanupThreshold: 0.9,
		}

		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		quota := manager.CalculateQuota()
		if quota != config.QuotaMin {
			t.Errorf("expected quota to be clamped to min %d, got %d", config.QuotaMin, quota)
		}
	})

	t.Run("quota clamped to max", func(t *testing.T) {
		config := DiskQuotaConfig{
			QuotaMin:         1 * GB,
			QuotaMax:         5 * GB, // Low max
			QuotaPercent:     0.99,   // Very high percent
			WarningThreshold: 0.8,
			CleanupThreshold: 0.9,
		}

		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		quota := manager.CalculateQuota()
		if quota > config.QuotaMax {
			t.Errorf("expected quota <= max %d, got %d", config.QuotaMax, quota)
		}
	})

	t.Run("quota within bounds", func(t *testing.T) {
		config := DiskQuotaConfig{
			QuotaMin:         1 * MB,
			QuotaMax:         100 * GB,
			QuotaPercent:     0.1,
			WarningThreshold: 0.8,
			CleanupThreshold: 0.9,
		}

		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		quota := manager.CalculateQuota()
		if quota < config.QuotaMin {
			t.Errorf("expected quota >= min %d, got %d", config.QuotaMin, quota)
		}
		if quota > config.QuotaMax {
			t.Errorf("expected quota <= max %d, got %d", config.QuotaMax, quota)
		}
	})
}

// TestClampQuota tests the clampQuota helper function.
func TestClampQuota(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         1 * GB,
		QuotaMax:         10 * GB,
		QuotaPercent:     0.1,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name     string
		input    int64
		expected int64
	}{
		{"below min", 500 * MB, 1 * GB},
		{"at min", 1 * GB, 1 * GB},
		{"within range", 5 * GB, 5 * GB},
		{"at max", 10 * GB, 10 * GB},
		{"above max", 15 * GB, 10 * GB},
		{"zero", 0, 1 * GB},
		{"negative", -1 * GB, 1 * GB},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.clampQuota(tt.input)
			if result != tt.expected {
				t.Errorf("clampQuota(%d) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

// TestCanWrite tests the CanWrite method.
func TestCanWrite(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         1 * GB, // Use 1GB as effective quota (min will be used)
		QuotaMax:         1 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("can write when under quota", func(t *testing.T) {
		if !manager.CanWrite(100 * MB) {
			t.Error("expected CanWrite(100MB) = true when usage is 0")
		}
	})

	t.Run("can write exact remaining quota", func(t *testing.T) {
		if !manager.CanWrite(1 * GB) {
			t.Error("expected CanWrite(1GB) = true when usage is 0 and quota is 1GB")
		}
	})

	t.Run("cannot write over quota", func(t *testing.T) {
		if manager.CanWrite(2 * GB) {
			t.Error("expected CanWrite(2GB) = false when quota is 1GB")
		}
	})

	t.Run("cannot write after usage recorded", func(t *testing.T) {
		_ = manager.RecordWrite(900 * MB)
		if manager.CanWrite(200 * MB) {
			t.Error("expected CanWrite(200MB) = false when 900MB used of 1GB quota")
		}
	})

	t.Run("can write small amount after usage", func(t *testing.T) {
		// Manager already has 900MB used
		if !manager.CanWrite(50 * MB) {
			t.Error("expected CanWrite(50MB) = true when 900MB used of 1GB quota")
		}
	})
}

// TestRecordWrite tests tracking of write operations.
func TestRecordWrite(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         1 * GB,
		QuotaMax:         1 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	t.Run("tracks usage correctly", func(t *testing.T) {
		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_ = manager.RecordWrite(100 * MB)
		if manager.Usage() != 100*MB {
			t.Errorf("expected usage 100MB, got %d", manager.Usage())
		}

		_ = manager.RecordWrite(200 * MB)
		if manager.Usage() != 300*MB {
			t.Errorf("expected usage 300MB, got %d", manager.Usage())
		}
	})

	t.Run("zero size write", func(t *testing.T) {
		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err = manager.RecordWrite(0)
		if err != nil {
			t.Errorf("unexpected error for zero write: %v", err)
		}
		if manager.Usage() != 0 {
			t.Errorf("expected usage 0, got %d", manager.Usage())
		}
	})
}

// TestRecordDelete tests that deletion reduces usage.
func TestRecordDelete(t *testing.T) {
	tempDir := t.TempDir()
	config := DefaultDiskQuotaConfig()

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("reduces usage correctly", func(t *testing.T) {
		_ = manager.RecordWrite(500 * MB)
		manager.RecordDelete(200 * MB)

		if manager.Usage() != 300*MB {
			t.Errorf("expected usage 300MB after delete, got %d", manager.Usage())
		}
	})

	t.Run("usage cannot go negative", func(t *testing.T) {
		manager.RecordDelete(1 * GB) // Delete more than current usage

		if manager.Usage() != 0 {
			t.Errorf("expected usage 0 (not negative), got %d", manager.Usage())
		}
	})

	t.Run("zero size delete", func(t *testing.T) {
		_ = manager.RecordWrite(100 * MB)
		manager.RecordDelete(0)

		if manager.Usage() != 100*MB {
			t.Errorf("expected usage unchanged at 100MB, got %d", manager.Usage())
		}
	})
}

// TestWarningThreshold tests that warning signal triggers at 80%.
func TestWarningThreshold(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         1 * GB,
		QuotaMax:         1 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mockPub := NewMockSignalPublisher()
	manager.SetSignalPublisher(mockPub)

	// Get the actual quota to calculate exact threshold amounts
	quota := manager.CalculateQuota()

	t.Run("no warning below threshold", func(t *testing.T) {
		belowWarning := int64(float64(quota) * 0.7) // 70% usage
		_ = manager.RecordWrite(belowWarning)
		if mockPub.GetWarningCallCount() != 0 {
			t.Errorf("expected no warning calls at 70%%, got %d", mockPub.GetWarningCallCount())
		}
	})

	t.Run("warning at 80%", func(t *testing.T) {
		// Current usage is 70%, add 11% to get to 81% (above warning threshold)
		additionalForWarning := int64(float64(quota) * 0.11)
		_ = manager.RecordWrite(additionalForWarning)
		if mockPub.GetWarningCallCount() < 1 {
			t.Errorf("expected at least 1 warning call at 81%%, got %d", mockPub.GetWarningCallCount())
		}
	})

	t.Run("warning call contains correct data", func(t *testing.T) {
		mockPub.mu.Lock()
		if len(mockPub.WarningCalls) > 0 {
			call := mockPub.WarningCalls[0]
			if call.QuotaBytes != quota {
				t.Errorf("expected QuotaBytes %d, got %d", quota, call.QuotaBytes)
			}
			// Verify usage is at least 80% of quota
			if float64(call.UsedBytes)/float64(call.QuotaBytes) < 0.8 {
				t.Errorf("expected UsedBytes to be >= 80%% of quota, got %f%%", float64(call.UsedBytes)/float64(call.QuotaBytes)*100)
			}
		}
		mockPub.mu.Unlock()
	})
}

// TestCleanupThreshold tests that cleanup signal triggers at 90%.
func TestCleanupThreshold(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         1 * GB,
		QuotaMax:         1 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mockPub := NewMockSignalPublisher()
	manager.SetSignalPublisher(mockPub)

	quota := manager.CalculateQuota()

	t.Run("no cleanup signal below threshold", func(t *testing.T) {
		belowCleanup := int64(float64(quota) * 0.85)
		_ = manager.RecordWrite(belowCleanup)
		if mockPub.GetCleanupCallCount() != 0 {
			t.Errorf("expected no cleanup calls at 85%%, got %d", mockPub.GetCleanupCallCount())
		}
	})

	t.Run("cleanup signal at 90%", func(t *testing.T) {
		additionalForCleanup := int64(float64(quota) * 0.06)
		err := manager.RecordWrite(additionalForCleanup)
		if err != ErrCleanupThreshold {
			t.Errorf("expected ErrCleanupThreshold at 91%%, got %v", err)
		}
		if mockPub.GetCleanupCallCount() < 1 {
			t.Errorf("expected at least 1 cleanup call at 91%%, got %d", mockPub.GetCleanupCallCount())
		}
	})

	t.Run("cleanup call contains correct data", func(t *testing.T) {
		mockPub.mu.Lock()
		if len(mockPub.CleanupCalls) > 0 {
			call := mockPub.CleanupCalls[0]
			if call.QuotaBytes != quota {
				t.Errorf("expected QuotaBytes %d, got %d", quota, call.QuotaBytes)
			}
			if float64(call.UsedBytes)/float64(call.QuotaBytes) < 0.9 {
				t.Errorf("expected UsedBytes to be >= 90%% of quota, got %f%%", float64(call.UsedBytes)/float64(call.QuotaBytes)*100)
			}
		}
		mockPub.mu.Unlock()
	})
}

// TestTriggerCleanup tests cleanup priority order.
func TestTriggerCleanup(t *testing.T) {
	tempDir := t.TempDir()
	config := DefaultDiskQuotaConfig()

	// Create directory structure
	dirs := []string{"tmp", "wal", "checkpoints", "staging"}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(tempDir, dir), 0755); err != nil {
			t.Fatalf("failed to create %s dir: %v", dir, err)
		}
	}

	// Create files in each directory
	now := time.Now()
	oldTime := now.Add(-48 * time.Hour) // 2 days old

	// tmp file (should be deleted regardless of age)
	tmpFile := filepath.Join(tempDir, "tmp", "temp.txt")
	if err := os.WriteFile(tmpFile, []byte("temp data"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	// Old WAL file (should be deleted, > 24h)
	walFile := filepath.Join(tempDir, "wal", "segment.wal")
	if err := os.WriteFile(walFile, []byte("wal data"), 0644); err != nil {
		t.Fatalf("failed to create wal file: %v", err)
	}
	os.Chtimes(walFile, oldTime, oldTime)

	// New staging file (should NOT be deleted, < 24h)
	stagingFile := filepath.Join(tempDir, "staging", "stage.dat")
	if err := os.WriteFile(stagingFile, []byte("staging data"), 0644); err != nil {
		t.Fatalf("failed to create staging file: %v", err)
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Record initial usage
	_ = manager.RecordWrite(int64(len("temp data") + len("wal data") + len("staging data")))

	// Trigger cleanup
	manager.TriggerCleanup()

	// Verify tmp file deleted
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("expected tmp file to be deleted")
	}

	// Verify old WAL file deleted
	if _, err := os.Stat(walFile); !os.IsNotExist(err) {
		t.Error("expected old WAL file to be deleted")
	}

	// Verify new staging file NOT deleted
	if _, err := os.Stat(stagingFile); os.IsNotExist(err) {
		t.Error("expected new staging file to NOT be deleted")
	}
}

// TestCleanupUpdatesUsage tests that cleanup correctly updates usage tracking.
func TestCleanupUpdatesUsage(t *testing.T) {
	tempDir := t.TempDir()
	config := DefaultDiskQuotaConfig()

	// Create tmp directory and file
	tmpDir := filepath.Join(tempDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}

	fileData := make([]byte, 1*MB)
	tmpFile := filepath.Join(tmpDir, "large.tmp")
	if err := os.WriteFile(tmpFile, fileData, 0644); err != nil {
		t.Fatalf("failed to create tmp file: %v", err)
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Record the write
	_ = manager.RecordWrite(1 * MB)
	initialUsage := manager.Usage()

	// Trigger cleanup
	manager.TriggerCleanup()

	// Usage should be reduced
	if manager.Usage() >= initialUsage {
		t.Errorf("expected usage to decrease after cleanup, was %d, now %d", initialUsage, manager.Usage())
	}
}

// TestUsagePercent tests usage percentage calculation.
func TestUsagePercent(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         1 * GB,
		QuotaMax:         1 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	quota := manager.CalculateQuota()

	t.Run("zero usage", func(t *testing.T) {
		pct := manager.UsagePercent()
		if pct != 0.0 {
			t.Errorf("expected 0%%, got %f", pct)
		}
	})

	t.Run("50% usage", func(t *testing.T) {
		halfQuota := quota / 2
		_ = manager.RecordWrite(halfQuota)
		pct := manager.UsagePercent()
		if pct < 0.49 || pct > 0.51 {
			t.Errorf("expected ~50%%, got %f%%", pct*100)
		}
	})
}

// TestScanUsage tests reconciliation of tracked vs actual usage.
func TestScanUsage(t *testing.T) {
	tempDir := t.TempDir()
	config := DefaultDiskQuotaConfig()

	// Create some files
	fileData := make([]byte, 100*KB)
	for i := 0; i < 5; i++ {
		fileName := filepath.Join(tempDir, "file"+string(rune('0'+i))+".dat")
		if err := os.WriteFile(fileName, fileData, 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Initially usage is 0
	if manager.Usage() != 0 {
		t.Errorf("expected initial usage 0, got %d", manager.Usage())
	}

	// Scan should update usage
	if err := manager.ScanUsage(); err != nil {
		t.Fatalf("ScanUsage failed: %v", err)
	}

	expectedUsage := int64(5 * 100 * KB)
	if manager.Usage() != expectedUsage {
		t.Errorf("expected usage %d after scan, got %d", expectedUsage, manager.Usage())
	}

	// LastScan should be updated
	if manager.LastScan().IsZero() {
		t.Error("expected LastScan to be set")
	}
}

// TestThreadSafety tests concurrent access to the manager.
func TestThreadSafety(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         100 * GB, // High quota to avoid threshold errors
		QuotaMax:         100 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mockPub := NewMockSignalPublisher()
	manager.SetSignalPublisher(mockPub)

	const numGoroutines = 100
	const writesPerGoroutine = 100
	const writeSize = 1 * KB

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // Writers and readers

	// Concurrent writers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				_ = manager.RecordWrite(writeSize)
			}
		}()
	}

	// Concurrent readers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				_ = manager.Usage()
				_ = manager.UsagePercent()
				_ = manager.CanWrite(writeSize)
			}
		}()
	}

	wg.Wait()

	expectedUsage := int64(numGoroutines * writesPerGoroutine * writeSize)
	actualUsage := manager.Usage()
	if actualUsage != expectedUsage {
		t.Errorf("expected usage %d after concurrent writes, got %d", expectedUsage, actualUsage)
	}
}

// TestConcurrentWriteAndDelete tests concurrent writes and deletes.
func TestConcurrentWriteAndDelete(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         100 * GB,
		QuotaMax:         100 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	const numOps = 1000
	const size = 1 * KB

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer
	go func() {
		defer wg.Done()
		for i := 0; i < numOps; i++ {
			_ = manager.RecordWrite(size)
		}
	}()

	// Deleter
	go func() {
		defer wg.Done()
		for i := 0; i < numOps; i++ {
			manager.RecordDelete(size)
		}
	}()

	wg.Wait()

	// Usage should be >= 0 (never negative)
	if manager.Usage() < 0 {
		t.Errorf("usage should never be negative, got %d", manager.Usage())
	}
}

// TestEdgeCases tests various edge cases.
func TestEdgeCases(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("very large write size", func(t *testing.T) {
		config := DiskQuotaConfig{
			QuotaMin:         1 * GB,
			QuotaMax:         1 * GB,
			QuotaPercent:     0.0001,
			WarningThreshold: 0.8,
			CleanupThreshold: 0.9,
		}

		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Writing more than quota should still be tracked
		_ = manager.RecordWrite(10 * GB)
		if manager.Usage() != 10*GB {
			t.Errorf("expected usage 10GB, got %d", manager.Usage())
		}
	})

	t.Run("max int64 handling", func(t *testing.T) {
		config := DiskQuotaConfig{
			QuotaMin:         1,
			QuotaMax:         1<<62 - 1, // Large but safe value
			QuotaPercent:     0.5,
			WarningThreshold: 0.8,
			CleanupThreshold: 0.9,
		}

		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		quota := manager.CalculateQuota()
		if quota < config.QuotaMin || quota > config.QuotaMax {
			t.Errorf("quota %d not within bounds [%d, %d]", quota, config.QuotaMin, config.QuotaMax)
		}
	})

	t.Run("config accessor", func(t *testing.T) {
		config := DiskQuotaConfig{
			QuotaMin:         5 * GB,
			QuotaMax:         50 * GB,
			QuotaPercent:     0.15,
			WarningThreshold: 0.75,
			CleanupThreshold: 0.95,
		}

		manager, err := NewDiskQuotaManager(tempDir, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := manager.Config()
		if got.QuotaMin != config.QuotaMin {
			t.Errorf("Config().QuotaMin = %d, want %d", got.QuotaMin, config.QuotaMin)
		}
		if got.QuotaMax != config.QuotaMax {
			t.Errorf("Config().QuotaMax = %d, want %d", got.QuotaMax, config.QuotaMax)
		}
		if got.QuotaPercent != config.QuotaPercent {
			t.Errorf("Config().QuotaPercent = %f, want %f", got.QuotaPercent, config.QuotaPercent)
		}
	})
}

// TestDefaultDiskQuotaConfig tests the default configuration.
func TestDefaultDiskQuotaConfig(t *testing.T) {
	config := DefaultDiskQuotaConfig()

	if config.QuotaMin != 1*GB {
		t.Errorf("expected QuotaMin 1GB, got %d", config.QuotaMin)
	}
	if config.QuotaMax != 20*GB {
		t.Errorf("expected QuotaMax 20GB, got %d", config.QuotaMax)
	}
	if config.QuotaPercent != 0.1 {
		t.Errorf("expected QuotaPercent 0.1, got %f", config.QuotaPercent)
	}
	if config.WarningThreshold != 0.8 {
		t.Errorf("expected WarningThreshold 0.8, got %f", config.WarningThreshold)
	}
	if config.CleanupThreshold != 0.9 {
		t.Errorf("expected CleanupThreshold 0.9, got %f", config.CleanupThreshold)
	}
}

// TestNoOpDiskSignalPublisher tests the no-op implementation.
func TestNoOpDiskSignalPublisher(t *testing.T) {
	pub := &NoOpDiskSignalPublisher{}

	// These should not panic
	pub.PublishWarning(0.85, 850*MB, 1*GB)
	pub.PublishCleanupNeeded(0.95, 950*MB, 1*GB)
}

// TestSetSignalPublisher tests setting a custom publisher.
func TestSetSignalPublisher(t *testing.T) {
	tempDir := t.TempDir()
	config := DiskQuotaConfig{
		QuotaMin:         1 * GB,
		QuotaMax:         1 * GB,
		QuotaPercent:     0.0001,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	quota := manager.CalculateQuota()

	belowCleanup := int64(float64(quota) * 0.85)
	_ = manager.RecordWrite(belowCleanup)

	mockPub := NewMockSignalPublisher()
	manager.SetSignalPublisher(mockPub)

	additionalForCleanup := int64(float64(quota) * 0.06)
	_ = manager.RecordWrite(additionalForCleanup)
	if mockPub.GetCleanupCallCount() < 1 {
		t.Errorf("expected cleanup signal after setting publisher, got %d calls", mockPub.GetCleanupCallCount())
	}
}

// TestCleanupSkipsDirectories tests that cleanup doesn't delete directories.
func TestCleanupSkipsDirectories(t *testing.T) {
	tempDir := t.TempDir()
	config := DefaultDiskQuotaConfig()

	// Create tmp directory with a subdirectory
	tmpDir := filepath.Join(tempDir, "tmp")
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	// Create a file in the subdirectory
	subFile := filepath.Join(subDir, "nested.txt")
	if err := os.WriteFile(subFile, []byte("nested"), 0644); err != nil {
		t.Fatalf("failed to create nested file: %v", err)
	}

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	manager.TriggerCleanup()

	// Subdirectory should still exist
	if _, err := os.Stat(subDir); os.IsNotExist(err) {
		t.Error("expected subdirectory to NOT be deleted")
	}
}

// TestCleanupWithNonExistentDirs tests cleanup handles missing directories.
func TestCleanupWithNonExistentDirs(t *testing.T) {
	tempDir := t.TempDir()
	config := DefaultDiskQuotaConfig()

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not panic when directories don't exist
	manager.TriggerCleanup()
}

// TestCleanupOldCheckpoints tests checkpoint cleanup respects 7-day age.
func TestCleanupOldCheckpoints(t *testing.T) {
	tempDir := t.TempDir()
	config := DefaultDiskQuotaConfig()

	// Create checkpoints directory
	checkpointDir := filepath.Join(tempDir, "checkpoints")
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		t.Fatalf("failed to create checkpoints dir: %v", err)
	}

	// Create old checkpoint (8 days old - should be deleted)
	oldFile := filepath.Join(checkpointDir, "old.ckpt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to create old checkpoint: %v", err)
	}
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(oldFile, oldTime, oldTime)

	// Create new checkpoint (1 day old - should NOT be deleted)
	newFile := filepath.Join(checkpointDir, "new.ckpt")
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatalf("failed to create new checkpoint: %v", err)
	}
	newTime := time.Now().Add(-1 * 24 * time.Hour)
	os.Chtimes(newFile, newTime, newTime)

	manager, err := NewDiskQuotaManager(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	manager.TriggerCleanup()

	// Old checkpoint should be deleted
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("expected old checkpoint to be deleted")
	}

	// New checkpoint should remain
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Error("expected new checkpoint to NOT be deleted")
	}
}

// TestSizeConstants tests size constant values.
func TestSizeConstants(t *testing.T) {
	if KB != 1024 {
		t.Errorf("KB should be 1024, got %d", KB)
	}
	if MB != 1024*1024 {
		t.Errorf("MB should be 1048576, got %d", MB)
	}
	if GB != 1024*1024*1024 {
		t.Errorf("GB should be 1073741824, got %d", GB)
	}
}

// TestErrors tests error variable values.
func TestErrors(t *testing.T) {
	if ErrQuotaExceeded.Error() != "disk quota exceeded" {
		t.Errorf("unexpected error message: %s", ErrQuotaExceeded.Error())
	}
	if ErrCleanupThreshold.Error() != "cleanup threshold reached" {
		t.Errorf("unexpected error message: %s", ErrCleanupThreshold.Error())
	}
	if ErrInvalidBasePath.Error() != "invalid base path" {
		t.Errorf("unexpected error message: %s", ErrInvalidBasePath.Error())
	}
	if ErrStatFailed.Error() != "failed to stat filesystem" {
		t.Errorf("unexpected error message: %s", ErrStatFailed.Error())
	}
}

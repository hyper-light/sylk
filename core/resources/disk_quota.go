package resources

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Disk quota errors
var (
	ErrQuotaExceeded    = errors.New("disk quota exceeded")
	ErrCleanupThreshold = errors.New("cleanup threshold reached")
	ErrInvalidBasePath  = errors.New("invalid base path")
	ErrStatFailed       = errors.New("failed to stat filesystem")
)

// Size constants
const (
	KB int64 = 1024
	MB int64 = 1024 * KB
	GB int64 = 1024 * MB
)

// DiskQuotaConfig defines quota limits and thresholds.
type DiskQuotaConfig struct {
	QuotaMin         int64
	QuotaMax         int64
	QuotaPercent     float64
	WarningThreshold float64
	CleanupThreshold float64
}

// DefaultDiskQuotaConfig returns sensible default quota settings.
func DefaultDiskQuotaConfig() DiskQuotaConfig {
	return DiskQuotaConfig{
		QuotaMin:         1 * GB,
		QuotaMax:         20 * GB,
		QuotaPercent:     0.1,
		WarningThreshold: 0.8,
		CleanupThreshold: 0.9,
	}
}

// DiskSignalPublisher is an interface for publishing disk quota signals.
type DiskSignalPublisher interface {
	PublishWarning(usagePercent float64, usedBytes, quotaBytes int64)
	PublishCleanupNeeded(usagePercent float64, usedBytes, quotaBytes int64)
}

// NoOpDiskSignalPublisher is a stub implementation of DiskSignalPublisher.
type NoOpDiskSignalPublisher struct{}

// PublishWarning is a no-op implementation.
func (n *NoOpDiskSignalPublisher) PublishWarning(usagePercent float64, usedBytes, quotaBytes int64) {}

// PublishCleanupNeeded is a no-op implementation.
func (n *NoOpDiskSignalPublisher) PublishCleanupNeeded(usagePercent float64, usedBytes, quotaBytes int64) {
}

// DiskQuotaManager manages disk usage quotas with auto-scaling.
type DiskQuotaManager struct {
	config    DiskQuotaConfig
	basePath  string
	usage     int64
	mu        sync.RWMutex
	publisher DiskSignalPublisher
	lastScan  time.Time
}

// NewDiskQuotaManager creates a new quota manager for the given path.
func NewDiskQuotaManager(basePath string, config DiskQuotaConfig) (*DiskQuotaManager, error) {
	if basePath == "" {
		return nil, ErrInvalidBasePath
	}

	return &DiskQuotaManager{
		config:    config,
		basePath:  basePath,
		publisher: &NoOpDiskSignalPublisher{},
	}, nil
}

// SetSignalPublisher sets the signal publisher for threshold notifications.
func (m *DiskQuotaManager) SetSignalPublisher(pub DiskSignalPublisher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publisher = pub
}

// CalculateQuota returns the effective quota based on available disk space.
func (m *DiskQuotaManager) CalculateQuota() int64 {
	freeSpace := m.getFreeSpace()
	dynamicQuota := int64(float64(freeSpace) * m.config.QuotaPercent)
	return m.clampQuota(dynamicQuota)
}

// getFreeSpace retrieves available disk space using syscall.Statfs.
func (m *DiskQuotaManager) getFreeSpace() int64 {
	available, blockSize, err := statFs(m.basePath)
	if err != nil {
		return m.config.QuotaMin
	}
	return int64(available) * int64(blockSize)
}

// clampQuota applies min/max bounds to the calculated quota.
func (m *DiskQuotaManager) clampQuota(quota int64) int64 {
	if quota < m.config.QuotaMin {
		return m.config.QuotaMin
	}
	if quota > m.config.QuotaMax {
		return m.config.QuotaMax
	}
	return quota
}

// Usage returns the current tracked disk usage.
func (m *DiskQuotaManager) Usage() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usage
}

// UsagePercent returns the current usage as a percentage of quota.
func (m *DiskQuotaManager) UsagePercent() float64 {
	quota := m.CalculateQuota()
	if quota == 0 {
		return 1.0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return float64(m.usage) / float64(quota)
}

// CanWrite checks if writing size bytes would exceed the quota.
func (m *DiskQuotaManager) CanWrite(size int64) bool {
	quota := m.CalculateQuota()
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usage+size <= quota
}

// RecordWrite tracks a write operation and checks thresholds.
func (m *DiskQuotaManager) RecordWrite(size int64) error {
	quota := m.CalculateQuota()

	m.mu.Lock()
	m.usage += size
	currentUsage := m.usage
	publisher := m.publisher
	m.mu.Unlock()

	return m.checkThresholds(currentUsage, quota, publisher)
}

// checkThresholds evaluates usage against warning and cleanup thresholds.
func (m *DiskQuotaManager) checkThresholds(usage, quota int64, pub DiskSignalPublisher) error {
	usagePercent := float64(usage) / float64(quota)

	if usagePercent >= m.config.CleanupThreshold {
		pub.PublishCleanupNeeded(usagePercent, usage, quota)
		return ErrCleanupThreshold
	}

	if usagePercent >= m.config.WarningThreshold {
		pub.PublishWarning(usagePercent, usage, quota)
	}

	return nil
}

// RecordDelete tracks a deletion, reducing tracked usage.
func (m *DiskQuotaManager) RecordDelete(size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage -= size
	if m.usage < 0 {
		m.usage = 0
	}
}

// TriggerCleanup removes old temporary and cached files.
func (m *DiskQuotaManager) TriggerCleanup() {
	m.cleanupTempFiles()
	m.cleanupOldWAL()
	m.cleanupOldCheckpoints()
	m.cleanupOldStaging()
}

// cleanupTempFiles removes files from the temp directory.
func (m *DiskQuotaManager) cleanupTempFiles() {
	tempDir := filepath.Join(m.basePath, "tmp")
	m.removeOldFiles(tempDir, 0)
}

// cleanupOldWAL removes old WAL segment files.
func (m *DiskQuotaManager) cleanupOldWAL() {
	walDir := filepath.Join(m.basePath, "wal")
	m.removeOldFiles(walDir, 24*time.Hour)
}

// cleanupOldCheckpoints removes old checkpoint files.
func (m *DiskQuotaManager) cleanupOldCheckpoints() {
	checkpointDir := filepath.Join(m.basePath, "checkpoints")
	m.removeOldFiles(checkpointDir, 7*24*time.Hour)
}

// cleanupOldStaging removes old staging files (not active sessions).
func (m *DiskQuotaManager) cleanupOldStaging() {
	stagingDir := filepath.Join(m.basePath, "staging")
	m.removeOldFiles(stagingDir, 24*time.Hour)
}

// removeOldFiles removes files older than maxAge from the directory.
func (m *DiskQuotaManager) removeOldFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		m.removeEntryIfOld(dir, entry, cutoff, maxAge)
	}
}

// removeEntryIfOld removes a single entry if it's older than cutoff.
func (m *DiskQuotaManager) removeEntryIfOld(dir string, entry os.DirEntry, cutoff time.Time, maxAge time.Duration) {
	if entry.IsDir() {
		return
	}

	info, err := entry.Info()
	if err != nil {
		return
	}

	if m.shouldRemoveFile(info, cutoff, maxAge) {
		m.removeAndTrack(filepath.Join(dir, entry.Name()), info.Size())
	}
}

func (m *DiskQuotaManager) shouldRemoveFile(info os.FileInfo, cutoff time.Time, maxAge time.Duration) bool {
	return maxAge == 0 || info.ModTime().Before(cutoff)
}

// removeAndTrack removes a file and updates usage tracking.
func (m *DiskQuotaManager) removeAndTrack(path string, size int64) {
	if err := os.Remove(path); err == nil {
		m.RecordDelete(size)
	}
}

// ScanUsage reconciles tracked usage with actual disk usage.
func (m *DiskQuotaManager) ScanUsage() error {
	totalSize, err := m.calculateDirectorySize()
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.usage = totalSize
	m.lastScan = time.Now()
	m.mu.Unlock()

	return nil
}

func (m *DiskQuotaManager) calculateDirectorySize() (int64, error) {
	var totalSize int64

	err := filepath.WalkDir(m.basePath, func(path string, d os.DirEntry, err error) error {
		totalSize += m.getEntrySize(d, err)
		return nil
	})

	return totalSize, err
}

func (m *DiskQuotaManager) getEntrySize(d os.DirEntry, walkErr error) int64 {
	if walkErr != nil || d.IsDir() {
		return 0
	}
	info, err := d.Info()
	if err != nil {
		return 0
	}
	return info.Size()
}

// LastScan returns the time of the last usage scan.
func (m *DiskQuotaManager) LastScan() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastScan
}

// Config returns a copy of the current configuration.
func (m *DiskQuotaManager) Config() DiskQuotaConfig {
	return m.config
}

// BasePath returns the managed base path.
func (m *DiskQuotaManager) BasePath() string {
	return m.basePath
}

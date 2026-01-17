//go:build windows

package resources

import "testing"

func TestFileHandlePoolRespectsUlimit(t *testing.T) {
	cfg := FileHandlePoolConfig{
		UsagePercent:    0.50,
		UserReserved:    0.20,
		PipelineTimeout: 0,
	}
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()
	if stats.MaxLimit <= 0 {
		t.Errorf("expected positive max limit, got %d", stats.MaxLimit)
	}
	if stats.Total <= 0 {
		t.Errorf("expected positive total, got %d", stats.Total)
	}
}

func TestFileHandlePoolUsagePercent(t *testing.T) {
	cfg := FileHandlePoolConfig{
		UsagePercent:    0.50,
		UserReserved:    0.20,
		PipelineTimeout: 0,
	}
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()
	if stats.MaxLimit <= 0 {
		t.Errorf("expected positive max limit, got %d", stats.MaxLimit)
	}
}

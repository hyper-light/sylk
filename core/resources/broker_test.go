package resources

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newTestBroker(t *testing.T) *ResourceBroker {
	t.Helper()

	memConfig := DefaultMemoryMonitorConfig()
	memConfig.MonitorInterval = 100 * time.Millisecond
	memMon := NewMemoryMonitor(memConfig)

	filePool := NewResourcePool(ResourceTypeFile, DefaultResourcePoolConfig(100))
	netPool := NewResourcePool(ResourceTypeNetwork, DefaultResourcePoolConfig(50))
	procPool := NewResourcePool(ResourceTypeProcess, DefaultResourcePoolConfig(20))

	diskQuota, _ := NewDiskQuotaManager(t.TempDir(), DefaultDiskQuotaConfig())

	config := DefaultBrokerConfig()
	config.AcquisitionTimeout = 1 * time.Second

	t.Cleanup(func() {
		_ = memMon.Close()
		_ = filePool.Close()
		_ = netPool.Close()
		_ = procPool.Close()
	})

	return NewResourceBroker(memMon, filePool, netPool, procPool, diskQuota, nil, config)
}

func TestResourceBroker_AcquireBundle_Success(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	bundle := ResourceBundle{
		FileHandles:    5,
		NetworkConns:   3,
		Subprocesses:   2,
		MemoryEstimate: 1024,
		DiskEstimate:   2048,
	}

	alloc, err := broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 5)
	if err != nil {
		t.Fatalf("AcquireBundle failed: %v", err)
	}

	if alloc == nil {
		t.Fatal("expected allocation, got nil")
	}

	if alloc.PipelineID != "pipeline-1" {
		t.Errorf("expected pipeline-1, got %s", alloc.PipelineID)
	}

	if len(alloc.FileHandles) != 5 {
		t.Errorf("expected 5 file handles, got %d", len(alloc.FileHandles))
	}

	if len(alloc.NetworkConns) != 3 {
		t.Errorf("expected 3 network conns, got %d", len(alloc.NetworkConns))
	}

	if len(alloc.Subprocesses) != 2 {
		t.Errorf("expected 2 subprocesses, got %d", len(alloc.Subprocesses))
	}
}

func TestResourceBroker_AcquireBundle_ZeroResources(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	bundle := ResourceBundle{
		FileHandles:    0,
		NetworkConns:   0,
		Subprocesses:   0,
		MemoryEstimate: 0,
		DiskEstimate:   0,
	}

	alloc, err := broker.AcquireBundle(context.Background(), "empty-pipeline", bundle, 1)
	if err != nil {
		t.Fatalf("AcquireBundle failed: %v", err)
	}

	if alloc == nil {
		t.Fatal("expected allocation, got nil")
	}

	if len(alloc.FileHandles) != 0 {
		t.Errorf("expected 0 file handles, got %d", len(alloc.FileHandles))
	}
}

func TestResourceBroker_ReleaseBundle(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	bundle := ResourceBundle{FileHandles: 3}
	alloc, err := broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)
	if err != nil {
		t.Fatalf("AcquireBundle failed: %v", err)
	}

	statsBefore := broker.Stats()
	if statsBefore.ActiveAllocations != 1 {
		t.Errorf("expected 1 active allocation, got %d", statsBefore.ActiveAllocations)
	}

	err = broker.ReleaseBundle(alloc)
	if err != nil {
		t.Fatalf("ReleaseBundle failed: %v", err)
	}

	statsAfter := broker.Stats()
	if statsAfter.ActiveAllocations != 0 {
		t.Errorf("expected 0 active allocations, got %d", statsAfter.ActiveAllocations)
	}
}

func TestResourceBroker_ReleaseBundleByID(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	bundle := ResourceBundle{FileHandles: 3}
	_, err := broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)
	if err != nil {
		t.Fatalf("AcquireBundle failed: %v", err)
	}

	err = broker.ReleaseBundleByID("pipeline-1")
	if err != nil {
		t.Fatalf("ReleaseBundleByID failed: %v", err)
	}

	statsAfter := broker.Stats()
	if statsAfter.ActiveAllocations != 0 {
		t.Errorf("expected 0 active allocations, got %d", statsAfter.ActiveAllocations)
	}
}

func TestResourceBroker_ReleaseBundle_Nil(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	err := broker.ReleaseBundle(nil)
	if err != nil {
		t.Errorf("expected no error for nil release, got %v", err)
	}
}

func TestResourceBroker_ReleaseBundleByID_NotFound(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	err := broker.ReleaseBundleByID("nonexistent")
	if err != nil {
		t.Errorf("expected no error for nonexistent release, got %v", err)
	}
}

func TestResourceBroker_GetAllocation(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	bundle := ResourceBundle{FileHandles: 1}
	_, _ = broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)

	alloc, found := broker.GetAllocation("pipeline-1")
	if !found {
		t.Fatal("expected to find allocation")
	}
	if alloc.PipelineID != "pipeline-1" {
		t.Errorf("expected pipeline-1, got %s", alloc.PipelineID)
	}

	_, found = broker.GetAllocation("nonexistent")
	if found {
		t.Error("expected not to find nonexistent allocation")
	}
}

func TestResourceBroker_AcquireBundle_Closed(t *testing.T) {
	broker := newTestBroker(t)
	_ = broker.Close()

	bundle := ResourceBundle{FileHandles: 1}
	_, err := broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)
	if err == nil {
		t.Fatal("expected error for closed broker")
	}
}

func TestResourceBroker_AcquireUser_Success(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	bundle := ResourceBundle{
		FileHandles:  3,
		NetworkConns: 2,
		Subprocesses: 1,
	}

	alloc, err := broker.AcquireUser(context.Background(), bundle)
	if err != nil {
		t.Fatalf("AcquireUser failed: %v", err)
	}

	if alloc == nil {
		t.Fatal("expected allocation")
	}

	if alloc.PipelineID != "user" {
		t.Errorf("expected 'user', got %s", alloc.PipelineID)
	}

	if len(alloc.FileHandles) != 3 {
		t.Errorf("expected 3 file handles, got %d", len(alloc.FileHandles))
	}
}

func TestResourceBroker_AcquireUser_Closed(t *testing.T) {
	broker := newTestBroker(t)
	_ = broker.Close()

	bundle := ResourceBundle{FileHandles: 1}
	_, err := broker.AcquireUser(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error for closed broker")
	}
}

func TestResourceBroker_Close_ReleasesAllocations(t *testing.T) {
	broker := newTestBroker(t)

	bundle := ResourceBundle{FileHandles: 5, NetworkConns: 3}
	_, _ = broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)
	_, _ = broker.AcquireBundle(context.Background(), "pipeline-2", bundle, 2)

	err := broker.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	stats := broker.Stats()
	if stats.ActiveAllocations != 0 {
		t.Errorf("expected 0 allocations after close, got %d", stats.ActiveAllocations)
	}
}

func TestResourceBroker_Close_Idempotent(t *testing.T) {
	broker := newTestBroker(t)

	err1 := broker.Close()
	err2 := broker.Close()

	if err1 != nil || err2 != nil {
		t.Errorf("Close should be idempotent, got err1=%v, err2=%v", err1, err2)
	}
}

func TestResourceBroker_ConcurrentAcquire(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	var wg sync.WaitGroup
	const numGoroutines = 10

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			bundle := ResourceBundle{FileHandles: 2, NetworkConns: 1}
			pipelineID := "pipeline-" + string(rune('A'+idx))
			_, _ = broker.AcquireBundle(context.Background(), pipelineID, bundle, idx)
		}(i)
	}

	wg.Wait()

	stats := broker.Stats()
	if stats.ActiveAllocations != numGoroutines {
		t.Errorf("expected %d allocations, got %d", numGoroutines, stats.ActiveAllocations)
	}
}

func TestResourceBroker_ConcurrentAcquireRelease(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	var wg sync.WaitGroup
	const iterations = 50

	for i := range iterations {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			bundle := ResourceBundle{FileHandles: 1}
			pipelineID := "pipeline-" + string(rune('A'+(idx%26)))

			alloc, err := broker.AcquireBundle(context.Background(), pipelineID, bundle, 1)
			if err != nil {
				return
			}
			if alloc != nil {
				_ = broker.ReleaseBundle(alloc)
			}
		}(i)
	}

	wg.Wait()
}

func TestResourceBroker_Stats(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	bundle := ResourceBundle{FileHandles: 5, NetworkConns: 3, Subprocesses: 2}
	_, _ = broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)

	stats := broker.Stats()

	if stats.ActiveAllocations != 1 {
		t.Errorf("expected 1 active allocation, got %d", stats.ActiveAllocations)
	}

	if stats.FilePool.InUse != 5 {
		t.Errorf("expected 5 file handles in use, got %d", stats.FilePool.InUse)
	}

	if stats.NetworkPool.InUse != 3 {
		t.Errorf("expected 3 network conns in use, got %d", stats.NetworkPool.InUse)
	}

	if stats.ProcessPool.InUse != 2 {
		t.Errorf("expected 2 processes in use, got %d", stats.ProcessPool.InUse)
	}
}

func TestResourceBroker_NilPools(t *testing.T) {
	config := DefaultBrokerConfig()
	broker := NewResourceBroker(nil, nil, nil, nil, nil, nil, config)
	defer broker.Close()

	bundle := ResourceBundle{FileHandles: 1}
	alloc, err := broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)
	if err != nil {
		t.Fatalf("expected success with nil pools, got %v", err)
	}

	if alloc == nil {
		t.Fatal("expected allocation")
	}
}

func TestResourceBroker_PartialAcquireRollback(t *testing.T) {
	memConfig := DefaultMemoryMonitorConfig()
	memMon := NewMemoryMonitor(memConfig)
	defer memMon.Close()

	filePool := NewResourcePool(ResourceTypeFile, DefaultResourcePoolConfig(10))
	netPool := NewResourcePool(ResourceTypeNetwork, DefaultResourcePoolConfig(1))
	procPool := NewResourcePool(ResourceTypeProcess, DefaultResourcePoolConfig(20))
	defer filePool.Close()
	defer netPool.Close()
	defer procPool.Close()

	diskQuota, _ := NewDiskQuotaManager(t.TempDir(), DefaultDiskQuotaConfig())

	config := DefaultBrokerConfig()
	config.AcquisitionTimeout = 100 * time.Millisecond
	broker := NewResourceBroker(memMon, filePool, netPool, procPool, diskQuota, nil, config)
	defer broker.Close()

	netHandle, _ := netPool.AcquirePipeline(context.Background(), 10)

	bundle := ResourceBundle{FileHandles: 5, NetworkConns: 2}
	_, err := broker.AcquireBundle(context.Background(), "pipeline-1", bundle, 1)
	if err == nil {
		t.Fatal("expected error when network pool exhausted")
	}

	_ = netPool.Release(netHandle)

	fileStats := filePool.Stats()
	if fileStats.InUse != 0 {
		t.Errorf("file handles not rolled back, %d still in use", fileStats.InUse)
	}
}

func TestResourceBroker_WaitGraph(t *testing.T) {
	broker := newTestBroker(t)
	defer broker.Close()

	broker.AddWaitEdge("pipeline-1", "pipeline-2")
	broker.AddWaitEdge("pipeline-2", "pipeline-3")

	stats := broker.Stats()
	if stats.WaitGraphSize != 2 {
		t.Errorf("expected wait graph size 2, got %d", stats.WaitGraphSize)
	}

	broker.RemoveWaitEdge("pipeline-1")

	stats = broker.Stats()
	if stats.WaitGraphSize != 1 {
		t.Errorf("expected wait graph size 1, got %d", stats.WaitGraphSize)
	}
}

func TestResourceBroker_DeadlockDetection(t *testing.T) {
	memConfig := DefaultMemoryMonitorConfig()
	memMon := NewMemoryMonitor(memConfig)
	defer memMon.Close()

	filePool := NewResourcePool(ResourceTypeFile, DefaultResourcePoolConfig(10))
	defer filePool.Close()

	config := DefaultBrokerConfig()
	config.DeadlockDetection = true
	config.DetectionInterval = 50 * time.Millisecond

	broker := NewResourceBroker(memMon, filePool, nil, nil, nil, nil, config)
	defer broker.Close()

	broker.AddWaitEdge("A", "B")
	broker.AddWaitEdge("B", "C")
	broker.AddWaitEdge("C", "A")

	time.Sleep(100 * time.Millisecond)
}

func TestResourceBroker_NoDeadlockDetection(t *testing.T) {
	config := DefaultBrokerConfig()
	config.DeadlockDetection = false

	broker := NewResourceBroker(nil, nil, nil, nil, nil, nil, config)

	err := broker.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

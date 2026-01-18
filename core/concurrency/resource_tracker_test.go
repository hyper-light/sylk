package concurrency

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type mockResource struct {
	id         string
	resType    ResourceType
	closed     atomic.Bool
	closeErr   error
	closeCalls atomic.Int32
}

func newMockResource(id string, resType ResourceType) *mockResource {
	return &mockResource{
		id:      id,
		resType: resType,
	}
}

func (m *mockResource) ID() string {
	return m.id
}

func (m *mockResource) Type() ResourceType {
	return m.resType
}

func (m *mockResource) ForceClose() error {
	m.closeCalls.Add(1)
	m.closed.Store(true)
	return m.closeErr
}

func (m *mockResource) IsClosed() bool {
	return m.closed.Load()
}

func (m *mockResource) withCloseError(err error) *mockResource {
	m.closeErr = err
	return m
}

func TestResourceTracker_TrackAddsResource(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res := newMockResource("file-1", ResourceTypeFile)

	tracker.Track(res)

	if tracker.Count() != 1 {
		t.Errorf("expected count 1, got %d", tracker.Count())
	}

	resources := tracker.List()
	if len(resources) != 1 {
		t.Errorf("expected 1 resource in list, got %d", len(resources))
	}
}

func TestResourceTracker_TrackIgnoresDuplicates(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res := newMockResource("file-1", ResourceTypeFile)

	tracker.Track(res)
	tracker.Track(res)

	if tracker.Count() != 1 {
		t.Errorf("expected count 1 after duplicate track, got %d", tracker.Count())
	}
}

func TestResourceTracker_ReleaseRemovesResource(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res := newMockResource("file-1", ResourceTypeFile)

	tracker.Track(res)
	tracker.Release(res)

	if tracker.Count() != 0 {
		t.Errorf("expected count 0 after release, got %d", tracker.Count())
	}
}

func TestResourceTracker_ReleaseIgnoresUntracked(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res := newMockResource("file-1", ResourceTypeFile)

	tracker.Release(res)

	if tracker.Count() != 0 {
		t.Errorf("expected count 0 for untracked release, got %d", tracker.Count())
	}
}

func TestResourceTracker_ForceCloseAllClosesResources(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res1 := newMockResource("file-1", ResourceTypeFile)
	res2 := newMockResource("conn-1", ResourceTypeConnection)

	tracker.Track(res1)
	tracker.Track(res2)

	errs := tracker.ForceCloseAll()

	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}

	if res1.closeCalls.Load() != 1 {
		t.Errorf("expected res1 ForceClose called once, got %d", res1.closeCalls.Load())
	}

	if res2.closeCalls.Load() != 1 {
		t.Errorf("expected res2 ForceClose called once, got %d", res2.closeCalls.Load())
	}

	if tracker.Count() != 0 {
		t.Errorf("expected count 0 after ForceCloseAll, got %d", tracker.Count())
	}
}

func TestResourceTracker_ForceCloseAllCollectsErrors(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res1 := newMockResource("file-1", ResourceTypeFile).withCloseError(errors.New("close failed"))
	res2 := newMockResource("conn-1", ResourceTypeConnection)

	tracker.Track(res1)
	tracker.Track(res2)

	errs := tracker.ForceCloseAll()

	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}
}

func TestResourceTracker_ForceCloseAllOnEmptyTracker(t *testing.T) {
	tracker := NewResourceTracker("agent-1")

	errs := tracker.ForceCloseAll()

	if len(errs) != 0 {
		t.Errorf("expected no errors on empty tracker, got %v", errs)
	}
}

func TestResourceTracker_ForceCloseByTypeOnlyClosesMatchingType(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	file := newMockResource("file-1", ResourceTypeFile)
	conn := newMockResource("conn-1", ResourceTypeConnection)
	proc := newMockResource("proc-1", ResourceTypeProcess)

	tracker.Track(file)
	tracker.Track(conn)
	tracker.Track(proc)

	errs := tracker.ForceCloseByType(ResourceTypeFile)

	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}

	if file.closeCalls.Load() != 1 {
		t.Errorf("expected file ForceClose called, got %d", file.closeCalls.Load())
	}

	if conn.closeCalls.Load() != 0 {
		t.Errorf("expected conn ForceClose not called, got %d", conn.closeCalls.Load())
	}

	if proc.closeCalls.Load() != 0 {
		t.Errorf("expected proc ForceClose not called, got %d", proc.closeCalls.Load())
	}

	if tracker.Count() != 2 {
		t.Errorf("expected 2 resources remaining, got %d", tracker.Count())
	}
}

func TestResourceTracker_CountByType(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	tracker.Track(newMockResource("file-1", ResourceTypeFile))
	tracker.Track(newMockResource("file-2", ResourceTypeFile))
	tracker.Track(newMockResource("conn-1", ResourceTypeConnection))

	fileCount := tracker.CountByType(ResourceTypeFile)
	connCount := tracker.CountByType(ResourceTypeConnection)
	procCount := tracker.CountByType(ResourceTypeProcess)

	if fileCount != 2 {
		t.Errorf("expected 2 files, got %d", fileCount)
	}

	if connCount != 1 {
		t.Errorf("expected 1 connection, got %d", connCount)
	}

	if procCount != 0 {
		t.Errorf("expected 0 processes, got %d", procCount)
	}
}

func TestResourceTracker_MetricsTrackedAccurately(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res1 := newMockResource("file-1", ResourceTypeFile)
	res2 := newMockResource("conn-1", ResourceTypeConnection)

	tracker.Track(res1)
	tracker.Track(res2)

	metrics := tracker.GetMetrics()
	if metrics.TotalTracked != 2 {
		t.Errorf("expected TotalTracked 2, got %d", metrics.TotalTracked)
	}

	tracker.Release(res1)
	metrics = tracker.GetMetrics()
	if metrics.TotalReleased != 1 {
		t.Errorf("expected TotalReleased 1, got %d", metrics.TotalReleased)
	}

	tracker.ForceCloseAll()
	metrics = tracker.GetMetrics()
	if metrics.TotalForceClosed != 1 {
		t.Errorf("expected TotalForceClosed 1, got %d", metrics.TotalForceClosed)
	}
}

func TestResourceTracker_MetricsActiveByType(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	tracker.Track(newMockResource("file-1", ResourceTypeFile))
	tracker.Track(newMockResource("file-2", ResourceTypeFile))
	tracker.Track(newMockResource("conn-1", ResourceTypeConnection))

	metrics := tracker.GetMetrics()

	if metrics.ActiveByType[ResourceTypeFile] != 2 {
		t.Errorf("expected 2 active files, got %d", metrics.ActiveByType[ResourceTypeFile])
	}

	if metrics.ActiveByType[ResourceTypeConnection] != 1 {
		t.Errorf("expected 1 active connection, got %d", metrics.ActiveByType[ResourceTypeConnection])
	}
}

func TestResourceTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res := newMockResource(
				"res-"+string(rune('a'+idx%26))+"-"+string(rune('0'+idx%10)),
				ResourceType([]ResourceType{ResourceTypeFile, ResourceTypeConnection, ResourceTypeProcess}[idx%3]),
			)
			tracker.Track(res)
			_ = tracker.Count()
			_ = tracker.GetMetrics()
			_ = tracker.List()
		}(i)
	}

	wg.Wait()

	count := tracker.Count()
	if count == 0 {
		t.Error("expected some resources to be tracked")
	}
}

func TestResourceTracker_ConcurrentTrackAndRelease(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	var wg sync.WaitGroup
	numOps := 50

	resources := make([]*mockResource, numOps)
	for i := 0; i < numOps; i++ {
		resources[i] = newMockResource("res-"+string(rune('a'+i%26)), ResourceTypeFile)
	}

	for i := 0; i < numOps; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			tracker.Track(resources[idx])
		}(i)
		go func(idx int) {
			defer wg.Done()
			tracker.Release(resources[idx])
		}(i)
	}

	wg.Wait()
}

func TestResourceTracker_ConcurrentForceCloseAll(t *testing.T) {
	tracker := NewResourceTracker("agent-1")

	for i := 0; i < 10; i++ {
		tracker.Track(newMockResource("res-"+string(rune('a'+i)), ResourceTypeFile))
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tracker.ForceCloseAll()
		}()
	}

	wg.Wait()

	if tracker.Count() != 0 {
		t.Errorf("expected count 0 after concurrent ForceCloseAll, got %d", tracker.Count())
	}
}

func TestResourceTracker_AgentID(t *testing.T) {
	tracker := NewResourceTracker("test-agent")

	if tracker.AgentID() != "test-agent" {
		t.Errorf("expected agent ID 'test-agent', got '%s'", tracker.AgentID())
	}
}

func TestResourceTracker_SkipsAlreadyClosedResources(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res := newMockResource("file-1", ResourceTypeFile)
	res.closed.Store(true)

	tracker.Track(res)
	errs := tracker.ForceCloseAll()

	if len(errs) != 0 {
		t.Errorf("expected no errors for already closed resource, got %v", errs)
	}

	if res.closeCalls.Load() != 0 {
		t.Errorf("expected ForceClose not called on already closed resource, got %d", res.closeCalls.Load())
	}
}

func TestResourceTracker_ReleaseUpdatesTypeCount(t *testing.T) {
	tracker := NewResourceTracker("agent-1")
	res := newMockResource("file-1", ResourceTypeFile)

	tracker.Track(res)
	if tracker.CountByType(ResourceTypeFile) != 1 {
		t.Errorf("expected 1 file after track, got %d", tracker.CountByType(ResourceTypeFile))
	}

	tracker.Release(res)
	if tracker.CountByType(ResourceTypeFile) != 0 {
		t.Errorf("expected 0 files after release, got %d", tracker.CountByType(ResourceTypeFile))
	}
}

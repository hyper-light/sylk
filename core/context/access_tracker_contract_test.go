package context

import (
	"testing"

	"github.com/adalundhe/sylk/core/resources"
)

// TestAccessTracker_ImplementsEvictableCache locks the MEM-08 interface
// contract: the existing AccessTracker must satisfy
// resources.EvictableCache so the eviction orchestration layer can
// register it alongside other bounded caches. A compile-time assertion
// is sufficient — the test body merely holds it at package scope so a
// regression (removed method, wrong signature) fails the build here
// rather than deep in the orchestration layer.
func TestAccessTracker_ImplementsEvictableCache(t *testing.T) {
	var _ resources.EvictableCache = (*AccessTracker)(nil)
	// Additionally drive each method so the test covers the actual call
	// surface, not just the type system.
	tracker := NewDefaultAccessTracker()
	if tracker.Name() != "access-tracker" {
		t.Errorf("Name() = %q, want access-tracker", tracker.Name())
	}
	tracker.RecordAccess("doc-1", 1, AccessSourceInResponse)
	if tracker.Size() <= 0 {
		t.Error("Size() must be > 0 after recording an access")
	}
	if freed := tracker.EvictPercent(1.0); freed < 0 {
		t.Errorf("EvictPercent = %d, want >= 0", freed)
	}
}

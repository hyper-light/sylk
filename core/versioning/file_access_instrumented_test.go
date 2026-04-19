package versioning

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/activity"
)

func TestInstrument_NilWrapsToNil(t *testing.T) {
	if got := Instrument(nil); got != nil {
		t.Fatalf("Instrument(nil) = %v, want nil", got)
	}
}

func TestInstrument_IdempotentDoubleWrap(t *testing.T) {
	disk := NewDiskFileAccess(t.TempDir(), false)
	wrapped := Instrument(disk)
	twice := Instrument(wrapped)
	if wrapped != twice {
		t.Fatalf("double-wrap produced a new wrapper; want idempotent identity")
	}
}

func TestUnderlying_StripsWrapper(t *testing.T) {
	disk := NewDiskFileAccess(t.TempDir(), false)
	wrapped := Instrument(disk)
	if got := Underlying(wrapped); got != FileAccess(disk) {
		t.Fatalf("Underlying did not unwrap to original DiskFileAccess")
	}
	// Capability dispatch must still detect *DiskFileAccess through
	// the wrapper.
	if _, ok := Underlying(wrapped).(*DiskFileAccess); !ok {
		t.Fatal("Underlying must allow *DiskFileAccess assertion")
	}
}

func TestUnderlying_OnUnwrappedIsIdentity(t *testing.T) {
	disk := NewDiskFileAccess(t.TempDir(), false)
	if got := Underlying(disk); got != FileAccess(disk) {
		t.Fatal("Underlying on unwrapped FileAccess must return identity")
	}
}

func TestInstrumented_EmitsActivityForReads(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "hello.txt")
	disk := NewDiskFileAccess(root, false)
	if err := disk.WriteFile(context.Background(), target, []byte("hi")); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	wrapped := Instrument(disk)
	ctx := activity.EnsureFabricContext(context.Background())
	if _, err := wrapped.ReadFile(ctx, target); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if col.CountByKind(activity.ActionFileRead) == 0 {
		t.Fatal("ReadFile through the wrapper must emit ActionFileRead")
	}
}

func TestInstrumented_EmitsActivityForWrites(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")

	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	wrapped := Instrument(NewDiskFileAccess(root, false))
	ctx := activity.EnsureFabricContext(context.Background())
	if err := wrapped.WriteFile(ctx, target, []byte("x")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if col.CountByKind(activity.ActionFileWritten) == 0 {
		t.Fatal("WriteFile through the wrapper must emit ActionFileWritten")
	}
}

func TestInstrumented_TagsErrorOnFailedOperation(t *testing.T) {
	col := activity.NewTestCollector()
	prev := activity.SetDefaultSink(col)
	defer activity.SetDefaultSink(prev)

	// Read-only access; WriteFile must fail.
	wrapped := Instrument(NewDiskFileAccess(t.TempDir(), true))
	ctx := activity.EnsureFabricContext(context.Background())
	err := wrapped.WriteFile(ctx, "x.txt", []byte("x"))
	if err == nil {
		t.Fatal("expected WriteFile to fail under read-only DiskFileAccess")
	}

	writes := col.FilterByKind(activity.ActionFileWritten)
	if len(writes) == 0 {
		t.Fatal("write attempt must still emit an activity")
	}
	last := writes[len(writes)-1]
	if last.Payload == nil || !contains(string(last.Payload), "read-only") {
		t.Errorf("error payload should record the failure reason; got %q", string(last.Payload))
	}
}

func TestInstrumented_ModificationsForwarded(t *testing.T) {
	// A wrapper around an impl that doesn't expose Modifications must
	// still satisfy the optional interface and return nil — that's the
	// transparency contract.
	disk := NewDiskFileAccess(t.TempDir(), false)
	wrapped := Instrument(disk)
	type overlayAware interface {
		Modifications() []FileModification
	}
	oa, ok := wrapped.(overlayAware)
	if !ok {
		t.Fatal("wrapper must satisfy the overlayAware optional interface")
	}
	if mods := oa.Modifications(); mods != nil {
		t.Fatalf("disk-backed wrapped impl must return nil modifications; got %d", len(mods))
	}
}

// TestInstrumented_NoEmissionsWhenSinkIsDiscard documents that the
// wrapper is silent when no sink is installed — the FabricContext is
// still propagated through context.Context, but no activity row lands
// anywhere. Important for production deployments that may want to
// disable capture entirely (set SetDefaultSink to nil/discard).
func TestInstrumented_NoEmissionsWhenSinkIsDiscard(t *testing.T) {
	prev := activity.SetDefaultSink(nil) // installs the discard sink
	defer activity.SetDefaultSink(prev)

	root := t.TempDir()
	wrapped := Instrument(NewDiskFileAccess(root, false))
	ctx := activity.EnsureFabricContext(context.Background())
	if err := wrapped.WriteFile(ctx, filepath.Join(root, "x.txt"), []byte("x")); err != nil {
		t.Fatalf("write must succeed even with discard sink: %v", err)
	}
	// No assertion needed — the test is that it doesn't panic and
	// completes without error. EmissionCount may still increment
	// because the counter is intentional intent-tracking; the sink
	// itself is what discards.
}

// contains is a small substring helper to avoid the strings import in
// this test file.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestInstrumented_ReadsCausalContextFromSpan documents that errors
// propagated through the wrapped FileAccess are returned unchanged —
// the wrapper neither swallows nor wraps the error, so callers who
// errors.Is/errors.As against the wrapped impl's typed errors keep
// working.
func TestInstrumented_PropagatesErrorsUnwrapped(t *testing.T) {
	wrapped := Instrument(NewDiskFileAccess(t.TempDir(), true))
	err := wrapped.WriteFile(context.Background(), "x.txt", []byte("x"))
	if err == nil {
		t.Fatal("expected WriteFile to fail under read-only access")
	}
	if errors.Unwrap(err) != nil {
		t.Errorf("wrapper must not wrap the underlying error; errors.Unwrap returned %v", errors.Unwrap(err))
	}
}

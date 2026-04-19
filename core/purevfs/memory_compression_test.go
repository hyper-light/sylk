package purevfs

import (
	"context"
	"errors"
	"testing"
)

// newTestBudgetedNamespace constructs a memory namespace with a tight budget
// for pressure tests. The budget is owned by a dedicated test governor that
// does not share state with other tests.
func newTestBudgetedNamespace(t *testing.T, maxBytes int64) *memoryNamespace {
	t.Helper()
	gov := &executionGovernor{
		totalLimitBytes: maxBytes,
		runLimitBytes:   maxBytes,
	}
	budget := &executionBudget{governor: gov, limit: maxBytes}
	return newMemoryNamespace(budget)
}

// TestMemoryCompression_CompressesColdFilesUnderPressure verifies the core
// invariant: when a write would exceed the budget, cold files in the same
// namespace get compressed to make room, and the write succeeds. Without
// this behavior, a pytest run that generates many small cache files would
// fail the first time the budget fills — the whole point of the feature.
func TestMemoryCompression_CompressesColdFilesUnderPressure(t *testing.T) {
	// Budget = 1000 bytes. Store three 300-byte highly compressible files
	// (total 900). A new 200-byte write would push us to 1100 — pressure.
	ns := newTestBudgetedNamespace(t, 1000)
	ctx := context.Background()

	compressible := make([]byte, 300)
	for i := range compressible {
		compressible[i] = 'a' // highly compressible: all same byte
	}
	for _, name := range []string{"/cold1", "/cold2", "/cold3"} {
		if err := ns.WriteFile(ctx, name, compressible); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Now attempt a write that would exceed the budget without compression.
	if err := ns.WriteFile(ctx, "/hot", make([]byte, 200)); err != nil {
		t.Fatalf("expected /hot write to succeed after cold-file compression, got %v", err)
	}

	// Verify at least one cold file got compressed. DEFLATE on 300 bytes of
	// a single byte hits ~6x; compressed content should be under 50 bytes.
	compressedCount := 0
	ns.mu.RLock()
	for _, name := range []string{"/cold1", "/cold2", "/cold3"} {
		if ns.files[name].compressed {
			compressedCount++
		}
	}
	ns.mu.RUnlock()
	if compressedCount == 0 {
		t.Fatal("expected at least one cold file to be compressed under pressure")
	}
}

// TestMemoryCompression_PreservesContentRoundTrip verifies correctness:
// compression must be lossless. A file written, then compressed under
// pressure, then read, returns byte-identical content.
func TestMemoryCompression_PreservesContentRoundTrip(t *testing.T) {
	ns := newTestBudgetedNamespace(t, 1000)
	ctx := context.Background()

	original := []byte("the quick brown fox jumps over the lazy dog; pack my box with five dozen liquor jugs")
	if err := ns.WriteFile(ctx, "/fixture", original); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Force compression by filling the budget with cold files that push
	// /fixture to the coldest slot, then triggering pressure.
	stuffing := make([]byte, 400)
	for i := range stuffing {
		stuffing[i] = 'b'
	}
	if err := ns.WriteFile(ctx, "/cold", stuffing); err != nil {
		t.Fatalf("WriteFile /cold: %v", err)
	}
	// Trigger pressure via a write larger than the remaining budget. /fixture
	// is the oldest, so it compresses first.
	if err := ns.WriteFile(ctx, "/another", make([]byte, 500)); err != nil {
		t.Fatalf("WriteFile /another under pressure: %v", err)
	}

	// Read back /fixture — must round-trip.
	got, err := ns.ReadFile(ctx, "/fixture")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("content = %q, want %q (compression must be lossless)", got, original)
	}
}

// TestMemoryCompression_InflatesOnReadWhenBudgetAllows verifies the hot-file
// path: reading a compressed file returns bytes, and if the budget has
// room, the file is re-stored uncompressed so subsequent reads don't pay
// the decompression cost.
func TestMemoryCompression_InflatesOnReadWhenBudgetAllows(t *testing.T) {
	ns := newTestBudgetedNamespace(t, 10_000) // generous budget
	ctx := context.Background()

	original := make([]byte, 500)
	for i := range original {
		original[i] = 'c'
	}
	if err := ns.WriteFile(ctx, "/cached", original); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Manually compress the file to simulate prior pressure.
	ns.mu.Lock()
	freed := ns.compressColdFilesLocked(100)
	ns.mu.Unlock()
	if freed == 0 {
		t.Fatal("expected compression pass to free some bytes")
	}

	// Confirm compressed.
	ns.mu.RLock()
	wasCompressed := ns.files["/cached"].compressed
	ns.mu.RUnlock()
	if !wasCompressed {
		t.Fatal("test precondition failed: file was not compressed")
	}

	// Read — should inflate in place because budget has plenty of room.
	got, err := ns.ReadFile(ctx, "/cached")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("content = %q, want equal to original", got[:10])
	}
	ns.mu.RLock()
	stillCompressed := ns.files["/cached"].compressed
	ns.mu.RUnlock()
	if stillCompressed {
		t.Fatal("expected file to be re-stored uncompressed after read with budget headroom")
	}
}

// TestMemoryCompression_ServesTransientWhenBudgetPinned verifies the fallback
// path: if inflating would exceed the budget AND no cold files can be
// compressed to make room, reads still return correct bytes via a
// transient buffer, leaving the file compressed in storage.
func TestMemoryCompression_ServesTransientWhenBudgetPinned(t *testing.T) {
	ns := newTestBudgetedNamespace(t, 2000)
	ctx := context.Background()

	// Write two compressible files. One gets compressed later; one stays
	// hot.
	hot := make([]byte, 800)
	cold := make([]byte, 800)
	for i := range hot {
		hot[i] = 'h'
		cold[i] = 'c'
	}
	if err := ns.WriteFile(ctx, "/cold", cold); err != nil {
		t.Fatalf("write /cold: %v", err)
	}
	if err := ns.WriteFile(ctx, "/hot", hot); err != nil {
		t.Fatalf("write /hot: %v", err)
	}
	// Touch /hot so /cold is older.
	if _, err := ns.ReadFile(ctx, "/hot"); err != nil {
		t.Fatalf("touch /hot: %v", err)
	}

	// Force compression of /cold.
	ns.mu.Lock()
	freed := ns.compressColdFilesLocked(500)
	ns.mu.Unlock()
	if freed == 0 {
		t.Fatal("expected compression pass to free bytes")
	}

	// Now fill the budget with a new file so inflate cannot fit /cold's
	// raw size back in.
	stuffing := make([]byte, 1100)
	if err := ns.WriteFile(ctx, "/pin", stuffing); err != nil {
		t.Fatalf("write /pin: %v", err)
	}

	// Read /cold — must return the original bytes even though inflate
	// can't fit. File stays compressed.
	got, err := ns.ReadFile(ctx, "/cold")
	if err != nil {
		t.Fatalf("ReadFile /cold: %v", err)
	}
	if string(got) != string(cold) {
		t.Fatalf("content mismatch on transient-read path")
	}
}

// TestMemoryCompression_StatReturnsLogicalSize verifies that Stat and
// ReadDir report the uncompressed (logical) size, not the compressed
// payload size. Processes inside the sandbox must see the same file size
// whether the file happens to be compressed or not.
func TestMemoryCompression_StatReturnsLogicalSize(t *testing.T) {
	ns := newTestBudgetedNamespace(t, 10_000)
	ctx := context.Background()

	data := make([]byte, 1000)
	for i := range data {
		data[i] = 'x'
	}
	if err := ns.WriteFile(ctx, "/observed", data); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ns.mu.Lock()
	freed := ns.compressColdFilesLocked(500)
	ns.mu.Unlock()
	if freed == 0 {
		t.Fatal("expected compression to free bytes")
	}

	info, err := ns.Stat(ctx, "/observed")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 1000 {
		t.Fatalf("stat size = %d, want 1000 (logical, not compressed)", info.Size())
	}
}

// TestMemoryCompression_FailsWhenBudgetTooSmallEvenCompressed verifies the
// escape hatch: if even with full compression the workload exceeds the
// budget, the write fails with ErrMemoryPressure. No silent spill to
// disk — the user must raise the budget explicitly.
func TestMemoryCompression_FailsWhenBudgetTooSmallEvenCompressed(t *testing.T) {
	ns := newTestBudgetedNamespace(t, 100)
	ctx := context.Background()

	// Incompressible payload: random-ish bytes that DEFLATE cannot
	// meaningfully shrink.
	pattern := []byte{0x7f, 0x3a, 0xe2, 0x09, 0xbb, 0x51, 0xc4, 0x8d}
	payload := make([]byte, 200)
	for i := range payload {
		payload[i] = pattern[i%len(pattern)]
	}
	err := ns.WriteFile(ctx, "/too-big", payload)
	if err == nil {
		t.Fatal("expected ErrMemoryPressure for write larger than budget")
	}
	if !errors.Is(err, ErrMemoryPressure) {
		t.Fatalf("got %v, want ErrMemoryPressure", err)
	}
}

// TestMemoryCompression_IncompressiblePayloadStaysAsIs verifies the
// "compression didn't help" path: when DEFLATE produces a result at least
// as large as the input (common for already-compressed content, random
// data), the file is left uncompressed rather than wasting memory on a
// larger compressed representation.
func TestMemoryCompression_IncompressiblePayloadStaysAsIs(t *testing.T) {
	ns := newTestBudgetedNamespace(t, 10_000)
	ctx := context.Background()

	// A short random-looking sequence — DEFLATE overhead typically
	// makes the compressed size larger than the raw payload here.
	payload := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	if err := ns.WriteFile(ctx, "/tiny", payload); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ns.mu.Lock()
	_ = ns.compressColdFilesLocked(1000)
	compressed := ns.files["/tiny"].compressed
	ns.mu.Unlock()
	if compressed {
		t.Fatal("expected tiny incompressible file to remain uncompressed (compression would not help)")
	}
}

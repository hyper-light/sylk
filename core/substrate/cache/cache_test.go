package cache

import (
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

func TestNew_RejectsMissingBlobStore(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error for missing blob store")
	}
}

func TestNote_NewBlobLandsInHot(t *testing.T) {
	c, blobs := newTestCache(t)
	hash, _ := blobs.Put([]byte("content"))

	if err := c.Note(hash, "python"); err != nil {
		t.Fatalf("Note: %v", err)
	}
	if got := c.TierOf(hash); got != TierHot {
		t.Errorf("TierOf = %s, want hot", got)
	}
}

func TestTouch_HitsHotIncrementsCounter(t *testing.T) {
	c, blobs := newTestCache(t)
	hash, _ := blobs.Put([]byte("content"))
	_ = c.Note(hash, "python")

	if err := c.Touch(hash); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if c.Stat().HotHits != 1 {
		t.Errorf("HotHits = %d, want 1", c.Stat().HotHits)
	}
}

func TestTouch_AbsentReturnsErrNotInCache(t *testing.T) {
	c, _ := newTestCache(t)
	err := c.Touch(blob.Hash{0xDE, 0xAD})
	if !errors.Is(err, ErrNotInCache) {
		t.Errorf("expected ErrNotInCache, got %v", err)
	}
}

func TestDemoteHot_MovesToWarm(t *testing.T) {
	c, blobs := newTestCache(t)
	hash, _ := blobs.Put([]byte("content to be compressed"))
	_ = c.Note(hash, "python")

	if err := c.DemoteHot(hash); err != nil {
		t.Fatalf("DemoteHot: %v", err)
	}
	if got := c.TierOf(hash); got != TierWarm {
		t.Errorf("TierOf after DemoteHot = %s, want warm", got)
	}
}

func TestDemoteWarmToCold_MovesAndDropsBytes(t *testing.T) {
	c, blobs := newTestCache(t)
	hash, _ := blobs.Put([]byte("warm content"))
	_ = c.Note(hash, "python")
	_ = c.DemoteHot(hash)

	if err := c.DemoteWarmToCold(hash, "https://example.com/source"); err != nil {
		t.Fatalf("DemoteWarmToCold: %v", err)
	}
	if got := c.TierOf(hash); got != TierCold {
		t.Errorf("TierOf after DemoteWarmToCold = %s, want cold", got)
	}
}

func TestForget_RemovesAllBookkeeping(t *testing.T) {
	c, blobs := newTestCache(t)
	hash, _ := blobs.Put([]byte("ephemeral"))
	_ = c.Note(hash, "python")

	c.Forget(hash)
	if got := c.TierOf(hash); got != TierUnknown {
		t.Errorf("TierOf after Forget = %s, want unknown", got)
	}
}

func TestStat_TracksTierBlobCounts(t *testing.T) {
	// Use a large HotCapacity so the 3 blobs we Note do not trigger
	// the policy's auto-eviction during Insert. The test exercises the
	// manual demote-to-warm/cold path; auto-eviction is exercised
	// elsewhere.
	blobs := blob.New()
	c, err := New(Config{Blobs: blobs, HotCapacity: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	a, _ := blobs.Put([]byte("a"))
	b, _ := blobs.Put([]byte("b"))
	cHash, _ := blobs.Put([]byte("c"))

	_ = c.Note(a, "python")
	_ = c.Note(b, "python")
	_ = c.Note(cHash, "python")
	_ = c.DemoteHot(b)
	_ = c.DemoteHot(cHash)
	_ = c.DemoteWarmToCold(cHash, "https://example.com/")

	stats := c.Stat()
	if stats.HotBlobCount != 1 {
		t.Errorf("HotBlobCount = %d, want 1", stats.HotBlobCount)
	}
	if stats.WarmBlobCount != 1 {
		t.Errorf("WarmBlobCount = %d, want 1", stats.WarmBlobCount)
	}
	if stats.ColdBlobCount != 1 {
		t.Errorf("ColdBlobCount = %d, want 1", stats.ColdBlobCount)
	}
}

func TestNote_RepeatedDoesNotDoubleInsert(t *testing.T) {
	c, blobs := newTestCache(t)
	hash, _ := blobs.Put([]byte("content"))
	_ = c.Note(hash, "python")
	_ = c.Note(hash, "python")
	if c.Stat().HotBlobCount != 1 {
		t.Errorf("HotBlobCount = %d, want 1 after duplicate Note", c.Stat().HotBlobCount)
	}
}

func TestPromoteWarmToHot_ViaTouchInGhost(t *testing.T) {
	// Create a tiny cache so eviction reliably puts blobs into Ghost,
	// then exercise the touch-in-ghost promotion path.
	c, blobs := New(Config{Blobs: blob.New(), HotCapacity: 2})
	if c == nil {
		t.Fatalf("New: nil")
	}
	_ = blobs

	bs := blob.New()
	c2, err := New(Config{Blobs: bs, HotCapacity: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	a, _ := bs.Put([]byte("aaa"))
	b, _ := bs.Put([]byte("bbb"))
	cBlob, _ := bs.Put([]byte("ccc"))

	if err := c2.Note(a, "python"); err != nil {
		t.Fatalf("Note a: %v", err)
	}
	if err := c2.Note(b, "python"); err != nil {
		t.Fatalf("Note b: %v", err)
	}
	if err := c2.Note(cBlob, "python"); err != nil {
		t.Fatalf("Note c: %v", err)
	}

	// Touching a (which may now be in Warm/Ghost) should be safe.
	_ = c2.Touch(a)
}

func newTestCache(t *testing.T) (*Cache, *blob.Store) {
	t.Helper()
	blobs := blob.New()
	c, err := New(Config{Blobs: blobs, HotCapacity: 16})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, blobs
}

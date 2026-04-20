package cache

import (
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

func TestS3FIFO_NewSizesQueuesProportionally(t *testing.T) {
	s := NewS3FIFO(100)
	if s.smallCap < 1 {
		t.Errorf("smallCap = %d, want ≥ 1", s.smallCap)
	}
	if s.mainCap < 1 {
		t.Errorf("mainCap = %d, want ≥ 1", s.mainCap)
	}
	if s.smallCap+s.mainCap != 100 && s.smallCap+s.mainCap != 101 {
		// roundoff-tolerant: small is 10% rounded up + 1
		t.Errorf("smallCap+mainCap = %d, want ~100", s.smallCap+s.mainCap)
	}
}

func TestS3FIFO_InsertEmptyDoesNotEvict(t *testing.T) {
	s := NewS3FIFO(10)
	d := s.Insert(blob.Hash{0x01})
	if !d.Evicted.IsZero() || !d.EvictedFromGhost.IsZero() {
		t.Errorf("first insert should not evict, got %+v", d)
	}
	if s.SmallLen() != 1 {
		t.Errorf("small len after insert = %d, want 1", s.SmallLen())
	}
}

func TestS3FIFO_InsertEvictsWhenSmallFull(t *testing.T) {
	s := NewS3FIFO(10) // smallCap = 2 (10% + 1)
	// Fill small.
	for i := range 10 {
		s.Insert(blob.Hash{byte(i)})
	}
	// Should have triggered evictions (some demoted to Warm or Ghost).
	if s.SmallLen() > s.smallCap {
		t.Errorf("small len = %d, want ≤ smallCap=%d", s.SmallLen(), s.smallCap)
	}
}

func TestS3FIFO_TouchInSmall_NoDecision(t *testing.T) {
	s := NewS3FIFO(10)
	hash := blob.Hash{0x42}
	s.Insert(hash)
	d := s.Touch(hash)
	if !d.Promoted.IsZero() {
		t.Error("touch on Small entry should not produce Promoted")
	}
}

func TestS3FIFO_TouchInGhost_PromotesBack(t *testing.T) {
	// Capacity sized so smallCap=1 (10% rounded up + 1) and ghostCap is
	// big enough to retain `a` after subsequent inserts.
	s := NewS3FIFO(20) // smallCap=3, mainCap=17, ghostCap=17
	a := blob.Hash{0xA}
	b := blob.Hash{0xB}
	c := blob.Hash{0xC}

	s.Insert(a)
	// Bump small to capacity, then trigger eviction of `a` to ghost.
	for i := byte(1); i <= byte(s.smallCap+1); i++ {
		s.Insert(blob.Hash{0x10 | i})
	}
	_ = b
	_ = c

	if _, inGhost := s.ghostIndex[a]; !inGhost {
		t.Fatalf("setup: expected `a` in ghost; ghostLen=%d, ghostIdx=%v", s.GhostLen(), s.ghostIndex)
	}

	d := s.Touch(a)
	if d.Promoted != a {
		t.Errorf("touch on Ghost entry should promote it, got Promoted=%v want %v", d.Promoted, a)
	}
}

func TestS3FIFO_RemoveDropsFromAllQueues(t *testing.T) {
	s := NewS3FIFO(10)
	hash := blob.Hash{0xFF}
	s.Insert(hash)
	if s.SmallLen() != 1 {
		t.Fatalf("setup: expected small len 1, got %d", s.SmallLen())
	}
	s.Remove(hash)
	if s.SmallLen() != 0 {
		t.Errorf("after Remove, small len = %d, want 0", s.SmallLen())
	}
}

func TestS3FIFO_FrequentTouchSurvivesEviction(t *testing.T) {
	s := NewS3FIFO(4) // smallCap=1, mainCap=3
	frequent := blob.Hash{0xAA}
	s.Insert(frequent)
	// Touch a few times to bump freq; should survive small-tail eviction
	// and graduate to main.
	for range 3 {
		s.Touch(frequent)
	}
	// Insert enough other blobs to push frequent out of small.
	for i := range 5 {
		s.Insert(blob.Hash{byte(i + 1)})
	}
	// frequent should now be in Main (graduated due to freq > 0).
	if _, inMain := s.mainIndex[frequent]; !inMain {
		t.Errorf("frequent blob should have graduated to Main; mainLen=%d, smallLen=%d", s.MainLen(), s.SmallLen())
	}
}

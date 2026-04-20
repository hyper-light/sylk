package manifest

import (
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

func TestRegister_AcquiresOneRefcountPerFile(t *testing.T) {
	blobs := blob.New()
	s := New(blobs)

	contentA := []byte("file A content")
	contentB := []byte("file B content")
	hashA, _ := blobs.Put(contentA) // refcount = 1 (from Put)
	hashB, _ := blobs.Put(contentB) // refcount = 1

	m := buildManifest("ecosystem-x", "pkg", "1.0.0", []FileEntry{
		{Path: "/a", BlobHash: hashA, Mode: 0644},
		{Path: "/b", BlobHash: hashB, Mode: 0644},
	})

	if err := s.Register(&m); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if got := blobs.Refcount(hashA); got != 2 {
		t.Errorf("after Register, blob A refcount = %d, want 2 (Put + Register)", got)
	}
	if got := blobs.Refcount(hashB); got != 2 {
		t.Errorf("after Register, blob B refcount = %d, want 2 (Put + Register)", got)
	}
}

func TestRegister_AssignsID(t *testing.T) {
	blobs := blob.New()
	s := New(blobs)
	hash, _ := blobs.Put([]byte("content"))

	m := buildManifest("e", "n", "v", []FileEntry{{Path: "/f", BlobHash: hash, Mode: 0644}})
	if !m.ID.IsZero() {
		t.Fatal("expected manifest to have zero ID before Register")
	}

	if err := s.Register(&m); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if m.ID.IsZero() {
		t.Fatal("Register did not assign an ID")
	}
}

func TestRegister_FailsOnNilManifest(t *testing.T) {
	s := New(blob.New())
	err := s.Register(nil)
	if !errors.Is(err, ErrNilManifest) {
		t.Fatalf("expected ErrNilManifest, got %v", err)
	}
}

func TestRegister_FailsOnEmptyFiles(t *testing.T) {
	s := New(blob.New())
	m := buildManifest("e", "n", "v", nil)
	err := s.Register(&m)
	if !errors.Is(err, ErrEmptyManifest) {
		t.Fatalf("expected ErrEmptyManifest, got %v", err)
	}
}

func TestRegister_FailsOnDuplicatePaths(t *testing.T) {
	blobs := blob.New()
	hash, _ := blobs.Put([]byte("content"))
	s := New(blobs)

	m := buildManifest("e", "n", "v", []FileEntry{
		{Path: "/same", BlobHash: hash, Mode: 0644},
		{Path: "/same", BlobHash: hash, Mode: 0644},
	})
	err := s.Register(&m)
	if !errors.Is(err, ErrDuplicatePath) {
		t.Fatalf("expected ErrDuplicatePath, got %v", err)
	}
}

func TestRegister_FailsOnMissingBlob(t *testing.T) {
	blobs := blob.New()
	s := New(blobs)

	missingHash := blob.Sum([]byte("never put"))
	m := buildManifest("e", "n", "v", []FileEntry{
		{Path: "/f", BlobHash: missingHash, Mode: 0644},
	})
	err := s.Register(&m)
	if !errors.Is(err, ErrBlobMissing) {
		t.Fatalf("expected ErrBlobMissing, got %v", err)
	}
}

func TestRegister_FailsOnDuplicatePackage(t *testing.T) {
	blobs := blob.New()
	hash, _ := blobs.Put([]byte("content"))
	s := New(blobs)

	m1 := buildManifest("e", "n", "v", []FileEntry{{Path: "/f", BlobHash: hash, Mode: 0644}})
	if err := s.Register(&m1); err != nil {
		t.Fatalf("Register #1: %v", err)
	}

	hash2, _ := blobs.Put([]byte("different"))
	m2 := buildManifest("e", "n", "v", []FileEntry{{Path: "/g", BlobHash: hash2, Mode: 0644}})
	err := s.Register(&m2)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("expected ErrAlreadyRegistered, got %v", err)
	}
}

func TestRegister_AtomicityOnFailure(t *testing.T) {
	// Verify that when registration fails partway through (e.g. because
	// some intermediate blob acquire would fail in some hypothetical
	// future), no refcounts are left dangling.
	//
	// We can't easily trigger an Acquire failure mid-list (the only way
	// is for a blob to disappear between Has and Acquire, which our
	// API doesn't expose). So we test by verifying that a failed
	// Register due to ErrAlreadyRegistered after we've already passed
	// the Has check leaves no extra refcounts.
	blobs := blob.New()
	hashA, _ := blobs.Put([]byte("a"))
	hashB, _ := blobs.Put([]byte("b"))
	s := New(blobs)

	m1 := buildManifest("e", "n", "v", []FileEntry{
		{Path: "/a", BlobHash: hashA, Mode: 0644},
		{Path: "/b", BlobHash: hashB, Mode: 0644},
	})
	if err := s.Register(&m1); err != nil {
		t.Fatalf("Register #1: %v", err)
	}
	refA1 := blobs.Refcount(hashA)
	refB1 := blobs.Refcount(hashB)

	// Attempt re-register — should fail with ErrAlreadyRegistered
	// before Acquire is called.
	m2 := buildManifest("e", "n", "v", []FileEntry{
		{Path: "/a", BlobHash: hashA, Mode: 0644},
		{Path: "/b", BlobHash: hashB, Mode: 0644},
	})
	_ = s.Register(&m2)

	if blobs.Refcount(hashA) != refA1 {
		t.Errorf("failed Register changed blob A refcount from %d to %d", refA1, blobs.Refcount(hashA))
	}
	if blobs.Refcount(hashB) != refB1 {
		t.Errorf("failed Register changed blob B refcount from %d to %d", refB1, blobs.Refcount(hashB))
	}
}

func TestGet_RetrievesRegistered(t *testing.T) {
	blobs := blob.New()
	hash, _ := blobs.Put([]byte("c"))
	s := New(blobs)

	m := buildManifest("e", "n", "v", []FileEntry{{Path: "/f", BlobHash: hash, Mode: 0644}})
	_ = s.Register(&m)

	got, ok := s.Get(m.ID)
	if !ok {
		t.Fatal("Get returned !ok for a registered manifest")
	}
	if got.ID != m.ID {
		t.Errorf("Get returned manifest with ID %s, want %s", got.ID, m.ID)
	}
}

func TestGet_AbsentReturnsFalse(t *testing.T) {
	s := New(blob.New())
	var someID ID
	someID[0] = 1 // non-zero
	if _, ok := s.Get(someID); ok {
		t.Fatal("Get returned ok for an absent manifest ID")
	}
}

func TestGetByPackage_RetrievesRegistered(t *testing.T) {
	blobs := blob.New()
	hash, _ := blobs.Put([]byte("c"))
	s := New(blobs)

	pkg := PackageID{Ecosystem: "e", Name: "n", Version: "v"}
	m := buildManifest(pkg.Ecosystem, pkg.Name, pkg.Version, []FileEntry{{Path: "/f", BlobHash: hash, Mode: 0644}})
	_ = s.Register(&m)

	got, ok := s.GetByPackage(pkg)
	if !ok {
		t.Fatal("GetByPackage returned !ok for a registered package")
	}
	if got.Package != pkg {
		t.Errorf("GetByPackage returned wrong package: got %s, want %s", got.Package, pkg)
	}
}

func TestDetach_ReleasesRefcounts(t *testing.T) {
	blobs := blob.New()
	hash, _ := blobs.Put([]byte("c"))
	s := New(blobs)

	m := buildManifest("e", "n", "v", []FileEntry{{Path: "/f", BlobHash: hash, Mode: 0644}})
	_ = s.Register(&m)

	beforeRef := blobs.Refcount(hash)
	if err := s.Detach(m.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	afterRef := blobs.Refcount(hash)
	if afterRef != beforeRef-1 {
		t.Errorf("Detach should drop refcount by 1; before=%d after=%d", beforeRef, afterRef)
	}
}

func TestDetach_RemovesFromStore(t *testing.T) {
	blobs := blob.New()
	hash, _ := blobs.Put([]byte("c"))
	s := New(blobs)

	m := buildManifest("e", "n", "v", []FileEntry{{Path: "/f", BlobHash: hash, Mode: 0644}})
	_ = s.Register(&m)
	_ = s.Detach(m.ID)

	if _, ok := s.Get(m.ID); ok {
		t.Error("Get still returns the manifest after Detach")
	}
	if _, ok := s.GetByPackage(m.Package); ok {
		t.Error("GetByPackage still returns the manifest after Detach")
	}
}

func TestDetach_AbsentIsError(t *testing.T) {
	s := New(blob.New())
	var someID ID
	someID[0] = 1
	err := s.Detach(someID)
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("expected ErrNotRegistered for Detach of missing ID, got %v", err)
	}
}

func TestStat(t *testing.T) {
	blobs := blob.New()
	hashA, _ := blobs.Put([]byte("a"))
	hashB, _ := blobs.Put([]byte("b"))
	hashC, _ := blobs.Put([]byte("c"))
	s := New(blobs)

	m1 := buildManifest("e", "p1", "1.0", []FileEntry{
		{Path: "/x", BlobHash: hashA, Mode: 0644},
		{Path: "/y", BlobHash: hashB, Mode: 0644},
	})
	m2 := buildManifest("e", "p2", "1.0", []FileEntry{
		{Path: "/z", BlobHash: hashC, Mode: 0644},
	})
	_ = s.Register(&m1)
	_ = s.Register(&m2)

	stats := s.Stat()
	if stats.ManifestCount != 2 {
		t.Errorf("ManifestCount = %d, want 2", stats.ManifestCount)
	}
	if stats.TotalFileCount != 3 {
		t.Errorf("TotalFileCount = %d, want 3", stats.TotalFileCount)
	}
}

func TestRegister_ConcurrentDistinctPackages(t *testing.T) {
	// Distinct packages should register concurrently without contention
	// or correctness issues. Run under -race.
	blobs := blob.New()
	s := New(blobs)

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			content := []byte(byteContent(idx))
			hash, _ := blobs.Put(content)
			m := buildManifest("e", "pkg-"+intToString(idx), "1.0", []FileEntry{
				{Path: "/f", BlobHash: hash, Mode: 0644},
			})
			if err := s.Register(&m); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Register failed: %v", err)
		}
	}
	if got := s.Stat().ManifestCount; got != goroutines {
		t.Errorf("after %d concurrent registrations, ManifestCount = %d", goroutines, got)
	}
}

func TestRegister_ConcurrentSamePackage_OneWins(t *testing.T) {
	// Concurrent Register of the same PackageID should produce exactly
	// one success and the rest ErrAlreadyRegistered.
	blobs := blob.New()
	hash, _ := blobs.Put([]byte("c"))
	s := New(blobs)

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	results := make(chan error, goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			m := buildManifest("e", "contended", "1.0", []FileEntry{
				{Path: "/f", BlobHash: hash, Mode: 0644},
			})
			results <- s.Register(&m)
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyRegistered):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d", successes)
	}
	if conflicts != goroutines-1 {
		t.Errorf("expected %d conflicts, got %d", goroutines-1, conflicts)
	}
}

func buildManifest(eco, name, ver string, files []FileEntry) Manifest {
	return Manifest{
		Package: PackageID{Ecosystem: eco, Name: name, Version: ver},
		Files:   files,
	}
}

// intToString and byteContent: local helpers to avoid importing fmt for
// the tests' content generation. Kept in sync with the blob package's
// equivalents for cross-package consistency in test-data shapes.
func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func byteContent(i int) string {
	return "content-" + intToString(i)
}

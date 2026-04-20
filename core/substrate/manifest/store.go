package manifest

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

// Store holds registered manifests and coordinates their blob refcounts
// with the underlying blob.Store.
//
// A manifest passes through three states:
//
//	unregistered → registered → detached
//
// Register validates that all referenced blobs exist in the blob store
// and acquires one refcount per file entry. Detach releases those
// refcounts and removes the manifest. Manifests are immutable while
// registered; updates happen by registering a new manifest with a new
// ID.
//
// All operations are safe for concurrent use.
type Store struct {
	blobs *blob.Store

	mu    sync.RWMutex
	byID  map[ID]*Manifest
	byPkg map[PackageID]*Manifest // a registered package coordinate maps to exactly one manifest at a time
}

// New constructs a manifest Store wired to the given blob Store.
//
// The blob Store is the authority for blob existence and refcounting;
// the manifest Store is the authority for typed bundle composition and
// (via the Register/Detach lifecycle) the timing of refcount changes.
func New(blobs *blob.Store) *Store {
	return &Store{
		blobs: blobs,
		byID:  make(map[ID]*Manifest),
		byPkg: make(map[PackageID]*Manifest),
	}
}

// Register installs a manifest into the store.
//
// Preconditions:
//   - All blobs referenced by m.Files must exist in the blob store
//     (caller is responsible for Putting them first).
//   - m.Files must not contain duplicate paths.
//   - The PackageID must not already be registered (PackageID is a
//     pinning coordinate; replacing requires explicit Detach first).
//
// On success, the manifest's ID is computed and assigned (overwriting
// any caller-supplied ID), CreatedAt is set, and one refcount is
// acquired per FileEntry on the underlying blob store.
//
// On any validation failure, no refcount changes are made.
func (s *Store) Register(m *Manifest) error {
	if m == nil {
		return ErrNilManifest
	}
	if err := validateFiles(m.Files); err != nil {
		return err
	}
	if err := s.verifyBlobsPresent(m.Files); err != nil {
		return err
	}
	return s.commitRegistration(m)
}

func validateFiles(files []FileEntry) error {
	if len(files) == 0 {
		return ErrEmptyManifest
	}
	return checkUniquePaths(files)
}

func checkUniquePaths(files []FileEntry) error {
	seen := make(map[string]struct{}, len(files))
	for i := range files {
		path := files[i].Path
		if _, dup := seen[path]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicatePath, path)
		}
		seen[path] = struct{}{}
	}
	return nil
}

func (s *Store) verifyBlobsPresent(files []FileEntry) error {
	for i := range files {
		if !s.blobs.Has(files[i].BlobHash) {
			return fmt.Errorf("%w: file %q hash %s", ErrBlobMissing, files[i].Path, files[i].BlobHash)
		}
	}
	return nil
}

func (s *Store) commitRegistration(m *Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byPkg[m.Package]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyRegistered, m.Package)
	}

	finalizeManifest(m)
	if err := s.acquireBlobs(m.Files); err != nil {
		return err
	}
	s.byID[m.ID] = m
	s.byPkg[m.Package] = m
	return nil
}

// finalizeManifest computes the ID and stamps CreatedAt.
//
// CreatedAt uses the local clock; it is not part of the ID. It exists
// for diagnostics and lockfile auditing.
func finalizeManifest(m *Manifest) {
	m.ID = ComputeID(m)
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
}

// acquireBlobs acquires one refcount per file entry. On any failure
// part-way through, previously-acquired refcounts are released so
// registration is atomic.
func (s *Store) acquireBlobs(files []FileEntry) error {
	for i := range files {
		if err := s.blobs.Acquire(files[i].BlobHash); err != nil {
			s.releaseBlobsRange(files, i)
			return fmt.Errorf("acquire blob for %q: %w", files[i].Path, err)
		}
	}
	return nil
}

func (s *Store) releaseBlobsRange(files []FileEntry, upto int) {
	for j := 0; j < upto; j++ {
		_ = s.blobs.Release(files[j].BlobHash)
	}
}

// Get returns the manifest with the given ID, if registered.
func (s *Store) Get(id ID) (*Manifest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.byID[id]
	return m, ok
}

// GetByPackage returns the manifest registered for the given package
// coordinates, if any.
func (s *Store) GetByPackage(pkg PackageID) (*Manifest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.byPkg[pkg]
	return m, ok
}

// Detach removes the manifest from the store and releases one refcount
// per file entry on the underlying blob store. After Detach, the
// manifest's blobs may be eligible for blob-store compaction (when no
// other reference holds them).
//
// Detach is idempotent in spirit but errors if the manifest is unknown,
// to make double-Detach bugs visible to callers.
func (s *Store) Detach(id ID) error {
	s.mu.Lock()
	m, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotRegistered, id)
	}
	delete(s.byID, id)
	delete(s.byPkg, m.Package)
	s.mu.Unlock()

	releaseAll(s.blobs, m.Files)
	return nil
}

func releaseAll(blobs *blob.Store, files []FileEntry) {
	for i := range files {
		_ = blobs.Release(files[i].BlobHash)
	}
}

// Stats summarizes the store's contents.
type Stats struct {
	ManifestCount  int
	TotalFileCount int
}

// Stat takes a snapshot of the store's contents.
func (s *Store) Stat() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Stats{ManifestCount: len(s.byID)}
	for _, m := range s.byID {
		out.TotalFileCount += len(m.Files)
	}
	return out
}

// Errors.
var (
	ErrNilManifest       = errors.New("manifest: nil manifest")
	ErrEmptyManifest     = errors.New("manifest: file list is empty")
	ErrDuplicatePath     = errors.New("manifest: duplicate path in file list")
	ErrBlobMissing       = errors.New("manifest: referenced blob is not in the blob store")
	ErrAlreadyRegistered = errors.New("manifest: package coordinate already registered")
	ErrNotRegistered     = errors.New("manifest: not registered")
)

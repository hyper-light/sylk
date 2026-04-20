// Package provision is the substrate's recipe execution runtime.
//
// The runtime orchestrates the universal pipeline that turns a recipe
// into a registered manifest:
//
//	resolve fetch spec → fetch bytes → verify hash → extract entries →
//	put each entry's content as a blob → assemble manifest → register
//
// In Phase 2 the runtime supports prebuilt and pure-data recipes
// (fetch + extract). The Build phase of recipes is wired in Phase 2.5
// when the sandbox + writable FUSE projection arrive; recipes with
// non-empty Build are accepted by the schema but rejected by this
// runtime with a structured error pointing at the missing capability.
package provision

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/provision/extractor"
	"github.com/adalundhe/sylk/core/substrate/provision/fetcher"
	"github.com/adalundhe/sylk/core/substrate/provision/verifier"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Runtime executes recipes against the substrate's storage layer.
type Runtime struct {
	blobs      *blob.Store
	manifests  *manifest.Store
	fetchers   *fetcher.Registry
	extractors *extractor.Registry
}

// Config wires the runtime's collaborators.
type Config struct {
	// Blobs is the blob store the runtime will Put extracted content
	// into. Required.
	Blobs *blob.Store

	// Manifests is the manifest store the runtime will register
	// finished manifests in. Required.
	Manifests *manifest.Store

	// Fetchers dispatches FetchSpecs by URL scheme. Optional; defaults
	// to fetcher.NewDefaultRegistry().
	Fetchers *fetcher.Registry

	// Extractors dispatches archive bytes by ExtractFormat. Optional;
	// defaults to extractor.NewDefaultRegistry().
	Extractors *extractor.Registry
}

// New constructs a Runtime.
func New(cfg Config) (*Runtime, error) {
	if cfg.Blobs == nil {
		return nil, ErrNilBlobStore
	}
	if cfg.Manifests == nil {
		return nil, ErrNilManifestStore
	}
	return &Runtime{
		blobs:      cfg.Blobs,
		manifests:  cfg.Manifests,
		fetchers:   defaultFetchers(cfg.Fetchers),
		extractors: defaultExtractors(cfg.Extractors),
	}, nil
}

func defaultFetchers(r *fetcher.Registry) *fetcher.Registry {
	if r == nil {
		return fetcher.NewDefaultRegistry()
	}
	return r
}

func defaultExtractors(r *extractor.Registry) *extractor.Registry {
	if r == nil {
		return extractor.NewDefaultRegistry()
	}
	return r
}

// ProvisionRequest names a recipe to provision and the runtime knobs
// that compose with it.
type ProvisionRequest struct {
	// Recipe is the recipe to execute. Must be Validate-clean.
	Recipe *recipe.Recipe

	// Platform is the host platform tuple used for fetch-source
	// substitution and platform-pin matching.
	Platform recipe.Platform

	// MaxFetchBytes caps the bytes any single fetch may read. Zero
	// disables the cap.
	MaxFetchBytes int64

	// FetchProgress is invoked with byte-count updates during fetch.
	// Optional; nil disables progress reporting.
	FetchProgress fetcher.ProgressFunc

	// PackageOverride lets the caller name the manifest's package
	// coordinates explicitly. When zero, the runtime derives them from
	// the recipe's Metadata (ecosystem/name/version).
	PackageOverride manifest.PackageID
}

// ProvisionResult summarizes what the runtime did.
type ProvisionResult struct {
	ManifestID    manifest.ID
	PackageID     manifest.PackageID
	BlobsCreated  int
	BlobsReused   int
	BytesFetched  int64
	BytesUnique   int64
	BytesTotal    int64
}

// Provision executes the recipe and returns the registered manifest's ID.
//
// The pipeline is:
//
//  1. Validate recipe shape (Validate)
//  2. Refuse if Build is non-empty (Phase 2.5 prerequisite)
//  3. Pick FetchSpec via PlatformPin matching
//  4. Fetch bytes through the fetcher registry
//  5. Verify bytes against the FetchSpec's expected hash
//  6. Extract bytes into entries
//  7. For each OutputMapping, capture matching entries and Put their
//     content as blobs, building FileEntry records
//  8. Construct and Register the manifest
//
// On any failure, no partial registration is left behind; blobs already
// Put for this provisioning are not eagerly removed (substrate
// compaction reclaims them when refcount drops to zero).
func (r *Runtime) Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	if err := preflightRequest(req); err != nil {
		return nil, err
	}
	bytes, err := r.fetchVerified(ctx, req)
	if err != nil {
		return nil, err
	}
	return r.assembleAndRegister(req, bytes)
}

func preflightRequest(req ProvisionRequest) error {
	if req.Recipe == nil {
		return ErrNilRecipe
	}
	if err := recipe.Validate(req.Recipe); err != nil {
		return fmt.Errorf("recipe validation: %w", err)
	}
	if len(req.Recipe.Build) > 0 {
		return ErrBuildNotYetSupported
	}
	return nil
}

func (r *Runtime) fetchVerified(ctx context.Context, req ProvisionRequest) ([]byte, error) {
	spec, err := fetcher.PickFetchSpec(req.Recipe.Fetch, req.Platform)
	if err != nil {
		return nil, fmt.Errorf("pick fetch spec: %w", err)
	}
	data, err := r.fetchers.Fetch(ctx, spec, fetcher.FetchOptions{
		MaxBytes: req.MaxFetchBytes,
		Progress: req.FetchProgress,
		Platform: req.Platform,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	return r.verifyOrFail(data, spec, req.Recipe.Verify)
}

func (r *Runtime) verifyOrFail(data []byte, spec recipe.FetchSpec, vspec recipe.VerifySpec) ([]byte, error) {
	v := verifier.New(vspec.PolicyClass)
	if err := v.Verify(data, spec.Hash); err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	return data, nil
}

func (r *Runtime) assembleAndRegister(req ProvisionRequest, archive []byte) (*ProvisionResult, error) {
	collected, totals, err := r.collectExtractedBlobs(archive, req.Recipe)
	if err != nil {
		return nil, err
	}
	files, err := buildManifestFiles(req.Recipe, collected)
	if err != nil {
		return nil, err
	}
	return r.registerManifest(req, files, totals, len(archive))
}

// collectedEntry pairs an extracted Entry with the substrate Hash of its
// content (after Put). One per emitted entry.
type collectedEntry struct {
	path string
	mode uint32
	hash blob.Hash
	link bool
}

type fetchTotals struct {
	bytesUnique   int64
	bytesTotal    int64
	blobsCreated  int
	blobsReused   int
	bytesFetched  int64
}

func (r *Runtime) collectExtractedBlobs(archive []byte, rcp *recipe.Recipe) ([]collectedEntry, fetchTotals, error) {
	state := newCollectorState()
	err := r.extractors.Extract(archive, rcp.Extract, func(e extractor.Entry) error {
		return r.absorbEntry(state, e)
	})
	if err != nil {
		return nil, fetchTotals{}, fmt.Errorf("extract: %w", err)
	}
	state.totals.bytesFetched = int64(len(archive))
	return state.entries, state.totals, nil
}

type collectorState struct {
	entries []collectedEntry
	totals  fetchTotals
	seen    map[blob.Hash]struct{}
}

func newCollectorState() *collectorState {
	return &collectorState{
		seen: make(map[blob.Hash]struct{}),
	}
}

func (r *Runtime) absorbEntry(state *collectorState, e extractor.Entry) error {
	hash, err := r.blobs.Put(e.Content)
	if err != nil {
		return fmt.Errorf("put blob for %q: %w", e.Path, err)
	}
	updateTotals(state, hash, int64(len(e.Content)))
	state.entries = append(state.entries, collectedEntry{
		path: e.Path,
		mode: e.Mode,
		hash: hash,
		link: e.Symlink,
	})
	return nil
}

func updateTotals(state *collectorState, hash blob.Hash, size int64) {
	state.totals.bytesTotal += size
	if _, dup := state.seen[hash]; dup {
		state.totals.blobsReused++
		return
	}
	state.seen[hash] = struct{}{}
	state.totals.bytesUnique += size
	state.totals.blobsCreated++
}

// buildManifestFiles applies the recipe's OutputMappings to the
// extracted entries, producing the FileEntry list the manifest will
// register.
//
// Each OutputMapping describes a substrate path and the source path
// (inside the extracted tree) to capture from. Trailing slash on both
// SubstratePath and SourcePath indicates "recursive copy"; otherwise
// the mapping captures a single entry.
//
// Entries not matched by any OutputMapping are silently dropped — the
// recipe author chose what to expose; what wasn't mapped is intentionally
// invisible from the substrate's perspective. The blobs were still
// Put (so they incremented refcount), but no manifest references them
// → they will be eligible for compaction. (For a small Phase 2 cost
// this is acceptable; Phase 7 wires more aggressive collection.)
func buildManifestFiles(rcp *recipe.Recipe, entries []collectedEntry) ([]manifest.FileEntry, error) {
	out := []manifest.FileEntry{}
	for i := range rcp.Output {
		matched := matchEntries(entries, rcp.Output[i])
		if len(matched) == 0 {
			return nil, fmt.Errorf("%w: output mapping %q matched no extracted entries",
				ErrEmptyOutputMapping, rcp.Output[i].SubstratePath)
		}
		out = append(out, matched...)
	}
	return deduplicateByPath(out)
}

func matchEntries(entries []collectedEntry, mapping recipe.OutputMapping) []manifest.FileEntry {
	if isRecursiveMapping(mapping) {
		return matchRecursive(entries, mapping)
	}
	return matchSingleEntry(entries, mapping)
}

func isRecursiveMapping(m recipe.OutputMapping) bool {
	return endsWithSlash(m.SourcePath) && endsWithSlash(m.SubstratePath)
}

func endsWithSlash(s string) bool {
	if s == "" {
		return false
	}
	return s[len(s)-1] == '/'
}

func matchRecursive(entries []collectedEntry, mapping recipe.OutputMapping) []manifest.FileEntry {
	out := []manifest.FileEntry{}
	prefix := trimLeadingSlash(mapping.SourcePath)
	subPrefix := mapping.SubstratePath
	for i := range entries {
		entry := &entries[i]
		entryPath := entry.path
		if !startsWith(entryPath, prefix) {
			continue
		}
		relPath := entryPath[len(prefix):]
		out = append(out, manifest.FileEntry{
			Path:     joinPath(subPrefix, relPath),
			BlobHash: entry.hash,
			Mode:     resolveOutputMode(mapping.Mode, entry.mode),
			Symlink:  entry.link,
		})
	}
	return out
}

func matchSingleEntry(entries []collectedEntry, mapping recipe.OutputMapping) []manifest.FileEntry {
	source := trimLeadingSlash(mapping.SourcePath)
	for i := range entries {
		if entries[i].path == source {
			return []manifest.FileEntry{{
				Path:     mapping.SubstratePath,
				BlobHash: entries[i].hash,
				Mode:     resolveOutputMode(mapping.Mode, entries[i].mode),
				Symlink:  entries[i].link,
			}}
		}
	}
	return nil
}

func resolveOutputMode(mappingMode recipe.OutputMode, entryMode uint32) manifest.Mode {
	if mappingMode.IsPreserve() {
		return manifest.Mode(entryMode)
	}
	return manifest.Mode(mappingMode)
}

func startsWith(s, prefix string) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

func trimLeadingSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}

func joinPath(prefix, suffix string) string {
	cleaned := path.Clean(prefix + suffix)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func deduplicateByPath(files []manifest.FileEntry) ([]manifest.FileEntry, error) {
	seen := make(map[string]struct{}, len(files))
	out := make([]manifest.FileEntry, 0, len(files))
	for i := range files {
		path := files[i].Path
		if _, dup := seen[path]; dup {
			return nil, fmt.Errorf("%w: %q claimed by multiple output mappings", ErrDuplicateOutputPath, path)
		}
		seen[path] = struct{}{}
		out = append(out, files[i])
	}
	return out, nil
}

func (r *Runtime) registerManifest(req ProvisionRequest, files []manifest.FileEntry, totals fetchTotals, archiveSize int) (*ProvisionResult, error) {
	pkg := resolvePackageID(req)
	m := &manifest.Manifest{
		Package:  pkg,
		Files:    files,
		Source:   buildSourceMetadata(req.Recipe),
		Metadata: buildMetadata(req.Recipe.Metadata),
	}
	if err := r.manifests.Register(m); err != nil {
		return nil, fmt.Errorf("register manifest: %w", err)
	}
	return &ProvisionResult{
		ManifestID:   m.ID,
		PackageID:    pkg,
		BlobsCreated: totals.blobsCreated,
		BlobsReused:  totals.blobsReused,
		BytesFetched: int64(archiveSize),
		BytesUnique:  totals.bytesUnique,
		BytesTotal:   totals.bytesTotal,
	}, nil
}

func resolvePackageID(req ProvisionRequest) manifest.PackageID {
	if !packageIDIsZero(req.PackageOverride) {
		return req.PackageOverride
	}
	return manifest.PackageID{
		Ecosystem: req.Recipe.Metadata.Ecosystem,
		Name:      req.Recipe.Metadata.Name,
		Version:   req.Recipe.Metadata.Version,
	}
}

func packageIDIsZero(p manifest.PackageID) bool {
	return p.Ecosystem == "" && p.Name == "" && p.Version == ""
}

func buildSourceMetadata(rcp *recipe.Recipe) manifest.SourceMetadata {
	id := recipe.ComputeID(rcp)
	if len(rcp.Fetch) == 0 {
		return manifest.SourceMetadata{RecipeID: id.String()}
	}
	primary := rcp.Fetch[0]
	return manifest.SourceMetadata{
		RecipeID:               id.String(),
		FetchURL:               primary.Source,
		EcosystemHash:          encodeDigestHex(primary.Hash.Digest),
		EcosystemHashAlgorithm: primary.Hash.Algorithm,
	}
}

func encodeDigestHex(digest []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, len(digest)*2)
	for i, b := range digest {
		out[2*i] = hexChars[b>>4]
		out[2*i+1] = hexChars[b&0xf]
	}
	return string(out)
}

func buildMetadata(rm recipe.RecipeMetadata) manifest.Metadata {
	return manifest.Metadata{
		License:     rm.License,
		Ecosystem:   rm.Ecosystem,
		Homepage:    rm.Homepage,
		Description: rm.Description,
		Tags:        rm.Tags,
	}
}

// Errors returned by Provision.
var (
	ErrNilBlobStore         = errors.New("provision: blob store is nil")
	ErrNilManifestStore     = errors.New("provision: manifest store is nil")
	ErrNilRecipe            = errors.New("provision: nil recipe")
	ErrBuildNotYetSupported = errors.New("provision: recipes with non-empty Build require Phase 2.5 (sandbox + writable FUSE) which is not yet wired")
	ErrEmptyOutputMapping   = errors.New("provision: output mapping matched no extracted entries")
	ErrDuplicateOutputPath  = errors.New("provision: duplicate substrate path in output mappings")
)

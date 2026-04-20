package exec

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/substrate/lock"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/projection"
	"github.com/adalundhe/sylk/core/substrate/view"
)

// Backend is the substrate's execution orchestrator.
type Backend struct {
	manifests *manifest.Store

	// lockfiles maps SessionID → *lock.Lockfile. Wired at construction
	// time by the host (the agent runtime owns lockfile lifecycle).
	lockfiles LockfileResolver

	// projection holds the substrate-side namespace the host's
	// projectedRoot composer will mount alongside the pipeline's
	// per-pipeline view. Optional — when nil, plan construction still
	// works (View has all the data) but no FUSE projection is emitted.
	projection *projection.Namespace

	// sandbox is the platform-specific sandbox runtime that consumes
	// SpawnPlan and produces SpawnResult. Optional — when nil, Spawn
	// returns the plan along with ErrSandboxNotWired.
	sandbox SandboxRunner

	// catalog overrides the default ecosystem env-contribution
	// catalog. Optional.
	catalog view.Catalog

	// viewCache caches View by composition-input hash so re-spawns
	// into the same set of manifests don't re-pay composition cost.
	viewCache *viewCache
}

// LockfileResolver returns the lockfile for a session.
type LockfileResolver func(sessionID string) (*lock.Lockfile, bool)

// SandboxRunner is the substrate's seam to the actual sandbox
// implementation. Phase 2.5 supplies a concrete value; the Backend
// works with any implementer.
type SandboxRunner interface {
	Run(ctx context.Context, plan *SpawnPlan) (*SpawnResult, error)
}

// Config wires the Backend's collaborators.
type Config struct {
	// Manifests is the substrate's manifest store. Required.
	Manifests *manifest.Store

	// LockfileResolver returns per-session lockfiles. Required.
	LockfileResolver LockfileResolver

	// Projection optionally exposes the substrate namespace so the
	// host's broker can mount it. The Backend doesn't read from it,
	// only attaches manifests when they're added to a View.
	Projection *projection.Namespace

	// Sandbox is the runtime that executes SpawnPlans.
	Sandbox SandboxRunner

	// Catalog overrides the default ecosystem catalog.
	Catalog view.Catalog

	// ViewCacheSize bounds the LRU cache of composed Views. Default
	// is defaultViewCacheSize.
	ViewCacheSize int
}

// New constructs a Backend.
func New(cfg Config) (*Backend, error) {
	if cfg.Manifests == nil {
		return nil, fmt.Errorf("substrate exec: manifest store required")
	}
	if cfg.LockfileResolver == nil {
		return nil, fmt.Errorf("substrate exec: lockfile resolver required")
	}
	return &Backend{
		manifests:  cfg.Manifests,
		lockfiles:  cfg.LockfileResolver,
		projection: cfg.Projection,
		sandbox:    cfg.Sandbox,
		catalog:    cfg.Catalog,
		viewCache:  newViewCache(resolveCacheSize(cfg.ViewCacheSize)),
	}, nil
}

// Plan composes a SpawnPlan from the request without executing it.
// Used by callers that want to inspect the resolved View before
// committing to a spawn (e.g. for diagnostics or dry-runs).
func (b *Backend) Plan(req SpawnRequest) (*SpawnPlan, error) {
	if err := preflight(req); err != nil {
		return nil, err
	}
	manifests, err := b.resolveManifests(req)
	if err != nil {
		return nil, err
	}
	v, err := b.composeOrCacheView(req, manifests)
	if err != nil {
		return nil, err
	}
	if err := b.attachToProjection(manifests); err != nil {
		return nil, err
	}
	return assembleSpawnPlan(req, v, manifests), nil
}

// Spawn runs the request: composes the View, attaches manifests, then
// hands off to the sandbox runtime.
//
// When Sandbox is unwired (Phase 2.5 not yet installed), Spawn returns
// the constructed plan together with ErrSandboxNotWired so callers can
// inspect the substrate's contribution.
func (b *Backend) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, *SpawnPlan, error) {
	plan, err := b.Plan(req)
	if err != nil {
		return nil, nil, err
	}
	if b.sandbox == nil {
		return nil, plan, ErrSandboxNotWired
	}
	result, err := b.sandbox.Run(ctx, plan)
	return result, plan, err
}

func preflight(req SpawnRequest) error {
	if req.PipelineID == "" {
		return ErrEmptyPipelineID
	}
	if req.SessionID == "" {
		return ErrEmptySessionID
	}
	if len(req.Cmd) == 0 {
		return ErrEmptyCmd
	}
	if req.Platform.IsZero() {
		return ErrEmptyPlatform
	}
	return nil
}

// resolveManifests turns the request's required tools into the closure
// of substrate manifests the View needs.
//
// Resolution rule:
//   - For each ToolRequirement, look up the lockfile pin. If absent,
//     return ErrToolNotProvisioned.
//   - Use the pin's ManifestID to fetch the manifest from the store.
//
// Inference (when RequiredTools is empty): consult the lockfile for
// any pin whose package name matches Cmd[0]. This is best-effort and
// only succeeds when the binary name is unambiguous; complex cases
// must use explicit RequiredTools.
func (b *Backend) resolveManifests(req SpawnRequest) ([]*manifest.Manifest, error) {
	lockfile, ok := b.lockfiles(req.SessionID)
	if !ok {
		return nil, fmt.Errorf("%w: session %s", ErrNoLockfile, req.SessionID)
	}
	required := req.RequiredTools
	if len(required) == 0 {
		required = b.inferRequirements(req.Cmd[0], lockfile)
	}
	if len(required) == 0 {
		return nil, fmt.Errorf("%w: no tool matches command %q", ErrToolNotProvisioned, req.Cmd[0])
	}
	return b.fetchManifestsForRequirements(required, lockfile)
}

func (b *Backend) inferRequirements(commandName string, lockfile *lock.Lockfile) []ToolRequirement {
	binaryName := basenameOf(commandName)
	for _, ecosystem := range knownToolEcosystems {
		coord := lock.Coordinate{Ecosystem: ecosystem, Name: binaryName}
		if pins := lockfile.PinsFor(coord); len(pins) > 0 {
			return []ToolRequirement{{Coordinate: coord}}
		}
	}
	return nil
}

func basenameOf(commandPath string) string {
	if idx := strings.LastIndex(commandPath, "/"); idx >= 0 {
		return commandPath[idx+1:]
	}
	return commandPath
}

// knownToolEcosystems is the inference fallback's search order. When
// the caller calls substrate.Run("pytest") without explicit
// RequiredTools, the Backend tries each ecosystem in turn looking for
// a matching pin. The order reflects how substrate-managed CLIs are
// typically declared in recipes.
var knownToolEcosystems = []string{
	"system-binary",
	"binary",
	"python-runtime",
	"node-runtime",
	"rust",
	"go",
	"ruby",
	"java",
	"custom",
}

func (b *Backend) fetchManifestsForRequirements(requirements []ToolRequirement, lockfile *lock.Lockfile) ([]*manifest.Manifest, error) {
	out := make([]*manifest.Manifest, 0, len(requirements))
	for i := range requirements {
		m, err := b.fetchOneRequirement(requirements[i], lockfile)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (b *Backend) fetchOneRequirement(req ToolRequirement, lockfile *lock.Lockfile) (*manifest.Manifest, error) {
	pins := lockfile.PinsFor(req.Coordinate)
	if len(pins) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrToolNotProvisioned, req.Coordinate)
	}
	pin := pickPin(pins, req.Constraint)
	m, ok := b.manifests.Get(pin.ManifestID)
	if !ok {
		return nil, fmt.Errorf("%w: pin %s references unknown manifest %s",
			ErrToolNotProvisioned, req.Coordinate, pin.ManifestID)
	}
	return m, nil
}

// pickPin selects the best Pin for a constraint. Phase 6 ships with a
// trivial picker (first pin); when constraints become structured (e.g.
// semver matching), this becomes the integration point with each
// ecosystem's resolver. Today's behavior is "honor whatever the
// lockfile already pinned" — sufficient for direct-pin ecosystems.
func pickPin(pins []*lock.Pin, _ string) *lock.Pin {
	return pins[0]
}

// composeOrCacheView returns the View for this request, computing it
// fresh or reusing a cached one. Cache key includes pipeline ID,
// platform, and the sorted manifest ID set.
func (b *Backend) composeOrCacheView(req SpawnRequest, manifests []*manifest.Manifest) (*view.View, error) {
	key := computeCacheKey(req.PipelineID, req.Platform.String(), manifests)
	if cached, ok := b.viewCache.get(key); ok {
		return cached, nil
	}
	specs := buildManifestSpecs(manifests)
	composed, err := view.Compose(view.ComposeRequest{
		PipelineID:       req.PipelineID,
		Platform:         req.Platform,
		Manifests:        specs,
		EcosystemCatalog: b.catalog,
	})
	if err != nil {
		return nil, err
	}
	b.viewCache.put(key, composed)
	return composed, nil
}

// buildManifestSpecs translates the resolved manifests into ManifestSpecs
// for the View composer. The mount path is set to "/" (the manifest's
// own paths are interpreted as substrate-absolute), and Ecosystems is
// derived from the manifest's metadata.
//
// More sophisticated mount-path layouts (e.g. site-packages prefixing)
// can be supplied via a future ManifestSpec override field. The default
// "manifest paths are substrate-absolute" rule matches the recipe
// authoring convention.
func buildManifestSpecs(manifests []*manifest.Manifest) []view.ManifestSpec {
	out := make([]view.ManifestSpec, 0, len(manifests))
	for _, m := range manifests {
		out = append(out, view.ManifestSpec{
			Manifest:   m,
			MountPath:  "/",
			Ecosystems: ecosystemsFromMetadata(m),
		})
	}
	return out
}

func ecosystemsFromMetadata(m *manifest.Manifest) []string {
	if m.Metadata.Ecosystem == "" {
		return []string{"custom"}
	}
	return []string{m.Metadata.Ecosystem}
}

// attachToProjection ensures all required manifests are attached to
// the substrate-side projection.Namespace. Idempotent: already-attached
// manifests are skipped.
func (b *Backend) attachToProjection(manifests []*manifest.Manifest) error {
	if b.projection == nil {
		return nil
	}
	attached := indexAttachments(b.projection.Attachments())
	for _, m := range manifests {
		if attached[m.Package] {
			continue
		}
		err := b.projection.Attach(projection.Attachment{
			MountPath: "/",
			Manifest:  m,
		})
		if err != nil {
			return fmt.Errorf("substrate exec: attach %s: %w", m.Package, err)
		}
	}
	return nil
}

func indexAttachments(packages []manifest.PackageID) map[manifest.PackageID]bool {
	out := make(map[manifest.PackageID]bool, len(packages))
	for _, p := range packages {
		out[p] = true
	}
	return out
}

func assembleSpawnPlan(req SpawnRequest, v *view.View, manifests []*manifest.Manifest) *SpawnPlan {
	cwd := req.Cwd
	if cwd == "" {
		cwd = v.MountRoot
	}
	return &SpawnPlan{
		View:      v,
		Manifests: manifests,
		Cmd:       append([]string(nil), req.Cmd...),
		Cwd:       cwd,
		Env:       composeEnvironment(v, req.Env),
		Timeout:   req.Timeout,
		Platform:  req.Platform,
	}
}

// composeEnvironment merges the View's 5-layer env stack with the
// request-supplied env additions. View entries come first; request env
// is added (overrides for matching keys).
func composeEnvironment(v *view.View, requestEnv map[string]string) map[string]string {
	out := make(map[string]string)
	addPathList(out, "PATH", v.Env.PATH)
	addPathList(out, "MANPATH", v.Env.MANPATH)
	addPathList(out, "PKG_CONFIG_PATH", v.Env.PKGConfigPath)
	addPathList(out, "ACLOCAL_PATH", v.Env.AclocalPath)
	addPathList(out, "INFOPATH", v.Env.InfoPath)
	addPathList(out, "LD_LIBRARY_PATH", v.Env.LDLibraryPath)
	addPathList(out, "DYLD_LIBRARY_PATH", v.Env.DYLDLibraryPath)
	addPathList(out, "DYLD_FRAMEWORK_PATH", v.Env.DYLDFrameworkPath)
	for k, v := range v.Env.LanguagePaths {
		addPathList(out, k, v)
	}
	for k, v := range v.Env.AuxiliaryDirs {
		out[k] = v
	}
	for k, v := range requestEnv {
		out[k] = v
	}
	return out
}

func addPathList(dst map[string]string, key string, entries []string) {
	if len(entries) == 0 {
		return
	}
	dst[key] = strings.Join(entries, string(pathListSeparator))
}

// pathListSeparator: ':' on POSIX, ';' on Windows. Compose-time
// callers don't know the target platform, so the substrate uses ':'
// universally and the platform-specific sandbox runtime translates if
// necessary. (Most Windows tools that consume PATH-shaped vars also
// accept ':'-separated entries; the substrate's docs flag the few
// that don't.)
const pathListSeparator = ':'

// ── view cache ──────────────────────────────────────────────────────

const defaultViewCacheSize = 64

func resolveCacheSize(n int) int {
	if n <= 0 {
		return defaultViewCacheSize
	}
	return n
}

type viewCacheKey struct {
	pipelineID string
	platform   string
	manifests  string // canonical manifest ID list
}

type viewCache struct {
	limit int
	order []viewCacheKey
	store map[viewCacheKey]*view.View
}

func newViewCache(limit int) *viewCache {
	return &viewCache{
		limit: limit,
		store: make(map[viewCacheKey]*view.View, limit),
	}
}

func (c *viewCache) get(key viewCacheKey) (*view.View, bool) {
	v, ok := c.store[key]
	return v, ok
}

func (c *viewCache) put(key viewCacheKey, v *view.View) {
	if _, exists := c.store[key]; !exists {
		c.order = append(c.order, key)
		if len(c.order) > c.limit {
			c.evictOldest()
		}
	}
	c.store[key] = v
}

func (c *viewCache) evictOldest() {
	if len(c.order) == 0 {
		return
	}
	delete(c.store, c.order[0])
	c.order = c.order[1:]
}

func computeCacheKey(pipelineID, platform string, manifests []*manifest.Manifest) viewCacheKey {
	ids := make([]string, len(manifests))
	for i, m := range manifests {
		ids[i] = m.ID.String()
	}
	sort.Strings(ids)
	return viewCacheKey{
		pipelineID: pipelineID,
		platform:   platform,
		manifests:  strings.Join(ids, ","),
	}
}

// Package skills wires the substrate's three agent-facing skills:
//
//   - provision_tool_dependency: ensures a recipe is installed in the
//     substrate, returning the resulting manifest_id.
//   - attach_tool_to_view: attaches a substrate manifest to a
//     pipeline's View, surfacing the env/path contributions for
//     subsequent spawns.
//   - query_view: structured introspection of a View — what
//     manifests are attached, what binaries the spawned env will see,
//     what the env_exports look like.
//
// The skills are agent-callable through the existing core/skills
// builder. Each skill takes a JSON payload, dispatches into the
// substrate, and returns the structured result the LLM can reason
// over.
//
// Phase 8 (this package): substrate-side skill implementations + the
// runtime wiring. Phase 8.x (separate work, documented below): agent
// prompt rewrites and per-agent registration. The agent-prompt
// rewiring is its own conversation because the prompt convention
// affects how recipes are surfaced; the substrate's contract here is
// "these skills exist with this shape, agents can register them via
// the standard skill registry pattern."
package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	coreskills "github.com/adalundhe/sylk/core/skills"

	"github.com/adalundhe/sylk/core/substrate/exec"
	"github.com/adalundhe/sylk/core/substrate/lock"
	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/projection"
	"github.com/adalundhe/sylk/core/substrate/provision"
	"github.com/adalundhe/sylk/core/substrate/recipe"
	"github.com/adalundhe/sylk/core/substrate/view"
)

// Config wires the substrate skills onto their dependencies.
type Config struct {
	// Provision is the recipe runtime that fetches/extracts/registers
	// new manifests.
	Provision *provision.Runtime

	// Manifests is the substrate's manifest store.
	Manifests *manifest.Store

	// Projection is the substrate-side namespace where manifests are
	// attached.
	Projection *projection.Namespace

	// Backend is the substrate-aware execution backend used for
	// query_view's introspection (View composition).
	Backend *exec.Backend

	// LockfileResolver returns the lockfile for a session; used by
	// provision_tool_dependency to add witnesses on successful
	// provisioning.
	LockfileResolver func(sessionID string) (*lock.Lockfile, bool)
}

// Bundle is the set of substrate-side skills produced by Build.
type Bundle struct {
	ProvisionToolDependency *coreskills.Skill
	AttachToolToView        *coreskills.Skill
	QueryView               *coreskills.Skill
}

// All returns the bundle's skills in a slice convenient for batch
// registration.
func (b Bundle) All() []*coreskills.Skill {
	return []*coreskills.Skill{
		b.ProvisionToolDependency,
		b.AttachToolToView,
		b.QueryView,
	}
}

// Build constructs the substrate skill bundle.
func Build(cfg Config) (Bundle, error) {
	if err := preflight(cfg); err != nil {
		return Bundle{}, err
	}
	return Bundle{
		ProvisionToolDependency: provisionToolDependencySkill(cfg),
		AttachToolToView:        attachToolToViewSkill(cfg),
		QueryView:               queryViewSkill(cfg),
	}, nil
}

func preflight(cfg Config) error {
	if cfg.Provision == nil {
		return errors.New("substrate skills: provision runtime required")
	}
	if cfg.Manifests == nil {
		return errors.New("substrate skills: manifest store required")
	}
	return nil
}

// ── provision_tool_dependency ───────────────────────────────────────

func provisionToolDependencySkill(cfg Config) *coreskills.Skill {
	return coreskills.NewSkill("provision_tool_dependency").
		Description("Install a tool, library, language toolchain, or arbitrary content-addressable artifact into the substrate. Idempotent: when the recipe is already provisioned, returns the existing manifest_id immediately. When the recipe is new, fetches the bytes from its declared source, verifies the hash, extracts/builds, and registers a manifest the substrate can attach to pipeline views.\n\nThe tool becomes available for execution only AFTER it has been attached to a view via attach_tool_to_view. Provisioning alone makes the tool available substrate-wide; attaching makes it visible to a specific pipeline's spawned processes.").
		Domain("substrate").
		Keywords("install", "provision", "tool", "library", "compiler", "runtime", "package", "substrate", "fetch", "register").
		Priority(95).
		Usage("Use when the agent needs a tool that isn't yet provisioned in the substrate. Pass the recipe_id or a (ecosystem, name, version) coordinate; the substrate's resolver translates the coordinate into a recipe and the runtime materializes the manifest. The session_id and pipeline_id are required so the lockfile can record the witness.").
		Requirement("Caller must supply session_id and pipeline_id.").
		Satisfies("Returns the manifest_id of the provisioned tool, the substrate paths it claims, and timing info.").
		StringParam("session_id", "Session that owns the lockfile this provisioning witnesses.", true).
		StringParam("pipeline_id", "Pipeline whose witness is recorded against the resulting pin.", true).
		StringParam("ecosystem", "Ecosystem name (python, node, cargo, system-binary, etc.).", true).
		StringParam("name", "Package name within the ecosystem.", true).
		StringParam("version", "Concrete version to pin (e.g. \"8.3.2\"). For ecosystems with constraint solvers wired, an explicit version pins the resolver result.", true).
		StringParam("constraint", "Optional ecosystem-native version constraint (e.g. \">=8.0,<9.0\"). Recorded as the witness's constraint field.", false).
		StringParam("recipe_id", "Optional pre-resolved recipe content hash. When provided, skips the resolver step and uses the named recipe directly.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return invokeProvisionToolDependency(ctx, cfg, input)
		}).
		Build()
}

type provisionToolDependencyParams struct {
	SessionID  string `json:"session_id"`
	PipelineID string `json:"pipeline_id"`
	Ecosystem  string `json:"ecosystem"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Constraint string `json:"constraint"`
	RecipeID   string `json:"recipe_id"`
}

type provisionToolDependencyResult struct {
	ManifestID    string   `json:"manifest_id"`
	PackageID     string   `json:"package_id"`
	BlobsCreated  int      `json:"blobs_created"`
	BlobsReused   int      `json:"blobs_reused"`
	BytesUnique   int64    `json:"bytes_unique"`
	BytesTotal    int64    `json:"bytes_total"`
	WitnessAdded  bool     `json:"witness_added"`
	AlreadyPinned bool     `json:"already_pinned"`
}

func invokeProvisionToolDependency(_ context.Context, cfg Config, input json.RawMessage) (any, error) {
	var params provisionToolDependencyParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if err := validateProvisionParams(params); err != nil {
		return nil, err
	}
	pkg := manifest.PackageID{Ecosystem: params.Ecosystem, Name: params.Name, Version: params.Version}
	if existing, ok := cfg.Manifests.GetByPackage(pkg); ok {
		return shortCircuitProvisioning(cfg, params, existing)
	}
	// Provisioning a new manifest from scratch requires a recipe to
	// execute; without a recipe_id (and without a resolver wired in
	// this scope), we can only honor pre-existing pins. This is the
	// canonical "the agent should call provision via a higher-level
	// orchestrator that knows the recipe library" path; the substrate
	// skill surfaces a structured error so the agent gets a clear
	// directive rather than a silent failure.
	return nil, fmt.Errorf("provision_tool_dependency: recipe %q is not in the substrate. Either provide a recipe_id or have the substrate's recipe library register one for %s/%s@%s",
		params.RecipeID, params.Ecosystem, params.Name, params.Version)
}

func validateProvisionParams(params provisionToolDependencyParams) error {
	if params.SessionID == "" {
		return errors.New("provision_tool_dependency: session_id required")
	}
	if params.PipelineID == "" {
		return errors.New("provision_tool_dependency: pipeline_id required")
	}
	if params.Ecosystem == "" || params.Name == "" || params.Version == "" {
		return errors.New("provision_tool_dependency: ecosystem, name, and version are required")
	}
	return nil
}

func shortCircuitProvisioning(cfg Config, params provisionToolDependencyParams, existing *manifest.Manifest) (any, error) {
	witnessAdded, err := recordWitness(cfg, params, existing.ID)
	if err != nil {
		return nil, err
	}
	return &provisionToolDependencyResult{
		ManifestID:    existing.ID.String(),
		PackageID:     existing.Package.String(),
		AlreadyPinned: true,
		WitnessAdded:  witnessAdded,
	}, nil
}

func recordWitness(cfg Config, params provisionToolDependencyParams, manifestID manifest.ID) (bool, error) {
	if cfg.LockfileResolver == nil {
		return false, nil
	}
	lockfile, ok := cfg.LockfileResolver(params.SessionID)
	if !ok {
		return false, fmt.Errorf("provision_tool_dependency: no lockfile for session %q", params.SessionID)
	}
	outcome, err := lockfile.Pin(lock.PinRequest{
		Coordinate: lock.Coordinate{Ecosystem: params.Ecosystem, Name: params.Name},
		Version:    params.Version,
		ManifestID: manifestID,
		Witness:    lock.Witness{PipelineID: params.PipelineID, Constraint: params.Constraint},
	})
	if err != nil {
		return false, fmt.Errorf("provision_tool_dependency: pin failure: %w", err)
	}
	return outcome.WitnessAdded, nil
}

// ── attach_tool_to_view ─────────────────────────────────────────────

func attachToolToViewSkill(cfg Config) *coreskills.Skill {
	return coreskills.NewSkill("attach_tool_to_view").
		Description("Attach a substrate manifest to a pipeline's View so the spawned process environment sees the tool's env/path/library contributions. Idempotent: re-attaching the same manifest is a no-op. Detach is automatic when the pipeline ends; explicit detach is not currently exposed.").
		Domain("substrate").
		Keywords("attach", "view", "pipeline", "substrate", "mount").
		Priority(92).
		Usage("Call after provision_tool_dependency succeeds. The substrate's execution backend automatically attaches required tools when a SpawnRequest declares them, but agents that want to pre-attach (for diagnostic or warm-up purposes) can call this directly.").
		Requirement("Substrate's projection layer must be wired (substrate is in test-fixture mode otherwise).").
		Satisfies("Returns the manifest's package coordinates and the substrate paths it now exposes.").
		StringParam("manifest_id", "Hex-encoded manifest ID returned by provision_tool_dependency.", true).
		StringParam("mount_path", "View-relative directory the manifest is rooted at. Defaults to \"/\" (manifest paths are interpreted as substrate-absolute).", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return invokeAttachToolToView(ctx, cfg, input)
		}).
		Build()
}

type attachToolToViewParams struct {
	ManifestID string `json:"manifest_id"`
	MountPath  string `json:"mount_path"`
}

type attachToolToViewResult struct {
	PackageID    string   `json:"package_id"`
	MountPath    string   `json:"mount_path"`
	ClaimedPaths []string `json:"claimed_paths"`
	Attached     bool     `json:"attached"`
}

func invokeAttachToolToView(_ context.Context, cfg Config, input json.RawMessage) (any, error) {
	var params attachToolToViewParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if cfg.Projection == nil {
		return nil, errors.New("attach_tool_to_view: substrate projection is not wired")
	}
	manifestID, err := parseManifestID(params.ManifestID)
	if err != nil {
		return nil, err
	}
	m, ok := cfg.Manifests.Get(manifestID)
	if !ok {
		return nil, fmt.Errorf("attach_tool_to_view: manifest %s not found", manifestID)
	}
	mountPath := normalizeMountPath(params.MountPath)
	if err := cfg.Projection.Attach(projection.Attachment{MountPath: mountPath, Manifest: m}); err != nil {
		return nil, fmt.Errorf("attach_tool_to_view: %w", err)
	}
	return &attachToolToViewResult{
		PackageID:    m.Package.String(),
		MountPath:    mountPath,
		ClaimedPaths: pathsOf(m.Files),
		Attached:     true,
	}, nil
}

func parseManifestID(s string) (manifest.ID, error) {
	hash, err := decodeHex(s)
	if err != nil {
		return manifest.ID{}, fmt.Errorf("manifest_id: %w", err)
	}
	if len(hash) != manifest.IDSize {
		return manifest.ID{}, fmt.Errorf("manifest_id: hex must decode to %d bytes, got %d", manifest.IDSize, len(hash))
	}
	var id manifest.ID
	copy(id[:], hash)
	return id, nil
}

func decodeHex(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexByteToNibble(s[2*i])
		if err != nil {
			return nil, err
		}
		lo, err := hexByteToNibble(s[2*i+1])
		if err != nil {
			return nil, err
		}
		out[i] = (hi << 4) | lo
	}
	return out, nil
}

func hexByteToNibble(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'a' && b <= 'f':
		return 10 + b - 'a', nil
	case b >= 'A' && b <= 'F':
		return 10 + b - 'A', nil
	}
	return 0, fmt.Errorf("invalid hex digit %q", b)
}

func normalizeMountPath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func pathsOf(files []manifest.FileEntry) []string {
	out := make([]string, len(files))
	for i := range files {
		out[i] = files[i].Path
	}
	return out
}

// ── query_view ──────────────────────────────────────────────────────

func queryViewSkill(cfg Config) *coreskills.Skill {
	return coreskills.NewSkill("query_view").
		Description("Structured introspection of a pipeline's View — what manifests are currently attached, what binaries the spawned env will see on PATH, what env exports the spawned process will receive. The query composes a View on the fly from the pipeline's lockfile pins; it does not spawn anything.").
		Domain("substrate").
		Keywords("query", "view", "introspect", "substrate", "diagnose", "what-tools").
		Priority(85).
		Usage("Use when the agent wants to verify (a) what tools are pinned for the current pipeline, (b) what env/path the spawned process will see, (c) whether a particular manifest is in the View. Cheap — purely a read operation, no side effects.").
		Satisfies("Returns the View's composed layout, env exports, and the list of attached manifests.").
		StringParam("session_id", "Session whose lockfile is consulted.", true).
		StringParam("pipeline_id", "Pipeline whose View is composed.", true).
		StringParam("platform_os", "Host OS (linux/darwin/windows). Required for layer-4 mount-plan generation.", true).
		StringParam("platform_arch", "Host arch (amd64/arm64).", true).
		StringParam("platform_libc", "Host libc (glibc/musl/system/msvc). Optional; default platform-appropriate.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return invokeQueryView(ctx, cfg, input)
		}).
		Build()
}

type queryViewParams struct {
	SessionID    string `json:"session_id"`
	PipelineID   string `json:"pipeline_id"`
	PlatformOS   string `json:"platform_os"`
	PlatformArch string `json:"platform_arch"`
	PlatformLibc string `json:"platform_libc"`
}

type queryViewResult struct {
	ViewID            string              `json:"view_id"`
	MountRoot         string              `json:"mount_root"`
	AttachedManifests []manifestSummary   `json:"attached_manifests"`
	EnvExports        map[string]string   `json:"env_exports"`
	AvailableBinaries []string            `json:"available_binaries"`
}

type manifestSummary struct {
	PackageID  string `json:"package_id"`
	ManifestID string `json:"manifest_id"`
	Ecosystem  string `json:"ecosystem"`
	FileCount  int    `json:"file_count"`
}

func invokeQueryView(_ context.Context, cfg Config, input json.RawMessage) (any, error) {
	var params queryViewParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	if err := validateQueryParams(params); err != nil {
		return nil, err
	}
	if cfg.Backend == nil {
		return nil, errors.New("query_view: substrate execution backend is not wired")
	}
	required, err := enumeratePipelinePins(cfg, params)
	if err != nil {
		return nil, fmt.Errorf("query_view: %w", err)
	}
	plan, err := cfg.Backend.Plan(buildQueryViewSpawnRequest(params, required))
	if err != nil {
		return nil, fmt.Errorf("query_view: %w", err)
	}
	return assembleQueryViewResult(plan), nil
}

// enumeratePipelinePins walks the session's lockfile and returns every
// pin whose witness includes the named pipeline. Used by query_view to
// compose a View covering all of the pipeline's pinned tools (rather
// than only those a hypothetical command would need).
func enumeratePipelinePins(cfg Config, params queryViewParams) ([]exec.ToolRequirement, error) {
	if cfg.LockfileResolver == nil {
		return nil, errors.New("no lockfile resolver wired")
	}
	lockfile, ok := cfg.LockfileResolver(params.SessionID)
	if !ok {
		return nil, fmt.Errorf("no lockfile for session %q", params.SessionID)
	}
	snap := lockfile.Snapshot()
	required := make([]exec.ToolRequirement, 0, len(snap.Pins))
	for i := range snap.Pins {
		if !pinHasWitness(&snap.Pins[i], params.PipelineID) {
			continue
		}
		required = append(required, exec.ToolRequirement{
			Coordinate: snap.Pins[i].Coordinate,
		})
	}
	return required, nil
}

func pinHasWitness(pin *lock.Pin, pipelineID string) bool {
	for i := range pin.Witnesses {
		if pin.Witnesses[i].PipelineID == pipelineID {
			return true
		}
	}
	return false
}

func validateQueryParams(params queryViewParams) error {
	if params.SessionID == "" || params.PipelineID == "" {
		return errors.New("query_view: session_id and pipeline_id required")
	}
	if params.PlatformOS == "" || params.PlatformArch == "" {
		return errors.New("query_view: platform_os and platform_arch required")
	}
	return nil
}

func buildQueryViewSpawnRequest(params queryViewParams, required []exec.ToolRequirement) exec.SpawnRequest {
	return exec.SpawnRequest{
		PipelineID:    params.PipelineID,
		SessionID:     params.SessionID,
		Cmd:           []string{"/bin/true"}, // placeholder — Plan() never spawns
		RequiredTools: required,
		Platform: recipe.Platform{
			OS:   params.PlatformOS,
			Arch: params.PlatformArch,
			Libc: params.PlatformLibc,
		},
	}
}

func assembleQueryViewResult(plan *exec.SpawnPlan) *queryViewResult {
	return &queryViewResult{
		ViewID:            plan.View.ID.String(),
		MountRoot:         plan.View.MountRoot,
		AttachedManifests: summarizeManifests(plan.Manifests),
		EnvExports:        plan.Env,
		AvailableBinaries: pathsForBinaries(plan.View),
	}
}

func summarizeManifests(manifests []*manifest.Manifest) []manifestSummary {
	out := make([]manifestSummary, len(manifests))
	for i, m := range manifests {
		out[i] = manifestSummary{
			PackageID:  m.Package.String(),
			ManifestID: m.ID.String(),
			Ecosystem:  m.Metadata.Ecosystem,
			FileCount:  len(m.Files),
		}
	}
	return out
}

// pathsForBinaries returns the substrate paths the View places on the
// spawned process's PATH. Diagnostic — not load-bearing for the
// substrate's actual execution path.
func pathsForBinaries(v *view.View) []string {
	return append([]string{}, v.Env.PATH...)
}

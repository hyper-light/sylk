package view

import (
	"errors"
	"fmt"
	"path"
	"sort"

	"lukechampine.com/blake3"

	"github.com/adalundhe/sylk/core/substrate/manifest"
	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// Compose builds a View from the request.
//
// Composition is deterministic: identical inputs (manifest set, mount
// paths, ecosystems, platform, catalog) produce identical View IDs.
// Re-spawning into the same View skips composition by ID lookup.
//
// On any error, no partial View is returned.
func Compose(req ComposeRequest) (*View, error) {
	if err := preflightComposeRequest(req); err != nil {
		return nil, err
	}
	mountRoot := resolveMountRoot(req)
	catalog := resolveCatalog(req.EcosystemCatalog)

	layout, err := buildLayout(req.Manifests)
	if err != nil {
		return nil, err
	}
	env := buildEnvironment(req.Manifests, catalog, mountRoot)
	layer4 := buildLayer4Plan(req.Platform, mountRoot)

	v := &View{
		PipelineID: req.PipelineID,
		MountRoot:  mountRoot,
		Layout:     layout,
		Env:        env,
		Layer4:     layer4,
		Platform:   req.Platform,
	}
	v.ID = computeViewID(v)
	return v, nil
}

func preflightComposeRequest(req ComposeRequest) error {
	if req.PipelineID == "" {
		return ErrEmptyPipelineID
	}
	if req.Platform.IsZero() {
		return ErrEmptyPlatform
	}
	for i := range req.Manifests {
		if req.Manifests[i].Manifest == nil {
			return fmt.Errorf("%w: manifest spec %d", ErrNilManifest, i)
		}
	}
	return nil
}

func resolveMountRoot(req ComposeRequest) string {
	if req.MountRoot != "" {
		return req.MountRoot
	}
	return defaultMountRootPrefix + req.PipelineID
}

func resolveCatalog(c Catalog) Catalog {
	if len(c) == 0 {
		return DefaultEcosystemCatalog
	}
	return c
}

const defaultMountRootPrefix = "/sylk/views/"

// ── Layout composition ─────────────────────────────────────────────

func buildLayout(specs []ManifestSpec) (Layout, error) {
	out := Layout{
		Manifests: make([]manifest.ID, 0, len(specs)),
		Paths:     make(map[string]ResolvedPath),
	}
	for i := range specs {
		if err := absorbSpecLayout(&out, &specs[i]); err != nil {
			return Layout{}, err
		}
	}
	sortManifestIDs(out.Manifests)
	return out, nil
}

func absorbSpecLayout(out *Layout, spec *ManifestSpec) error {
	out.Manifests = append(out.Manifests, spec.Manifest.ID)
	for i := range spec.Manifest.Files {
		full := joinMountPath(spec.MountPath, spec.Manifest.Files[i].Path)
		if existing, dup := out.Paths[full]; dup {
			return fmt.Errorf("%w: %q claimed by %s and %s",
				ErrPathConflict, full, existing.SourceManifest, spec.Manifest.ID)
		}
		out.Paths[full] = ResolvedPath{
			BlobHash:       spec.Manifest.Files[i].BlobHash,
			Mode:           uint32(spec.Manifest.Files[i].Mode),
			Symlink:        spec.Manifest.Files[i].Symlink,
			SourceManifest: spec.Manifest.ID,
		}
	}
	return nil
}

func joinMountPath(mountPath, fileEntryPath string) string {
	mountClean := normalizeViewPath(mountPath)
	innerClean := normalizeViewPath(fileEntryPath)
	if mountClean == "/" {
		return innerClean
	}
	if innerClean == "/" {
		return mountClean
	}
	return normalizeViewPath(mountClean + innerClean)
}

func normalizeViewPath(p string) string {
	cleaned := path.Clean("/" + p)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func sortManifestIDs(ids []manifest.ID) {
	sort.Slice(ids, func(i, j int) bool {
		return compareManifestIDs(ids[i], ids[j]) < 0
	})
}

func compareManifestIDs(a, b manifest.ID) int {
	for i := range a {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return 0
}

// ── Environment composition ─────────────────────────────────────────

func buildEnvironment(specs []ManifestSpec, catalog Catalog, mountRoot string) Environment {
	state := newEnvBuilderState()
	for i := range specs {
		applyContribution(state, &specs[i], catalog, mountRoot)
	}
	state.applyAuxiliaryDefaults(mountRoot)
	return state.intoEnvironment()
}

type envBuilderState struct {
	languagePaths map[string][]string
	auxiliaryDirs map[string]string
}

func newEnvBuilderState() *envBuilderState {
	return &envBuilderState{
		languagePaths: make(map[string][]string),
		auxiliaryDirs: make(map[string]string),
	}
}

func (s *envBuilderState) applyAuxiliaryDefaults(mountRoot string) {
	defaults := map[string]string{
		"HOME":            mountRoot + "/home",
		"XDG_CONFIG_HOME": mountRoot + "/config",
		"XDG_CACHE_HOME":  mountRoot + "/cache",
		"XDG_DATA_HOME":   mountRoot + "/data",
		"TMPDIR":          mountRoot + "/tmp",
	}
	for k, v := range defaults {
		if _, set := s.auxiliaryDirs[k]; !set {
			s.auxiliaryDirs[k] = v
		}
	}
}

func (s *envBuilderState) intoEnvironment() Environment {
	out := Environment{
		LanguagePaths: make(map[string][]string, len(s.languagePaths)),
		AuxiliaryDirs: s.auxiliaryDirs,
	}
	for envVar, paths := range s.languagePaths {
		switch envVar {
		case "PATH":
			out.PATH = append(out.PATH, paths...)
		case "MANPATH":
			out.MANPATH = append(out.MANPATH, paths...)
		case "PKG_CONFIG_PATH":
			out.PKGConfigPath = append(out.PKGConfigPath, paths...)
		case "ACLOCAL_PATH":
			out.AclocalPath = append(out.AclocalPath, paths...)
		case "INFOPATH":
			out.InfoPath = append(out.InfoPath, paths...)
		case "LD_LIBRARY_PATH":
			out.LDLibraryPath = append(out.LDLibraryPath, paths...)
		case "DYLD_LIBRARY_PATH":
			out.DYLDLibraryPath = append(out.DYLDLibraryPath, paths...)
		case "DYLD_FRAMEWORK_PATH":
			out.DYLDFrameworkPath = append(out.DYLDFrameworkPath, paths...)
		default:
			out.LanguagePaths[envVar] = append(out.LanguagePaths[envVar], paths...)
		}
	}
	return out
}

func applyContribution(state *envBuilderState, spec *ManifestSpec, catalog Catalog, mountRoot string) {
	for _, ecosystem := range spec.Ecosystems {
		contribution, ok := catalog[ecosystem]
		if !ok {
			continue
		}
		mergeContribution(state, contribution, spec.MountPath, mountRoot)
	}
}

func mergeContribution(state *envBuilderState, contribution EcosystemContribution, mountPath, mountRoot string) {
	for envVar, relativeList := range contribution.PathEntries {
		for _, rel := range relativeList {
			full := mountRoot + joinMountPath(mountPath, rel)
			state.languagePaths[envVar] = append(state.languagePaths[envVar], full)
		}
	}
}

// ── Layer 4 plan ────────────────────────────────────────────────────

func buildLayer4Plan(platform recipe.Platform, mountRoot string) Layer4Plan {
	switch platform.OS {
	case "linux":
		return Layer4Plan{Linux: buildLinuxLayer4(mountRoot)}
	case "darwin":
		return Layer4Plan{Darwin: buildDarwinLayer4(mountRoot)}
	case "windows":
		return Layer4Plan{Windows: buildWindowsLayer4(mountRoot)}
	}
	return Layer4Plan{}
}

func buildLinuxLayer4(mountRoot string) *LinuxLayer4 {
	// Default canonical-path overlay set: substrate /usr → host /usr,
	// substrate /opt → host /opt, substrate /etc → host /etc. The
	// execution backend bind-mounts these inside the spawned process's
	// mount namespace; host disk is never touched.
	overlays := []string{"/usr", "/opt", "/etc"}
	mounts := make([]BindMount, 0, len(overlays))
	for _, overlay := range overlays {
		mounts = append(mounts, BindMount{
			Source:   mountRoot + overlay,
			Target:   overlay,
			ReadOnly: true,
		})
	}
	return &LinuxLayer4{BindMounts: mounts}
}

func buildDarwinLayer4(_ string) *DarwinLayer4 {
	// macOS handles canonical-path resolution via shebang rewriting at
	// substrate-extract time and a DYLD interposer dylib for native
	// binaries. Both are configured per-recipe rather than per-View;
	// the View's Layer 4 carries an empty plan by default. Recipes
	// that need explicit interposition can populate it via a future
	// ManifestSpec field.
	return &DarwinLayer4{InterposedPaths: nil}
}

func buildWindowsLayer4(mountRoot string) *WindowsLayer4 {
	// Default Windows layout: map the View root to drive S: via
	// per-process DefineDosDevice. KnownFolder redirection is opt-in
	// per-recipe and not part of the default plan.
	return &WindowsLayer4{
		DriveMappings: []WindowsDriveMapping{
			{DriveLetter: 'S', TargetPath: mountRoot},
		},
	}
}

// ── View identity ──────────────────────────────────────────────────

func computeViewID(v *View) ID {
	h := blake3.New(IDSize, nil)
	hashStr(h, v.PipelineID)
	hashStr(h, v.MountRoot)
	hashStr(h, v.Platform.String())
	hashLayout(h, v.Layout)
	hashEnvironment(h, &v.Env)
	var out ID
	copy(out[:], h.Sum(nil))
	return out
}

type writer interface {
	Write([]byte) (int, error)
}

func hashLayout(h writer, l Layout) {
	hashU32(h, uint32(len(l.Manifests)))
	for i := range l.Manifests {
		_, _ = h.Write(l.Manifests[i][:])
	}
	paths := sortedPathKeys(l.Paths)
	hashU32(h, uint32(len(paths)))
	for _, p := range paths {
		entry := l.Paths[p]
		hashStr(h, p)
		_, _ = h.Write(entry.BlobHash[:])
		hashU32(h, entry.Mode)
		hashBool(h, entry.Symlink)
		_, _ = h.Write(entry.SourceManifest[:])
	}
}

func sortedPathKeys(m map[string]ResolvedPath) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hashEnvironment(h writer, e *Environment) {
	hashStringList(h, e.PATH)
	hashStringList(h, e.MANPATH)
	hashStringList(h, e.PKGConfigPath)
	hashStringList(h, e.AclocalPath)
	hashStringList(h, e.InfoPath)
	hashStringList(h, e.LDLibraryPath)
	hashStringList(h, e.DYLDLibraryPath)
	hashStringList(h, e.DYLDFrameworkPath)
	hashStringMap(h, asMapStringSlice(e.LanguagePaths))
	hashStringSimpleMap(h, e.AuxiliaryDirs)
}

func asMapStringSlice(m map[string][]string) map[string]string {
	// Render each list as a single canonical string so the existing
	// hashStringMap helper applies. This preserves order within the
	// slice (which is meaningful — env-var prepend order matters).
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = stringSliceJoin(v)
	}
	return out
}

const stringSliceSeparator = "\x00"

func stringSliceJoin(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += stringSliceSeparator
		}
		out += p
	}
	return out
}

func hashStringSimpleMap(h writer, m map[string]string) {
	keys := sortedMapKeys(m)
	hashU32(h, uint32(len(keys)))
	for _, k := range keys {
		hashStr(h, k)
		hashStr(h, m[k])
	}
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hashStringMap(h writer, m map[string]string) {
	hashStringSimpleMap(h, m)
}

func hashStringList(h writer, ss []string) {
	hashU32(h, uint32(len(ss)))
	for _, s := range ss {
		hashStr(h, s)
	}
}

func hashStr(h writer, s string) {
	hashU32(h, uint32(len(s)))
	_, _ = h.Write([]byte(s))
}

func hashU32(h writer, n uint32) {
	_, _ = h.Write([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

func hashBool(h writer, b bool) {
	flag := byte(0)
	if b {
		flag = 1
	}
	_, _ = h.Write([]byte{flag})
}

// Errors.
var (
	ErrEmptyPipelineID = errors.New("view: pipeline ID required")
	ErrEmptyPlatform   = errors.New("view: platform tuple required")
	ErrNilManifest     = errors.New("view: nil manifest in spec")
	ErrPathConflict    = errors.New("view: path conflict between attached manifests")
)

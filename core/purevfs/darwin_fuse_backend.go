package purevfs

// darwin_fuse_backend.go holds the pure classification logic for selecting
// between macFUSE and FUSE-T on darwin. The file has no build tag so the
// tests run on any CI host — the actual filesystem probes live behind a
// predicate injection in process_broker_darwin.go, which IS darwin-tagged.

// darwinFUSEBackend identifies which userland FUSE implementation is in use
// for strict-disk execution. The broker uses whichever is installed; the
// caller does not need to branch on backend — cgofuse's dlopen fallback
// picks the runtime library automatically (macFUSE preferred for semantic
// parity with Linux FUSE, FUSE-T fallback for kext-free installs).
type darwinFUSEBackend int

const (
	darwinFUSEBackendNone darwinFUSEBackend = iota
	darwinFUSEBackendMacFUSE
	darwinFUSEBackendFUSET
)

// String returns a human-readable backend name for diagnostics.
func (b darwinFUSEBackend) String() string {
	switch b {
	case darwinFUSEBackendMacFUSE:
		return "macFUSE"
	case darwinFUSEBackendFUSET:
		return "FUSE-T"
	default:
		return "none"
	}
}

// darwinMacFUSEMarkerPaths are the filesystem paths macFUSE installs that
// signal its presence. Either .fs bundle is sufficient: macFUSE.fs is the
// v4+ layout, osxfuse.fs is the legacy name kept for source compatibility.
var darwinMacFUSEMarkerPaths = []string{
	"/Library/Filesystems/macfuse.fs",
	"/Library/Filesystems/osxfuse.fs",
}

// darwinFUSETLibraryPaths are the locations where FUSE-T's dynamic library
// may live. Both are loadable at runtime because sylk vendors a patched
// cgofuse (see third_party/cgofuse/fuse/host_cgo.go) whose dlopen chain
// includes both the Intel/.pkg install prefix (/usr/local/lib) and the
// Apple Silicon Homebrew prefix (/opt/homebrew/lib). If you add a path
// here, also add it to the cgofuse dlopen chain or the probe will
// misreport mountable-on-this-host.
var darwinFUSETLibraryPaths = []string{
	"/usr/local/lib/libfuse-t.dylib",
	"/opt/homebrew/lib/libfuse-t.dylib",
}

// classifyDarwinFUSEBackend runs the detection policy given an arbitrary
// "path exists" predicate. Tests can pass a deterministic in-memory
// predicate to exercise every branch without needing the real filesystem.
//
// Order matters: macFUSE wins when both are installed because cgofuse's
// dlopen fallback prefers it. Keeping this function's order in sync with
// cgofuse's dlopen order is a correctness-critical invariant; diverging
// would mean the probe reports one backend while cgofuse silently picks
// another — exactly the kind of drift the zero-leak invariant cannot
// tolerate.
func classifyDarwinFUSEBackend(exists func(path string) bool) darwinFUSEBackend {
	for _, path := range darwinMacFUSEMarkerPaths {
		if exists(path) {
			return darwinFUSEBackendMacFUSE
		}
	}
	for _, path := range darwinFUSETLibraryPaths {
		if exists(path) {
			return darwinFUSEBackendFUSET
		}
	}
	return darwinFUSEBackendNone
}

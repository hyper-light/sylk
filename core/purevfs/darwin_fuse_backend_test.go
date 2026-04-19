package purevfs

import (
	"bytes"
	"os"
	"testing"
)

// readVendoredCGOFuse reads the vendored cgofuse source so tests can assert
// the dlopen chain stays in lockstep with the probe's path list.
func readVendoredCGOFuse(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// containsSubstringInBytes is a simple wrapper — exposed separately so the
// test does not pull the full strings/bytes import surface into every
// assertion site.
func containsSubstringInBytes(haystack, needle []byte) bool {
	return bytes.Contains(haystack, needle)
}

// TestClassifyDarwinFUSEBackend_MacFUSEWinsWhenBothPresent locks in the
// selection order when both backends are installed. cgofuse's dlopen chain
// prefers macFUSE (line 175-179 of cgofuse host_cgo.go v1.6.0), so the
// probe must agree or we silently report a backend that cgofuse won't use.
func TestClassifyDarwinFUSEBackend_MacFUSEWinsWhenBothPresent(t *testing.T) {
	exists := stubExists(
		"/Library/Filesystems/macfuse.fs",
		"/usr/local/lib/libfuse-t.dylib",
	)
	if got := classifyDarwinFUSEBackend(exists); got != darwinFUSEBackendMacFUSE {
		t.Fatalf("backend = %s, want macFUSE", got)
	}
}

// TestClassifyDarwinFUSEBackend_LegacyOSXFuseMarkerCountsAsMacFUSE covers
// the legacy pre-v4 macFUSE install, which ships the same FS family under
// a different bundle name. The probe must treat it as macFUSE for the same
// reason cgofuse does.
func TestClassifyDarwinFUSEBackend_LegacyOSXFuseMarkerCountsAsMacFUSE(t *testing.T) {
	exists := stubExists("/Library/Filesystems/osxfuse.fs")
	if got := classifyDarwinFUSEBackend(exists); got != darwinFUSEBackendMacFUSE {
		t.Fatalf("backend = %s, want macFUSE (osxfuse.fs marker)", got)
	}
}

// TestClassifyDarwinFUSEBackend_FUSETWhenOnlyLibraryPresent covers the FUSE-T
// install: no kext, just the dynamic library at /usr/local/lib.
func TestClassifyDarwinFUSEBackend_FUSETWhenOnlyLibraryPresent(t *testing.T) {
	exists := stubExists("/usr/local/lib/libfuse-t.dylib")
	if got := classifyDarwinFUSEBackend(exists); got != darwinFUSEBackendFUSET {
		t.Fatalf("backend = %s, want FUSE-T", got)
	}
}

// TestClassifyDarwinFUSEBackend_FUSETHomebrewAppleSilicon covers Apple
// Silicon users who installed FUSE-T via Homebrew (whose prefix is
// /opt/homebrew, not /usr/local). The probe recognizes it so the user
// gets a diagnostic rather than silent mount failure.
//
// NOTE: cgofuse v1.6.0 itself only dlopens /usr/local/lib/libfuse-t.dylib,
// so even with this probe returning FUSE-T, the actual mount may fail for
// Apple Silicon Homebrew users until cgofuse adds /opt/homebrew support
// (or we vendor a patched cgofuse). This test documents the intent; the
// subsequent mount-failure path remains a known install-time friction for
// that specific configuration.
func TestClassifyDarwinFUSEBackend_FUSETHomebrewAppleSilicon(t *testing.T) {
	exists := stubExists("/opt/homebrew/lib/libfuse-t.dylib")
	if got := classifyDarwinFUSEBackend(exists); got != darwinFUSEBackendFUSET {
		t.Fatalf("backend = %s, want FUSE-T (Apple Silicon Homebrew)", got)
	}
}

// TestClassifyDarwinFUSEBackend_NoneWhenNothingInstalled verifies the
// all-clear-no-backend case maps to None — the probe's way of telling the
// broker to refuse strict execution.
func TestClassifyDarwinFUSEBackend_NoneWhenNothingInstalled(t *testing.T) {
	exists := stubExists() // nothing exists
	if got := classifyDarwinFUSEBackend(exists); got != darwinFUSEBackendNone {
		t.Fatalf("backend = %s, want None", got)
	}
}

// TestDarwinFUSEBackend_StringDistinct locks the diagnostic labels. If
// someone flips two constants, String() collisions would silently break
// user-facing error messages.
func TestDarwinFUSEBackend_StringDistinct(t *testing.T) {
	seen := map[string]darwinFUSEBackend{}
	for _, b := range []darwinFUSEBackend{darwinFUSEBackendNone, darwinFUSEBackendMacFUSE, darwinFUSEBackendFUSET} {
		s := b.String()
		if s == "" {
			t.Errorf("backend %d has empty String()", b)
		}
		if prior, ok := seen[s]; ok {
			t.Errorf("backends %d and %d collide on %q", prior, b, s)
		}
		seen[s] = b
	}
}

// TestVendoredCGOFuseCoversAllProbedFUSETPaths locks the invariant that
// every FUSE-T library path the probe reports as "installed" is also a
// path the vendored cgofuse actually dlopens at runtime. Drift here would
// cause the probe to say "FUSE-T available" while cgofuse fails to load
// it — the exact silent-mount-failure scenario the vendored patch exists
// to prevent.
//
// The test reads the vendored cgofuse source and greps the dlopen list
// so changes to either side trigger the mismatch immediately.
func TestVendoredCGOFuseCoversAllProbedFUSETPaths(t *testing.T) {
	const cgofuseHost = "../../third_party/cgofuse/fuse/host_cgo.go"
	raw, err := readVendoredCGOFuse(cgofuseHost)
	if err != nil {
		t.Fatalf("reading vendored cgofuse: %v", err)
	}
	for _, path := range darwinFUSETLibraryPaths {
		if !containsSubstringInBytes(raw, []byte(path)) {
			t.Errorf("probe knows about FUSE-T at %s but vendored cgofuse host_cgo.go does not dlopen it; either add the dlopen line or remove the probe entry", path)
		}
	}
	for _, path := range darwinMacFUSEMarkerPaths {
		// MacFUSE markers are kernel-extension bundles, not library paths
		// — they're correctly in the probe but not in the dlopen chain.
		// This block is documentation; no assertion.
		_ = path
	}
}

// stubExists returns an `exists` predicate that reports true only for the
// exact paths listed. Useful for driving classifyDarwinFUSEBackend through
// every branch without hitting the real filesystem.
func stubExists(paths ...string) func(string) bool {
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		set[p] = struct{}{}
	}
	return func(path string) bool {
		_, ok := set[path]
		return ok
	}
}

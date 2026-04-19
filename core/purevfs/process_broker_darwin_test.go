//go:build darwin && cgo

package purevfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDetectDarwinFUSEBackend_OrdersByCGOFuseDlopen verifies the detection
// function returns the backend cgofuse will actually pick at runtime. The
// order mirrors cgofuse's dlopen fallback: macFUSE before FUSE-T. A test
// environment without either reports None.
//
// The real /Library/Filesystems layout cannot be perturbed without root, so
// this test verifies reporting against the live system. Callers running in
// a FUSE-less CI environment see None, which is itself a correctness
// signal — the broker should refuse to mount in that environment, not
// silently select a backend that isn't installed.
func TestDetectDarwinFUSEBackend_ReportsInstalledBackend(t *testing.T) {
	backend := detectDarwinFUSEBackend()
	// macFUSE marker files — present iff macFUSE is installed.
	_, macfuseErr := os.Stat("/Library/Filesystems/macfuse.fs")
	_, osxfuseErr := os.Stat("/Library/Filesystems/osxfuse.fs")
	macFUSEPresent := macfuseErr == nil || osxfuseErr == nil

	// FUSE-T library presence at any probed path.
	fusetPresent := false
	for _, p := range darwinFUSETLibraryPaths {
		if _, err := os.Stat(p); err == nil {
			fusetPresent = true
			break
		}
	}

	switch {
	case macFUSEPresent && backend != darwinFUSEBackendMacFUSE:
		t.Errorf("backend = %s, want macFUSE (macFUSE marker present)", backend)
	case !macFUSEPresent && fusetPresent && backend != darwinFUSEBackendFUSET:
		t.Errorf("backend = %s, want FUSE-T (FUSE-T library present, macFUSE absent)", backend)
	case !macFUSEPresent && !fusetPresent && backend != darwinFUSEBackendNone:
		t.Errorf("backend = %s, want None (no FUSE backend installed)", backend)
	}
}

// TestDarwinFUSEBackendString_Distinct verifies String() returns distinct,
// human-readable names for each backend. Diagnostics depend on the names
// being differentiable when a user reports "my sandbox isn't mounting".
func TestDarwinFUSEBackendString_Distinct(t *testing.T) {
	seen := make(map[string]darwinFUSEBackend)
	for _, b := range []darwinFUSEBackend{darwinFUSEBackendNone, darwinFUSEBackendMacFUSE, darwinFUSEBackendFUSET} {
		s := b.String()
		if s == "" {
			t.Errorf("backend %d returned empty string", b)
		}
		if prior, ok := seen[s]; ok {
			t.Errorf("backends %d and %d share string %q", prior, b, s)
		}
		seen[s] = b
	}
}

// TestStrictExecutionProbe_ReportsBackendGapClearly verifies the probe's
// error string mentions both FUSE-T and macFUSE when neither is installed —
// a user hitting this message needs to know there's a choice of install.
//
// Runs only if neither backend is installed on the test host; otherwise
// skipped. This guards the diagnostic path without requiring destructive
// test-side installation.
func TestStrictExecutionProbe_ReportsBackendGapClearly(t *testing.T) {
	if detectDarwinFUSEBackend() != darwinFUSEBackendNone {
		t.Skip("a FUSE backend is installed; skipping the no-backend diagnostic test")
	}
	err := strictExecutionProbe()
	if err == nil {
		t.Fatal("expected probe to fail when no backend is installed")
	}
	msg := err.Error()
	for _, want := range []string{"FUSE-T", "macFUSE"} {
		if !containsSubstring(msg, want) {
			t.Errorf("error message %q missing hint for %q — users need to know what to install", msg, want)
		}
	}
	if !errors.Is(err, ErrStrictExecutionUnavailable) {
		t.Errorf("error does not wrap ErrStrictExecutionUnavailable: %v", err)
	}
}

// TestDarwinFUSETLibraryPaths_IncludeAppleSiliconHomebrew locks in the
// inclusion of /opt/homebrew/lib. cgofuse v1.6.0 only dlopens /usr/local
// paths; without an explicit entry here, an Apple Silicon user with a
// Homebrew-installed FUSE-T would hit a generic "no backend" error
// instead of the probe at least surfacing that a library was found at a
// path cgofuse cannot use.
func TestDarwinFUSETLibraryPaths_IncludeAppleSiliconHomebrew(t *testing.T) {
	wantPrefixes := []string{"/usr/local/lib", "/opt/homebrew/lib"}
	for _, want := range wantPrefixes {
		found := false
		for _, p := range darwinFUSETLibraryPaths {
			if filepath.Dir(p) == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("darwinFUSETLibraryPaths missing entry under %s; Apple Silicon Homebrew users will silently fail", want)
		}
	}
}

// containsSubstring is a tiny helper to avoid pulling strings.Contains
// across build-tagged files just for assertions.
func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

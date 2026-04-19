package shared

import (
	"testing"

	"github.com/adalundhe/sylk/core/manifest"
)

// TestTestFrameworkCompatibility_PytestVsUnittestIsIncompatible is the
// screenshot bug as a unit test: two parallel testers picking pytest and
// stdlib unittest must surface as Incompatible so the manifest forces
// reconciliation.
func TestTestFrameworkCompatibility_PytestVsUnittestIsIncompatible(t *testing.T) {
	if got := manifest.CompatibilityFor("test_framework", "pytest", "unittest"); got != manifest.CompatibilityIncompatible {
		t.Fatalf("CompatibilityFor(pytest, unittest) = %s, want incompatible", got)
	}
	// And the reverse direction.
	if got := manifest.CompatibilityFor("test_framework", "unittest", "pytest"); got != manifest.CompatibilityIncompatible {
		t.Fatalf("CompatibilityFor(unittest, pytest) = %s, want incompatible", got)
	}
}

// TestTestFrameworkCompatibility_PytestExtensionsAreEquivalent verifies
// that pytest + pytest-asyncio (or any other pytest extension) doesn't
// produce a spurious conflict.
func TestTestFrameworkCompatibility_PytestExtensionsAreEquivalent(t *testing.T) {
	cases := []struct{ a, b string }{
		{"pytest", "pytest-asyncio"},
		{"pytest", "pytest-xdist"},
		{"pytest-cov", "pytest-mock"},
		{"PYTEST", "pytest"}, // case-insensitive
	}
	for _, c := range cases {
		t.Run(c.a+"/"+c.b, func(t *testing.T) {
			if got := manifest.CompatibilityFor("test_framework", c.a, c.b); got != manifest.CompatibilityEquivalent {
				t.Fatalf("CompatibilityFor(%s, %s) = %s, want equivalent", c.a, c.b, got)
			}
		})
	}
}

// TestTestFrameworkCompatibility_ComplementaryAddonsAreCompatible covers
// assertion / mock libraries that bolt onto a base runner without
// conflicting (pytest + hypothesis, go-test + testify when used as the
// addon, etc.).
func TestTestFrameworkCompatibility_ComplementaryAddonsAreCompatible(t *testing.T) {
	cases := []struct{ a, b string }{
		{"pytest", "hypothesis"},
		{"hypothesis", "pytest"},
		{"go-test", "testify"},
	}
	for _, c := range cases {
		t.Run(c.a+"/"+c.b, func(t *testing.T) {
			got := manifest.CompatibilityFor("test_framework", c.a, c.b)
			// Note: testify is in the go-test family map AND complementary;
			// family match short-circuits to Equivalent. That's fine — both
			// answers (Equivalent and Compatible) prevent spurious conflict.
			if got == manifest.CompatibilityIncompatible {
				t.Fatalf("CompatibilityFor(%s, %s) = %s, want compatible or equivalent", c.a, c.b, got)
			}
		})
	}
}

// TestTestFrameworkCompatibility_CrossFamilyIsIncompatible covers
// runner-vs-runner pairs that genuinely conflict (jest vs vitest, gtest
// vs catch2, etc.).
func TestTestFrameworkCompatibility_CrossFamilyIsIncompatible(t *testing.T) {
	cases := []struct{ a, b string }{
		{"jest", "vitest"},
		{"go-test", "ginkgo"}, // ginkgo is not in the go-test family
		{"cargo-test", "criterion"},
	}
	for _, c := range cases {
		t.Run(c.a+"/"+c.b, func(t *testing.T) {
			if got := manifest.CompatibilityFor("test_framework", c.a, c.b); got != manifest.CompatibilityIncompatible {
				t.Fatalf("CompatibilityFor(%s, %s) = %s, want incompatible", c.a, c.b, got)
			}
		})
	}
}

// TestBuildBackendCompatibility_PythonPEP517FamilyIsEquivalent verifies
// that swapping between PEP 517 backends doesn't trigger a spurious
// conflict — they all produce wheels with the same metadata schema.
func TestBuildBackendCompatibility_PythonPEP517FamilyIsEquivalent(t *testing.T) {
	cases := []struct{ a, b string }{
		{"setuptools", "hatchling"},
		{"poetry-core", "flit-core"},
		{"hatchling", "pdm-backend"},
	}
	for _, c := range cases {
		t.Run(c.a+"/"+c.b, func(t *testing.T) {
			if got := manifest.CompatibilityFor("build_backend", c.a, c.b); got != manifest.CompatibilityEquivalent {
				t.Fatalf("CompatibilityFor(%s, %s) = %s, want equivalent", c.a, c.b, got)
			}
		})
	}
}

// TestBuildBackendCompatibility_CrossLanguageIsIncompatible covers the
// most dangerous case the manifest must surface: parallel pipelines
// committing to incompatible build systems for the same project.
func TestBuildBackendCompatibility_CrossLanguageIsIncompatible(t *testing.T) {
	cases := []struct{ a, b string }{
		{"setuptools", "cargo"}, // python vs rust
		{"go-modules", "npm"},   // go vs js
		{"cmake", "meson"},      // cpp atoms (not a family)
		{"gradle", "maven"},     // jvm atoms (not a family)
		{"poetry", "yarn"},      // python vs js
	}
	for _, c := range cases {
		t.Run(c.a+"/"+c.b, func(t *testing.T) {
			if got := manifest.CompatibilityFor("build_backend", c.a, c.b); got != manifest.CompatibilityIncompatible {
				t.Fatalf("CompatibilityFor(%s, %s) = %s, want incompatible", c.a, c.b, got)
			}
		})
	}
}

// TestBuildBackendCompatibility_JSPackageManagerFamily ensures that
// switching package managers (npm ↔ pnpm ↔ yarn ↔ bun) doesn't fire a
// false-positive conflict — they consume the same package.json contract.
func TestBuildBackendCompatibility_JSPackageManagerFamily(t *testing.T) {
	cases := []struct{ a, b string }{
		{"npm", "pnpm"},
		{"yarn", "bun"},
		{"npm", "bun"},
	}
	for _, c := range cases {
		t.Run(c.a+"/"+c.b, func(t *testing.T) {
			if got := manifest.CompatibilityFor("build_backend", c.a, c.b); got != manifest.CompatibilityEquivalent {
				t.Fatalf("CompatibilityFor(%s, %s) = %s, want equivalent", c.a, c.b, got)
			}
		})
	}
}

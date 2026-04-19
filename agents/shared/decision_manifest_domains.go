package shared

import (
	"strings"

	"github.com/adalundhe/sylk/core/manifest"
)

// init registers the canonical decision domains shipped with phase 1 of
// the Decision Manifest. Additional domains can be registered by any
// package that imports core/manifest — agent packages, plan-emission
// code, or test fixtures — without modifying this file.
//
// Phase 1 ships with test_framework only; that's the domain underlying
// the screenshot bug (parallel pipeline testers picking pytest vs
// stdlib unittest for the same Python project). Build backend, lint
// config, migration tooling, and other domains can be added as the
// pattern proves out.
func init() {
	manifest.RegisterDomain(manifest.DomainSpec{
		Name:        "test_framework",
		Description: "Which testing framework / runner / harness to use for authored tests. Examples: pytest, pytest-asyncio, unittest, go-test, cargo-test, jest, vitest, busted (Lua), googletest, catch2.",
		RecommendedDimensions: []manifest.Dimension{
			manifest.DimensionLanguage,
			manifest.DimensionPath,
			manifest.DimensionSurface,
		},
		Compatibility:    testFrameworkCompatibility,
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})

	manifest.RegisterDomain(manifest.DomainSpec{
		Name:        "build_backend",
		Description: "Which build backend / packaging tool a project uses to assemble distributables. Examples (Python): setuptools, hatchling, poetry-core, flit-core, pdm-backend. (Rust): cargo. (Go): go-modules. (JS/TS): npm, pnpm, yarn, bun. (C/C++): cmake, meson, bazel. (Java/Kotlin): gradle, maven. Choosing two incompatible backends in parallel pipelines produces non-buildable output.",
		RecommendedDimensions: []manifest.Dimension{
			manifest.DimensionLanguage,
			manifest.DimensionPath,
		},
		Compatibility:    buildBackendCompatibility,
		ResolutionPolicy: manifest.ResolvePolicySpecificityFirstMover,
	})
}

// buildBackendCompatibility classifies pairs of build-backend values.
// Family membership covers PEP 517 backends that produce wheels with the
// same metadata schema (functionally interchangeable for the build step
// even if developer ergonomics differ). JS/TS package managers form
// another family because they consume the same package.json contract.
// Cross-family pairs (cargo vs go-modules, cmake vs meson) are genuinely
// incompatible and force reconciliation.
func buildBackendCompatibility(a, b string) manifest.Compatibility {
	an := normalizeBackendName(a)
	bn := normalizeBackendName(b)
	if an == bn {
		return manifest.CompatibilityEquivalent
	}
	for _, family := range buildBackendFamilies {
		if family[an] && family[bn] {
			return manifest.CompatibilityEquivalent
		}
	}
	return manifest.CompatibilityIncompatible
}

// buildBackendFamilies group backends that produce interchangeable
// distributables for the same project. Different distributables on the
// same project (cargo + go-modules) is the bug we're guarding against.
var buildBackendFamilies = []map[string]bool{
	// Python PEP 517 backends — all produce wheels conforming to PEP
	// 660/621. Switching between them changes pyproject.toml [build-system]
	// but doesn't change the wheel's metadata format.
	{
		"setuptools":     true,
		"hatchling":      true,
		"poetry-core":    true,
		"poetry":         true,
		"flit-core":      true,
		"flit":           true,
		"pdm-backend":    true,
		"pdm":            true,
		"meson-python":   true,
	},
	// JS/TS package managers — same package.json contract, equivalent
	// for build-step purposes (lockfile differs but lockfile is not the
	// build artifact we're coordinating on).
	{
		"npm":  true,
		"pnpm": true,
		"yarn": true,
		"bun":  true,
	},
	// Rust — cargo is the only first-class option. Listed for symmetry
	// with the structure (and forward-compat with future workspace
	// extensions like cargo-make / xtask).
	{
		"cargo":      true,
		"cargo-make": true,
	},
	// Go modules — go.mod / go.sum is the single canonical contract.
	// gccgo is a different toolchain but produces conforming archives.
	{
		"go-modules": true,
		"go-mod":     true,
		"gccgo":      true,
	},
	// C/C++ build systems are NOT in a single family — cmake, meson,
	// bazel, autotools all produce different invocation contracts and
	// cannot be silently swapped. They appear as separate atoms; pairs
	// resolve to Incompatible.
	// Java/Kotlin — gradle and maven both consume Maven coordinates but
	// the lifecycle hooks differ enough that mixing in one project is
	// genuinely error-prone. Treated as separate atoms.
}

func normalizeBackendName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// testFrameworkCompatibility classifies pairs of test-framework values.
// "Equivalent" covers same-runner extension families (pytest ↔ pytest-asyncio,
// vitest ↔ vitest-coverage). "Compatible" covers complementary tooling
// that can coexist in the same suite without conflict (e.g. assert + mock
// libraries that bolt onto a base runner). Everything else is genuine
// disagreement.
//
// Family membership is intentionally conservative — when in doubt the
// predicate falls through to Incompatible so the system surfaces a
// conflict the agent can review, rather than silently coalescing values
// that aren't actually interchangeable.
func testFrameworkCompatibility(a, b string) manifest.Compatibility {
	an := normalizeFrameworkName(a)
	bn := normalizeFrameworkName(b)
	if an == bn {
		return manifest.CompatibilityEquivalent
	}
	for _, family := range testFrameworkFamilies {
		if family[an] && family[bn] {
			return manifest.CompatibilityEquivalent
		}
	}
	if testFrameworkComplementary(an, bn) || testFrameworkComplementary(bn, an) {
		return manifest.CompatibilityCompatible
	}
	return manifest.CompatibilityIncompatible
}

// testFrameworkFamilies group runners that produce semantically equivalent
// test execution behavior so adding an extension package doesn't trigger a
// spurious conflict against the base runner.
var testFrameworkFamilies = []map[string]bool{
	// Python pytest family
	{
		"pytest":              true,
		"pytest-asyncio":      true,
		"pytest-xdist":        true,
		"pytest-cov":          true,
		"pytest-mock":         true,
		"pytest-trio":         true,
		"pytest-bdd":          true,
		"pytest-django":       true,
	},
	// Python stdlib unittest family
	{
		"unittest":          true,
		"unittest.mock":     true,
		"stdlib-unittest":   true,
		"python-unittest":   true,
	},
	// JavaScript/TypeScript jest family
	{
		"jest":           true,
		"@jest/globals":  true,
		"ts-jest":        true,
	},
	// JS/TS vitest family
	{
		"vitest":          true,
		"@vitest/coverage": true,
	},
	// Go testing family
	{
		"go-test":      true,
		"testing":      true,
		"go-testify":   true,
		"testify":      true,
	},
	// Rust testing family
	{
		"cargo-test":   true,
		"rustest":      true,
		"proptest":     true,
		"quickcheck":   true,
	},
	// C++ googletest family
	{
		"googletest":   true,
		"gtest":        true,
		"gmock":        true,
	},
	// Lua busted family
	{
		"busted": true,
		"luaunit": true,
	},
}

// testFrameworkComplementary lists pairs that bolt together without
// conflict (assertion library + base runner, fixture lib + runner, etc.).
// One direction is enough — the caller checks both orderings.
func testFrameworkComplementary(base, addon string) bool {
	switch base {
	case "pytest":
		switch addon {
		case "hypothesis", "freezegun", "responses", "factoryboy":
			return true
		}
	case "go-test", "testing":
		switch addon {
		// testify and gomock are addons that bolt onto go-test without
		// changing the runner. Different runners (ginkgo, gocheck) belong
		// in separate families, not here.
		case "testify", "gomock":
			return true
		}
	case "jest", "vitest":
		switch addon {
		case "msw", "testing-library":
			return true
		}
	}
	return false
}

func normalizeFrameworkName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

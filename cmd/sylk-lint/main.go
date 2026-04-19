// sylk-lint is the umbrella linter command for Sylk's in-tree analyzers.
// Currently bundles: nodirectexec (strict-disk broker mandate).
//
// Usage:
//
//	go run ./cmd/sylk-lint ./...
//
// CI integration is equivalent to any other go/analysis vettool. Exit code
// is non-zero when any analyzer reports a diagnostic.
package main

import (
	"github.com/adalundhe/sylk/core/ci/analyzers/nodirectexec"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	multichecker.Main(
		nodirectexec.Analyzer,
	)
}

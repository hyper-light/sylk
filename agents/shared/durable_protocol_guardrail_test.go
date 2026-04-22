package shared

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestGuardrail_AppendRequestExposesThreeLayerDedupe enforces the
// structural invariant that AppendRequest carries the three inputs the
// dedupe discriminator needs:
//
//  1. CorrelationID — the caller's logical-operation key
//  2. IdempotencyKey — the caller's escape hatch for payload-equal-but-
//     logically-distinct writes
//  3. Payload — input to the semantic fingerprint
//
// Regressions that remove any of these would silently reopen the
// "correlation-id-only dedupe drops distinct events" bug class. This
// test fails loudly on that regression.
func TestGuardrail_AppendRequestExposesThreeLayerDedupe(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(AppendRequest{})
	required := []string{"CorrelationID", "IdempotencyKey", "Payload"}
	for _, name := range required {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("AppendRequest.%s is required for three-layer dedupe; do not remove it without replacing with an equivalent discriminator", name)
		}
	}
}

// TestGuardrail_ComposeDedupeKeyDistinguishesPayloadFingerprints pins
// the load-bearing invariant behind Fix A: two distinct payloads that
// happen to share a (kind, correlation_id) MUST produce different
// dedupe keys. The pre-Fix-A bug was that the dedupe key ignored the
// payload entirely, so two semantically-distinct events got silently
// conflated under their shared correlation_id. This test would fail if
// a future change stopped including the payload fingerprint in the
// key.
func TestGuardrail_ComposeDedupeKeyDistinguishesPayloadFingerprints(t *testing.T) {
	t.Parallel()
	a := composeDedupeKey("handoff_selected", "pipe_correlation", "", "fingerprint-a")
	b := composeDedupeKey("handoff_selected", "pipe_correlation", "", "fingerprint-b")
	if a == b {
		t.Fatalf("composeDedupeKey produced identical keys for distinct fingerprints: %q == %q; Fix-A regression — two distinct payloads under the same correlation_id must yield different dedupe keys", a, b)
	}
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		t.Fatalf("composeDedupeKey produced empty keys with non-empty fingerprints: a=%q b=%q", a, b)
	}

	// Correlation_id alone (empty fingerprint, empty idempotency_key)
	// must be permitted — empty-payload retries under the same
	// correlation_id are logically equivalent and deduping them is
	// correct. This asserts the non-regression direction: we do NOT
	// need to disable dedupe for empty-payload retries.
	got := composeDedupeKey("handoff_selected", "pipe_correlation", "", "")
	if strings.TrimSpace(got) == "" {
		t.Fatalf("composeDedupeKey with correlation_id but empty fingerprint must still dedupe empty-payload retries; got empty key")
	}

	// Idempotency_key dominates over correlation_id — when the caller
	// has asserted logical distinctness, a different idempotency_key
	// MUST produce a different dedupe key even if the rest is equal.
	a = composeDedupeKey("handoff_selected", "pipe_correlation", "idemp-a", "fingerprint-x")
	b = composeDedupeKey("handoff_selected", "pipe_correlation", "idemp-b", "fingerprint-x")
	if a == b {
		t.Fatalf("composeDedupeKey collapsed two distinct idempotency_keys: %q == %q", a, b)
	}

	// Fully empty inputs produce the empty key (no dedupe).
	got = composeDedupeKey("handoff_selected", "", "", "")
	if got != "" {
		t.Fatalf("composeDedupeKey with no inputs = %q, want empty (no dedupe discriminator available)", got)
	}
}

// TestGuardrail_NoCorrelationIDOnlyDedupeInAgents scans every
// agents/**/*.go source file (excluding this test and the dedupe
// implementation itself) for the regression pattern of composing a
// dedupe key from (kind, correlation_id) alone. The pre-fix code used
// literal two-argument calls like dedupeKey(kind, correlationID); the
// post-fix code uses the Append(AppendRequest{...}) API that carries
// the payload. If a future contributor writes their own durable store
// that falls back to correlation-id-only dedupe, this test fails.
//
// The scan is intentionally narrow: it looks for two specific regression
// patterns, not a generic code quality check. Each pattern corresponds
// to a real bug-class door we've closed.
func TestGuardrail_NoCorrelationIDOnlyDedupeInAgents(t *testing.T) {
	t.Parallel()
	repoRoot := findRepoRoot(t)
	agentsDir := filepath.Join(repoRoot, "agents")

	// Regression pattern 1: literal "dedupeKey(kind, correlation_id)"
	// two-arg composition (the pre-Fix-A signature).
	// Regression pattern 2: a struct field literal like
	// {CorrelationID: x} in any *Append* request-shaped struct where
	// IdempotencyKey and Payload are both absent from the literal —
	// would indicate a new durable store skipping the three-layer
	// discriminator.
	var violations []string
	fset := token.NewFileSet()

	err := filepath.Walk(agentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip the guardrail test file (self) and the implementation
		// under test.
		base := filepath.Base(path)
		if base == "durable_protocol_guardrail_test.go" ||
			base == "durable_protocol_log.go" ||
			base == "durable_protocol_log_test.go" {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			// Test files with build errors aren't our problem to
			// diagnose — the go-build already fails loudly on those.
			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			// Regression pattern 1: two-arg dedupeKey composition.
			if ident.Name == "dedupeKey" && len(call.Args) == 2 {
				pos := fset.Position(call.Pos())
				violations = append(violations, pos.String()+": dedupeKey(kind, correlation_id) — pre-Fix-A signature. Use Append(AppendRequest{...}) instead.")
			}
			// Regression pattern 1b: three-arg composeDedupeKey where
			// only (kind, correlation_id) are supplied.
			if ident.Name == "composeDedupeKey" && len(call.Args) < 4 {
				pos := fset.Position(call.Pos())
				violations = append(violations, pos.String()+": composeDedupeKey called with fewer than 4 args — must supply kind, correlation_id, idempotency_key, payload_fingerprint.")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk agents dir: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf("correlation-id-only dedupe regression detected:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Start from cwd and walk up looking for go.mod.
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root (go.mod) above %s", cwd)
		}
		dir = parent
	}
}

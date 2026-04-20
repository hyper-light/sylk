package resolver

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

type stubResolver struct {
	ecosystem string
	calls     int
	out       ResolveResult
	err       error
}

func (s *stubResolver) Ecosystem() string { return s.ecosystem }
func (s *stubResolver) Resolve(_ context.Context, _ ResolveRequest) (ResolveResult, error) {
	s.calls++
	return s.out, s.err
}

func TestRegistry_DispatchesByEcosystem(t *testing.T) {
	r := NewRegistry()
	stub := &stubResolver{ecosystem: "stub", out: ResolveResult{Closure: []recipe.ID{{0xab}}}}
	r.Install(stub)

	got, err := r.Resolve(context.Background(), "stub", ResolveRequest{
		Constraints: []Constraint{{Name: "x", Constraint: "1"}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Closure) != 1 {
		t.Errorf("Closure has %d items, want 1", len(got.Closure))
	}
	if stub.calls != 1 {
		t.Errorf("stub.calls = %d, want 1", stub.calls)
	}
}

func TestRegistry_UnknownEcosystem(t *testing.T) {
	r := NewRegistry()
	_, err := r.Resolve(context.Background(), "missing", ResolveRequest{})
	if !errors.Is(err, ErrUnknownEcosystem) {
		t.Fatalf("expected ErrUnknownEcosystem, got %v", err)
	}
}

func TestRegistry_InstallReplaces(t *testing.T) {
	r := NewRegistry()
	first := &stubResolver{ecosystem: "x", out: ResolveResult{Closure: []recipe.ID{{0x01}}}}
	second := &stubResolver{ecosystem: "x", out: ResolveResult{Closure: []recipe.ID{{0x02}}}}
	r.Install(first)
	r.Install(second)

	got, err := r.Resolve(context.Background(), "x", ResolveRequest{
		Constraints: []Constraint{{Name: "y", Constraint: "1"}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Closure[0][0] != 0x02 {
		t.Errorf("Resolve dispatched to first registration; expected the second to win")
	}
	if first.calls != 0 {
		t.Errorf("first should not have been called after replacement, got %d calls", first.calls)
	}
}

func TestRegistry_EcosystemsLists(t *testing.T) {
	r := NewRegistry()
	r.Install(&stubResolver{ecosystem: "a"})
	r.Install(&stubResolver{ecosystem: "b"})

	got := r.Ecosystems()
	if len(got) != 2 {
		t.Errorf("Ecosystems returned %d, want 2", len(got))
	}
}

func TestResolveResult_HasConflicts(t *testing.T) {
	if (ResolveResult{}).HasConflicts() {
		t.Error("empty ResolveResult should not report HasConflicts")
	}
	with := ResolveResult{Conflicts: []ConstraintConflict{{Reason: "x"}}}
	if !with.HasConflicts() {
		t.Error("ResolveResult with conflict should report HasConflicts")
	}
}

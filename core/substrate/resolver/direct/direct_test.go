package direct

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
	"github.com/adalundhe/sylk/core/substrate/resolver"
)

func TestResolver_Ecosystem(t *testing.T) {
	r := New("github-release")
	if r.Ecosystem() != "github-release" {
		t.Errorf("Ecosystem = %q, want %q", r.Ecosystem(), "github-release")
	}
}

func TestResolve_RegisteredRecipe(t *testing.T) {
	r := New("github-release")
	want := recipe.ID{0xab, 0xcd}
	r.Register("ripgrep", "14.1.0", want)

	got, err := r.Resolve(context.Background(), resolver.ResolveRequest{
		Constraints: []resolver.Constraint{{Name: "ripgrep", Constraint: "14.1.0"}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.HasConflicts() {
		t.Errorf("expected no conflicts, got %+v", got.Conflicts)
	}
	if len(got.Closure) != 1 || got.Closure[0] != want {
		t.Errorf("Closure = %v, want [%v]", got.Closure, want)
	}
}

func TestResolve_UnregisteredIsConflict(t *testing.T) {
	r := New("github-release")
	got, err := r.Resolve(context.Background(), resolver.ResolveRequest{
		Constraints: []resolver.Constraint{{Name: "missing", Constraint: "1.0"}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.HasConflicts() {
		t.Error("expected conflict for unregistered constraint")
	}
}

func TestResolve_PartialResolution(t *testing.T) {
	r := New("github-release")
	r.Register("present", "1.0", recipe.ID{0x01})
	got, err := r.Resolve(context.Background(), resolver.ResolveRequest{
		Constraints: []resolver.Constraint{
			{Name: "present", Constraint: "1.0"},
			{Name: "missing", Constraint: "1.0"},
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Closure) != 1 {
		t.Errorf("Closure length = %d, want 1 (one resolved, one conflict)", len(got.Closure))
	}
	if !got.HasConflicts() {
		t.Error("expected conflict for missing constraint")
	}
}

func TestResolve_EmptyConstraintsIsError(t *testing.T) {
	r := New("github-release")
	_, err := r.Resolve(context.Background(), resolver.ResolveRequest{})
	if !errors.Is(err, ErrNoConstraints) {
		t.Fatalf("expected ErrNoConstraints, got %v", err)
	}
}

func TestRegister_OverwritesPrevious(t *testing.T) {
	r := New("x")
	first := recipe.ID{0x01}
	second := recipe.ID{0x02}
	r.Register("name", "1.0", first)
	r.Register("name", "1.0", second)

	got, _ := r.Resolve(context.Background(), resolver.ResolveRequest{
		Constraints: []resolver.Constraint{{Name: "name", Constraint: "1.0"}},
	})
	if len(got.Closure) != 1 || got.Closure[0] != second {
		t.Errorf("after re-register, expected second recipe ID, got %v", got.Closure)
	}
}

func TestUnregister_RemovesMapping(t *testing.T) {
	r := New("x")
	r.Register("name", "1.0", recipe.ID{0x01})
	if !r.Unregister("name", "1.0") {
		t.Error("Unregister returned false for known mapping")
	}
	got, _ := r.Resolve(context.Background(), resolver.ResolveRequest{
		Constraints: []resolver.Constraint{{Name: "name", Constraint: "1.0"}},
	})
	if !got.HasConflicts() {
		t.Error("expected conflict after Unregister")
	}
}

func TestUnregister_UnknownReturnsFalse(t *testing.T) {
	r := New("x")
	if r.Unregister("missing", "1.0") {
		t.Error("Unregister returned true for unknown mapping")
	}
}

func TestResolve_PinJustifications(t *testing.T) {
	r := New("github-release")
	r.Register("ripgrep", "14.1.0", recipe.ID{0xab})
	got, _ := r.Resolve(context.Background(), resolver.ResolveRequest{
		Constraints: []resolver.Constraint{{Name: "ripgrep", Constraint: "14.1.0"}},
	})
	if len(got.PinJustifications) == 0 {
		t.Error("expected PinJustifications to be populated")
	}
}

package ecosystem

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

type fakeEco struct {
	name      string
	parsePref string // command prefix this fake claims to recognize
	build     func(BuildRecipeRequest) (*recipe.Recipe, error)
}

func (f *fakeEco) Name() string { return f.name }

func (f *fakeEco) Parse(command string) (Coordinates, bool) {
	if len(command) < len(f.parsePref) || command[:len(f.parsePref)] != f.parsePref {
		return Coordinates{}, false
	}
	return Coordinates{Name: command[len(f.parsePref):]}, true
}

func (f *fakeEco) BuildRecipe(_ context.Context, req BuildRecipeRequest) (*recipe.Recipe, error) {
	if f.build == nil {
		return nil, errors.New("no build configured")
	}
	return f.build(req)
}

func TestRegistryParseFirstMatchWins(t *testing.T) {
	r := NewRegistry()
	r.Install(&fakeEco{name: "alpha", parsePref: "a "})
	r.Install(&fakeEco{name: "beta", parsePref: "b "})

	coords, ok := r.Parse("a foo")
	if !ok {
		t.Fatalf("expected match")
	}
	if coords.Ecosystem != "alpha" {
		t.Fatalf("expected alpha, got %q", coords.Ecosystem)
	}
	if coords.Name != "foo" {
		t.Fatalf("expected name foo, got %q", coords.Name)
	}

	if _, ok := r.Parse("z nothing"); ok {
		t.Fatalf("expected no match")
	}
}

func TestRegistryReinstallReplacesAndKeepsOrder(t *testing.T) {
	r := NewRegistry()
	a1 := &fakeEco{name: "alpha", parsePref: "a "}
	r.Install(a1)
	r.Install(&fakeEco{name: "beta", parsePref: "b "})
	a2 := &fakeEco{name: "alpha", parsePref: "x "}
	r.Install(a2)

	if got := r.Names(); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("unexpected order after reinstall: %v", got)
	}
	if _, ok := r.Parse("a foo"); ok {
		t.Fatalf("old plugin should have been replaced")
	}
	if coords, ok := r.Parse("x foo"); !ok || coords.Name != "foo" {
		t.Fatalf("new plugin should match: ok=%v coords=%+v", ok, coords)
	}
}

func TestRegistryBuildRecipeUnknownEcosystem(t *testing.T) {
	r := NewRegistry()
	_, err := r.BuildRecipe(context.Background(), BuildRecipeRequest{
		Coords: Coordinates{Ecosystem: "missing", Name: "foo"},
	})
	if !errors.Is(err, ErrUnknownEcosystem) {
		t.Fatalf("expected ErrUnknownEcosystem, got %v", err)
	}
}

func TestRegistryBuildRecipeRejectsNilFromPlugin(t *testing.T) {
	r := NewRegistry()
	r.Install(&fakeEco{
		name: "x",
		build: func(BuildRecipeRequest) (*recipe.Recipe, error) {
			return nil, nil
		},
	})
	_, err := r.BuildRecipe(context.Background(), BuildRecipeRequest{
		Coords: Coordinates{Ecosystem: "x"},
	})
	if err == nil {
		t.Fatalf("expected non-nil-recipe error, got nil")
	}
}

func TestRegistryParseEmptyCommand(t *testing.T) {
	r := NewRegistry()
	r.Install(&fakeEco{name: "x", parsePref: ""})
	if _, ok := r.Parse("   "); ok {
		t.Fatalf("expected no match for whitespace command")
	}
}

func TestRegistryLookupCanonicalizes(t *testing.T) {
	r := NewRegistry()
	r.Install(&fakeEco{name: "Python"})
	if _, ok := r.Lookup("PYTHON"); !ok {
		t.Fatalf("expected case-insensitive lookup")
	}
	if _, ok := r.Lookup(" python "); !ok {
		t.Fatalf("expected whitespace-tolerant lookup")
	}
}
